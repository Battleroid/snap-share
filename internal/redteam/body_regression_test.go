package redteam_test

// Regression test: anonymous multipart bodies on account forms used to be
// parsed (file parts spooled to os.TempDir without a limit) before any
// rate limit ran.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnonymousMultipartIsRefused(t *testing.T) {
	e := newEnv(t)
	before := tempMultipart(t)

	for _, path := range []string{"/login", "/register", "/verify", "/reset", "/reset/check", "/reset/request", "/invite", "/logout"} {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		req := e.s.Request(t, "POST", path, pr)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		anonymous()(req) // a script, not a browser
		go func() {
			fw, _ := mw.CreateFormFile("junk", "junk.bin")
			chunk := bytes.Repeat([]byte("A"), 1<<20)
			for range 64 { // 64 MiB, twice the in-memory budget
				if _, err := fw.Write(chunk); err != nil {
					break // the server stopped reading: what we want
				}
			}
			_ = mw.Close()
			_ = pw.Close()
		}()
		res, err := e.client.Do(req)
		_ = pr.CloseWithError(io.ErrClosedPipe)
		if err != nil {
			continue // the server hung up on the body: also fine
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("POST %s multipart: %d, want 415", path, res.StatusCode)
		}
	}
	time.Sleep(100 * time.Millisecond)
	for f, n := range tempMultipart(t) {
		// Other packages' tests share os.TempDir; only a spill of our size counts.
		if _, old := before[f]; !old && n > 16<<20 {
			t.Errorf("multipart spooled %d bytes to %s", n, f)
		}
	}

	// A large urlencoded body is cut off at the form limit, too.
	big := url.Values{"login": {"x"}, "password": {strings.Repeat("p", 2<<20)}}.Encode()
	req := e.s.Request(t, "POST", "/login", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := e.client.Do(req)
	if err == nil {
		_ = res.Body.Close()
		if res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("2 MiB form: %d, want 413", res.StatusCode)
		}
	}
}

func tempMultipart(t *testing.T) map[string]int64 {
	out := map[string]int64{}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && !strings.HasSuffix(m, ".tmp") {
			out[m] = fi.Size()
		}
	}
	return out
}
