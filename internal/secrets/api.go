package secrets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Request headers that carry bearer secrets. Authorization is reserved for
// API keys (the auth middleware consumes it), so these have their own names.
const (
	HeaderUploadToken = "X-Snap-Upload-Token" // #nosec G101 -- a header name
	HeaderTicket      = "X-Snap-Ticket"
)

// b64 is unpadded, strict base64url: the encoding of every binary value in
// the API.
var b64 = base64.RawURLEncoding.Strict()

func ts(ms int64) string { return db.Time(ms).Format(time.RFC3339) }

func tsPtr(v sql.NullInt64) *string {
	if !v.Valid {
		return nil
	}
	s := ts(v.Int64)
	return &s
}

// validID reports whether raw is the canonical encoding of a 16-byte id.
func validID(raw string) bool {
	_, err := e2e.Decode16(raw)
	return err == nil
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.d.Errors.Error(w, r, status, msg)
}

func (s *Service) notFound(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, http.StatusNotFound, msgNotFound)
}

func (s *Service) internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.d.Log.Error("secrets: "+what, "err", err)
	s.fail(w, r, http.StatusInternalServerError, "Something went wrong.")
}

// apiErr renders an error from web.DecodeJSON.
func (s *Service) apiErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *web.APIError
	if errors.As(err, &ae) {
		s.fail(w, r, ae.Status, ae.Msg)
		return
	}
	s.fail(w, r, http.StatusBadRequest, "Invalid request.")
}

func (s *Service) tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(max(wait.Seconds(), 1)))))
	s.fail(w, r, http.StatusTooManyRequests, "Too many requests. Wait a moment and try again.")
}

func (s *Service) exempt(r *http.Request) bool {
	return clientip.In(clientip.From(r.Context()), s.d.RateLimitExempt)
}

// allowIP takes a token from the per-IP open limiter (info, claim).
func (s *Service) allowIP(w http.ResponseWriter, r *http.Request) bool {
	if s.exempt(r) {
		return true
	}
	if ok, wait := s.openIP.Allow("ip:" + clientip.From(r.Context()).String()); !ok {
		s.tooMany(w, r, wait)
		return false
	}
	return true
}

// allowSecret takes a token from the per-secret limiter, after the caller
// has shown it holds the link.
func (s *Service) allowSecret(w http.ResponseWriter, r *http.Request, id string) bool {
	if ok, wait := s.openSecret.Allow(id); !ok {
		s.tooMany(w, r, wait)
		return false
	}
	return true
}

// record writes a secret event: never for anonymous secrets or the owner's
// own requests. honourDrop skips crawlers and scanners; state changes
// (revealed, failed, burned) are always recorded, so a scanner that used up a
// view still shows in the owner's timeline.
func (s *Service) record(r *http.Request, rw *row, kind stats.Kind, honourDrop bool) {
	if !rw.OwnerID.Valid {
		return
	}
	if p := auth.FromContext(r.Context()); p != nil && p.UserID == rw.OwnerID.Int64 {
		return
	}
	c := stats.Classify(r)
	if c.Drop && honourDrop {
		return
	}
	ip := clientip.From(r.Context()).String()
	s.d.Stats.Record(r.Context(), stats.FromRequest(r, c, ip, s.d.VisitorSecret, stats.SubjectSecret, rw.ID, kind))
}

func (s *Service) notify(r *http.Request, rw *row, kind stats.Kind, reason string) {
	if s.d.Notifier == nil || !rw.OwnerID.Valid {
		return
	}
	if p := auth.FromContext(r.Context()); p != nil && p.UserID == rw.OwnerID.Int64 && kind == stats.KindRevealed {
		return // the owner opening their own secret
	}
	ua := r.UserAgent()
	s.d.Notifier.Notify(context.WithoutCancel(r.Context()), Notification{
		OwnerID:   rw.OwnerID.Int64,
		SecretID:  rw.ID,
		Note:      rw.Note,
		Kind:      kind,
		Reason:    reason,
		ViewsLeft: rw.ViewsLeft,
		MaxViews:  rw.MaxViews,
		At:        s.now(),
		IP:        clientip.From(r.Context()).String(),
		UASummary: stats.SummarizeUA(ua),
	})
}

// ---- create ----

