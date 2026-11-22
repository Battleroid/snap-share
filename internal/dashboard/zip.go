package dashboard

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// Zip downloads: POST /api/v1/items/bulk with download_zip checks the
// selection and returns a short-lived URL; GET on it streams a zip archive
// (stored, not compressed) of the files and pastes. Links and albums have no
// content and are skipped.
//
// The ticket holds only the item ids and its owner. The GET loads the items
// (names and blob keys) in one read, closes it, and only then writes to the
// client, so no database rows or transactions stay open while the archive
// streams.

// Ticket caps: a user holds at most maxZipTicketsUser live tickets (a new
// one replaces their oldest), so one account cannot push out everyone
// else's; past maxZipTickets for the whole site, new ones are refused.
const (
	zipTTL            = 10 * time.Minute
	maxZipTickets     = 256
	maxZipTicketsUser = 8
)

var errZipBusy = web.Errorf(http.StatusTooManyRequests, "Too many downloads are being prepared. Try again in a few minutes.")

type zipTicket struct {
	owner int64
	ids   []int64
	exp   time.Time
}

func newZipToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// issueZip stores a ticket and returns its token.
func (s *Service) issueZip(owner int64, ids []int64) (string, time.Time, error) {
	now := s.now()
	s.zipMu.Lock()
	defer s.zipMu.Unlock()
	mine := 0
	for k, t := range s.tickets {
		switch {
		case !t.exp.After(now):
			delete(s.tickets, k)
		case t.owner == owner:
			mine++
		}
	}
	for ; mine >= maxZipTicketsUser; mine-- { // drop this user's oldest
		var first string
		for k, t := range s.tickets {
			if t.owner == owner && (first == "" || t.exp.Before(s.tickets[first].exp)) {
				first = k
			}
		}
		delete(s.tickets, first)
	}
	if len(s.tickets) >= maxZipTickets {
		return "", time.Time{}, errZipBusy
	}
	tok := newZipToken()
	exp := now.Add(zipTTL)
	s.tickets[tok] = zipTicket{owner: owner, ids: ids, exp: exp}
	return tok, exp, nil
}

func (s *Service) ticket(tok string, owner int64) (zipTicket, bool) {
	s.zipMu.Lock()
	defer s.zipMu.Unlock()
	t, ok := s.tickets[tok]
	if !ok || t.owner != owner || !t.exp.After(s.now()) {
		return zipTicket{}, false
	}
	return t, true
}

// hasContent reports whether it goes into a zip.
func hasContent(it *items.Item) bool {
	return it.BlobKey != "" && (it.Kind == items.KindFile || it.Kind == items.KindPaste)
}

func (s *Service) downloadZip(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	if err := params(b.req.Params, &struct{}{}); err != nil {
		return err
	}
	list, err := s.resolve(r.Context(), s.d.DB.R, b.p.UserID, b.sel)
	if err != nil {
		return err
	}
	var ids []int64
	var size int64
	for _, it := range list {
		if hasContent(it) {
			ids = append(ids, it.ID)
			size += it.Size
		}
	}
	if len(ids) == 0 {
		return web.Errorf(http.StatusBadRequest, "Nothing to download: links and albums have no files.")
	}
	tok, exp, err := s.issueZip(b.p.UserID, ids)
	if err != nil {
		return err
	}
	respond(w, map[string]any{
		"action": b.req.Action, "count": len(ids), "skipped": len(list) - len(ids), "size": size,
		"url": s.d.Cfg.Origin() + "/api/v1/zip/" + tok, "expires_at": exp.UTC(),
	})
	return nil
}

// zipEntry is one file of the archive, fixed before streaming starts.
type zipEntry struct {
	name     string
	key      string
	size     int64
	modified time.Time
}

// zipEntries loads owner's items ids and names their entries: sanitized,
// with duplicate names (ignoring case) numbered "name (2).ext".
func (s *Service) zipEntries(ctx context.Context, owner int64, ids []int64) ([]zipEntry, error) {
	list, err := s.resolve(ctx, s.d.DB.R, owner, selection{ids: ids})
	if errors.Is(err, errMissing) {
		// Some were deleted since the ticket was issued: take what is left.
		list = nil
		for _, id := range ids {
			if l, err := s.resolve(ctx, s.d.DB.R, owner, selection{ids: []int64{id}}); err == nil {
				list = append(list, l...)
			}
		}
	} else if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	out := make([]zipEntry, 0, len(list))
	for _, it := range list {
		if !hasContent(it) {
			continue
		}
		name := uniqueName(entryName(it), used)
		out = append(out, zipEntry{name: name, key: it.BlobKey, size: it.Size, modified: it.CreatedAt})
	}
	return out, nil
}

// entryName is a safe archive name for it: its download name with path
// separators, control characters and leading dots removed.
func entryName(it *items.Item) string {
	n := items.SanitizeFilename(items.DownloadName(it))
	n = strings.TrimLeft(n, ". ")
	n = strings.NewReplacer(":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_").Replace(n)
	if n == "" {
		n = it.Slug
		if e := it.Ext(); e != "" {
			n += "." + e
		}
	}
	return n
}

func uniqueName(n string, used map[string]bool) string {
	key := strings.ToLower(n)
	if !used[key] {
		used[key] = true
		return n
	}
	ext := path.Ext(n)
	stem := strings.TrimSuffix(n, ext)
	if stem == "" { // ".txt": treat it all as the name
		stem, ext = n, ""
	}
	for i := 2; ; i++ {
		c := stem + " (" + strconv.Itoa(i) + ")" + ext
		if k := strings.ToLower(c); !used[k] {
			used[k] = true
			return c
		}
	}
}

// zip handles GET /api/v1/zip/{token}.
func (s *Service) zip(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	t, ok := s.ticket(r.PathValue("token"), p.UserID)
	if !ok {
		return web.Errorf(http.StatusNotFound, "This download link has expired. Start the download again.")
	}
	entries, err := s.zipEntries(r.Context(), p.UserID, t.ids)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return web.Errorf(http.StatusNotFound, "These items are gone.")
	}
	// From here on only the blob store and the client are involved.
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+s.zipFilename()+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := s.writeZip(r.Context(), w, entries); err != nil && r.Context().Err() == nil {
		s.log.Warn("zip download", "err", err)
	}
	return nil
}

func (s *Service) zipFilename() string {
	return "snap-" + s.now().UTC().Format("20060102-150405") + ".zip"
}

// writeZip streams entries into w. A blob that has disappeared is left out.
func (s *Service) writeZip(ctx context.Context, w io.Writer, entries []zipEntry) error {
	zw := zip.NewWriter(w)
	for _, e := range entries {
		f, err := s.d.Blobs.Open(ctx, e.key)
		if errors.Is(err, blob.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Store, Modified: e.modified}
		hdr.SetMode(0o644)
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = f.Close()
			return err
		}
		_, err = io.Copy(dst, f)
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}
