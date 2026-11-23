package items

// Visitor event capture (media+stats): every request that reaches a
// share page, short link or item content is classified and, unless it is the
// owner's own or stats.Classify drops it, recorded as one stats event. The
// view/download counters in serve.go are separate and unchanged.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/stats"
)

type events struct {
	rec    stats.Recorder
	key    []byte
	dedupe *stats.Deduper
}

func newEvents(d Deps) events {
	rec := d.Stats
	if rec == nil {
		rec = stats.Nop{}
	}
	return events{rec: rec, key: d.StatsKey, dedupe: stats.NewDeduper(0, 0, d.Now)}
}

// recordEvent records the stats event for a request to it in mode m, if any.
// Repeats of the same kind from one visitor within 30 minutes are merged, so
// a video's Range requests make one play and a reload is not a new view.
func (s *Service) recordEvent(r *http.Request, it *Item, m mode, owner bool) {
	if owner {
		return
	}
	c := stats.Classify(r)
	kind, ok := s.eventKind(r, it, m, c)
	if !ok {
		return
	}
	ip := ""
	if a := clientip.From(r.Context()); a.IsValid() {
		ip = a.String()
	}
	e := stats.FromRequest(r, c, ip, s.ev.key, stats.SubjectItem, strconv.FormatInt(it.ID, 10), kind)
	if s.ourHost(e.RefererHost) {
		e.RefererHost = "" // our own share page is not a referring site
	}
	if e.Kind == stats.KindPreview {
		s.markPreviewed(it.ID)
	}
	if !s.ev.dedupe.Allow(e.Subject, e.VisitorKey, e.Kind) {
		return
	}
	s.ev.rec.Record(r.Context(), e)
}

// eventKind classifies a request that stats.Classify did not drop:
//
//   - preview bots: preview, on the page, raw, download and thumbnail (never
//     the hover teaser)
//   - share page: view (a document load, or a client without fetch metadata);
//     short link: click
//   - /download, and raw content opened as a document: download
//   - raw content loaded as a subresource by another site: embed
//   - raw audio or video played from the share page (or by Range requests
//     from a client without fetch metadata): play
//   - thumbnails and teasers: nothing
func (s *Service) eventKind(r *http.Request, it *Item, m mode, c stats.Class) (stats.Kind, bool) {
	switch {
	case c.Drop:
		return "", false
	case c.Preview:
		return stats.KindPreview, m != modePreview
	}
	dest := strings.ToLower(r.Header.Get("Sec-Fetch-Dest"))
	switch m {
	case modePage:
		if it.Kind == KindLink {
			return stats.KindClick, true
		}
		return stats.KindView, dest == "" || dest == "document"
	case modePlayer:
		// The player page, framed by a chat app.
		return stats.KindEmbed, true
	case modeDownload:
		// An <img> on one of our pages (Markdown) pointing at /download.
		return stats.KindDownload, !s.selfEmbedded(r)
	case modeRaw:
	default: // thumbnails and teasers
		return "", false
	}
	av := strings.HasPrefix(it.MIME, "video/") || strings.HasPrefix(it.MIME, "audio/")
	ref := stats.RefererHost(r)
	fromUs := s.ourHost(ref) || r.Header.Get("Sec-Fetch-Site") == "same-origin"
	switch {
	case dest == "document":
		return stats.KindDownload, true
	case dest != "": // a subresource: <img>, <video>, <audio>, <iframe>, fetch()
		if !fromUs {
			return stats.KindEmbed, true
		}
		if av {
			return stats.KindPlay, true
		}
		return "", false // an image on the share page: counted as the page's view
	case ref != "" && !fromUs:
		return stats.KindEmbed, true
	case av && r.Header.Get("Range") != "":
		return stats.KindPlay, true
	}
	return stats.KindDownload, true
}

// ourHost reports whether host (a Referer host name) is the public or raw origin.
func (s *Service) ourHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, s.d.Cfg.PublicURL.Hostname()) {
		return true
	}
	return s.d.Cfg.RawURL != nil && strings.EqualFold(host, s.d.Cfg.RawURL.Hostname())
}

// markPreviewed notes the first preview of an item for the next counter
// flush, which sets items.previewed_at once.
func (s *Service) markPreviewed(id int64) {
	s.counts.lock()
	defer s.counts.unlock()
	s.markPreviewedLocked(id, s.now().UnixMilli())
}

func (s *Service) markPreviewedLocked(id, at int64) {
	if s.counts.previews == nil {
		s.counts.previews = map[int64]int64{}
	}
	if cur, ok := s.counts.previews[id]; !ok || at < cur {
		s.counts.previews[id] = at
	}
}