type createReq struct {
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`       // plaintext stream size: padded for text
	Views     int    `json:"views"`      // 0 means 1
	ExpiresIn int64  `json:"expires_in"` // seconds; 0 means the default
	Note      string `json:"note"`
	Notify    bool   `json:"notify"`
}

type createResp struct {
	ID          string `json:"id"`
	UploadToken string `json:"upload_token"`
	ChunkSize   int    `json:"chunk_size"`
	Chunks      int64  `json:"chunks"`
	CTSize      int64  `json:"ct_size"`
	ExpiresAt   string `json:"expires_at"`
}

// options are the creator's choices, validated.
type options struct {
	views  int
	expiry time.Duration
	note   string
	notify bool
}

func (s *Service) defaultExpiry() time.Duration {
	return min(DefaultExpiry, time.Duration(MaxExpiry.Get(s.d.Settings))*time.Second)
}

// checkOptions validates views and expiry against the site settings.
func (s *Service) checkOptions(views int, expiry time.Duration) (int, time.Duration, error) {
	if views == 0 {
		views = 1
	}
	if maxV := MaxViews.Get(s.d.Settings); views < 1 || views > maxV {
		return 0, 0, web.Errorf(http.StatusBadRequest, "Views must be 1 to %d.", maxV)
	}
	if expiry == 0 {
		expiry = s.defaultExpiry()
	}
	maxE := time.Duration(MaxExpiry.Get(s.d.Settings)) * time.Second
	if expiry < MinExpiry || expiry > maxE {
		return 0, 0, web.Errorf(http.StatusBadRequest, "Expiry must be between 5 minutes and %s.", humanDuration(maxE))
	}
	return views, expiry, nil
}

func humanDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		n := int(d / (24 * time.Hour))
		return fmt.Sprintf("%d %s", n, plural(n, "day", "days"))
	case d%time.Hour == 0:
		n := int(d / time.Hour)
		return fmt.Sprintf("%d %s", n, plural(n, "hour", "hours"))
	default:
		n := int(d / time.Minute)
		return fmt.Sprintf("%d %s", n, plural(n, "minute", "minutes"))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// cleanNote trims the owner's note and refuses control and bidi characters.
func cleanNote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return "", web.Errorf(http.StatusBadRequest, "The note must be UTF-8 text.")
	}
	if utf8.RuneCountInString(s) > maxNoteRunes {
		return "", web.Errorf(http.StatusBadRequest, "The note can be at most %d characters.", maxNoteRunes)
	}
	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Bidi_Control, c) {
			return "", web.Errorf(http.StatusBadRequest, "The note can't contain control characters.")
		}
	}
	return s, nil
}

// creator is who is creating a secret: a principal, or anonymous.
type creator struct {
	p  *auth.Principal // nil: anonymous
	ip string
}

// authorizeCreate checks who may create and applies the create rate limits.
// It writes the error response and returns false when refused.
func (s *Service) authorizeCreate(w http.ResponseWriter, r *http.Request) (creator, bool) {
	c := creator{p: auth.FromContext(r.Context()), ip: clientip.From(r.Context()).String()}
	if c.p == nil {
		if !Anonymous.Get(s.d.Settings) {
			s.d.Errors.Unauthenticated(w, r)
			return c, false
		}
		if !s.exempt(r) {
			if ok, wait := s.anonIP.Allow("ip:" + c.ip); !ok {
				s.tooMany(w, r, wait)
				return c, false
			}
		}
		return c, true
	}
	if !c.p.Can(auth.ScopeSecretsWrite) {
		s.fail(w, r, http.StatusForbidden, "This needs the secrets:write permission.")
		return c, false
	}
	if !c.p.EmailVerified {
		s.fail(w, r, http.StatusForbidden, "Verify your email address before creating secrets.")
		return c, false
	}
	if ok, wait := s.createUser.Allow("user:" + strconv.FormatInt(c.p.UserID, 10)); !ok {
		s.tooMany(w, r, wait)
		return c, false
	}
	return c, true
}

// errTooManyOpen means the creator already has too many unfinalized uploads.
var errTooManyOpen = web.Errorf(http.StatusTooManyRequests, "Too many uploads in progress. Finish or wait for them first.")

// checkOpenUploads enforces the caps on unfinalized secrets, inside the
// create transaction so concurrent creates can't overshoot.
func (s *Service) checkOpenUploads(ctx context.Context, tx *sql.Tx, c creator, exempt bool) error {
	var n int
	if c.p != nil {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE state = 'uploading' AND owner_id = ?`,
			c.p.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= maxOpenUploadsPerUser {
			return errTooManyOpen
		}
	}
	if !exempt {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE state = 'uploading' AND upload_ip = ?`,
			c.ip).Scan(&n); err != nil {
			return err
		}
		if n >= maxOpenUploadsPerIP {
			return errTooManyOpen
		}
	}
	return nil
}

// newID returns a fresh secret id and its encoding.
func newID() ([e2e.IDSize]byte, string, error) {
	raw, err := e2e.NewID()
	if err != nil {
		return raw, "", err
	}
	return raw, e2e.Encode(raw[:]), nil
}

// newRow inserts an uploading secret, enforcing the open-upload caps.
func (s *Service) newRow(ctx context.Context, c creator, exempt bool, id, kind, mode string, ctSize, chunks int64, o options, tokenHash []byte) (time.Time, error) {
	now := s.now()
	exp := now.Add(o.expiry)
	var owner any
	if c.p != nil {
		owner = c.p.UserID
	}
	notify := 0
	if o.notify && c.p != nil {
		notify = 1
	}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.checkOpenUploads(ctx, tx, c, exempt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO secrets (id, owner_id, kind, mode, state, ct_size, chunk_count,
  views_left, max_views, max_failures, expires_at, note, notify, upload_token_hash, upload_ip, created_at, updated_at)
VALUES (?, ?, ?, ?, 'uploading', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, owner, kind, mode, ctSize, chunks, o.views, o.views, MaxFailures.Get(s.d.Settings),
			db.Millis(exp), o.note, notify, tokenHash, c.ip, db.Millis(now), db.Millis(now))
		return err
	})
	if err != nil {
		return time.Time{}, err
	}
	return exp, nil
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authorizeCreate(w, r)
	if !ok {
		return
	}
	var req createReq
	if err := web.DecodeJSON(w, r, 4<<10, &req); err != nil {
		s.apiErr(w, r, err)
		return
	}
	anon := c.p == nil
	switch req.Kind {
	case e2e.KindText:
		limit := int64(TextMaxBytes)
		if anon {
			limit = AnonymousMaxBytes.Get(s.d.Settings)
		}
		if req.Size < e2e.MinTextPad || req.Size > limit || int64(e2e.PaddedTextLen(int(req.Size))) != req.Size {
			s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("Text must be padded to a power of two from 1 KiB to %s.", web.HumanBytes(limit)))
			return
		}
	case e2e.KindFile:
		if anon {
			s.fail(w, r, http.StatusForbidden, "Sign in to share files.")
			return
		}
		if maxB := MaxBytes.Get(s.d.Settings); req.Size < 0 || req.Size > maxB {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "Files can be at most "+web.HumanBytes(maxB)+".")
			return
		}
	default:
		s.fail(w, r, http.StatusBadRequest, `Kind must be "text" or "file".`)
		return
	}
	if req.ExpiresIn < 0 || req.ExpiresIn > math.MaxInt64/int64(time.Second) {
		s.fail(w, r, http.StatusBadRequest, "Invalid expiry.")
		return
	}
	views, expiry, err := s.checkOptions(req.Views, time.Duration(req.ExpiresIn)*time.Second)
	if err != nil {
		s.apiErr(w, r, err)
		return
	}
	o := options{views: views, expiry: expiry, notify: req.Notify}
	if !anon {
		if o.note, err = cleanNote(req.Note); err != nil {
			s.apiErr(w, r, err)
			return
		}
	}

	var token [32]byte
	_, _ = rand.Read(token[:])
	th := sha256.Sum256(token[:])
	ctSize, chunks := e2e.CiphertextSize(req.Size), e2e.NumChunks(req.Size)
	_, id, err := newID()
	if err != nil {
		s.internal(w, r, "create", err)
		return
	}
	exp, err := s.newRow(r.Context(), c, s.exempt(r), id, req.Kind, "e2e", ctSize, chunks, o, th[:])
	if err != nil {
		if errors.Is(err, errTooManyOpen) {
			s.apiErr(w, r, err)
			return
		}
		s.internal(w, r, "create", err)
		return
	}
	web.JSON(w, http.StatusCreated, createResp{
		ID: id, UploadToken: e2e.Encode(token[:]), ChunkSize: e2e.ChunkSize,
		Chunks: chunks, CTSize: ctSize, ExpiresAt: exp.UTC().Format(time.RFC3339),
	})
}

// uploading loads a secret for the upload endpoints: it must be unfinalized,
// younger than UploadTTL, and the request must carry its upload token.
func (s *Service) uploading(r *http.Request, q querier) (*row, [32]byte, error) {
	id := r.PathValue("id")
	tok, err := e2e.Decode32(r.Header.Get(HeaderUploadToken))
	if err != nil || !validID(id) {
		return nil, tok, errNotFound
	}
	rw, err := load(r.Context(), q, id)
	if err != nil {
		return nil, tok, err
	}
	if rw.State != stateUploading || rw.Mode != "e2e" || !rw.tokenOK(tok) ||
		s.now().Sub(db.Time(rw.CreatedAt)) >= UploadTTL {
		return nil, tok, errNotFound
	}
	return rw, tok, nil
}

// ---- chunks ----

func (s *Service) putChunk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rw, _, err := s.uploading(r, s.d.DB.R)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "chunk", err)
		return
	}
	raw := r.PathValue("n")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || n >= rw.ChunkCount || strconv.FormatInt(n, 10) != raw {
		s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("Chunk number must be 0 to %d.", rw.ChunkCount-1))
		return
	}
	if n > rw.ChunksReceived {
		s.fail(w, r, http.StatusConflict, fmt.Sprintf("Send chunk %d next.", rw.ChunksReceived))
		return
	}
	want := rw.expectedChunkSize(n)
	if r.ContentLength >= 0 && r.ContentLength != want {
		s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("Chunk %d must be %d bytes.", n, want))
		return
	}
	body := http.MaxBytesReader(w, r.Body, want)

	if n < rw.ChunksReceived {
		// A resend of a chunk we have: fine if it is the same ciphertext.
		b, err := io.ReadAll(body)
		if err != nil || int64(len(b)) != want {
			s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("Chunk %d must be %d bytes.", n, want))
			return
		}
		s.sameChunk(w, r, rw.ID, n, sha256.Sum256(b))
		return
	}

	bw, err := s.d.Blobs.Create(ctx)
	if err != nil {
		s.storeErr(w, r, err)
		return
	}
	h := sha256.New()
	got, err := io.Copy(io.MultiWriter(bw, h), body)
	if err != nil || got != want {
		_ = bw.Abort()
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) || (err == nil && got != want) {
			s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("Chunk %d must be %d bytes.", n, want))
			return
		}
		if errors.Is(err, blob.ErrInsufficientSpace) {
			s.storeErr(w, r, err)
			return
		}
		s.fail(w, r, http.StatusBadRequest, "The upload was interrupted. Send the chunk again.")
		return
	}
	key, err := bw.Commit()
	if err != nil {
		s.storeErr(w, r, err)
		return
	}
	sum := h.Sum(nil)
	stored := false
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		cur, _, err := s.uploading(r, tx)
		if err != nil {
			return err
		}
		if cur.ChunksReceived != n {
			return nil // someone else stored it meanwhile; compare below
		}
		if err := insertPart(ctx, tx, cur.ID, part{Part: n, Key: key, Size: got, Sum: sum}); err != nil {
			return err
		}
		stored = true
		_, err = tx.ExecContext(ctx, `UPDATE secrets SET chunks_received = ?, updated_at = ? WHERE id = ?`,
			n+1, db.Millis(s.now()), cur.ID)
		return err
	})
	if !stored {
		_ = s.d.Blobs.Delete(context.WithoutCancel(ctx), key)
	}
	switch {
	case errors.Is(err, errNotFound):
		s.notFound(w, r)
	case err != nil:
		s.internal(w, r, "store chunk", err)
	case stored:
		web.JSON(w, http.StatusOK, map[string]int64{"next": n + 1})
	default:
		s.sameChunk(w, r, rw.ID, n, [32]byte(sum))
	}
}

// sameChunk answers a resend of chunk n: 200 when it matches what is
// stored, 409 otherwise (a client must never re-encrypt a chunk).
func (s *Service) sameChunk(w http.ResponseWriter, r *http.Request, id string, n int64, sum [32]byte) {
	var stored []byte
	err := s.d.DB.R.QueryRowContext(r.Context(), `SELECT sha256 FROM secret_blobs WHERE secret_id = ? AND part = ?`, id, n).Scan(&stored)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.notFound(w, r)
			return
		}
		s.internal(w, r, "chunk resend", err)
		return
	}
	if subtle.ConstantTimeCompare(stored, sum[:]) != 1 {
		s.fail(w, r, http.StatusConflict, fmt.Sprintf("Chunk %d was already received with different content.", n))
		return
	}
	web.JSON(w, http.StatusOK, map[string]int64{"next": n + 1})
}

func (s *Service) storeErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, blob.ErrInsufficientSpace) {
		s.fail(w, r, http.StatusInsufficientStorage, "The server is out of space for secrets.")
		return
	}
	s.internal(w, r, "blob store", err)
}

// ---- finalize ----

type finalizeReq struct {
	Salt   string         `json:"salt"`
	KDF    *e2e.KDFParams `json:"kdf"`
	HProbe string         `json:"h_probe"`
	HAuth  string         `json:"h_auth"`
	Commit string         `json:"commit"`
	MetaCT string         `json:"meta_ct"`
}

type finalizeResp struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
	Views     int    `json:"views"`
}

func (s *Service) finalize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rw, tok, err := s.uploading(r, s.d.DB.R)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "finalize", err)
		return
	}
	var req finalizeReq
	if err := web.DecodeJSON(w, r, 8<<10, &req); err != nil {
		s.apiErr(w, r, err)
		return
	}
	salt, err1 := e2e.Decode16(req.Salt)
	hProbe, err2 := e2e.Decode32(req.HProbe)
	hAuth, err3 := e2e.Decode32(req.HAuth)
	commit, err4 := e2e.Decode32(req.Commit)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		s.fail(w, r, http.StatusBadRequest, "salt must be 22 and h_probe, h_auth and commit 43 base64url characters.")
		return
	}
	metaCT, err := b64.DecodeString(req.MetaCT)
	if err != nil || len(metaCT) != e2e.SealedMetaSize {
		s.fail(w, r, http.StatusBadRequest, fmt.Sprintf("meta_ct must be %d bytes of base64url.", e2e.SealedMetaSize))
		return
	}
	var kdf any // NULL: no passphrase
	if req.KDF != nil {
		if err := req.KDF.Validate(); err != nil {
			s.fail(w, r, http.StatusBadRequest, "kdf parameters are out of bounds (m 19456 to 262144 KiB, t 1 to 10, p 1).")
			return
		}
		b, _ := json.Marshal(req.KDF)
		kdf = string(b)
	}
	if rw.ChunksReceived != rw.ChunkCount {
		s.fail(w, r, http.StatusConflict, fmt.Sprintf("Upload chunk %d first.", rw.ChunksReceived))
		return
	}

	meta, err := s.writeBlob(ctx, metaCT)
	if err != nil {
		s.storeErr(w, r, err)
		return
	}
	meta.Part = metaPart
	now := db.Millis(s.now())
	var out finalizeResp
	var expires int64
	th := sha256.Sum256(tok[:])
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE secrets SET state = 'ready', salt = ?, kdf = ?, h_probe = ?, h_auth = ?,
  commit_tag = ?, expires_at = ? + (expires_at - created_at), upload_token_hash = NULL, upload_ip = NULL, updated_at = ?
WHERE id = ? AND state = 'uploading' AND chunks_received = chunk_count AND upload_token_hash = ?`,
			salt[:], kdf, hProbe[:], hAuth[:], commit[:], now, now, rw.ID, th[:])
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, errNotFound)
		}
		if err := insertPart(ctx, tx, rw.ID, meta); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT expires_at, max_views FROM secrets WHERE id = ?`, rw.ID).Scan(&expires, &out.Views)
	})
	if err != nil {
		_ = s.d.Blobs.Delete(context.WithoutCancel(ctx), meta.Key)
		if errors.Is(err, errNotFound) {
			s.notFound(w, r)
			return
		}
		s.internal(w, r, "finalize", err)
		return
	}
	out.ID, out.ExpiresAt = rw.ID, ts(expires)
	web.JSON(w, http.StatusOK, out)
}

// ---- info ----

type probeReq struct {
	Probe string `json:"probe"`
}

type infoResp struct {
	State     string         `json:"state"`
	Mode      string         `json:"mode"`
	Kind      string         `json:"kind"`
	Salt      string         `json:"salt"`
	KDF       *e2e.KDFParams `json:"kdf"`
	Size      int64          `json:"size"`
	CTSize    int64          `json:"ct_size"`
	ViewsLeft int            `json:"views_left"`
	ExpiresAt string         `json:"expires_at"`
}

type goneResp struct {
	Error      string  `json:"error"`
	State      string  `json:"state"`
	OpenedAt   *string `json:"opened_at"`
	BurnReason string  `json:"burn_reason"`
}

// gone writes the tombstone: the state, when it was first opened, and why
// it is gone. Never who opened it.
func (s *Service) gone(w http.ResponseWriter, rw *row, state string) {
	out := goneResp{State: state, OpenedAt: tsPtr(rw.OpenedAt)}
	switch state {
	case stateExpired:
		out.Error, out.BurnReason = "This secret has expired.", reasonExpired
	case stateRevoked:
		out.Error, out.BurnReason = "The sender revoked this secret.", reasonRevoked
	default:
		out.BurnReason = rw.BurnReason.String
		if out.BurnReason == reasonFailures {
			out.Error = "This secret was destroyed after too many wrong passphrases."
		} else {
			out.Error = "This secret was already opened."
		}
	}
	web.JSON(w, http.StatusGone, out)
}

// linkHolder checks id and probe and returns the finalized row, or
// errNotFound for any failure. It reads from q (R pool or a transaction).
func linkHolder(ctx context.Context, q querier, id string, probe [32]byte) (*row, error) {
	rw, err := load(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if rw.State == stateUploading || !rw.probeOK(probe) {
		return nil, errNotFound
	}
	return rw, nil
}

func (s *Service) info(w http.ResponseWriter, r *http.Request) {
	if !s.allowIP(w, r) {
		return
	}
	var req probeReq
	if err := web.DecodeJSON(w, r, 1<<10, &req); err != nil {
		s.apiErr(w, r, err)
		return
	}
	id := r.PathValue("id")
	probe, err := e2e.Decode32(req.Probe)
	if err != nil || !validID(id) {
		s.notFound(w, r)
		return
	}
	rw, err := linkHolder(r.Context(), s.d.DB.R, id, probe)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "info", err)
		return
	}
	if !s.allowSecret(w, r, id) {
		return
	}
	s.record(r, rw, stats.KindLinkOpened, true)
	if st := rw.effectiveState(s.now()); st != stateReady {
		s.gone(w, rw, st)
		return
	}
	kdf, err := rw.kdf()
	if err != nil {
		s.internal(w, r, "info", err)
		return
	}
	web.JSON(w, http.StatusOK, infoResp{
		State: stateReady, Mode: rw.Mode, Kind: rw.Kind, Salt: e2e.Encode(rw.Salt), KDF: kdf,
		Size: rw.plainSize(), CTSize: rw.CTSize, ViewsLeft: rw.ViewsLeft, ExpiresAt: ts(rw.ExpiresAt),
	})
}

// ---- claim ----

type claimReq struct {
	Probe string `json:"probe"`
	Auth  string `json:"auth"`
}

type claimResp struct {
	Kind            string `json:"kind"`
	Commit          string `json:"commit"`
	MetaCT          string `json:"meta_ct"`
	ViewsLeft       int    `json:"views_left"`
	CTSize          int64  `json:"ct_size"`
	Content         string `json:"content,omitempty"` // text: the whole ciphertext
	Ticket          string `json:"ticket,omitempty"`  // file: for GET .../blob
	TicketExpiresAt string `json:"ticket_expires_at,omitempty"`
}

type wrongAuthResp struct {
	Error        string `json:"error"`
	AttemptsLeft int    `json:"attempts_left"`
	State        string `json:"state"`
}

// ticketTTL is max(15 min, the time to fetch ctSize bytes at 1 Mbit/s).
func ticketTTL(ctSize int64) time.Duration {
	return max(MinTicketTTL, time.Duration(ctSize/125_000+1)*time.Second)
}

// claimFiles reads what a successful claim returns: the sealed metadata
// and, for text, the content ciphertext.
func (s *Service) claimFiles(ctx context.Context, rw *row) (meta, content []byte, err error) {
	ps, err := s.parts(ctx, s.d.DB.R, rw.ID, metaPart)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range ps {
		switch {
		case p.Part == metaPart:
			if meta, err = s.readPart(ctx, p); err != nil {
				return nil, nil, err
			}
		case p.Part == 0 && rw.Kind == e2e.KindText:
			if content, err = s.readPart(ctx, p); err != nil {
				return nil, nil, err
			}
		}
	}
	if meta == nil || (rw.Kind == e2e.KindText && content == nil) {
		return nil, nil, blob.ErrNotFound
	}
	return meta, content, nil
}

func (s *Service) claim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.allowIP(w, r) {
		return
	}
	var req claimReq
	if err := web.DecodeJSON(w, r, 1<<10, &req); err != nil {
		s.apiErr(w, r, err)
		return
	}
	id := r.PathValue("id")
	probe, err := e2e.Decode32(req.Probe)
	if err != nil || !validID(id) {
		s.notFound(w, r)
		return
	}
	pre, err := linkHolder(ctx, s.d.DB.R, id, probe)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "claim", err)
		return
	}
	if !s.allowSecret(w, r, id) {
		return
	}
	if st := pre.effectiveState(s.now()); st != stateReady {
		s.gone(w, pre, st)
		return
	}
	authKey, err := e2e.Decode32(req.Auth)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "auth must be 43 base64url characters.")
		return
	}

	// Read the ciphertext before taking a view, so a read error never costs
	// the recipient a view, and only for claims that will succeed, so wrong
	// passphrases cost no disk reads. The transaction re-checks everything.
	var meta, content []byte
	if pre.authOK(authKey) {
		meta, content, err = s.claimFiles(ctx, pre)
		if err != nil {
			// The last view may have been taken (and the files deleted) since.
			if cur, lerr := load(ctx, s.d.DB.R, id); lerr == nil && cur.effectiveState(s.now()) != stateReady {
				s.gone(w, cur, cur.effectiveState(s.now()))
				return
			}
			s.internal(w, r, "claim read", err)
			return
		}
	}

	var (
		rw          *row
		goneState   string
		wrong, burn bool
		ticket      [32]byte
		ticketExp   time.Time
	)
	now := s.now()
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if rw, err = linkHolder(ctx, tx, id, probe); err != nil {
			return err
		}
		if st := rw.effectiveState(now); st != stateReady {
			goneState = st
			return nil
		}
		ms := db.Millis(now)
		if !rw.authOK(authKey) {
			wrong = true
			rw.Failures++
			burn = rw.Failures >= rw.MaxFailures
			if burn {
				rw.State = stateBurned
				_, err = tx.ExecContext(ctx, `UPDATE secrets SET failures = ?, state = 'burned', burned_at = ?, burn_reason = ?,
  updated_at = ? WHERE id = ?`, rw.Failures, ms, reasonFailures, ms, id)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE secrets SET failures = ?, updated_at = ? WHERE id = ?`, rw.Failures, ms, id)
			}
			return err
		}
		if meta == nil {
			return errors.New("secrets: auth matched only inside the transaction")
		}
		rw.ViewsLeft--
		burn = rw.ViewsLeft == 0
		if !rw.OpenedAt.Valid {
			rw.OpenedAt = sql.NullInt64{Int64: ms, Valid: true}
		}
		state, reason, burnedAt := stateReady, any(nil), any(nil)
		if burn {
			state, reason, burnedAt = stateBurned, reasonViewed, ms
			rw.State = stateBurned
		}
		if _, err := tx.ExecContext(ctx, `UPDATE secrets SET views_left = ?, opened_at = ?, state = ?, burn_reason = ?,
  burned_at = ?, updated_at = ? WHERE id = ?`, rw.ViewsLeft, rw.OpenedAt.Int64, state, reason, burnedAt, ms, id); err != nil {
			return err
		}
		if rw.Kind == e2e.KindFile {
			_, _ = rand.Read(ticket[:])
			ticketExp = now.Add(ticketTTL(rw.CTSize))
			th := sha256.Sum256(ticket[:])
			if _, err := tx.ExecContext(ctx, `INSERT INTO secret_tickets (hash, secret_id, expires_at) VALUES (?, ?, ?)`,
				th[:], id, db.Millis(ticketExp)); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, errNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.internal(w, r, "claim", err)
		return
	case goneState != "":
		s.gone(w, rw, goneState)
		return
	}

	if wrong {
		s.record(r, rw, stats.KindFailed, false)
		if burn {
			s.record(r, rw, stats.KindBurned, false)
			s.notify(r, rw, stats.KindBurned, reasonFailures)
			if n, err := liveTickets(ctx, s.d.DB.R, id, now); err == nil && n == 0 {
				_ = s.purgeBlobs(ctx, id, purgeAll)
			}
		}
		web.JSON(w, http.StatusForbidden, wrongAuthResp{
			Error: "Wrong passphrase.", AttemptsLeft: max(0, rw.MaxFailures-rw.Failures), State: rw.State,
		})
		return
	}

	s.record(r, rw, stats.KindRevealed, false)
	if burn {
		s.record(r, rw, stats.KindBurned, false)
	}
	if rw.Notify {
		s.notify(r, rw, stats.KindRevealed, "")
	}
	out := claimResp{
		Kind: rw.Kind, Commit: e2e.Encode(rw.Commit), MetaCT: e2e.Encode(meta),
		ViewsLeft: rw.ViewsLeft, CTSize: rw.CTSize,
	}
	if rw.Kind == e2e.KindText {
		out.Content = e2e.Encode(content)
	} else {
		out.Ticket = e2e.Encode(ticket[:])
		out.TicketExpiresAt = ticketExp.UTC().Format(time.RFC3339)
	}
	web.JSON(w, http.StatusOK, out)
	if burn {
		// The last view: the text ciphertext (and the metadata, which the
		// recipient now holds) go as soon as the response is out. A file's
		// content stays for its ticket, until done or the ticket expires.
		_ = http.NewResponseController(w).Flush()
		which := purgeAll
		if rw.Kind == e2e.KindFile {
			which = purgeMeta
		}
		_ = s.purgeBlobs(ctx, id, which)
	}
}

