package stats

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVisitorKey(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	const ua = "Mozilla/5.0 Firefox/133.0"
	k := VisitorKey(secret, "42", "203.0.113.4", ua)
	if len(k) != 32 {
		t.Fatalf("len = %d, want 32 hex chars", len(k))
	}
	if _, err := hex.DecodeString(k); err != nil {
		t.Fatalf("not hex: %v", err)
	}
	if k != VisitorKey(secret, "42", "203.0.113.4", ua) {
		t.Error("not deterministic")
	}
	differ := map[string]string{
		"other subject": VisitorKey(secret, "43", "203.0.113.4", ua),
		"other ip":      VisitorKey(secret, "42", "203.0.113.5", ua),
		"other ua":      VisitorKey(secret, "42", "203.0.113.4", ua+" "),
		"other secret":  VisitorKey([]byte("another secret"), "42", "203.0.113.4", ua),
	}
	for name, other := range differ {
		if other == k {
			t.Errorf("%s gave the same key", name)
		}
	}
	// Length prefixes keep field boundaries apart.
	if VisitorKey(secret, "4", "2203.0.113.4", ua) == VisitorKey(secret, "42", "203.0.113.4", ua) {
		t.Error("subject/ip boundary is ambiguous")
	}
	if VisitorKey(secret, "1", "", "x") == VisitorKey(secret, "1", "x", "") {
		t.Error("ip/ua boundary is ambiguous")
	}
	// IPv6 privacy addresses in one /64 are one visitor; mapped IPv4 is IPv4.
	if VisitorKey(secret, "1", "2001:db8:1:2::a", ua) != VisitorKey(secret, "1", "2001:db8:1:2:ffff::1", ua) {
		t.Error("same /64 gave different keys")
	}
	if VisitorKey(secret, "1", "2001:db8:1:2::a", ua) == VisitorKey(secret, "1", "2001:db8:1:3::a", ua) {
		t.Error("different /64 gave the same key")
	}
	if VisitorKey(secret, "1", "::ffff:192.0.2.1", ua) != VisitorKey(secret, "1", "192.0.2.1", ua) {
		t.Error("mapped IPv4 differs from IPv4")
	}
}

func TestNormalizeIP(t *testing.T) {
	tests := map[string]string{
		"192.168.1.1":          "192.168.1.1",
		"::ffff:192.168.1.1":   "192.168.1.1",
		"2001:db8::1":          "2001:db8::",
		"fe80::1%eth0":         "fe80::",
		"not an ip":            "not an ip",
		"2001:db8:a:b:c:d:e:f": "2001:db8:a:b::",
	}
	for in, want := range tests {
		if got := normalizeIP(in); got != want {
			t.Errorf("normalizeIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefererHost(t *testing.T) {
	tests := map[string]string{
		"":                                     "",
		"https://Example.COM:8443/a?token=x":   "example.com",
		"https://chat.example/channels/1#frag": "chat.example",
		"android-app://com.slack/":             "com.slack",
		"not a url %%":                         "",
		"/relative/path":                       "",
		"https://[2001:db8::1]:443/":           "2001:db8::1",
	}
	for ref, want := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if ref != "" {
			r.Header.Set("Referer", ref)
		}
		if got := RefererHost(r); got != want {
			t.Errorf("RefererHost(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestFromRequest(t *testing.T) {
	secret := []byte("k")
	r := httptest.NewRequest(http.MethodGet, "/abc", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	r.Header.Set("Referer", "https://discord.com/channels/1/2")
	c := Classify(r)

	e := FromRequest(r, c, "198.51.100.7", secret, SubjectItem, "42", KindEmbed)
	want := Event{
		Subject:     "42",
		SubjectType: SubjectItem,
		Kind:        KindPreview,
		IP:          "198.51.100.7",
		UA:          r.UserAgent(),
		UASummary:   "Discord bot",
		Bot:         "Discord",
		RefererHost: "discord.com",
		VisitorKey:  VisitorKey(secret, "42", "198.51.100.7", r.UserAgent()),
	}
	if e != want {
		t.Errorf("FromRequest =\n%+v\nwant\n%+v", e, want)
	}
	if e := FromRequest(r, c, "198.51.100.7", secret, SubjectSecret, "s1", KindLinkOpened); e.Kind != KindLinkOpened || e.Bot != "Discord" {
		t.Errorf("secret event = %+v", e)
	}
}

func TestKinds(t *testing.T) {
	for _, k := range []Kind{KindPreview, KindView, KindEmbed, KindDownload, KindClick, KindPlay} {
		if !k.ItemKind() || k.SecretKind() {
			t.Errorf("%s", k)
		}
	}
	for _, k := range []Kind{KindLinkOpened, KindRevealed, KindFailed, KindBurned} {
		if k.ItemKind() || !k.SecretKind() || k.Opens() {
			t.Errorf("%s", k)
		}
	}
	opens := map[Kind]bool{KindView: true, KindDownload: true, KindClick: true, KindPlay: true}
	for _, k := range []Kind{KindPreview, KindView, KindEmbed, KindDownload, KindClick, KindPlay} {
		if k.Opens() != opens[k] {
			t.Errorf("%s.Opens() = %v", k, k.Opens())
		}
	}
}
