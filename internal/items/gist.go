package items

// Gists: one item with one link holding several text files, shown together
// on one page. The items row carries the share options and the totals (size
// and render size are its files' sums, so quota accounting needs no special
// case); gist_files holds each file's blob and cached render. Files are
// served one at a time with ?f=N, the file's 0-based position, exactly as a
// paste is, and /download without f is a zip of them all.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/render"
	"github.com/Battleroid/snap-share/internal/web"
)

// MaxGistFiles is the most files one gist holds.
const MaxGistFiles = 50

const (
	// gistRenderBudget bounds the time spent highlighting one gist's files
	// at creation. Files left when it runs out get no cached render and are
	// shown with a raw link.
	gistRenderBudget = 20 * time.Second
	// gistPageRenders bounds the cached renders inlined on one gist page.
	// Files past it are shown with a raw link.
	gistPageRenders = 8 << 20
	// gistSearchBytes caps the text indexed for search: the start of each
	// file, up to this much in all.
	gistSearchBytes = 4 * searchExcerpt
)

// GistFile is one file of a gist (a gist_files row).
type GistFile struct {
	Position   int // 0-based, the f in its URLs
	Name       string
	Lang       string // the highlighter's language ("Go", "markdown", "plaintext")
	MIME       string // text/plain or text/markdown
	Size       int64
	SHA256     string
	BlobKey    string
	RenderKey  string // "" without a cached render
	RenderSize int64
}

// LoadGistFiles sets Files on each gist in list, reading through q (a
// transaction or the read pool). Other items are left alone.
func LoadGistFiles(ctx context.Context, q Querier, list ...*Item) error {
	byID := map[int64]*Item{}
	var ids []any
	for _, it := range list {
		if it != nil && it.Kind == KindGist {
			byID[it.ID] = it
			it.Files = nil
			ids = append(ids, it.ID)
		}
	}
	for len(ids) > 0 {
		chunk := ids[:min(len(ids), 500)]
		ids = ids[len(chunk):]
		type row struct {
			id int64
			f  GistFile
		}
		rows, err := queryAll(ctx, q, `SELECT item_id, position, filename, lang, mime, size, sha256, blob_key, coalesce(render_key, ''), render_size
			FROM gist_files WHERE item_id IN (?`+strings.Repeat(", ?", len(chunk)-1)+`) ORDER BY item_id, position`, // #nosec G202 -- only placeholders are added
			func(rows *sql.Rows) (row, error) {
				var r row
				err := rows.Scan(&r.id, &r.f.Position, &r.f.Name, &r.f.Lang, &r.f.MIME, &r.f.Size, &r.f.SHA256, &r.f.BlobKey, &r.f.RenderKey, &r.f.RenderSize)
				return r, err
			}, chunk...)
		if err != nil {
			return fmt.Errorf("load gist files: %w", err)
		}
		for _, r := range rows {
			if it := byID[r.id]; it != nil {
				it.Files = append(it.Files, r.f)
			}
		}
	}
	return nil
}

// filesText is "1 file", "3 files".
func filesText(n int) string {
	if n == 1 {
		return "1 file"
	}
	return strconv.Itoa(n) + " files"
}

// gistFile returns the file at position pos.
func (it *Item) gistFile(pos int) (GistFile, bool) {
	for _, f := range it.Files {
		if f.Position == pos {
			return f, true
		}
	}
	return GistFile{}, false
}

// fileItem is it with one of its files in place of its own content, so the
// file is served through writeContent exactly like a paste and counted on
// the gist.
func (it *Item) fileItem(f GistFile) *Item {
	c := *it
	c.Files = nil
	c.Filename, c.MIME, c.Size, c.SHA256 = f.Name, f.MIME, f.Size, f.SHA256
	c.BlobKey, c.RenderKey, c.RenderSize = f.BlobKey, f.RenderKey, f.RenderSize
	c.ThumbKey, c.PreviewKey = "", ""
	return &c
}