// ---- file download ----

// ticketed checks the X-Snap-Ticket header against the secret in the path.
func (s *Service) ticketed(r *http.Request) (id string, hash [32]byte, err error) {
	id = r.PathValue("id")
	t, derr := e2e.Decode32(r.Header.Get(HeaderTicket))
	if derr != nil || !validID(id) {
		return "", hash, errNotFound
	}
	hash = sha256.Sum256(t[:])
	var exp int64
	err = s.d.DB.R.QueryRowContext(r.Context(), `SELECT expires_at FROM secret_tickets WHERE hash = ? AND secret_id = ?`,
		hash[:], id).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && exp <= db.Millis(s.now())) {
		return "", hash, errNotFound
	}
	if err != nil {
		return "", hash, fmt.Errorf("secrets: ticket: %w", err)
	}
	return id, hash, nil
}

func (s *Service) blob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _, err := s.ticketed(r)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "blob", err)
		return
	}
	rw, err := load(ctx, s.d.DB.R, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	ps, err := s.parts(ctx, s.d.DB.R, id, 0)
	if err != nil {
		s.internal(w, r, "blob", err)
		return
	}
	var total int64
	for i, p := range ps {
		if p.Part != int64(i) {
			total = -1
			break
		}
		total += p.Size
	}
	if int64(len(ps)) != rw.ChunkCount || total != rw.CTSize {
		s.notFound(w, r)
		return
	}
	rd := newPartsReader(ctx, s.d.Blobs, ps)
	defer func() { _ = rd.Close() }()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", "attachment")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-Robots-Tag", "noindex")
	http.ServeContent(w, r, "", time.Time{}, rd)
}

