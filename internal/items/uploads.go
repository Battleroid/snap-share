package items

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

// upState is the in-memory side of a chunked upload: a lock serialising
// requests for one id, and the running hash (lost on restart, then the
// partial is rehashed from disk at completion).
type upState struct {
	mu     sync.Mutex
	h      hash.Hash
	hashed int64
}

func (s *Service) lockUpload(id string) *upState {
	s.upMu.Lock()
	st := s.ups[id]
	if st == nil {
		st = &upState{}
		s.ups[id] = st
	}
	s.upMu.Unlock()
	st.mu.Lock()
	return st
}

func (s *Service) tryLockUpload(id string) (*upState, bool) {
	s.upMu.Lock()
	st := s.ups[id]
	if st == nil {
		st = &upState{}
		s.ups[id] = st
	}
	s.upMu.Unlock()
	return st, st.mu.TryLock()
}

func (s *Service) unlockUpload(id string, st *upState, forget bool) {
	if forget {
		s.upMu.Lock()
		if s.ups[id] == st {
			delete(s.ups, id)
		}
		s.upMu.Unlock()
	}
	st.mu.Unlock()
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

type upload struct {
	ID       string
	OwnerID  int64
	Filename string
	Size     int64
	Reserved int64
	TempRef  string
	Options  resolved
	Expires  time.Time
}

func (s *Service) getUpload(ctx context.Context, id string, owner int64) (*upload, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	var u upload
	var opts string
	var ref sql.NullString
	var exp int64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT id, owner_id, filename, size, reserved, temp_ref, options, expires_at
		FROM uploads WHERE id = ? AND owner_id = ? AND kind = 'chunked'`, id, owner).
		Scan(&u.ID, &u.OwnerID, &u.Filename, &u.Size, &u.Reserved, &ref, &opts, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TempRef, u.Expires = ref.String, db.Time(exp)
	if err := json.Unmarshal([]byte(opts), &u.Options); err != nil {
		return nil, fmt.Errorf("upload options: %w", err)
	}
	return &u, nil
}

// extendDeadline gives a long upload its own read/write budget.
func extendDeadline(w http.ResponseWriter, d time.Duration) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(d))
	_ = rc.SetWriteDeadline(time.Now().Add(d))
}

func conflict(w http.ResponseWriter, cur int64, msg string) {
	w.Header().Set("Upload-Offset", strconv.FormatInt(cur, 10))
	web.JSON(w, http.StatusConflict, map[string]any{"error": msg, "offset": cur})
}

type initReq struct {
	Name    string  `json:"name"`
	Size    int64   `json:"size"`
	Type    string  `json:"type"`
	Options options `json:"options"`
}

// uploadInit declares a chunked upload and reserves its size against the quota.
func (s *Service) uploadInit(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	if err := s.allowCreate(w, r, p); err != nil {
		return err
	}
	ctx := r.Context()
	var req initReq
	if err := web.DecodeJSON(w, r, 64<<10, &req); err != nil {
		return err
	}
	name := SanitizeFilename(req.Name)
	if name == "" {
		name = "file"
	}
	if req.Size < 0 {
		return web.Errorf(http.StatusBadRequest, "Size must not be negative.")
	}
	lim, err := s.userLimits(ctx, s.d.DB.R, p.UserID)
	if err != nil {
		return err
	}
	if req.Size > lim.maxUpload {
		return tooLarge(lim.maxUpload)
	}
	if s.blockedName(name) || (req.Type != "" && s.blockedType(req.Type)) {
		return errBlocked
	}
	if req.Size > lim.remaining() {
		return errQuota
	}
	if err := s.d.Blobs.CheckFree(req.Size); err != nil {
		return err
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	opts, err := s.resolve(ctx, p.UserID, prefs, req.Options, KindFile)
	if err != nil {
		return err
	}
	optJSON, _ := json.Marshal(opts)
	partial, err := s.d.Blobs.CreatePartial(ctx)
	if err != nil {
		return err
	}
	id := randomID()
	now := s.now()
	exp := now.Add(UploadTTL)
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.reserveTx(ctx, tx, p.UserID, req.Size, true); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO uploads (id, owner_id, kind, filename, declared_type, size, reserved, temp_ref, options, created_at, updated_at, expires_at)
			VALUES (?, ?, 'chunked', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.UserID, name, cleanText(req.Type, 128), req.Size, req.Size, partial, string(optJSON), db.Millis(now), db.Millis(now), db.Millis(exp))
		return err
	})
	if err != nil {
		_ = s.d.Blobs.RemovePartial(partial)
		return err
	}
	st := s.lockUpload(id)
	st.h, st.hashed = sha256.New(), 0
	s.unlockUpload(id, st, false)
	web.JSON(w, http.StatusCreated, map[string]any{
		"id": id, "part_size": PartSize, "offset": 0, "size": req.Size, "name": name, "expires_at": exp.UTC(),
	})
	return nil
}

// uploadPut appends one part at ?offset=N.
func (s *Service) uploadPut(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	id := r.PathValue("id")
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		return web.Errorf(http.StatusBadRequest, "Send ?offset= with the byte offset of this part.")
	}
	if r.ContentLength < 0 {
		return web.Errorf(http.StatusLengthRequired, "Send Content-Length with each part.")
	}
	// Ownership first, so lock entries exist only for real uploads.
	up, err := s.getUpload(ctx, id, p.UserID)
	if err != nil {
		return err
	}
	st := s.lockUpload(id)
	defer s.unlockUpload(id, st, false)
	cur, err := s.d.Blobs.PartialSize(up.TempRef)
	if errors.Is(err, blob.ErrNotFound) {
		return web.Errorf(http.StatusNotFound, "This upload has expired.")
	}
	if err != nil {
		return err
	}
	if offset != cur {
		conflict(w, cur, "Wrong offset; resume from the current one.")
		return nil
	}
	if r.ContentLength > up.Size-cur {
		return web.Errorf(http.StatusRequestEntityTooLarge, "This part goes past the declared size.")
	}
	extendDeadline(w, 15*time.Minute)
	var src io.Reader = http.MaxBytesReader(w, r.Body, up.Size-cur)
	hashing := st.h != nil && st.hashed == cur
	if hashing {
		src = io.TeeReader(src, st.h)
	} else {
		st.h = nil
	}
	n, werr := s.d.Blobs.AppendPartial(ctx, up.TempRef, cur, src)
	if hashing {
		if werr != nil {
			st.h = nil // the hash may include bytes that were not written
		} else {
			st.hashed += n
		}
	}
	now := s.now()
	uctx := context.WithoutCancel(ctx)
	if n > 0 {
		if _, err := s.d.DB.W.ExecContext(uctx, `UPDATE uploads SET received = ?, updated_at = ?, expires_at = ? WHERE id = ?`,
			cur+n, db.Millis(now), db.Millis(now.Add(UploadTTL)), id); err != nil {
			return err
		}
	}
	var oe *local.OffsetError
	switch {
	case werr == nil:
	case errors.Is(werr, blob.ErrInsufficientSpace):
		return werr
	case errors.As(werr, &oe):
		conflict(w, oe.Current, "Wrong offset; resume from the current one.")
		return nil
	default:
		if ctx.Err() != nil {
			return context.Canceled
		}
		conflict(w, cur+n, "The part was cut short; resume from the current offset.")
		return nil
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(cur+n, 10))
	web.JSON(w, http.StatusOK, map[string]any{"offset": cur + n, "size": up.Size})
	return nil
}

func (s *Service) uploadStatusOf(r *http.Request) (*upload, int64, error) {
	p, err := creator(r)
	if err != nil {
		return nil, 0, err
	}
	up, err := s.getUpload(r.Context(), r.PathValue("id"), p.UserID)
	if err != nil {
		return nil, 0, err
	}
	cur, err := s.d.Blobs.PartialSize(up.TempRef)
	if errors.Is(err, blob.ErrNotFound) {
		return nil, 0, web.Errorf(http.StatusNotFound, "This upload has expired.")
	}
	return up, cur, err
}

func (s *Service) uploadHead(w http.ResponseWriter, r *http.Request) error {
	up, cur, err := s.uploadStatusOf(r)
	if err != nil {
		var ae *web.APIError
		if errors.As(err, &ae) {
			w.WriteHeader(ae.Status)
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return nil
		}
		return err
	}
	h := w.Header()
	h.Set("Upload-Offset", strconv.FormatInt(cur, 10))
	h.Set("Upload-Length", strconv.FormatInt(up.Size, 10))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Service) uploadStatus(w http.ResponseWriter, r *http.Request) error {
	up, cur, err := s.uploadStatusOf(r)
	if err != nil {
		return err
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(cur, 10))
	web.JSON(w, http.StatusOK, map[string]any{
		"id": up.ID, "name": up.Filename, "size": up.Size, "offset": cur, "part_size": PartSize, "expires_at": up.Expires,
	})
	return nil
}

// hashPartial returns the SHA-256 and first 512 bytes of a finished partial,
// using the running hash when it covers every byte.
func (s *Service) hashPartial(st *upState, name string, size int64) (string, []byte, error) {
	f, err := s.d.Blobs.OpenPartial(name)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", nil, err
	}
	head = head[:n]
	if st.h != nil && st.hashed == size {
		return hex.EncodeToString(st.h.Sum(nil)), head, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), head, nil
}

// uploadComplete turns a fully received upload into an item.
func (s *Service) uploadComplete(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	id := r.PathValue("id")
	up, err := s.getUpload(ctx, id, p.UserID)
	if err != nil {
		return err
	}
	st := s.lockUpload(id)
	forget := false
	defer func() { s.unlockUpload(id, st, forget) }()
	cur, err := s.d.Blobs.PartialSize(up.TempRef)
	if errors.Is(err, blob.ErrNotFound) {
		s.dropUpload(ctx, up.ID, up.OwnerID, up.Reserved)
		forget = true
		return web.Errorf(http.StatusNotFound, "This upload has expired.")
	}
	if err != nil {
		return err
	}
	if cur != up.Size {
		conflict(w, cur, "The upload is not complete.")
		return nil
	}
	sum, head, err := s.hashPartial(st, up.TempRef, cur)
	if err != nil {
		return err
	}
	key, err := s.d.Blobs.CommitPartial(ctx, up.TempRef)
	if err != nil {
		return err
	}
	forget = true
	it, tok, err := s.finishFile(ctx, p.UserID, up.ID, up.Reserved, fileIn{key: key, size: cur, sum: sum, head: head, filename: up.Filename}, up.Options)
	if err != nil {
		return err
	}
	s.respondCreated(w, r, it, tok)
	return nil
}

// uploadAbort discards an upload and releases its reservation.
func (s *Service) uploadAbort(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	id := r.PathValue("id")
	up, err := s.getUpload(ctx, id, p.UserID)
	if err != nil {
		return err
	}
	st := s.lockUpload(id)
	defer s.unlockUpload(id, st, true)
	s.dropUpload(ctx, up.ID, up.OwnerID, up.Reserved)
	if err := s.d.Blobs.RemovePartial(up.TempRef); err != nil {
		s.log.Warn("remove partial", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ReapUploads drops uploads idle past UploadTTL, releasing their reservations
// and partial files.
func (s *Service) ReapUploads(ctx context.Context) error {
	now := db.Millis(s.now())
	type dead struct {
		id       string
		owner    int64
		reserved int64
		ref      string
	}
	list, err := queryAll(ctx, s.d.DB.R, `SELECT id, owner_id, reserved, coalesce(temp_ref, '') FROM uploads WHERE expires_at <= ?`,
		func(rows *sql.Rows) (dead, error) {
			var d dead
			err := rows.Scan(&d.id, &d.owner, &d.reserved, &d.ref)
			return d, err
		}, now)
	if err != nil {
		return err
	}
	for _, d := range list {
		st, ok := s.tryLockUpload(d.id)
		if !ok {
			continue // a request is using it right now
		}
		err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND expires_at <= ?`, d.id, now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			return releaseTx(ctx, tx, d.owner, d.reserved)
		})
		if err == nil && d.ref != "" {
			err = s.d.Blobs.RemovePartial(d.ref)
		}
		s.unlockUpload(d.id, st, true)
		if err != nil {
			return err
		}
	}
	return nil
}

