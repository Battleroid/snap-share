package items

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/render"
	"github.com/Battleroid/snap-share/internal/web"
)

// searchExcerpt is how much text is indexed for search.
const searchExcerpt = 4 << 10

// rendered is the cached render of a text item, stored as JSON under render_key.
type rendered struct {
	Lang     string `json:"lang"`
	Code     string `json:"code"`  // highlighted HTML for <pre><code>
	Lines    int    `json:"lines"` // for the gutter
	Markdown string `json:"markdown,omitempty"`
}

func lineCount(src []byte) int {
	if len(src) == 0 {
		return 1
	}
	n := bytes.Count(src, []byte("\n"))
	if src[len(src)-1] != '\n' {
		n++
	}
	return max(n, 1)
}

func excerpt(src []byte) string {
	if len(src) > searchExcerpt {
		src = src[:searchExcerpt]
	}
	return strings.ToValidUTF8(string(src), "")
}

// pickLang is the explicit language, else the one the filename implies, else
// plain text. Content-based guessing is unreliable, so it is not used.
func pickLang(lang, filename string) string {
	if lang = strings.TrimSpace(lang); lang != "" {
		return lang
	}
	if filename != "" {
		return render.DetectLang(filename, nil)
	}
	return render.PlainText
}

// renderText highlights src (and renders Markdown) once, storing the result
// as a blob. Text over render.DefaultMaxBytes is not rendered.
func (s *Service) renderText(ctx context.Context, src []byte, lang, filename string, markdown bool) (key, langUsed string, err error) {
	lang = pickLang(lang, filename)
	if len(src) > render.DefaultMaxBytes {
		return "", lang, nil
	}
	hctx, cancel := context.WithTimeout(ctx, render.DefaultTimeout)
	defer cancel()
	code, used, herr := render.Highlight(hctx, src, lang, "", 0)
	if herr != nil {
		code = render.Plain(src)
	}
	if used == "" {
		used = lang
	}
	out := rendered{Lang: used, Code: string(code), Lines: lineCount(src)}
	if markdown {
		if md, err := render.Markdown(src, 0); err == nil {
			out.Markdown = string(md)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", used, err
	}
	key, err = s.putBlob(ctx, b)
	return key, used, err
}

func (s *Service) putBlob(ctx context.Context, b []byte) (string, error) {
	w, err := s.d.Blobs.Create(ctx)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(b); err != nil {
		_ = w.Abort()
		return "", err
	}
	return w.Commit()
}

// readBlob reads at most max bytes of key (ok=false when it is larger).
func (s *Service) readBlob(ctx context.Context, key string, max int64) ([]byte, bool, error) {
	f, err := s.d.Blobs.Open(ctx, key)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if f.Size() > max {
		return nil, false, nil
	}
	b, err := io.ReadAll(io.LimitReader(f, max))
	return b, true, err
}

// fileIn is uploaded content already committed to the blob store.
type fileIn struct {
	key      string
	size     int64
	sum      string
	head     []byte
	filename string
}

// finishFile turns committed content into a file item: it sniffs the type,
// applies the block list, renders text, inserts the item, settles the quota
// reservation and drops the uploads row, all or nothing.
func (s *Service) finishFile(ctx context.Context, owner int64, uploadID string, reserved int64, f fileIn, opts resolved) (*Item, string, error) {
	ctx = context.WithoutCancel(ctx) // the content is in; finish even if the client hangs up
	mime := sniff(f.head, f.filename)
	keys := []string{f.key}
	fail := func(err error) (*Item, string, error) {
		for _, k := range keys {
			_ = s.d.Blobs.Delete(ctx, k)
		}
		s.dropUpload(ctx, uploadID, owner, reserved)
		return nil, "", err
	}
	if s.blockedType(mime) {
		return fail(errBlocked)
	}
	exp, err := s.expiresAt(opts)
	if err != nil {
		return fail(err)
	}
	n := NewItem{
		OwnerID: owner, Kind: KindFile, Title: opts.Title, Filename: f.filename, MIME: mime, Size: f.size, SHA256: f.sum,
		BlobKey: f.key, Visibility: opts.Visibility, PasswordHash: opts.PasswordHash, ExpiresAt: exp,
		ExpiryAction: opts.ExpiryAction, Naming: opts.Naming,
	}
	if vk := viewKind(KindFile, mime, ""); vk == viewText || vk == viewMarkdown {
		src, ok, err := s.readBlob(ctx, f.key, render.DefaultMaxBytes)
		if err != nil {
			return fail(err)
		}
		if ok {
			key, lang, err := s.renderText(ctx, src, "", f.filename, vk == viewMarkdown)
			if err != nil {
				return fail(err)
			}
			n.RenderKey, n.Meta = key, map[string]any{"lang": lang}
			keys = append(keys, key)
			n.SearchText = excerpt(src)
		}
	}
	var it *Item
	var tok string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if it, tok, err = s.InsertTx(ctx, tx, n); err != nil {
			return err
		}
		if err := settleTx(ctx, tx, owner, reserved, f.size, s.now()); err != nil {
			return err
		}
		if uploadID != "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, uploadID)
		}
		return err
	})
	if err != nil {
		return fail(err)
	}
	return it, tok, nil
}