func (s *Service) done(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, hash, err := s.ticketed(r)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "done", err)
		return
	}
	now := s.now()
	purge := false
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM secret_tickets WHERE hash = ? AND secret_id = ? AND expires_at > ?`,
			hash[:], id, db.Millis(now))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, errNotFound)
		}
		rw, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		left, err := liveTickets(ctx, tx, id, now)
		if err != nil {
			return err
		}
		purge = rw.effectiveState(now) != stateReady && left == 0
		return nil
	})
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "done", err)
		return
	}
	if purge {
		_ = s.purgeBlobs(ctx, id, purgeAll)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// ---- owner ----

func (s *Service) revokeAPI(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id := r.PathValue("id")
	if !validID(id) {
		s.notFound(w, r)
		return
	}
	state, ok, err := s.Revoke(r.Context(), id, p.UserID)
	if err != nil {
		s.internal(w, r, "revoke", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	web.JSON(w, http.StatusOK, map[string]string{"state": state})
}

// Summary is an owner's view of one secret: metadata only.
type Summary struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Mode        string  `json:"mode"`
	State       string  `json:"state"`
	Note        string  `json:"note"`
	Passphrase  bool    `json:"passphrase"`
	Size        int64   `json:"size"`
	ViewsLeft   int     `json:"views_left"`
	MaxViews    int     `json:"max_views"`
	Failures    int     `json:"failures"`
	MaxFailures int     `json:"max_failures"`
	Notify      bool    `json:"notify"`
	ExpiresAt   string  `json:"expires_at"`
	CreatedAt   string  `json:"created_at"`
	OpenedAt    *string `json:"opened_at"`
	BurnedAt    *string `json:"burned_at"`
	BurnReason  string  `json:"burn_reason"`
	ViewsUsed   int     `json:"views_used"`

	// For templates.
	Opened, Created, Expires time.Time `json:"-"`
}

func (s *Service) summary(rw *row) Summary {
	st := rw.effectiveState(s.now())
	reason := rw.BurnReason.String
	if st == stateExpired && reason == "" {
		reason = reasonExpired
	}
	sm := Summary{
		ID: rw.ID, Kind: rw.Kind, Mode: rw.Mode, State: st, Note: rw.Note, Passphrase: rw.KDF.Valid,
		Size: rw.plainSize(), ViewsLeft: rw.ViewsLeft, MaxViews: rw.MaxViews, Failures: rw.Failures,
		MaxFailures: rw.MaxFailures, Notify: rw.Notify, ExpiresAt: ts(rw.ExpiresAt), CreatedAt: ts(rw.CreatedAt),
		OpenedAt: tsPtr(rw.OpenedAt), BurnedAt: tsPtr(rw.BurnedAt), BurnReason: reason,
		ViewsUsed: rw.MaxViews - rw.ViewsLeft, Created: db.Time(rw.CreatedAt), Expires: db.Time(rw.ExpiresAt),
	}
	if rw.OpenedAt.Valid {
		sm.Opened = db.Time(rw.OpenedAt.Int64)
	}
	return sm
}

// ownerList returns a page of the owner's secrets, newest first, and the total.
func (s *Service) ownerList(ctx context.Context, owner int64, limit, offset int) ([]Summary, int, error) {
	var total int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE owner_id = ?`, owner).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("secrets: count: %w", err)
	}
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT `+rowCols+` FROM secrets WHERE owner_id = ?
ORDER BY created_at DESC, id LIMIT ? OFFSET ?`, owner, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("secrets: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Summary
	for rows.Next() {
		rw, err := scanRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("secrets: list: %w", err)
		}
		out = append(out, s.summary(rw))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("secrets: list: %w", err)
	}
	return out, total, nil
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	q := r.URL.Query()
	limit, offset := 50, 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			s.fail(w, r, http.StatusBadRequest, "limit must be 1 to 200.")
			return
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			s.fail(w, r, http.StatusBadRequest, "offset must be 0 or more.")
			return
		}
		offset = n
	}
	items, total, err := s.ownerList(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.internal(w, r, "list", err)
		return
	}
	if items == nil {
		items = []Summary{}
	}
	web.JSON(w, http.StatusOK, map[string]any{"secrets": items, "total": total})
}

// Event is one entry of an owner's timeline.
type Event struct {
	At        string `json:"at"`
	Kind      string `json:"kind"`
	IP        string `json:"ip"`
	UA        string `json:"user_agent"`
	UASummary string `json:"ua_summary"`
	Bot       string `json:"bot"`

	Time time.Time `json:"-"` // for templates
}

// ownerEvents returns the latest events of a secret the owner holds, or
// errNotFound.
func (s *Service) ownerEvents(ctx context.Context, owner int64, id string, limit int) ([]Event, error) {
	var got int64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT owner_id FROM secrets WHERE id = ? AND owner_id = ?`, id, owner).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: events: %w", err)
	}
	evs, err := stats.LatestSecretEvents(ctx, s.d.DB.R, id, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		out = append(out, Event{At: e.At.Format(time.RFC3339), Kind: string(e.Kind), IP: e.IP, UA: e.UA,
			UASummary: e.UASummary, Bot: e.Bot, Time: e.At})
	}
	return out, nil
}

func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id := r.PathValue("id")
	if !validID(id) {
		s.notFound(w, r)
		return
	}
	evs, err := s.ownerEvents(r.Context(), p.UserID, id, 200)
	if errors.Is(err, errNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.internal(w, r, "events", err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{"events": evs})
}
