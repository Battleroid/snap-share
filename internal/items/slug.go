package items

import (
	"crypto/rand"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/words"
)

// SlugCooldownDays is how long a slug freed by a delete, an expiry or a
// rename stays held for its previous owner; 0 frees it at once.
var SlugCooldownDays = settings.Define("items.slug_cooldown_days", 90, settings.Range(0, 3650))

// Naming schemes.
const (
	NamingRandom   = "random"
	NamingWords    = "words"
	NamingOriginal = "original"
	NamingCustom   = "custom"
)

// Slug length limits. Slugs match case-insensitively, so random ones use
// 36 symbols: 10 characters are about 51.7 bits.
const (
	MinRandomLength     = 10
	MaxRandomLength     = 32
	DefaultRandomLength = 10
	MaxSlugLength       = 64
)

const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// reservedNames are first path segments the app uses or will use. Slugs are
// compared to them case-insensitively, and to their stems ("robots" for
// "robots.txt"), because /{slug}.{ext} is a raw URL.
var reservedNames = []string{
	"api", "s", "x", "dl", "static", "healthz", "robots.txt", "favicon.ico",
	"login", "logout", "register", "verify", "reset", "invite", "auth",
	"dashboard", "settings", "admin", "_kit", "unlock", "metrics", "u", "raw",
	".well-known", "new", "albums", "secrets", "sharex", "oembed", "notifications",
}

// rootFileNames are files third parties look for at a site's root to prove
// control of the domain or to read its policies (Bing, Keybase, ads.txt,
// security.txt, sitemaps, Flash/Silverlight and Apple app policies). No
// account may claim them, under any extension, so the dispatcher answers
// 404 for them. rootFilePrefixes cover the verification files with a
// per-site token (Google's google<hex>.html is checked in IsReserved).
var (
	rootFileNames = []string{
		"bingsiteauth", "keybase", "ads", "app-ads", "sellers", "security", "humans",
		"crossdomain", "clientaccesspolicy", "apple-app-site-association", "apple-developer-merchantid-domain-association",
	}
	rootFilePrefixes = []string{"sitemap", "yandex_", "baidu_verify_", "pinterest-", "mywot"}
)

// googleVerification matches google<hex> (Search Console's HTML file).
func googleVerification(s string) bool {
	rest, ok := strings.CutPrefix(s, "google")
	if !ok || rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if c := rest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ReservedNames returns the built-in reserved first path segments. Other
// packages that add a top-level route add its segment here, or pass extras in
// Deps.ExtraReserved.
func ReservedNames() []string { return slices.Clone(reservedNames) }

// IsReserved reports whether slug collides with a reserved path segment or
// a well-known root file (rootFileNames), ignoring case.
func IsReserved(slug string, extra ...string) bool {
	s := strings.ToLower(slug)
	if slices.Contains(rootFileNames, s) || googleVerification(s) {
		return true
	}
	for _, p := range rootFilePrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	check := func(r string) bool {
		r = strings.ToLower(r)
		if s == r {
			return true
		}
		if i := strings.LastIndexByte(r, '.'); i > 0 && s == r[:i] {
			return true
		}
		return false
	}
	for _, r := range reservedNames {
		if check(r) {
			return true
		}
	}
	for _, r := range extra {
		if check(r) {
			return true
		}
	}
	return false
}

func isSlugChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// ValidSlug reports whether s uses only [A-Za-z0-9_-] and is 1 to MaxSlugLength long.
func ValidSlug(s string) bool {
	if s == "" || len(s) > MaxSlugLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isSlugChar(s[i]) {
			return false
		}
	}
	return true
}

func validExt(e string) bool {
	if e == "" || len(e) > 16 {
		return false
	}
	for i := 0; i < len(e); i++ {
		c := e[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// randomSlug returns n lowercase base36 characters from crypto/rand.
func randomSlug(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(base36)))
	for i := range b {
		j, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand does not fail
		}
		b[i] = base36[j.Int64()]
	}
	return string(b)
}

func wordsSlug() string {
	s, err := words.Generate(3, "-")
	if err != nil {
		panic(err)
	}
	return s
}

// originalStem turns a filename into a slug: the name without its last
// extension, accents folded, other characters replaced by '-'.
func originalStem(filename string) string {
	stem := filename
	if i := strings.LastIndexByte(stem, '.'); i > 0 {
		stem = stem[:i]
	}
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(stem) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r < 128 && isSlugChar(byte(r)): // #nosec G115 -- r < 128
			b.WriteRune(r)
			dash = r == '-'
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	if len(out) > 48 {
		out = strings.TrimRight(out[:48], "-_")
	}
	return out
}

// Naming says how to pick a new item's slug.
type Naming struct {
	Scheme string `json:"scheme"` // random | words | original | custom
	Length int    `json:"length,omitempty"`
	Custom string `json:"custom,omitempty"`
}

// candidates yields slug attempts for a naming scheme. Custom slugs have one
// candidate; the others retry with fresh randomness (or suffixes).
func (n Naming) candidate(attempt int, filename string) string {
	switch n.Scheme {
	case NamingCustom:
		if attempt > 0 {
			return ""
		}
		return n.Custom
	case NamingWords:
		return wordsSlug()
	case NamingOriginal:
		stem := originalStem(filename)
		if stem == "" {
			return randomSlug(DefaultRandomLength)
		}
		switch {
		case attempt == 0:
			return stem
		case attempt < 10:
			return stem + "-" + strconv.Itoa(attempt+1)
		default:
			return stem + "-" + randomSlug(6)
		}
	default:
		l := n.Length
		if l < MinRandomLength || l > MaxRandomLength {
			l = DefaultRandomLength
		}
		if attempt > 4 {
			l++ // a crowded namespace: widen
		}
		return randomSlug(min(l, MaxRandomLength))
	}
}

// Path dispatch.

type mode int

const (
	modePage mode = iota
	modeRaw
	modeDownload
	modeThumb
	modePreview
)

var subModes = map[string]mode{"raw": modeRaw, "download": modeDownload, "thumb": modeThumb, "preview": modePreview}

// parsePath splits a fallback path into slug and mode:
//
//	/{slug}            share page
//	/{slug}.{ext}      raw (the extension is ignored)
//	/{slug}/raw        raw
//	/{slug}/download   attachment
//	/{slug}/thumb      thumbnail
//	/{slug}/preview    hover teaser
func parsePath(p string) (string, mode, bool) {
	p, ok := strings.CutPrefix(p, "/")
	if !ok || p == "" {
		return "", 0, false
	}
	first, rest, sub := strings.Cut(p, "/")
	m := modePage
	if sub {
		var known bool
		if m, known = subModes[rest]; !known {
			return "", 0, false
		}
		if !ValidSlug(first) {
			return "", 0, false
		}
		return first, m, true
	}
	if i := strings.LastIndexByte(first, '.'); i >= 0 {
		if !validExt(first[i+1:]) {
			return "", 0, false
		}
		first, m = first[:i], modeRaw
	}
	if !ValidSlug(first) {
		return "", 0, false
	}
	return first, m, true
}