// dropUpload deletes an uploads row and releases its reservation.
func (s *Service) dropUpload(ctx context.Context, id string, owner, reserved int64) {
	if id == "" {
		return
	}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // already gone (reaped)
		}
		return releaseTx(ctx, tx, owner, reserved)
	})
	if err != nil {
		s.log.Error("drop upload", "err", err)
	}
}

// ItemJSON is an item in API responses.
type ItemJSON struct {
	ID            int64      `json:"id"`
	Kind          Kind       `json:"kind"`
	Slug          string     `json:"slug"`
	URL           string     `json:"url"`
	PageURL       string     `json:"page_url"`
	RawURL        string     `json:"raw_url,omitempty"`
	DownloadURL   string     `json:"download_url,omitempty"`
	ThumbnailURL  string     `json:"thumbnail_url,omitempty"`
	DeletionURL   string     `json:"deletion_url,omitempty"`
	Name          string     `json:"name"`
	Title         string     `json:"title"`
	Filename      string     `json:"filename,omitempty"`
	Size          int64      `json:"size"`
	Type          string     `json:"type,omitempty"`
	Lang          string     `json:"lang,omitempty"`
	TargetURL     string     `json:"target_url,omitempty"`
	Visibility    Visibility `json:"visibility"`
	Password      bool       `json:"password"`
	ExpiresAt     *time.Time `json:"expires_at"`
	ExpiryAction  string     `json:"expiry_action"`
	SHA256        string     `json:"sha256,omitempty"`
	Width         int64      `json:"width,omitempty"`
	Height        int64      `json:"height,omitempty"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
	Views         int64      `json:"views"`
	Downloads     int64      `json:"downloads"`
	OpenedCount   int64      `json:"opened_count"`
	FirstOpenedAt *time.Time `json:"first_opened_at"`
	LastOpenedAt  *time.Time `json:"last_opened_at"`
	PreviewedAt   *time.Time `json:"previewed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// JSON renders it for the API. directLink makes url the raw link for
// unprotected files (the "Copy direct link" preference).
func (s *Service) JSON(it *Item, directLink bool) ItemJSON {
	j := ItemJSON{
		ID: it.ID, Kind: it.Kind, Slug: it.Slug, PageURL: s.PublicURL(it.Slug), Name: it.Name(), Title: it.Title,
		Filename: it.Filename, Size: it.Size, Type: it.MIME, Lang: it.MetaString("lang"), TargetURL: it.TargetURL,
		Visibility: it.Visibility, Password: it.PasswordHash != "", ExpiresAt: it.ExpiresAt, ExpiryAction: it.ExpiryAction,
		SHA256: it.SHA256, Width: it.Width, Height: it.Height, DurationMS: it.DurationMS,
		Views: it.Views, Downloads: it.Downloads, OpenedCount: it.OpenedCount, FirstOpenedAt: it.FirstOpened,
		LastOpenedAt: it.LastOpened, PreviewedAt: it.PreviewedAt, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
	}
	j.URL = j.PageURL
	if it.BlobKey != "" {
		j.RawURL = s.contentURL(it, modeRaw, false)
		j.DownloadURL = s.contentURL(it, modeDownload, false)
		if directLink && it.Kind == KindFile && !it.Protected() {
			j.URL = j.RawURL
		}
	}
	switch {
	case it.ThumbKey != "":
		j.ThumbnailURL = s.contentURL(it, modeThumb, false)
	case it.BlobKey != "" && viewKind(it.Kind, it.MIME, "") == viewImage:
		j.ThumbnailURL = j.RawURL
	}
	return j
}

// respondCreated writes the creation response (JSON, or the URL for curl).
func (s *Service) respondCreated(w http.ResponseWriter, r *http.Request, it *Item, token string) {
	ctx := context.WithoutCancel(r.Context())
	if s.d.OnCreate != nil {
		s.d.OnCreate(ctx, it)
		if fresh, err := s.Get(ctx, it.ID); err == nil {
			it = fresh
		}
	}
	prefs, _ := LoadPrefs(ctx, s.d.DB, it.OwnerID)
	j := s.JSON(it, prefs.DirectLink)
	j.DeletionURL = s.origin() + "/x#" + token
	if web.WantsURL(r) {
		web.URLText(w, http.StatusCreated, j.URL)
		return
	}
	web.JSON(w, http.StatusCreated, j)
}
