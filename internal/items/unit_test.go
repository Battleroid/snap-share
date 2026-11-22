package items

import (
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/words"
)

func TestValidSlugAndReserved(t *testing.T) {
	for _, s := range []string{"a", "aB3dE9xQ", "brisk-amber-otter", "under_score", strings.Repeat("x", 64)} {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a.b", "a/b", "a b", "ü", "a%20", strings.Repeat("x", 65), "..", "a\x00"} {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true", s)
		}
	}
	for _, s := range []string{"api", "API", "Login", "_KIT", "robots", "ROBOTS", "favicon", "x", "S", "Dashboard", "unlock", "raw", "well-known-no"[:0] + "metrics"} {
		if !IsReserved(s) {
			t.Errorf("IsReserved(%q) = false", s)
		}
	}
	for _, s := range []string{"apis", "xx", "loginx", "robots-txt", "well-known"} {
		if IsReserved(s) {
			t.Errorf("IsReserved(%q) = true", s)
		}
	}
	if !IsReserved("Albums2", "albums2") || IsReserved("albums2", "other") {
		t.Error("extra reserved names")
	}
	names := ReservedNames()
	names[0] = "changed"
	if ReservedNames()[0] != "api" {
		t.Error("ReservedNames exposes internal state")
	}
}

func TestNamingCandidates(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		s := Naming{Scheme: NamingRandom, Length: 8}.candidate(0, "")
		if len(s) != 8 || !ValidSlug(s) || strings.ContainsAny(s, "-_") {
			t.Fatalf("random %q", s)
		}
		seen[s] = true
	}
	if len(seen) < 499 {
		t.Errorf("random slugs repeat: %d distinct", len(seen))
	}
	if s := (Naming{Scheme: NamingRandom, Length: 32}).candidate(0, ""); len(s) != 32 {
		t.Errorf("length 32: %q", s)
	}
	if s := (Naming{Scheme: NamingRandom, Length: 3}).candidate(0, ""); len(s) != DefaultRandomLength {
		t.Errorf("below minimum must use the default: %q", s)
	}
	w := Naming{Scheme: NamingWords}.candidate(0, "")
	in := map[string]bool{}
	for _, x := range words.List() {
		in[x] = true
	}
	parts := strings.Split(w, "-")
	if !ValidSlug(w) || len(parts) < 3 {
		t.Errorf("words %q", w)
	}
	cases := map[string]string{
		"My Photo.JPG":   "My-Photo",
		"café crème.png": "cafe-creme",
		"backup.tar.gz":  "backup-tar",
		"日本語.txt":        "",
		"--x--.md":       "x",
		".hidden":        "hidden",
		"a__b c":         "a__b-c",
		"report (1).pdf": "report-1",
	}
	for in, want := range cases {
		if got := originalStem(in); got != want {
			t.Errorf("originalStem(%q) = %q want %q", in, got, want)
		}
	}
	o := Naming{Scheme: NamingOriginal}
	if o.candidate(0, "My Photo.jpg") != "My-Photo" || o.candidate(1, "My Photo.jpg") != "My-Photo-2" || o.candidate(9, "My Photo.jpg") != "My-Photo-10" {
		t.Error("original suffixes")
	}
	if c := o.candidate(12, "a.jpg"); !strings.HasPrefix(c, "a-") || len(c) != 8 {
		t.Errorf("original random suffix %q", c)
	}
	if c := o.candidate(0, "日本.png"); len(c) != DefaultRandomLength {
		t.Errorf("unsluggable name falls back to random: %q", c)
	}
	c := Naming{Scheme: NamingCustom, Custom: "mine"}
	if c.candidate(0, "") != "mine" || c.candidate(1, "") != "" {
		t.Error("custom has a single candidate")
	}
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		path string
		slug string
		mode mode
		ok   bool
	}{
		{"/aB3dE9xQ", "aB3dE9xQ", modePage, true},
		{"/aB3dE9xQ.png", "aB3dE9xQ", modeRaw, true},
		{"/aB3dE9xQ.PNG", "aB3dE9xQ", modeRaw, true},
		{"/aB3dE9xQ/raw", "aB3dE9xQ", modeRaw, true},
		{"/aB3dE9xQ/download", "aB3dE9xQ", modeDownload, true},
		{"/aB3dE9xQ/thumb", "aB3dE9xQ", modeThumb, true},
		{"/aB3dE9xQ/preview", "aB3dE9xQ", modePreview, true},
		{"/brisk-amber-otter", "brisk-amber-otter", modePage, true},
		{"/s/raw", "s", modeRaw, true}, // parses; "s" is reserved, so the dispatcher 404s
		{"/aB3dE9xQ/unlock", "", 0, false},
		{"/aB3dE9xQ/raw/", "", 0, false},
		{"/aB3dE9xQ/raw/x", "", 0, false},
		{"/aB3dE9xQ.png/raw", "", 0, false},
		{"/a.tar.gz", "", 0, false},
		{"/a.", "", 0, false},
		{"/.png", "", 0, false},
		{"/a/", "", 0, false},
		{"/", "", 0, false},
		{"", "", 0, false},
		{"/a b", "", 0, false},
		{"/a.p-g", "", 0, false},
		{"/" + strings.Repeat("a", 65), "", 0, false},
	}
	for _, c := range cases {
		slug, m, ok := parsePath(c.path)
		if ok != c.ok || slug != c.slug || (ok && m != c.mode) {
			t.Errorf("parsePath(%q) = %q, %d, %v; want %q, %d, %v", c.path, slug, m, ok, c.slug, c.mode, c.ok)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":           "photo.jpg",
		"../../etc/passwd":    "....etcpasswd",
		`C:\Windows\x.exe`:    "C:Windowsx.exe",
		"a\x00b\x1fc\x7f.txt": "abc.txt",
		"evil\u202egnp.exe":   "evilgnp.exe",
		"  spaced  ":          "spaced",
		"trailing dots...":    "trailing dots",
		"..":                  "",
		".bashrc":             ".bashrc",
		"cafe\u0301.png":      "café.png",
		"\xff\xfeinvalid.txt": "invalid.txt",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 200) + ".jpeg"
	got := SanitizeFilename(long)
	if len(got) > MaxFilenameBytes || !strings.HasSuffix(got, ".jpeg") || !utf8.ValidString(got) {
		t.Errorf("long name: %d bytes %q", len(got), got[len(got)-10:])
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("attachment", `naïve "quote"; x.txt`)
	want := `attachment; filename="na_ve _quote__ x.txt"; filename*=UTF-8''na%C3%AFve%20%22quote%22%3B%20x.txt`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := contentDisposition("inline", ""); !strings.Contains(got, `filename="download"`) {
		t.Error(got)
	}
}

func TestSniffAndServedType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	cases := []struct {
		head     []byte
		name     string
		stored   string
		served   string
		viewKind string
	}{
		{png, "x.jpg", "image/png", "image/png", viewImage},
		{[]byte("<!DOCTYPE html><html><script>alert(1)</script>"), "a.png", "text/html", textPlain, viewText},
		{[]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`), "a.svg", "image/svg+xml", "image/svg+xml", viewImage},
		{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "a.txt", "image/svg+xml", "image/svg+xml", viewImage},
		{[]byte("alert(document.cookie)"), "a.js", "text/javascript", textPlain, viewText},
		{[]byte("body{color:red}"), "a.css", "text/css", textPlain, viewText},
		{[]byte(`{"a":1}`), "a.json", "application/json", textPlain, viewText},
		{[]byte("# Title\n\ntext"), "README.md", "text/markdown", textPlain, viewMarkdown},
		{[]byte("package main\n"), "main.go", "text/plain", textPlain, viewText},
		{[]byte("%PDF-1.7\n"), "a.pdf", "application/pdf", "application/pdf", viewPDF},
		{[]byte("\x00\x00\x00\x18ftypmp42\x00\x00"), "v.mov", "video/mp4", "video/mp4", viewVideo},
		{[]byte("\x00\x00\x00\x18ftypavif\x00\x00"), "a.bin", "image/avif", "image/avif", viewImage},
		{[]byte("\x1a\x45\xdf\xa3....webm"), "v", "video/webm", "video/webm", viewVideo},
		{[]byte("ID3\x03\x00"), "a.mp3", "audio/mpeg", "audio/mpeg", viewAudio},
		{[]byte("PK\x03\x04"), "a.zip", "application/zip", "application/zip", viewFile},
		{[]byte("\x00asm\x01\x00\x00\x00"), "a.wasm", "application/wasm", textPlain, viewText},
		{[]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0}, "a.png", "application/octet-stream", "application/octet-stream", viewFile},
		{[]byte(`<?xml version="1.0"?><x/>`), "a.xml", "text/xml", textPlain, viewText},
	}
	for _, c := range cases {
		got := sniff(c.head, c.name)
		if got != c.stored {
			t.Errorf("sniff(%q, %s) = %s want %s", c.head[:min(len(c.head), 12)], c.name, got, c.stored)
			continue
		}
		if s := servedType(got); s != c.served {
			t.Errorf("servedType(%s) = %s want %s", got, s, c.served)
		}
		if v := viewKind(KindFile, got, ""); v != c.viewKind {
			t.Errorf("viewKind(%s) = %s want %s", got, v, c.viewKind)
		}
	}
	for _, m := range []string{"text/html", "application/xhtml+xml", "image/svg+xml; charset=utf-8", "text/javascript", "application/javascript", "application/x-javascript", "text/ecmascript", "application/rss+xml", "application/ld+json", "application/wasm"} {
		if s := servedType(m); s == m || strings.Contains(s, "html") || strings.Contains(s, "script") {
			if m != "image/svg+xml; charset=utf-8" {
				t.Errorf("servedType(%s) = %s", m, s)
			}
		}
	}
	if servedType("image/svg+xml; charset=utf-8") != "image/svg+xml" {
		t.Error("svg keeps its type (the sandbox CSP blocks its scripts)")
	}
}

func TestNormalizeLink(t *testing.T) {
	ok := map[string]string{
		"https://Example.COM/Path?q=1#frag": "https://example.com/Path?q=1#frag",
		"HTTP://example.com:80/":            "http://example.com/",
		"https://example.com:443":           "https://example.com",
		"https://example.com:8443/x":        "https://example.com:8443/x",
		"  https://example.com/a b  ":       "https://example.com/a%20b",
		"http://[::1]:8080/":                "http://[::1]:8080/",
		"https://example.com./":             "https://example.com/",
	}
	for in, want := range ok {
		got, err := NormalizeLink(in)
		if err != nil || got != want {
			t.Errorf("NormalizeLink(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "javascript:alert(1)", "JaVaScRiPt:alert(1)", "data:text/html,<script>alert(1)</script>", "ftp://example.com/",
		"//evil.example/", "/relative", "http://", "https://user:pw@example.com/", "https://user@example.com/",
		"vbscript:x", "file:///etc/passwd", "https:example.com", "https://exa mple.com/", "http://\x00x.com",
		"https://example.com/" + strings.Repeat("a", MaxLinkLength),
	}
	for _, in := range bad {
		if got, err := NormalizeLink(in); err == nil {
			t.Errorf("NormalizeLink(%q) accepted as %q", in, got)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	check := func(spec string, maxDays int, want time.Duration, wantNil bool) {
		t.Helper()
		got, err := parseExpiry(spec, now, maxDays)
		if err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
		if wantNil {
			if got != nil {
				t.Errorf("%q: %v want never", spec, got)
			}
			return
		}
		if got == nil || got.Sub(now) != want {
			t.Errorf("%q (max %d): %v want +%v", spec, maxDays, got, want)
		}
	}
	check("never", 0, 0, true)
	check("", 0, 0, true)
	check("1h", 0, time.Hour, false)
	check("7d", 0, 7*24*time.Hour, false)
	check("90d", 0, 90*24*time.Hour, false)
	check("2026-11-23T12:00:00Z", 0, 24*time.Hour, false)
	check("never", 30, 30*24*time.Hour, false)
	check("90d", 30, 30*24*time.Hour, false)
	check("1d", 30, 24*time.Hour, false)
	for _, bad := range []string{"2d", "forever", "2026-11-21T12:00:00Z", "tomorrow", "-1h"} {
		if _, err := parseExpiry(bad, now, 0); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func testService(t *testing.T, now func() time.Time) *Service {
	t.Helper()
	u, _ := url.Parse("https://snap.test")
	s := &Service{d: Deps{Cfg: &config.Config{PublicURL: u}}, now: now}
	s.unlockKeys = [][]byte{[]byte(strings.Repeat("k", 32)), []byte(strings.Repeat("p", 32))}
	s.rawKeys = s.unlockKeys
	return s
}

func TestUnlockCookie(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key, prev, other := []byte(strings.Repeat("k", 32)), []byte(strings.Repeat("p", 32)), []byte(strings.Repeat("o", 32))
	list := []unlockEntry{{id: 7, exp: now.Add(time.Hour).Unix(), fp: "aabbccdd"}, {id: 9, exp: now.Add(-time.Second).Unix(), fp: "11223344"}}
	v := encodeUnlocks(key, list)
	got := decodeUnlocks([][]byte{key}, v, now)
	if len(got) != 1 || got[0].id != 7 || got[0].fp != "aabbccdd" {
		t.Fatalf("roundtrip (expired entries dropped): %+v", got)
	}
	if len(decodeUnlocks([][]byte{other, key}, encodeUnlocks(prev, list), now)) != 0 {
		t.Error("cookie signed with an unknown key accepted")
	}
	if len(decodeUnlocks([][]byte{key, prev}, encodeUnlocks(prev, list), now)) != 1 {
		t.Error("previous key not accepted during rotation")
	}
	p64, m64, _ := strings.Cut(v, ".")
	tampered := []string{
		"", ".", v + "x", "x" + v, p64, p64 + ".", "." + m64,
	}
	// Another payload carrying the original MAC.
	p8, _, _ := strings.Cut(encodeUnlocks(key, []unlockEntry{{id: 8, exp: now.Add(time.Hour).Unix(), fp: "aabbccdd"}}), ".")
	tampered = append(tampered, p8+"."+m64)
	// Flip each payload character.
	for i := range p64 {
		b := []byte(p64)
		b[i] ^= 1
		tampered = append(tampered, string(b)+"."+m64)
	}
	for _, x := range tampered {
		if got := decodeUnlocks([][]byte{key}, x, now); len(got) != 0 {
			t.Fatalf("tampered cookie %q accepted: %+v", x, got)
		}
	}
	// An attacker-chosen payload with a recomputed MAC under the wrong key.
	if len(decodeUnlocks([][]byte{key}, encodeUnlocks(other, []unlockEntry{{id: 1, exp: now.Add(time.Hour).Unix(), fp: "x"}}), now)) != 0 {
		t.Error("forged cookie accepted")
	}
}

func TestSignedRawURL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	s := testService(t, func() time.Time { return clock })
	it := &Item{ID: 5, Slug: "abc", Visibility: Private, PasswordHash: "h1"}
	q, _ := url.ParseQuery(s.sign(it))
	if !s.validSignature(q, it) {
		t.Fatal("fresh signature rejected")
	}
	other := *it
	other.ID = 6
	if s.validSignature(q, &other) {
		t.Error("signature valid for another item")
	}
	changed := *it
	changed.PasswordHash = "h2"
	if s.validSignature(q, &changed) {
		t.Error("signature survives a password change")
	}
	bad := url.Values{"e": {q.Get("e")}, "s": {q.Get("s")[:10] + "AAAA"}}
	if s.validSignature(bad, it) {
		t.Error("tampered signature accepted")
	}
	later := url.Values{"e": {"9999999999"}, "s": q["s"]}
	if s.validSignature(later, it) {
		t.Error("extended expiry accepted")
	}
	clock = now.Add(rawURLTTL + 2*rawURLWindow + time.Minute)
	if s.validSignature(q, it) {
		t.Error("expired signature accepted")
	}
}
