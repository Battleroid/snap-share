// Package stats records who opened shared items and secrets: request
// classification (people, preview bots, crawlers), User-Agent summaries,
// per-item visitor keys, dedupe, a buffered writer with daily rollups,
// retention and the queries behind the stats panel. See docs/stats.md.
package stats

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Kind is an event kind. Item and secret events have separate sets.
type Kind string

// Item event kinds.
const (
	KindPreview  Kind = "preview"  // chat or social unfurler fetched it
	KindView     Kind = "view"     // a browser loaded the share page
	KindEmbed    Kind = "embed"    // raw media loaded from another site
	KindDownload Kind = "download" // raw file or download link
	KindClick    Kind = "click"    // short link followed
	KindPlay     Kind = "play"     // media playback (Range requests merged)
)

// Secret event kinds.
const (
	KindLinkOpened Kind = "link_opened" // valid probe for the secret link
	KindRevealed   Kind = "revealed"    // a view was consumed
	KindFailed     Kind = "failed"      // wrong passphrase
	KindBurned     Kind = "burned"      // last view consumed or burned
)

// Subject types.
const (
	SubjectItem   = "item"
	SubjectSecret = "secret"
)

// ItemKind reports whether k is an item event kind.
func (k Kind) ItemKind() bool {
	switch k {
	case KindPreview, KindView, KindEmbed, KindDownload, KindClick, KindPlay:
		return true
	}
	return false
}

// SecretKind reports whether k is a secret event kind.
func (k Kind) SecretKind() bool {
	switch k {
	case KindLinkOpened, KindRevealed, KindFailed, KindBurned:
		return true
	}
	return false
}

// Opens reports whether an item event of this kind means a person opened
// the item: view, download, click and play. These count towards uniques
// and the opened marker. Embeds don't: they are often fetched by caches
// and CDNs, so they say little about who saw the item.
func (k Kind) Opens() bool {
	switch k {
	case KindView, KindDownload, KindClick, KindPlay:
		return true
	}
	return false
}

// Event is one recorded request.
//
// Subject is the item id (decimal) or the secret id. At is set by the
// Writer when zero. VisitorKey is VisitorKey(secret, Subject, IP, UA).
type Event struct {
	Subject     string // item id or secret id
	SubjectType string // "item" | "secret"
	At          time.Time
	Kind        Kind
	IP          string
	UA          string
	UASummary   string
	Bot         string
	RefererHost string
	VisitorKey  string
}

// Recorder accepts events. Implementations must not block the caller.
type Recorder interface {
	Record(ctx context.Context, e Event)
}

// Nop is a Recorder that discards events.
type Nop struct{}

// Record implements Recorder.
func (Nop) Record(context.Context, Event) {}

// FromRequest builds an event for a request that Classify did not drop.
// ip is the client IP as resolved behind trusted proxies; secret is the
// visitor-key secret. For items, a preview bot's request is always
// KindPreview, whatever kind is passed. For secrets the kind is kept and
// Bot is set; callers decide whether to record previews of secret links.
// At is left zero so the Writer stamps it.
func FromRequest(r *http.Request, c Class, ip string, secret []byte, subjectType, subject string, kind Kind) Event {
	ua := r.UserAgent()
	if subjectType == SubjectItem && c.Preview {
		kind = KindPreview
	}
	return Event{
		Subject:     subject,
		SubjectType: subjectType,
		Kind:        kind,
		IP:          ip,
		UA:          ua,
		UASummary:   SummarizeUA(ua),
		Bot:         c.Bot,
		RefererHost: RefererHost(r),
		VisitorKey:  VisitorKey(secret, subject, ip, ua),
	}
}

// RefererHost returns the lowercase host of the Referer header without
// port, or "". Only the host is kept: paths and queries can carry tokens.
func RefererHost(r *http.Request) string {
	ref := r.Referer()
	if ref == "" || len(ref) > 2048 {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 {
		return ""
	}
	return host
}

// visitorKeyLabel separates visitor keys from other uses of the secret.
const visitorKeyLabel = "snap stats visitor v1"

// VisitorKey identifies one visitor of one subject without storing who it
// is: HMAC-SHA256 over a length-prefixed (label, subject, ip, ua), hex of
// the first 16 bytes. Including the subject means keys for different items
// can't be joined. IPv6 addresses are reduced to their /64, which privacy
// extensions rotate within; IPv4-mapped addresses are unmapped.
func VisitorKey(secret []byte, subject, ip, ua string) string {
	m := hmac.New(sha256.New, secret)
	var n [8]byte
	for _, s := range [...]string{visitorKeyLabel, subject, normalizeIP(ip), ua} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		m.Write(n[:])
		m.Write([]byte(s))
	}
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func normalizeIP(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap().WithZone("")
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.Addr().String()
		}
	}
	return a.String()
}
