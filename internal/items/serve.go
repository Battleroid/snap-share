package items

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/web"
)

// dispatch is the router fallback: /{slug}[.ext][/raw|/download|/thumb|/preview].
func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	slug, m, ok := parsePath(r.URL.Path)
	if !ok || IsReserved(slug, s.d.ExtraReserved...) {
		s.notFound(w, r)
		return
	}
	if s.onRawHost(r) {
		if m == modePage {
			http.Redirect(w, r, s.origin()+r.URL.RequestURI(), http.StatusFound) // #nosec G710 -- fixed origin, path only
			return
		}
		s.serveRawHost(w, r, slug, m)
		return
	}
	it, err := s.GetBySlug(r.Context(), slug)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("load item", "err", err)
		}
		s.notFound(w, r)
		return
	}
	p := auth.FromContext(r.Context())
	owner := p != nil && p.UserID == it.OwnerID
	if it.Expired(s.now()) || (it.Visibility == Private && !owner) {
		s.notFound(w, r)
		return
	}
	if m == modePage {
		s.sharePage(w, r, it, owner)
		return
	}
	s.serveContent(w, r, it, m, owner)
}

// notFound answers 404, rate-limiting clients that probe for slugs.
func (s *Service) notFound(w http.ResponseWriter, r *http.Request) {
	ip := clientip.From(r.Context())
	if ip.IsValid() && !clientip.In(ip, s.d.Cfg.RateLimitExempt) {
		if ok, _ := s.notFoundLimit.Allow("404:" + ip.String()); !ok {
			s.d.Errors.Error(w, r, http.StatusTooManyRequests, "Too many requests. Try again in a minute.")
			return
		}
	}
	if s.onRawHost(r) {
		web.SetRawHeaders(w.Header())
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	s.d.Errors.NotFound(w, r)
}

func isNavigation(r *http.Request) bool {
	if d := r.Header.Get("Sec-Fetch-Dest"); d != "" {
		return d == "document"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// serveContent serves raw/download/thumb/preview on the app origin.
func (s *Service) serveContent(w http.ResponseWriter, r *http.Request, it *Item, m mode, owner bool) {
	if it.BlobKey == "" {
		s.notFound(w, r)
		return
	}
	if it.PasswordHash != "" && !owner && !s.unlocked(r, it) {
		if isNavigation(r) {
			http.Redirect(w, r, "/"+it.Slug, http.StatusSeeOther)
			return
		}
		s.d.Errors.Error(w, r, http.StatusForbidden, "This item is password protected.")
		return
	}
	if s.d.Cfg.RawURL != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		http.Redirect(w, r, s.contentURL(it, m, true), http.StatusFound)
		return
	}
	s.writeContent(w, r, it, m, owner)
}

// serveRawHost serves content on the separate raw origin. Unprotected items
// need only the link; protected ones need a valid signature.
func (s *Service) serveRawHost(w http.ResponseWriter, r *http.Request, slug string, m mode) {
	it, err := s.GetBySlug(r.Context(), slug)
	if err != nil || it.Expired(s.now()) || it.BlobKey == "" {
		s.notFound(w, r)
		return
	}
	if it.Protected() && !s.validSignature(r.URL.Query(), it) {
		s.notFound(w, r)
		return
	}
	s.writeContent(w, r, it, m, false)
}

// pdfCSP keeps PDFs inert without the sandbox directive, which stops
// Chromium's viewer from rendering them.
func (s *Service) pdfCSP(r *http.Request) string {
	ancestors := "'none'"
	if s.onRawHost(r) {
		ancestors = s.origin()
	}
	return "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; frame-ancestors " + ancestors
}

func downloadName(it *Item) string {
	if it.Filename != "" {
		return it.Filename
	}
	if e := it.Ext(); e != "" {
		return it.Slug + "." + e
	}
	return it.Slug
}

// writeContent streams a blob with the isolation headers.
func (s *Service) writeContent(w http.ResponseWriter, r *http.Request, it *Item, m mode, owner bool) {
	key := it.BlobKey
	switch m {
	case modeThumb:
		key = it.ThumbKey
	case modePreview:
		key = it.PreviewKey
	}
	if key == "" {
		s.notFound(w, r)
		return
	}
	f, err := s.d.Blobs.Open(r.Context(), key)
	if err != nil {
		if !errors.Is(err, blob.ErrNotFound) {
			s.log.Error("open blob", "item", it.ID, "err", err)
		}
		s.notFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()

	h := w.Header()
	ctype := servedType(it.MIME)
	etag := `"` + it.SHA256 + `"`
	cache := "private, no-cache"
	if m == modeThumb || m == modePreview {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			s.notFound(w, r)
			return
		}
		ctype = derivedType(sniff(head[:n], ""))
		etag, cache = `"`+key+`"`, "private, max-age=3600"
	}
	if it.SHA256 == "" {
		etag = `"` + key + `"`
	}
	if ctype == "application/pdf" && m == modeRaw {
		h.Set("Content-Security-Policy", s.pdfCSP(r))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("X-Robots-Tag", "noindex")
		h.Del("Cross-Origin-Opener-Policy")
		if s.onRawHost(r) {
			h.Del("X-Frame-Options")
		}
	} else {
		web.SetRawHeaders(h)
	}
	disp := "inline"
	if m == modeDownload {
		disp = "attachment"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", contentDisposition(disp, downloadName(it)))
	h.Set("ETag", etag)
	h.Set("Cache-Control", cache)
	if m == modeRaw || m == modeDownload {
		s.countContent(r, it, m, owner)
	}
	http.ServeContent(w, r, "", it.CreatedAt, f)
}

// derivedType limits thumbnails and teasers to image and video types.
func derivedType(t string) string {
	switch t {
	case "image/jpeg", "image/png", "image/webp", "image/avif", "image/gif", "video/mp4", "video/webm":
		return t
	}
	return "application/octet-stream"
}

// unlockForm handles POST /unlock/{slug}.
func (s *Service) unlock(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	it, err := s.GetBySlug(r.Context(), slug)
	p := auth.FromContext(r.Context())
	if err != nil || it.Expired(s.now()) || (it.Visibility == Private && (p == nil || p.UserID != it.OwnerID)) {
		s.notFound(w, r)
		return
	}
	if it.PasswordHash == "" {
		http.Redirect(w, r, "/"+it.Slug, http.StatusSeeOther)
		return
	}
	ip := clientip.From(r.Context())
	if !clientip.In(ip, s.d.Cfg.RateLimitExempt) {
		if ok, _ := s.unlockLimit.Allow("unlock:" + ip.String() + ":" + it.Slug); !ok {
			s.unlockPage(w, r, it, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		s.unlockPage(w, r, it, http.StatusBadRequest, "Enter the password.")
		return
	}
	ok, err := s.passwords.Verify(it.PasswordHash, r.PostForm.Get("password"))
	if err != nil {
		s.log.Error("verify item password", "item", it.ID, "err", err)
	}
	if !ok {
		s.unlockPage(w, r, it, http.StatusForbidden, "Wrong password.")
		return
	}
	s.addUnlock(w, r, it)
	next := "/" + it.Slug
	if n := web.SafeNext(r.PostForm.Get("next")); strings.HasPrefix(n, next+"/") || strings.HasPrefix(n, next+".") {
		next = n
	}
	http.Redirect(w, r, next, http.StatusSeeOther) // #nosec G710 -- same-origin path for this slug
}

// Counters are batched in memory and flushed periodically (never a write per request).

type counterDelta struct {
	views, downloads, opens int64
	first, last             int64
}

type counters struct {
	mu sync.Mutex
	m  map[int64]*counterDelta
}

func (c *counters) lock()   { c.mu.Lock() }
func (c *counters) unlock() { c.mu.Unlock() }

// previewBots are chat and social unfurlers whose fetches are not opens.
var previewBots = []string{"bot", "facebookexternalhit", "whatsapp", "slack", "discord", "telegram", "embedly", "iframely", "preview", "crawler", "spider", "curl/", "wget/"}

func countable(r *http.Request, owner bool) bool {
	if owner || r.Method != http.MethodGet {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	if ua == "" {
		return false
	}
	for _, b := range previewBots {
		if strings.Contains(ua, b) {
			return false
		}
	}
	return true
}

func (s *Service) bump(id int64, views, downloads int64) {
	now := s.now().UnixMilli()
	s.counts.lock()
	defer s.counts.unlock()
	d := s.counts.m[id]
	if d == nil {
		d = &counterDelta{first: now}
		s.counts.m[id] = d
	}
	d.views += views
	d.downloads += downloads
	d.opens++
	d.last = now
}

func (s *Service) countView(r *http.Request, it *Item, owner bool) {
	if countable(r, owner) {
		s.bump(it.ID, 1, 0)
	}
}

// countContent counts downloads, and raw files opened directly in a tab as
// views. Range requests past the start (seeking, resuming) are not counted.
func (s *Service) countContent(r *http.Request, it *Item, m mode, owner bool) {
	if !countable(r, owner) {
		return
	}
	if rg := r.Header.Get("Range"); rg != "" && !strings.HasPrefix(rg, "bytes=0-") {
		return
	}
	switch {
	case m == modeDownload:
		s.bump(it.ID, 0, 1)
	case r.Header.Get("Sec-Fetch-Dest") == "document":
		s.bump(it.ID, 1, 0)
	}
}

// FlushCounters writes the batched view/download counters.
func (s *Service) FlushCounters(ctx context.Context) error {
	s.counts.lock()
	m := s.counts.m
	s.counts.m = map[int64]*counterDelta{}
	s.counts.unlock()
	if len(m) == 0 {
		return nil
	}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for id, d := range m {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET views = views + ?, downloads = downloads + ?, opened_count = opened_count + ?,
				first_opened_at = coalesce(first_opened_at, ?), last_opened_at = max(coalesce(last_opened_at, 0), ?) WHERE id = ?`,
				d.views, d.downloads, d.opens, d.first, d.last, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Put the counts back so the next flush retries them.
		s.counts.lock()
		for id, d := range m {
			cur := s.counts.m[id]
			if cur == nil {
				s.counts.m[id] = d
				continue
			}
			cur.views += d.views
			cur.downloads += d.downloads
			cur.opens += d.opens
			cur.first = min(cur.first, d.first)
			cur.last = max(cur.last, d.last)
		}
		s.counts.unlock()
	}
	return err
}
