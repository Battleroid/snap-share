package stats

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"
)

// UnknownBot is the preview name for requests with no User-Agent.
const UnknownBot = "Unknown"

// maxUALen bounds how much of a User-Agent is inspected and stored.
const maxUALen = 512

// Class is the result of classifying a request.
//
//   - Drop: don't record it (crawler, scanner, monitor, HEAD, prefetch).
//   - Preview: a chat or social unfurler; record it as KindPreview with Bot.
//   - neither: a person.
//
// Bot is the rule's display name ("Discord") for previews and named drops.
type Class struct {
	Drop    bool
	Bot     string
	Preview bool
}

//go:embed bots.txt
var botsTxt string

// rules is parsed once from bots.txt and only read afterwards.
var rules = mustParseRules(botsTxt)

type rule struct {
	preview bool
	name    string
	tokens  []string // lowercase; all must appear
}

func mustParseRules(src string) []rule {
	rs, err := parseRules(src)
	if err != nil {
		panic(err)
	}
	return rs
}

func parseRules(src string) ([]rule, error) {
	var rs []rule
	for i, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			return nil, fmt.Errorf("stats: bots.txt line %d: want 3 fields", i+1)
		}
		var r rule
		switch strings.TrimSpace(parts[0]) {
		case "preview":
			r.preview = true
		case "drop":
		default:
			return nil, fmt.Errorf("stats: bots.txt line %d: unknown action %q", i+1, parts[0])
		}
		r.name = strings.TrimSpace(parts[1])
		for tok := range strings.SplitSeq(parts[2], "&") {
			if tok = strings.ToLower(strings.TrimSpace(tok)); tok != "" {
				r.tokens = append(r.tokens, tok)
			}
		}
		if r.name == "" || len(r.tokens) == 0 {
			return nil, fmt.Errorf("stats: bots.txt line %d: empty name or token", i+1)
		}
		rs = append(rs, r)
	}
	return rs, nil
}

// matchRule returns the first rule matching ua.
func matchRule(ua string) (rule, bool) {
	if len(ua) > maxUALen {
		ua = ua[:maxUALen]
	}
	lower := strings.ToLower(ua)
	for _, r := range rules {
		ok := true
		for _, tok := range r.tokens {
			if !strings.Contains(lower, tok) {
				ok = false
				break
			}
		}
		if ok {
			return r, true
		}
	}
	return rule{}, false
}

// Classify decides whether a request to a share page, raw file or short
// link is a person, a preview bot or something to drop. The rules are in
// bots.txt; see docs/stats.md. It does not know who is signed in: callers
// must skip the owner's own requests themselves.
func Classify(r *http.Request) Class {
	if r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return Class{Drop: true}
	}
	if isPrefetch(r.Header) {
		return Class{Drop: true}
	}
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" {
		return Class{Preview: true, Bot: UnknownBot}
	}
	if rl, ok := matchRule(ua); ok {
		return Class{Drop: !rl.preview, Preview: rl.preview, Bot: rl.name}
	}
	return Class{}
}

// isPrefetch reports a speculative browser fetch: the page may never be
// shown, and if it is, no further request is made either way.
func isPrefetch(h http.Header) bool {
	for _, k := range []string{"Sec-Purpose", "Purpose", "X-Moz", "X-Purpose"} {
		if v := strings.ToLower(h.Get(k)); strings.Contains(v, "prefetch") || strings.Contains(v, "prerender") {
			return true
		}
	}
	return false
}

// SummarizeUA turns a User-Agent into a short label for the stats panel:
// "Firefox · macOS", "Chrome · Android", "Safari · iOS", "curl",
// "Discord bot". It returns "" for an empty User-Agent and "unknown" when
// nothing is recognised.
func SummarizeUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > maxUALen {
		ua = ua[:maxUALen]
	}
	if ua == "" {
		return ""
	}
	if rl, ok := matchRule(ua); ok {
		return rl.name + " bot"
	}
	if tool := cliTool(ua); tool != "" {
		return tool
	}
	browser, system := browserName(ua), osName(ua)
	switch {
	case browser != "" && system != "":
		return browser + " · " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	return firstProduct(ua)
}