// Streamed uploads (multipart and raw PUT).

// multipartSlack allows for boundaries and option fields around the file.
const multipartSlack = 1 << 20

// beginStream reserves quota for a streamed upload and records it.
func (s *Service) beginStream(ctx context.Context, owner, want int64, exact bool) (string, int64, error) {
	id := randomID()
	var granted int64
	now := s.now()
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if granted, err = s.reserveTx(ctx, tx, owner, want, exact); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO uploads (id, owner_id, kind, size, reserved, created_at, updated_at, expires_at)
			VALUES (?, ?, 'stream', ?, ?, ?, ?, ?)`, id, owner, granted, granted, db.Millis(now), db.Millis(now), db.Millis(now.Add(UploadTTL)))
		return err
	})
	return id, granted, err
}

// streamToBlob copies at most limit bytes of r into a new blob.
func (s *Service) streamToBlob(ctx context.Context, r io.Reader, limit, maxUpload int64) (fileIn, error) {
	w, err := s.d.Blobs.Create(ctx)
	if err != nil {
		return fileIn{}, err
	}
	h := sha256.New()
	hd := &headWriter{max: 512}
	lr := &io.LimitedReader{R: r, N: limit + 1}
	n, err := io.CopyBuffer(io.MultiWriter(w, h, hd), lr, make([]byte, 256<<10))
	if err != nil {
		_ = w.Abort()
		return fileIn{}, err
	}
	if n > limit {
		_ = w.Abort()
		if limit < maxUpload {
			return fileIn{}, errQuota
		}
		return fileIn{}, tooLarge(maxUpload)
	}
	key, err := w.Commit()
	if err != nil {
		return fileIn{}, err
	}
	return fileIn{key: key, size: n, sum: hex.EncodeToString(h.Sum(nil)), head: hd.b}, nil
}

type headWriter struct {
	b   []byte
	max int
}

func (h *headWriter) Write(p []byte) (int, error) {
	if room := h.max - len(h.b); room > 0 {
		h.b = append(h.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// streamLimits works out the reservation for a body of unknown or declared length.
func (s *Service) streamLimits(ctx context.Context, owner, contentLength, slack int64) (limits, int64, bool, error) {
	lim, err := s.userLimits(ctx, s.d.DB.R, owner)
	if err != nil {
		return lim, 0, false, err
	}
	if contentLength > lim.maxUpload+slack {
		return lim, 0, false, tooLarge(lim.maxUpload)
	}
	if contentLength >= 0 {
		return lim, min(contentLength, lim.maxUpload), true, nil
	}
	return lim, lim.maxUpload, false, nil
}

// uploadMultipart handles POST /api/v1/upload (multipart/form-data, field "file").
// The body is streamed part by part; nothing is spooled to temp files.
func (s *Service) uploadMultipart(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	if err := s.allowCreate(w, r, p); err != nil {
		return err
	}
	ctx := r.Context()
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "multipart/form-data" {
		return web.Errorf(http.StatusUnsupportedMediaType, "Send multipart/form-data with the file in a field named file.")
	}
	lim, want, exact, err := s.streamLimits(ctx, p.UserID, r.ContentLength, multipartSlack)
	if err != nil {
		return err
	}
	if err := s.d.Blobs.CheckFree(want); err != nil {
		return err
	}
	upID, granted, err := s.beginStream(ctx, p.UserID, want, exact)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			s.dropUpload(context.WithoutCancel(ctx), upID, p.UserID, granted)
		}
	}()
	r.Body = http.MaxBytesReader(w, r.Body, granted+multipartSlack)
	extendDeadline(w, 2*time.Hour)
	mr, err := r.MultipartReader()
	if err != nil {
		return web.Errorf(http.StatusBadRequest, "Malformed multipart body.")
	}
	var (
		o        options
		name     string
		file     *fileIn
		partName string
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return tooLarge(lim.maxUpload)
			}
			return web.Errorf(http.StatusBadRequest, "Malformed multipart body.")
		}
		field := part.FormName()
		switch {
		case field == "file" && file == nil:
			f, err := s.streamToBlob(ctx, part, granted, lim.maxUpload)
			if err != nil {
				_ = part.Close()
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					return tooLarge(lim.maxUpload)
				}
				return err
			}
			file, partName = &f, part.FileName()
		case field == "name" || optionFields[field]:
			v, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil {
				_ = part.Close()
				return web.Errorf(http.StatusBadRequest, "Malformed multipart body.")
			}
			if len(v) > 4096 {
				_ = part.Close()
				return web.Errorf(http.StatusBadRequest, "Field %s is too long.", field)
			}
			if field == "name" {
				name = string(v)
			} else {
				o.set(field, string(v))
			}
		default:
			_, _ = io.Copy(io.Discard, part)
		}
		_ = part.Close()
	}
	if file == nil {
		return web.Errorf(http.StatusBadRequest, "Send the file in a field named file.")
	}
	cleanup := func() { _ = s.d.Blobs.Delete(context.WithoutCancel(ctx), file.key) }
	if name == "" {
		name = partName
	}
	file.filename = SanitizeFilename(name)
	if file.filename == "" {
		file.filename = "file"
	}
	if s.blockedName(file.filename) {
		cleanup()
		return errBlocked
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		cleanup()
		return err
	}
	opts, err := s.resolve(ctx, p.UserID, prefs, o, KindFile)
	if err != nil {
		cleanup()
		return err
	}
	done = true // finishFile settles or releases the reservation
	it, tok, err := s.finishFile(ctx, p.UserID, upID, granted, *file, opts)
	if err != nil {
		return err
	}
	s.respondCreated(w, r, it, tok)
	return nil
}

var optionFields = map[string]bool{
	"title": true, "expires": true, "expiry": true, "expiry_action": true, "visibility": true,
	"password": true, "naming": true, "slug": true, "length": true,
}

// PasswordHeader carries an item password for raw-body requests, which keep
// other options in the query string.
const PasswordHeader = "X-Snap-Password"

func queryOptions(r *http.Request) options {
	var o options
	for k, v := range r.URL.Query() {
		if k != "password" && len(v) > 0 {
			o.set(k, v[0])
		}
	}
	o.Password = r.Header.Get(PasswordHeader)
	return o
}

// uploadPutRaw handles PUT /api/v1/upload/{filename} with the file as the body (curl -T).
func (s *Service) uploadPutRaw(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	if err := s.allowCreate(w, r, p); err != nil {
		return err
	}
	ctx := r.Context()
	name := SanitizeFilename(r.PathValue("filename"))
	if name == "" {
		name = "file"
	}
	if s.blockedName(name) {
		return errBlocked
	}
	lim, want, exact, err := s.streamLimits(ctx, p.UserID, r.ContentLength, 0)
	if err != nil {
		return err
	}
	if err := s.d.Blobs.CheckFree(want); err != nil {
		return err
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	opts, err := s.resolve(ctx, p.UserID, prefs, queryOptions(r), KindFile)
	if err != nil {
		return err
	}
	upID, granted, err := s.beginStream(ctx, p.UserID, want, exact)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			s.dropUpload(context.WithoutCancel(ctx), upID, p.UserID, granted)
		}
	}()
	extendDeadline(w, 2*time.Hour)
	file, err := s.streamToBlob(ctx, http.MaxBytesReader(w, r.Body, granted+1), granted, lim.maxUpload)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			if granted < lim.maxUpload {
				return errQuota
			}
			return tooLarge(lim.maxUpload)
		}
		return err
	}
	file.filename = name
	done = true
	it, tok, err := s.finishFile(ctx, p.UserID, upID, granted, file, opts)
	if err != nil {
		return err
	}
	s.respondCreated(w, r, it, tok)
	return nil
}