// Creating: POST /api/v1/gists.

type gistFileReq struct {
	Name    string `json:"name"`
	Lang    string `json:"lang"`
	Content string `json:"content"`
}

type gistReq struct {
	Files []gistFileReq `json:"files"`
	options
}

// gistPlan is one checked file, before it is stored.
type gistPlan struct {
	name  string
	named bool // the client gave the name
	lang  string
	md    bool
	src   []byte
}

// planGist checks the files of a request: 1 to MaxGistFiles, none empty,
// at most max bytes together, names sanitized (an empty one becomes
// file<n> with the language's extension), not blocked and unique ignoring
// case. It returns the files and their total size.
func (s *Service) planGist(files []gistFileReq, max int64) ([]gistPlan, int64, error) {
	switch {
	case len(files) == 0:
		return nil, 0, web.Errorf(http.StatusBadRequest, "Add at least one file.")
	case len(files) > MaxGistFiles:
		return nil, 0, web.Errorf(http.StatusBadRequest, "A gist holds at most %d files.", MaxGistFiles)
	}
	var total int64
	seen := make(map[string]bool, len(files))
	out := make([]gistPlan, 0, len(files))
	for i, f := range files {
		total += int64(len(f.Content))
		if total > max {
			return nil, 0, web.Errorf(http.StatusRequestEntityTooLarge, "The files are over the %s limit together.", web.HumanBytes(max))
		}
		p := gistPlan{name: SanitizeFilename(f.Name), src: []byte(f.Content)}
		p.named = p.name != ""
		p.lang = pickLang(cleanText(f.Lang, 64), p.name)
		if !p.named {
			ext := render.Ext(p.lang)
			if ext == "" {
				ext = ".txt"
			}
			p.name = "file" + strconv.Itoa(i+1) + ext
		}
		if strings.TrimSpace(f.Content) == "" {
			return nil, 0, web.Errorf(http.StatusBadRequest, "%s is empty.", p.name)
		}
		if s.blockedName(p.name) {
			return nil, 0, errBlocked
		}
		key := strings.ToLower(p.name)
		if seen[key] {
			return nil, 0, web.Errorf(http.StatusBadRequest, "Two files are named %s.", p.name)
		}
		seen[key] = true
		p.md = isMarkdownLang(p.lang) || strings.EqualFold(extOf(p.name), ".md")
		out = append(out, p)
	}
	return out, total, nil
}

