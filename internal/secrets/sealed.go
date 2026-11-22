package secrets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"hash"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/web"
)

// Sealed mode: for clients that can't encrypt (curl, ShareX). The server
// makes K_link and S, encrypts the upload while it streams in, in the same
// envelope as the browser and sp, returns the link and forgets the key. It
// never logs, stores or processes the plaintext: no sniffing, thumbnails or
// type checks beyond copying the client's file name and type into the
// sealed metadata.

type sealedResp struct {
	URL       string `json:"url"`
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
	Views     int    `json:"views"`
}

// parseExpiry reads "300" (seconds), "5m", "1h", "1d", "7d" or any Go
// duration. Empty means the default.
func parseExpiry(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return 0, nil
	case strings.HasSuffix(v, "d"):
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n < 1 || n > 3650 {
			return 0, errors.New("bad expiry")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 1 || n > 3650*24*3600 {
			return 0, errors.New("bad expiry")
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("bad expiry")
	}
	return d, nil
}

// sealedOpts collects views and expires from the query and form fields.
type sealedOpts struct {
	views  int
	expiry time.Duration
}

func (o *sealedOpts) set(name, value string) error {
	switch name {
	case "views":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 {
			return web.Errorf(http.StatusBadRequest, "views must be a whole number.")
		}
		o.views = n
	case "expires":
		d, err := parseExpiry(value)
		if err != nil {
			return web.Errorf(http.StatusBadRequest, "expires must be like 5m, 1h, 1d or 7d.")
		}
		o.expiry = d
	}
	return nil
}

// sealedUpload is one sealed create in progress. The secret row exists in
// state uploading from the first content byte, and every stored chunk is in
// the ledger, so a crash or abort leaves nothing the sweeper can't find.
type sealedUpload struct {
	s     *Service
	ctx   context.Context
	c     creator
	rawID [e2e.IDSize]byte
	id    string
	key   [e2e.KeySize]byte
	salt  [e2e.SaltSize]byte
	keys  e2e.Keys
	row   bool
}

func (u *sealedUpload) wipe() {
	clear(u.key[:])
	u.keys = e2e.Keys{}
}

func (s *Service) beginSealed(ctx context.Context, c creator, exempt bool, kind string) (*sealedUpload, error) {
	u := &sealedUpload{s: s, ctx: ctx, c: c}
	var err error
	if u.rawID, u.id, err = newID(); err != nil {
		return nil, err
	}
	if u.key, err = e2e.NewLinkKey(); err != nil {
		return nil, err
	}
	if u.salt, err = e2e.NewSalt(); err != nil {
		return nil, err
	}
	if u.keys, err = e2e.DeriveKeys(u.key, u.rawID, u.salt, nil, e2e.KDFParams{}); err != nil {
		return nil, err
	}
	o := options{views: 1, expiry: s.defaultExpiry()}
	if _, err := s.newRow(ctx, c, exempt, u.id, kind, "sealed", e2e.TagSize, 1, 0, o, nil); err != nil {
		u.wipe()
		return nil, err
	}
	u.row = true
	return u, nil
}

// abort deletes everything stored so far.
func (u *sealedUpload) abort() {
	u.wipe()
	if !u.row {
		return
	}
	ctx := context.WithoutCancel(u.ctx)
	if err := u.s.purgeBlobs(ctx, u.id, purgeAll); err != nil {
		return // the sweeper removes the row and files after UploadTTL
	}
	_ = u.s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE id = ? AND state = 'uploading'`, u.id)
		return err
	})
}

// chunkSink cuts the STREAM ciphertext into chunk-sized blobs and records
// each in the ledger, and charges it to the creator's quota, as it is
// committed.
type chunkSink struct {
	u     *sealedUpload
	w     blob.Writer
	h     hash.Hash
	n     int64 // bytes in the current blob
	parts int64
	total int64
}

func (k *chunkSink) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if k.w == nil {
			w, err := k.u.s.d.Blobs.Create(k.u.ctx)
			if err != nil {
				return written, err
			}
			k.w, k.h, k.n = w, sha256.New(), 0
		}
		m := min(int64(len(p)), fullChunk-k.n)
		if _, err := k.w.Write(p[:m]); err != nil {
			return written, err
		}
		k.h.Write(p[:m])
		k.n += m
		k.total += m
		written += int(m)
		p = p[m:]
		if k.n == fullChunk {
			if err := k.commit(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (k *chunkSink) commit() error {
	w := k.w
	k.w = nil
	key, err := w.Commit()
	if err != nil {
		return err
	}
	ctx := k.u.ctx
	p := part{Part: k.parts, Key: key, Size: k.n, Sum: k.h.Sum(nil)}
	if err := k.u.s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := k.u.s.reserveTx(ctx, tx, k.u.id, k.u.c.owner(), p.Size); err != nil {
			return err
		}
		return insertPart(ctx, tx, k.u.id, p)
	}); err != nil {
		_ = k.u.s.d.Blobs.Delete(context.WithoutCancel(ctx), key)
		return err
	}
	k.parts++
	return nil
}

// Close commits the last, partial chunk.
func (k *chunkSink) Close() error {
	if k.w == nil {
		return nil
	}
	return k.commit()
}

func (k *chunkSink) abort() {
	if k.w != nil {
		_ = k.w.Abort()
		k.w = nil
	}
}

// encrypt streams plaintext through the STREAM encryptor into the blob
// store, refusing more than limit bytes. It returns the plaintext size.
func (u *sealedUpload) encrypt(src io.Reader, limit int64) (int64, error) {
	sink := &chunkSink{u: u}
	enc := e2e.NewEncryptor(sink, u.keys.Enc)
	n, err := io.Copy(enc, io.LimitReader(src, limit+1))
	if err == nil && n > limit {
		err = errTooLarge
	}
	if err == nil {
		err = enc.Close()
	}
	if err == nil {
		err = sink.Close()
	}
	if err != nil {
		sink.abort()
		return 0, err
	}
	if sink.parts != e2e.NumChunks(n) || sink.total != e2e.CiphertextSize(n) {
		return 0, errors.New("secrets: sealed ciphertext does not match its framing")
	}
	return n, nil
}

var errTooLarge = errors.New("secrets: too large")

// finish seals the metadata, makes the secret ready and returns the link.
func (u *sealedUpload) finish(m e2e.Meta, ptSize int64, o options) (string, time.Time, error) {
	s := u.s
	ctx := u.ctx
	metaCT, err := e2e.SealMeta(u.keys.Meta, m)
	if err != nil {
		m.Type = "application/octet-stream"
		if metaCT, err = e2e.SealMeta(u.keys.Meta, m); err != nil {
			return "", time.Time{}, err
		}
	}
	meta, err := s.writeBlob(ctx, metaCT)
	if err != nil {
		return "", time.Time{}, err
	}
	meta.Part = metaPart
	probe := e2e.ProbeKey(u.key, u.rawID)
	hProbe, hAuth := e2e.Hash(probe[:]), e2e.Hash(u.keys.Auth[:])
	now := s.now()
	exp := u.c.expiresAt(now, o.expiry)
	ctSize, chunks := e2e.CiphertextSize(ptSize), e2e.NumChunks(ptSize)
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := insertPart(ctx, tx, u.id, meta); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE secrets SET state = 'ready', salt = ?, kdf = NULL, has_passphrase = 0, h_probe = ?, h_auth = ?,
  commit_tag = ?, ct_size = ?, chunk_count = ?, chunks_received = ?, views_left = ?, max_views = ?, expires_at = ?,
  upload_ip = NULL, updated_at = ? WHERE id = ? AND state = 'uploading'`,
			u.salt[:], hProbe[:], hAuth[:], u.keys.Commit[:], ctSize, chunks, chunks, o.views, o.views,
			db.Millis(exp), db.Millis(now), u.id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, errors.New("secrets: sealed secret vanished"))
		}
		return settleTx(ctx, tx, u.id, now)
	})
	if err != nil {
		_ = s.d.Blobs.Delete(context.WithoutCancel(ctx), meta.Key)
		return "", time.Time{}, err
	}
	link := e2e.FormatLink(s.d.PublicURL, u.rawID, u.key)
	u.wipe()
	return link, exp, nil
}

