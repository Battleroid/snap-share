package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRawHost(t *testing.T) {
	raw, _ := url.Parse("https://raw.test")
	var seen *http.Request
	h := RawHost(raw, "https://app.test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		http.SetCookie(w, &http.Cookie{Name: "c", Value: "v"})
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct {
		method, host, path string
		status             int
		location           string
	}{
		{"GET", "raw.test", "/abc.png", 200, ""},
		{"HEAD", "RAW.test", "/abc/raw?e=1&s=x", 200, ""},
		{"GET", "raw.test", "/abc/download", 200, ""},
		{"GET", "raw.test", "/abc/thumb", 200, ""},
		{"GET", "raw.test", "/abc/preview", 200, ""},
		{"GET", "raw.test", "/abc", 302, "https://app.test/abc"},
		{"GET", "raw.test", "/", 302, "https://app.test/"},
		{"GET", "raw.test", "/login?next=%2Fx", 302, "https://app.test/login?next=%2Fx"},
		{"GET", "raw.test", "/static/js/app.js", 302, "https://app.test/static/js/app.js"},
		{"GET", "raw.test", "/abc/other", 302, "https://app.test/abc/other"},
		{"GET", "raw.test", "/a.b/raw", 302, "https://app.test/a.b/raw"},
		{"GET", "raw.test", "/abc.", 302, "https://app.test/abc."},
		{"POST", "raw.test", "/login", 404, ""},
		{"PUT", "raw.test", "/abc.png", 404, ""},
		{"POST", "app.test", "/login", 200, ""},
		{"GET", "app.test", "/login", 200, ""},
	}
	for _, c := range cases {
		seen = nil
		req := httptest.NewRequest(c.method, "https://"+c.host+c.path, nil)
		req.Header.Set("Cookie", "__Host-snap_session=s")
		req.Header.Set("Authorization", "Bearer k")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.status || rec.Header().Get("Location") != c.location {
			t.Errorf("%s %s%s: %d %q, want %d %q", c.method, c.host, c.path, rec.Code, rec.Header().Get("Location"), c.status, c.location)
		}
		onRaw := c.host != "app.test"
		if onRaw && rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s %s%s set a cookie on the raw host", c.method, c.host, c.path)
		}
		if seen != nil && onRaw && (seen.Header.Get("Cookie") != "" || seen.Header.Get("Authorization") != "") {
			t.Errorf("%s %s%s: credentials reached the handler on the raw host", c.method, c.host, c.path)
		}
		if seen != nil && !onRaw && seen.Header.Get("Cookie") == "" {
			t.Errorf("%s %s%s: cookie removed on the app host", c.method, c.host, c.path)
		}
	}
	// Without a raw origin nothing changes.
	if RawHost(nil, "https://app.test")(h) == nil {
		t.Fatal("nil handler")
	}
}
