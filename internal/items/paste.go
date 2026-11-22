package items

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Battleroid/snap-share/internal/web"
)

type pasteReq struct {
	Content string `json:"content"`
	Lang    string `json:"lang"`
	Name    string `json:"name"`
	options
}

// readPaste accepts JSON, a urlencoded form, or a raw text body (options in
// the query string, password in X-Snap-Password).
func readPaste(w http.ResponseWriter, r *http.Request, max int64) (pasteReq, error) {
	var req pasteReq
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mt {
	case "application/json":
		if err := web.DecodeJSON(w, r, 2*max+64<<10, &req); err != nil {
			return req, err
		}
	case "application/x-www-form-urlencoded":
		r.Body = http.MaxBytesReader(w, r.Body, 3*max+64<<10)
		if err := r.ParseForm(); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return req, tooLarge(max)
			}
			return req, web.Errorf(http.StatusBadRequest, "Malformed form body.")
		}
		req.Content = r.PostForm.Get("content")
		req.Lang, req.Name = r.PostForm.Get("lang"), r.PostForm.Get("name")
		formOptions(&req.options, r.PostForm)
	case "multipart/form-data":
		return req, web.Errorf(http.StatusUnsupportedMediaType, "Send pastes as JSON, a form or text/plain; upload files to /api/v1/upload.")
	default:
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return req, tooLarge(max)
			}
			return req, err
		}
		req.Content = string(b)
		q := r.URL.Query()
		req.Lang, req.Name = q.Get("lang"), q.Get("name")
		req.options = queryOptions(r)
	}
	return req, nil
}

func formOptions(o *options, v url.Values) {
	for k := range optionFields {
		if x := v.Get(k); x != "" {
			o.set(k, x)
		}
	}
}

// createPaste handles POST /api/v1/pastes.
func (s *Service) createPaste(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	lim, err := s.userLimits(ctx, s.d.DB.R, p.UserID)
	if err != nil {
		return err
	}
	max := min(int64(maxPasteBytes), lim.maxUpload)
	req, err := readPaste(w, r, max)
	if err != nil {
		return err
	}
	src := []byte(req.Content)
	if len(strings.TrimSpace(req.Content)) == 0 {
		return web.Errorf(http.StatusBadRequest, "The paste is empty.")
	}
	if int64(len(src)) > max {
		return tooLarge(max)
	}
	name := SanitizeFilename(req.Name)
	lang := cleanText(req.Lang, 64)
	if name != "" && s.blockedName(name) {
		return errBlocked
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	opts, err := s.resolve(ctx, prefs, req.options, KindPaste)
	if err != nil {
		return err
	}
	if opts.Naming.Scheme == NamingOriginal && name == "" {
		opts.Naming.Scheme = NamingRandom
	}
	exp, err := s.expiresAt(opts)
	if err != nil {
		return err
	}
	if err := s.d.Blobs.CheckFree(int64(len(src))); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	key, err := s.putBlob(ctx, src)
	if err != nil {
		return err
	}
	lang = pickLang(lang, name)
	md := isMarkdownLang(lang) || strings.EqualFold(strings.TrimPrefix(extOf(name), "."), "md")
	renderKey, used, err := s.renderText(ctx, src, lang, name, md)
	if err != nil {
		_ = s.d.Blobs.Delete(ctx, key)
		return err
	}
	if md {
		used = "markdown"
	}
	mimeType := "text/plain"
	if md {
		mimeType = "text/markdown"
	}
	sum := sha256.Sum256(src)
	n := NewItem{
		OwnerID: p.UserID, Kind: KindPaste, Title: opts.Title, Filename: name, MIME: mimeType, Size: int64(len(src)),
		SHA256: hex.EncodeToString(sum[:]), BlobKey: key, RenderKey: renderKey, Visibility: opts.Visibility,
		PasswordHash: opts.PasswordHash, ExpiresAt: exp, ExpiryAction: opts.ExpiryAction, Naming: opts.Naming,
		Meta: map[string]any{"lang": used}, SearchText: excerpt(src),
	}
	var it *Item
	var tok string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.reserveTx(ctx, tx, p.UserID, n.Size, true); err != nil {
			return err
		}
		var err error
		if it, tok, err = s.InsertTx(ctx, tx, n); err != nil {
			return err
		}
		return settleTx(ctx, tx, p.UserID, n.Size, n.Size, s.now())
	})
	if err != nil {
		_ = s.d.Blobs.Delete(ctx, key)
		if renderKey != "" {
			_ = s.d.Blobs.Delete(ctx, renderKey)
		}
		return err
	}
	s.respondCreated(w, r, it, tok)
	return nil
}

func extOf(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}
