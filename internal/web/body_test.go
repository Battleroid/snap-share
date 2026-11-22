package web

import (
	"bytes"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bodyRouter(t *testing.T, timeout time.Duration) http.Handler {
	t.Helper()
	assets, err := NewAssets()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rd, err := NewRenderer(assets, func(*http.Request) Site { return Site{Name: "snap"} }, log)
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRouter(NewCSP("https://snap.test", ""), nil, &ErrorPages{R: rd})
	rt.bodyTimeout = timeout
	form := func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "field="+r.PostFormValue("field"))
	}
	all := func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}
	// Reads its small body, then answers slowly (a long download).
	slow := func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		for range 5 {
			if r.Context().Err() != nil {
				return
			}
			_, _ = io.WriteString(w, "x")
			http.NewResponseController(w).Flush()
			time.Sleep(timeout / 2)
		}
	}
	rt.HandleFunc("POST /form", form, Public())
	rt.HandleFunc("POST /slow", slow, Public())
	rt.HandleFunc("POST /upload", all, Public(), LargeBody())
	return rt.Handler()
}

func TestBodyLimits(t *testing.T) {
	h := bodyRouter(t, BodyReadTimeout)
	send := func(path, ct string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "https://snap.test"+path, body)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("field", "v")
	_ = mw.Close()

	if rec := send("/form", "application/x-www-form-urlencoded", strings.NewReader("field=v")); rec.Code != 200 || rec.Body.String() != "field=v" {
		t.Errorf("small form: %d %q", rec.Code, rec.Body.String())
	}
	// Multipart is refused before anything reads it.
	if rec := send("/form", mw.FormDataContentType(), bytes.NewReader(mp.Bytes())); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("multipart form: %d", rec.Code)
	}
	// A declared length over the limit is refused at once; an undeclared one
	// (chunked) is cut off at the limit.
	big := strings.Repeat("a", MaxFormBody+1)
	if rec := send("/form", "application/x-www-form-urlencoded", strings.NewReader("field="+big)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large form: %d", rec.Code)
	}
	if rec := send("/form", "application/x-www-form-urlencoded", io.MultiReader(strings.NewReader("field="), strings.NewReader(big))); rec.Code == 200 {
		t.Errorf("chunked large form accepted")
	}
	// Upload routes are left alone.
	if rec := send("/upload", mw.FormDataContentType(), io.MultiReader(bytes.NewReader(mp.Bytes()), strings.NewReader(big))); rec.Code != 200 {
		t.Errorf("upload route: %d %s", rec.Code, rec.Body.String())
	}
}

// slowBody delivers its bytes one at a time, slowly.
type slowBody struct {
	n     int
	delay time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.n == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	b.n--
	p[0] = 'a'
	return 1, nil
}

// Over a real connection, a body that trickles in past the read timeout is
// cut off. The deadline is lifted once the body is read, so a response that
// takes longer than the timeout is not cut short.
func TestBodyReadDeadline(t *testing.T) {
	const timeout = 300 * time.Millisecond
	srv := httptest.NewServer(bodyRouter(t, timeout))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/form", io.NopCloser(&slowBody{n: 100, delay: 50 * time.Millisecond}))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		_ = res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("slow body accepted")
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("slow body cut off after %v", d)
	}

	req, _ = http.NewRequest("POST", srv.URL+"/slow", strings.NewReader("field=v"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || string(b) != "xxxxx" {
		t.Fatalf("long response cut short: %q %v", b, err)
	}
}