// createGist handles POST /api/v1/gists. It takes JSON only.
func (s *Service) createGist(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		return web.Errorf(http.StatusUnsupportedMediaType, "Send gists as JSON (Content-Type: application/json).")
	}
	if err := s.allowCreate(w, r, p); err != nil {
		return err
	}
	ctx := r.Context()
	lim, err := s.userLimits(ctx, s.d.DB.R, p.UserID)
	if err != nil {
		return err
	}
	max := min(int64(maxPasteBytes), lim.maxUpload)
	var req gistReq
	if err := web.DecodeJSON(w, r, 2*max+64<<10, &req); err != nil {
		return err
	}
	plans, total, err := s.planGist(req.Files, max)
	if err != nil {
		return err
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	opts, err := s.resolve(ctx, p.UserID, prefs, req.options, KindGist)
	if err != nil {
		return err
	}
	if opts.Naming.Scheme == NamingOriginal && !plans[0].named {
		opts.Naming.Scheme = NamingRandom // as a paste without a name
	}
	exp, err := s.expiresAt(opts)
	if err != nil {
		return err
	}
	if err := s.d.Blobs.CheckFree(total); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	files, keys, err := s.storeGist(ctx, plans)
	cleanup := func() {
		for _, k := range keys {
			_ = s.d.Blobs.Delete(ctx, k)
		}
	}
	if err != nil {
		cleanup()
		return err
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	n := NewItem{
		OwnerID: p.UserID, Kind: KindGist, Title: opts.Title, Filename: files[0].Name, Size: total,
		Visibility: opts.Visibility, PasswordHash: opts.PasswordHash, ExpiresAt: exp, ExpiryAction: opts.ExpiryAction,
		Naming: opts.Naming, SearchNames: strings.Join(names, " "), SearchText: gistSearchText(plans), CreatedByKey: opts.KeyID,
	}
	var it *Item
	var tok string
	var drop []string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if drop, n.RenderSize, err = s.chargeGistTx(ctx, tx, p.UserID, total, files); err != nil {
			return err
		}
		if it, tok, err = s.InsertTx(ctx, tx, n); err != nil {
			return err
		}
		for _, f := range files {
			if _, err := tx.ExecContext(ctx, `INSERT INTO gist_files (item_id, position, filename, lang, mime, size, sha256, blob_key, render_key, render_size)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, it.ID, f.Position, f.Name, f.Lang, f.MIME, f.Size, f.SHA256, f.BlobKey,
				nullStr(f.RenderKey), f.RenderSize); err != nil {
				return fmt.Errorf("insert gist file: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		cleanup()
		return err
	}
	for _, k := range drop {
		_ = s.d.Blobs.Delete(ctx, k)
	}
	it.Files = files
	s.respondCreated(w, r, it, tok)
	return nil
}

// storeGist writes each file and its cached render to the blob store. keys
// lists every blob written, also on error, for the caller to delete.
func (s *Service) storeGist(ctx context.Context, plans []gistPlan) (files []GistFile, keys []string, err error) {
	rctx, cancel := context.WithTimeout(ctx, gistRenderBudget)
	defer cancel()
	files = make([]GistFile, 0, len(plans))
	for i, p := range plans {
		key, err := s.putBlob(ctx, p.src)
		if err != nil {
			return nil, keys, err
		}
		keys = append(keys, key)
		sum := sha256.Sum256(p.src)
		f := GistFile{Position: i, Name: p.name, Lang: p.lang, MIME: "text/plain", Size: int64(len(p.src)),
			SHA256: hex.EncodeToString(sum[:]), BlobKey: key}
		if p.md {
			f.MIME = "text/markdown"
		}
		if rctx.Err() == nil {
			b, used, ok, err := renderJSON(rctx, p.src, p.lang, p.name, p.md)
			if err != nil {
				return nil, keys, err
			}
			f.Lang = used
			if ok {
				if f.RenderKey, err = s.putBlob(ctx, b); err != nil {
					return nil, keys, err
				}
				keys = append(keys, f.RenderKey)
				f.RenderSize = int64(len(b))
			}
		}
		if p.md {
			f.Lang = "markdown"
		}
		files = append(files, f)
	}
	return files, keys, nil
}

// chargeGistTx charges a gist's content (total bytes, all or nothing) and
// its renders to owner inside tx. A render that does not fit in the quota is
// dropped from files and its key returned for deletion after the commit,
// as for a paste. rendered is what the kept renders take.
func (s *Service) chargeGistTx(ctx context.Context, tx *sql.Tx, owner, total int64, files []GistFile) (drop []string, rendered int64, err error) {
	if _, err := s.reserveTx(ctx, tx, owner, total, true); err != nil {
		return nil, 0, err
	}
	if err := settleTx(ctx, tx, owner, total, total, s.now()); err != nil {
		return nil, 0, err
	}
	for i := range files {
		f := &files[i]
		if f.RenderKey == "" {
			continue
		}
		if _, err := s.reserveTx(ctx, tx, owner, f.RenderSize, true); errors.Is(err, errQuota) {
			drop = append(drop, f.RenderKey)
			f.RenderKey, f.RenderSize = "", 0
			continue
		} else if err != nil {
			return nil, 0, err
		}
		rendered += f.RenderSize
	}
	if rendered > 0 {
		err = settleTx(ctx, tx, owner, rendered, rendered, s.now())
	}
	return drop, rendered, err
}

// gistSearchText is the start of each file, up to gistSearchBytes in all.
func gistSearchText(plans []gistPlan) string {
	var b strings.Builder
	for _, p := range plans {
		left := gistSearchBytes - b.Len()
		if left <= 0 {
			break
		}
		src := p.src
		if len(src) > min(left, searchExcerpt) {
			src = src[:min(left, searchExcerpt)]
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.ToValidUTF8(string(src), ""))
	}
	return b.String()
}

// Serving: /{slug}/raw?f=N, /{slug}/download?f=N and /{slug}/download.

// gistTarget is what a gist content URL names: one file, or the zip.
type gistTarget struct {
	file GistFile
	zip  bool
}

// gistTargetOf resolves a gist content request. ?f=N names the file at
// position N in plain decimal, for raw and download; download without f is
// the zip. Anything else (raw without f, thumbnails, a malformed or
// repeated f, a position out of range) is not found.
func gistTargetOf(it *Item, q url.Values, m mode) (gistTarget, bool) {
	if m != modeRaw && m != modeDownload {
		return gistTarget{}, false
	}
	v, ok := q["f"]
	if !ok {
		return gistTarget{zip: true}, m == modeDownload
	}
	if len(v) != 1 {
		return gistTarget{}, false
	}
	pos, ok := parsePosition(v[0])
	if !ok {
		return gistTarget{}, false
	}
	f, ok := it.gistFile(pos)
	return gistTarget{file: f}, ok
}

// parsePosition reads a file position: decimal digits only, no sign, no
// leading zero, at most three digits.
func parsePosition(s string) (int, bool) {
	if s == "" || len(s) > 3 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// serveGist serves a gist's content on the app origin. The dispatcher has
// checked expiry and visibility.
func (s *Service) serveGist(w http.ResponseWriter, r *http.Request, it *Item, m mode, owner bool) {
	if s.locked(w, r, it, owner) {
		return
	}
	if err := LoadGistFiles(r.Context(), s.d.DB.R, it); err != nil {
		s.log.Error("load gist files", "item", it.ID, "err", err)
		s.notFound(w, r)
		return
	}
	t, ok := gistTargetOf(it, r.URL.Query(), m)
	if !ok {
		s.notFound(w, r)
		return
	}
	if s.d.Cfg.RawURL != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		http.Redirect(w, r, s.gistTargetURL(it, t, m, true), http.StatusFound)
		return
	}
	s.writeGist(w, r, it, t, m, owner)
}

// serveGistRaw serves a gist's content on the raw origin: unprotected gists
// need only the link, protected ones a valid signature (which covers every
// file; f is outside it).
func (s *Service) serveGistRaw(w http.ResponseWriter, r *http.Request, it *Item, m mode) {
	q := r.URL.Query()
	if it.Protected() && !s.validSignature(q, it) {
		s.notFound(w, r)
		return
	}
	if err := LoadGistFiles(r.Context(), s.d.DB.R, it); err != nil {
		s.log.Error("load gist files", "item", it.ID, "err", err)
		s.notFound(w, r)
		return
	}
	t, ok := gistTargetOf(it, q, m)
	if !ok {
		s.notFound(w, r)
		return
	}
	s.writeGist(w, r, it, t, m, false)
}

func (s *Service) writeGist(w http.ResponseWriter, r *http.Request, it *Item, t gistTarget, m mode, owner bool) {
	if t.zip {
		s.writeGistZip(w, r, it, owner)
		return
	}
	s.writeContent(w, r, it.fileItem(t.file), m, owner)
}

// gistTargetURL is the URL of t (see fileURL).
func (s *Service) gistTargetURL(it *Item, t gistTarget, m mode, viewer bool) string {
	if t.zip {
		return s.contentURL(it, modeDownload, viewer)
	}
	return s.fileURL(it, t.file.Position, m, viewer)
}

// gistZipName is the zip's download name: the title, else the slug.
func gistZipName(it *Item) string {
	base := it.Slug
	if it.Title != "" {
		base = it.Title
	}
	return ArchiveName(base, it.Slug) + ".zip"
}

// writeGistZip streams the gist's files as a zip (stored, not compressed)
// with the raw content headers. It counts as a download.
func (s *Service) writeGistZip(w http.ResponseWriter, r *http.Request, it *Item, owner bool) {
	h := w.Header()
	web.SetRawHeaders(h)
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition("attachment", gistZipName(it)))
	h.Set("Cache-Control", "private, no-cache")
	s.countContent(r, it, modeDownload, owner)
	s.recordEvent(r, it, modeDownload, owner)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if err := s.zipGist(r.Context(), w, it); err != nil && r.Context().Err() == nil {
		s.log.Warn("gist zip", "item", it.ID, "err", err)
	}
}

// zipGist writes the gist's files into w as a zip archive. Entry names are
// ArchiveName and unique ignoring case. A file whose blob has disappeared
// is left out.
func (s *Service) zipGist(ctx context.Context, w io.Writer, it *Item) error {
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	for _, f := range it.Files {
		b, err := s.d.Blobs.Open(ctx, f.BlobKey)
		if errors.Is(err, blob.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		name := UniqueName(ArchiveName(f.Name, "file"+strconv.Itoa(f.Position+1)+".txt"), used)
		hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: it.CreatedAt}
		hdr.SetMode(0o644)
		dst, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(dst, b)
		}
		_ = b.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// The share page.

// gistView is the gist share page's template data.
type gistView struct {
	Item        *Item
	Name        string
	PageURL     string
	DownloadURL string
	Meta        []metaPair
	Files       []*textBlock
	Owner       bool
	Private     bool
	Password    bool
	Embed       *embedView
}

// gistPage renders a gist's share page. The dispatcher has checked access
// and the password, and counted the view.
func (s *Service) gistPage(w http.ResponseWriter, r *http.Request, it *Item, owner bool) {
	ctx := r.Context()
	if err := LoadGistFiles(ctx, s.d.DB.R, it); err != nil {
		s.fail(w, r, err)
		return
	}
	v := &gistView{
		Item: it, Name: it.Name(), PageURL: s.PublicURL(it.Slug), DownloadURL: s.contentURL(it, modeDownload, true),
		Owner: owner, Private: it.Visibility == Private, Password: it.PasswordHash != "", Embed: s.embed(ctx, it),
	}
	expires := "never"
	if it.ExpiresAt != nil {
		expires = it.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	v.Meta = []metaPair{
		{"files", strconv.Itoa(len(it.Files))},
		{"size", web.HumanBytes(it.Size)},
		{"expires", expires},
		{"uploaded", it.CreatedAt.UTC().Format("2006-01-02")},
	}
	budget := int64(gistPageRenders)
	for _, f := range it.Files {
		b := &textBlock{
			ID: "f" + strconv.Itoa(f.Position), Name: f.Name, View: viewText, Size: web.HumanBytes(f.Size),
			RawURL: s.fileURL(it, f.Position, modeRaw, true), DownloadURL: s.fileURL(it, f.Position, modeDownload, true), Bare: true,
		}
		if f.MIME == "text/markdown" {
			b.View = viewMarkdown
		}
		// Only the cached render is shown; without one (too large, over
		// the quota or past the page's budget) the file gets a raw link.
		budget -= s.fillText(ctx, b, f.RenderKey, "", f.Size, f.Lang, budget)
		v.Files = append(v.Files, b)
	}
	s.d.Render.Page(w, r, http.StatusOK, "share/gist", web.Page{
		Title: v.Name, NoIndex: true, Scripts: []string{"js/share.js"}, Data: v,
	})
}
