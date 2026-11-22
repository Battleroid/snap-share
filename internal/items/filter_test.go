package items

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMatchQuery(t *testing.T) {
	cases := []struct {
		in, match string
		short     []string
	}{
		{"", "", nil},
		{"photo", `"photo"`, nil},
		{"rack photo", `"rack" "photo"`, nil},
		{`say "hi" there`, `"say" """hi""" "there"`, nil},
		{`a"b`, `"a""b"`, nil},
		// FTS5 syntax is matched as text, never interpreted.
		{"cat OR dog", `"cat" "dog"`, []string{"OR"}},
		{"NOT secret", `"NOT" "secret"`, nil},
		{"title:passwd", `"title:passwd"`, nil},
		{"pho* ^start NEAR(a b)", `"pho*" "^start" "NEAR(a"`, []string{"b)"}},
		{"-x +y (z)", `"(z)"`, []string{"-x", "+y"}},
		// Short terms are left for LIKE (trigrams need three characters).
		{"go db", "", []string{"go", "db"}},
		{"go go", "", []string{"go"}},
		{"日本 日本語", `"日本語"`, []string{"日本"}},
		{"photo photo", `"photo"`, nil},
		{"tab\tand\nnewline", `"tab" "and" "newline"`, nil},
		{"\x00ctl\x01", `"ctl"`, nil},
	}
	for _, c := range cases {
		m, s := MatchQuery(c.in)
		if m != c.match || !reflect.DeepEqual(s, c.short) {
			t.Errorf("MatchQuery(%q) = %q %q, want %q %q", c.in, m, s, c.match, c.short)
		}
	}
	// At most maxTerms terms.
	m, _ := MatchQuery(strings.Repeat("word ", 40))
	if m != `"word"` {
		t.Errorf("repeated: %q", m)
	}
	m, _ = MatchQuery(strings.TrimSpace(strings.Repeat("abc ", 5) + strings.Repeat("xyz1 xyz2 xyz3 ", 10)))
	if n := strings.Count(m, `" "`) + 1; n > maxTerms {
		t.Errorf("%d terms", n)
	}
}

func TestLikePattern(t *testing.T) {
	for in, want := range map[string]string{"ab": "%ab%", "5%": `%5\%%`, "a_b": `%a\_b%`, `c:\x`: `%c:\\x%`} {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q want %q", in, got, want)
		}
	}
}

func TestParseFilter(t *testing.T) {
	day := func(s string) *time.Time { t, _ := time.Parse(time.DateOnly, s); return &t }
	cb := time.Date(2026, 11, 22, 10, 0, 0, 123e6, time.UTC)
	full := url.Values{
		"q": {"cat pics"}, "type": {"image"}, "visibility": {"password"}, "expiring": {"1"}, "album": {"12"},
		"tag": {"  Travel  "}, "from": {"2026-01-01"}, "to": {"2026-02-01"}, "opened": {"7d"}, "sort": {"size"},
		"created_before": {cb.Format(time.RFC3339Nano)}, "page": {"3"}, "view": {"grid"},
	}
	f, err := ParseFilter(full)
	if err != nil {
		t.Fatal(err)
	}
	want := Filter{Query: "cat pics", Type: "image", Visibility: "password", Expiring: true, Album: 12, Tag: "Travel",
		From: day("2026-01-01"), To: day("2026-02-01"), Opened: "7d", Sort: "size", CreatedBefore: &cb}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("parsed %+v\nwant %+v", f, want)
	}
	// Values round-trips.
	back, err := ParseFilter(f.Values())
	if err != nil || !reflect.DeepEqual(back, f) {
		t.Errorf("round trip: %+v %v", back, err)
	}
	if !f.Narrowed() || (Filter{Sort: "old"}).Narrowed() {
		t.Error("Narrowed")
	}
	// Defaults are left out.
	if v, _ := ParseFilter(url.Values{"type": {"all"}, "sort": {"new"}, "visibility": {"all"}}); v != (Filter{}) || v.Values().Encode() != "" {
		t.Errorf("defaults: %+v", v)
	}
	// Invalid values are dropped and the first is reported.
	for k, bad := range map[string]string{
		"type": "exe", "visibility": "public", "expiring": "maybe", "album": "x", "tag": strings.Repeat("t", 41),
		"from": "01/02/2026", "opened": "yes", "sort": "random", "created_before": "yesterday",
	} {
		f, err := ParseFilter(url.Values{k: {bad}, "q": {"kept"}})
		if err == nil {
			t.Errorf("%s=%q accepted", k, bad)
		}
		if f != (Filter{Query: "kept"}) {
			t.Errorf("%s=%q: %+v", k, bad, f)
		}
	}
}

func TestTagName(t *testing.T) {
	for in, want := range map[string]string{" travel ": "travel", "two  words": "two words", "Ünï": "Ünï", strings.Repeat("é", 40): strings.Repeat("é", 40)} {
		if got, err := TagName(in); err != nil || got != want {
			t.Errorf("TagName(%q) = %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "   ", "\x00", strings.Repeat("x", 41), "a,b"} {
		if _, err := TagName(in); err == nil {
			t.Errorf("TagName(%q) accepted", in)
		}
	}
}

func TestTypeOf(t *testing.T) {
	cases := map[string]*Item{
		"image": {Kind: KindFile, MIME: "image/svg+xml"},
		"video": {Kind: KindFile, MIME: "video/mp4"},
		"audio": {Kind: KindFile, MIME: "audio/flac"},
		"text":  {Kind: KindFile, MIME: "application/json"},
		"other": {Kind: KindFile, MIME: "application/zip"},
		"paste": {Kind: KindPaste, MIME: "text/plain"},
		"link":  {Kind: KindLink},
		"album": {Kind: KindAlbum},
	}
	for want, it := range cases {
		if got := TypeOf(it); got != want {
			t.Errorf("TypeOf(%s %s) = %s", it.Kind, it.MIME, got)
		}
	}
	if (&Item{Kind: KindFile, MIME: "text/markdown"}).Kind != KindFile || TypeOf(&Item{Kind: KindFile, MIME: "text/markdown"}) != "text" {
		t.Error("markdown")
	}
}
