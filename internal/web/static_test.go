package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticCacheHeaders(t *testing.T) {
	a, err := NewAssets()
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	get := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		return w
	}
	const immutable = "public, max-age=31536000, immutable"
	// patchbay.css requests fonts without ?v=: they are still immutable.
	for _, f := range []string{"SourceSerif4-Variable.woff2", "ObelMono-VF.woff2"} {
		w := get("/static/vendor/patchbay/fonts/" + f)
		if w.Code != 200 || w.Header().Get("Cache-Control") != immutable || w.Header().Get("Content-Type") != "font/woff2" {
			t.Errorf("%s: %d %q %q", f, w.Code, w.Header().Get("Cache-Control"), w.Header().Get("Content-Type"))
		}
	}
	// Other unversioned assets still revalidate; versioned ones are immutable.
	if cc := get("/static/css/app.css").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("unversioned app.css: %q", cc)
	}
	u, _ := a.URL("css/app.css")
	if cc := get(u).Header().Get("Cache-Control"); cc != immutable {
		t.Errorf("versioned app.css: %q", cc)
	}
	// A second request that revalidates by ETag gets a 304.
	etag := get("/static/vendor/patchbay/fonts/ObelMono-VF.woff2").Header().Get("ETag")
	r := httptest.NewRequest("GET", "/static/vendor/patchbay/fonts/ObelMono-VF.woff2", nil)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotModified {
		t.Errorf("revalidation: %d", w.Code)
	}
}

// The preloads must name exactly the URLs patchbay.css requests (resolved
// against the stylesheet), or the browser fetches each font twice.
func TestFontPreloadsMatchCSS(t *testing.T) {
	b, err := staticFS.ReadFile("static/vendor/patchbay/patchbay.css")
	if err != nil {
		t.Fatal(err)
	}
	base, err := templateFS.ReadFile("templates/_layout/base.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"SourceSerif4-Variable.woff2", "ObelMono-VF.woff2"} {
		if !strings.Contains(string(b), `url("fonts/`+f+`")`) {
			t.Errorf("patchbay.css no longer requests fonts/%s", f)
		}
		if !strings.Contains(string(base), `<link rel="preload" as="font" type="font/woff2" crossorigin href="/static/vendor/patchbay/fonts/`+f+`">`) {
			t.Errorf("base.html lacks the preload for %s", f)
		}
	}
}