// cliTools are matched as a prefix of the User-Agent, where these tools
// put their name.
var cliTools = []struct{ prefix, name string }{
	{"curl/", "curl"},
	{"wget/", "Wget"},
	{"httpie/", "HTTPie"},
	{"sp/", "sp"},
	{"go-http-client/", "Go http client"},
	{"python-requests/", "python-requests"},
	{"python-httpx/", "Python"},
	{"python-urllib/", "Python"},
	{"python/", "Python"},
	{"aiohttp/", "Python"},
	{"okhttp/", "okhttp"},
	{"java/", "Java"},
	{"apache-httpclient/", "Java"},
	{"node-fetch", "Node.js"},
	{"node/", "Node.js"},
	{"undici", "Node.js"},
	{"axios/", "Node.js"},
	{"deno/", "Deno"},
	{"bun/", "Bun"},
	{"libwww-perl/", "Perl"},
	{"ruby", "Ruby"},
	{"rust-reqwest", "Rust"},
	{"reqwest/", "Rust"},
	{"lynx/", "Lynx"},
	{"w3m/", "w3m"},
	{"links ", "Links"},
}

func cliTool(ua string) string {
	lower := strings.ToLower(ua)
	for _, t := range cliTools {
		if strings.HasPrefix(lower, t.prefix) {
			return t.name
		}
	}
	if strings.Contains(lower, "powershell/") {
		return "PowerShell"
	}
	return ""
}

// browserName checks tokens in order: browsers built on Chrome or Safari
// keep those products in their User-Agent, so the derived ones come first.
func browserName(ua string) string {
	has := func(toks ...string) bool {
		for _, t := range toks {
			if strings.Contains(ua, t) {
				return true
			}
		}
		return false
	}
	switch {
	case has("HeadlessChrome/"):
		return "Headless Chrome"
	case has("FBAN/", "FBAV/", "FB_IAB/"):
		return "Facebook app"
	case has("Instagram "):
		return "Instagram app"
	case has("GSA/"):
		return "Google app"
	case has("Edg/", "EdgA/", "EdgiOS/", "Edge/"):
		return "Edge"
	case has("OPR/", "OPiOS/", "OPT/", "Opera"):
		return "Opera"
	case has("Vivaldi/"):
		return "Vivaldi"
	case has("YaBrowser/"):
		return "Yandex Browser"
	case has("SamsungBrowser/"):
		return "Samsung Internet"
	case has("UCBrowser/"):
		return "UC Browser"
	case has("DuckDuckGo/", "Ddg/"):
		return "DuckDuckGo"
	case has("Brave/"):
		return "Brave"
	case has("Electron/"):
		return "Electron"
	case has("FxiOS/", "Firefox/"):
		return "Firefox"
	case has("CriOS/"):
		return "Chrome"
	case has("; wv)"):
		return "WebView"
	case has("Chromium/"):
		return "Chromium"
	case has("Chrome/"):
		return "Chrome"
	case has("MSIE ", "Trident/"):
		return "Internet Explorer"
	case has("Safari/") && has("Version/"):
		return "Safari"
	case has("AppleWebKit/"):
		return "WebView"
	}
	return ""
}

func osName(ua string) string {
	has := func(toks ...string) bool {
		for _, t := range toks {
			if strings.Contains(ua, t) {
				return true
			}
		}
		return false
	}
	switch {
	case has("iPhone", "iPad", "iPod"):
		return "iOS"
	case has("Android"):
		return "Android"
	case has("CrOS"):
		return "ChromeOS"
	case has("Windows"):
		return "Windows"
	case has("Mac OS X", "Macintosh"):
		return "macOS"
	case has("FreeBSD"):
		return "FreeBSD"
	case has("OpenBSD"):
		return "OpenBSD"
	case has("Linux", "X11"):
		return "Linux"
	}
	return ""
}

// firstProduct returns the leading product name ("SomeApp" from
// "SomeApp/1.2 (…)"), limited to safe characters, or "unknown".
func firstProduct(ua string) string {
	end := strings.IndexAny(ua, "/ (;")
	if end < 0 {
		end = len(ua)
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return -1
	}, ua[:end])
	if len(name) > 32 {
		name = name[:32]
	}
	if name == "" || strings.EqualFold(name, "mozilla") {
		return "unknown"
	}
	return name
}