// sealedErr renders an error from the sealed flow; maxFile and maxText are
// the size limits that applied.
func (s *Service) sealedErr(w http.ResponseWriter, r *http.Request, err error, maxFile, maxText int64) {
	var mbe *http.MaxBytesError
	var ae *web.APIError
	switch {
	case errors.As(err, &ae):
		s.fail(w, r, ae.Status, ae.Msg)
	case errors.Is(err, errTooLarge) || errors.As(err, &mbe):
		s.fail(w, r, http.StatusRequestEntityTooLarge, "Files can be at most "+web.HumanBytes(maxFile)+
			" and text at most "+web.HumanBytes(maxText)+".")
	case errors.Is(err, errTooManyOpen):
		s.apiErr(w, r, err)
	case errors.Is(err, blob.ErrInsufficientSpace):
		s.storeErr(w, r, err)
	default:
		s.internal(w, r, "sealed", err)
	}
}

func (s *Service) sealed(w http.ResponseWriter, r *http.Request) {
	c, ok := s.authorizeCreate(w, r)
	if !ok {
		return
	}
	var so sealedOpts
	q := r.URL.Query()
	for _, name := range []string{"views", "expires"} {
		if q.Has(name) {
			if err := so.set(name, q.Get(name)); err != nil {
				s.apiErr(w, r, err)
				return
			}
		}
	}
	lim, err := s.userLimits(r.Context(), s.d.DB.R, c.p.UserID)
	if err != nil {
		s.createErr(w, r, err)
		return
	}
	maxFile := lim.sizeLimit(c.p, MaxBytes.Get(s.d.Settings))
	maxText := lim.sizeLimit(c.p, TextMaxBytes)
	r.Body = http.MaxBytesReader(w, r.Body, max(maxFile, maxText)+1<<20)
	exempt := s.exempt(r)

	var (
		u      *sealedUpload
		meta   e2e.Meta
		ptSize int64
	)
	fail := func(err error) {
		if u != nil {
			u.abort()
		}
		s.sealedErr(w, r, err, maxFile, maxText)
	}
	// encryptText pads and encrypts a text secret read from src.
	encryptText := func(src io.Reader) error {
		b, err := io.ReadAll(io.LimitReader(src, maxText+1))
		if err != nil {
			return err
		}
		if int64(len(b)) > maxText || int64(e2e.PaddedTextLen(len(b))) > maxText {
			return errTooLarge
		}
		if !utf8.Valid(b) {
			return web.Errorf(http.StatusBadRequest, "Text must be UTF-8. Send other data as a file (multipart field \"file\").")
		}
		if u, err = s.beginSealed(r.Context(), c, exempt, e2e.KindText); err != nil {
			return err
		}
		padded := e2e.PadText(b)
		if ptSize, err = u.encrypt(bytes.NewReader(padded), maxText); err != nil {
			return err
		}
		meta = e2e.Meta{Kind: e2e.KindText, Type: "text/plain; charset=utf-8", Size: int64(len(b))}
		return nil
	}

	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			fail(web.Errorf(http.StatusBadRequest, "Invalid multipart body."))
			return
		}
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fail(web.Errorf(http.StatusBadRequest, "Invalid multipart body."))
				return
			}
			name := p.FormName()
			switch name {
			case "views", "expires":
				v, err := io.ReadAll(io.LimitReader(p, 64))
				if err == nil {
					err = so.set(name, string(v))
				}
				if err != nil {
					_ = p.Close()
					fail(err)
					return
				}
			case "file", "text":
				if u != nil {
					_ = p.Close()
					fail(web.Errorf(http.StatusBadRequest, "Send one file or one text per secret."))
					return
				}
				// Check what we know so far before storing anything.
				if _, _, err := s.checkOptions(so.views, so.expiry); err != nil {
					_ = p.Close()
					fail(err)
					return
				}
				if name == "text" {
					err = encryptText(p)
				} else {
					if u, err = s.beginSealed(r.Context(), c, exempt, e2e.KindFile); err == nil {
						ptSize, err = u.encrypt(p, maxFile)
					}
					typ := p.Header.Get("Content-Type")
					if typ == "" || len(typ) > 255 {
						typ = "application/octet-stream"
					}
					meta = e2e.Meta{Kind: e2e.KindFile, Name: p.FileName(), Type: typ, Size: ptSize}
				}
				if err != nil {
					_ = p.Close()
					fail(err)
					return
				}
			}
			_ = p.Close()
		}
		if u == nil {
			fail(web.Errorf(http.StatusBadRequest, "Send the secret in a multipart field named \"file\" or \"text\"."))
			return
		}
	} else if err := encryptText(r.Body); err != nil {
		fail(err)
		return
	}

	views, expiry, err := s.checkOptions(so.views, so.expiry)
	if err != nil {
		fail(err)
		return
	}
	link, exp, err := u.finish(meta, ptSize, options{views: views, expiry: expiry})
	if err != nil {
		fail(err)
		return
	}
	if web.WantsURL(r) {
		web.URLText(w, http.StatusCreated, link)
		return
	}
	web.JSON(w, http.StatusCreated, sealedResp{URL: link, ID: u.id, ExpiresAt: exp.UTC().Format(time.RFC3339), Views: views})
}
