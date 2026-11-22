package items_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

func randomText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const alpha = "abcdefghijklmnopqrstuvwxyz \n"
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return string(b)
}

func (e *env) initUpload(user, name string, size int, opts map[string]any) (*resp, map[string]any) {
	e.t.Helper()
	if opts == nil {
		opts = map[string]any{}
	}
	return e.doJSON("POST", "/api/v1/uploads", map[string]any{"name": name, "size": size, "type": "text/plain", "options": opts}, as(user))
}

func (e *env) putPart(user, id string, offset int, data string) (*resp, map[string]any) {
	e.t.Helper()
	res, body := e.do("PUT", fmt.Sprintf("/api/v1/uploads/%s?offset=%d", id, offset), strings.NewReader(data), as(user), header("Accept", "application/json"))
	return res, decode(e.t, body)
}

func TestChunkedUpload(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	content := randomText(300_000)
	res, up := e.initUpload("ann", "notes.txt", len(content), map[string]any{"expires": "7d"})
	if res.StatusCode != 201 || num(up, "part_size") != 8<<20 {
		t.Fatalf("init: %d %v", res.StatusCode, up)
	}
	id := str(up, "id")
	if _, reserved := e.usage(1); reserved != int64(len(content)) {
		t.Errorf("reserved %d", reserved)
	}
	parts := []int{0, 100_000, 200_000, len(content)}
	for i := 0; i+1 < len(parts); i++ {
		res, body := e.putPart("ann", id, parts[i], content[parts[i]:parts[i+1]])
		if res.StatusCode != 200 || num(body, "offset") != int64(parts[i+1]) || res.Header.Get("Upload-Offset") != fmt.Sprint(parts[i+1]) {
			t.Fatalf("part %d: %d %v", i, res.StatusCode, body)
		}
		h, _ := e.do("HEAD", "/api/v1/uploads/"+id, nil, as("ann"))
		if h.Header.Get("Upload-Offset") != fmt.Sprint(parts[i+1]) || h.Header.Get("Upload-Length") != fmt.Sprint(len(content)) {
			t.Fatalf("HEAD after part %d: %v", i, h.Header)
		}
	}
	res, it := e.doJSON("POST", "/api/v1/uploads/"+id+"/complete", nil, as("ann"))
	if res.StatusCode != 201 {
		t.Fatalf("complete: %d %v", res.StatusCode, it)
	}
	if str(it, "sha256") != sha(content) || num(it, "size") != int64(len(content)) || str(it, "name") != "notes.txt" || it["expires_at"] == nil {
		t.Errorf("item: %v", it)
	}
	for _, k := range []string{"url", "raw_url", "deletion_url"} {
		if !strings.HasPrefix(str(it, k), e.s.URL+"/") {
			t.Errorf("%s = %q", k, str(it, k))
		}
	}
	if used, reserved := e.usage(1); used != int64(len(content)) || reserved != 0 {
		t.Errorf("usage after complete: used %d reserved %d", used, reserved)
	}
	_, raw := e.do("GET", path(str(it, "raw_url")), nil)
	if raw != content {
		t.Error("raw content differs")
	}
	if r, page := e.do("GET", path(str(it, "url")), nil); r.StatusCode != 200 || !strings.Contains(page, "notes.txt") || !strings.Contains(page, "pb-code") {
		t.Errorf("share page: %d", r.StatusCode)
	}
	// The upload is gone once completed.
	if r, _ := e.doJSON("POST", "/api/v1/uploads/"+id+"/complete", nil, as("ann")); r.StatusCode != 404 {
		t.Errorf("second complete: %d", r.StatusCode)
	}
	if n := e.tmpFiles(); len(n) != 0 {
		t.Errorf("temp files left: %v", n)
	}
}

func TestChunkedWrongOffsetAndIncomplete(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	_, up := e.initUpload("ann", "a.txt", 10, nil)
	id := str(up, "id")
	res, body := e.putPart("ann", id, 5, "hello")
	if res.StatusCode != 409 || num(body, "offset") != 0 || res.Header.Get("Upload-Offset") != "0" {
		t.Fatalf("wrong offset: %d %v", res.StatusCode, body)
	}
	if res, _ := e.putPart("ann", id, 0, "hello"); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	// Replaying the same part is a wrong offset now.
	if res, body := e.putPart("ann", id, 0, "hello"); res.StatusCode != 409 || num(body, "offset") != 5 {
		t.Errorf("replay: %d %v", res.StatusCode, body)
	}
	if res, body := e.doJSON("POST", "/api/v1/uploads/"+id+"/complete", nil, as("ann")); res.StatusCode != 409 || num(body, "offset") != 5 {
		t.Errorf("incomplete complete: %d %v", res.StatusCode, body)
	}
	if res, _ := e.putPart("ann", id, 5, "hello world"); res.StatusCode != 413 {
		t.Errorf("past declared size: %d", res.StatusCode)
	}
	if res, _ := e.do("PUT", "/api/v1/uploads/"+id, strings.NewReader("x"), as("ann")); res.StatusCode != 400 {
		t.Errorf("no offset: %d", res.StatusCode)
	}
}

func TestChunkedResumeAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	content := randomText(50_000)
	_, up := e.initUpload("ann", "r.txt", len(content), nil)
	id := str(up, "id")
	if res, _ := e.putPart("ann", id, 0, content[:20_000]); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	e.svc.DropUploadState() // the process restarts: the running hash is gone
	h, _ := e.do("HEAD", "/api/v1/uploads/"+id, nil, as("ann"))
	if h.Header.Get("Upload-Offset") != "20000" {
		t.Fatalf("offset after restart: %q", h.Header.Get("Upload-Offset"))
	}
	if res, _ := e.putPart("ann", id, 20_000, content[20_000:]); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res, it := e.doJSON("POST", "/api/v1/uploads/"+id+"/complete", nil, as("ann"))
	if res.StatusCode != 201 || str(it, "sha256") != sha(content) {
		t.Fatalf("complete after restart: %d %v (want sha %s)", res.StatusCode, it, sha(content))
	}
}

func TestAbortReleasesQuota(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.exec(`UPDATE users SET quota_bytes = 1000000 WHERE id = 1`)
	res, up := e.initUpload("ann", "a.bin", 600_000, nil)
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	if res, body := e.initUpload("ann", "b.bin", 600_000, nil); res.StatusCode != 413 || !strings.Contains(str(body, "error"), "quota") {
		t.Fatalf("over quota: %d %v", res.StatusCode, body)
	}
	_, _ = e.putPart("ann", str(up, "id"), 0, randomText(1000))
	if res, _ := e.do("DELETE", "/api/v1/uploads/"+str(up, "id"), nil, as("ann")); res.StatusCode != 204 {
		t.Fatalf("abort: %d", res.StatusCode)
	}
	if used, reserved := e.usage(1); used != 0 || reserved != 0 {
		t.Errorf("after abort: used %d reserved %d", used, reserved)
	}
	if n := e.tmpFiles(); len(n) != 0 {
		t.Errorf("partial left: %v", n)
	}
	if res, _ := e.initUpload("ann", "b.bin", 600_000, nil); res.StatusCode != 201 {
		t.Errorf("after release: %d", res.StatusCode)
	}
}

func TestConcurrentUploadsCannotOvershootQuota(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.setting("uploads.default_quota_bytes", 1_000_000)
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := e.initUpload("ann", "c.bin", 100_000, nil)
			switch res.StatusCode {
			case 201:
				ok.Add(1)
			case 413:
				refused.Add(1)
			default:
				t.Errorf("status %d", res.StatusCode)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 || refused.Load() != 10 {
		t.Errorf("chunked: %d accepted, %d refused", ok.Load(), refused.Load())
	}
	if _, reserved := e.usage(1); reserved != 1_000_000 {
		t.Errorf("reserved %d", reserved)
	}

	// Streamed uploads race for what is left after releasing everything.
	e.exec(`DELETE FROM uploads`)
	e.exec(`UPDATE users SET reserved_bytes = 0`)
	body := randomText(300_000)
	ok.Store(0)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, b := e.do("PUT", "/api/v1/upload/s.txt", strings.NewReader(body), as("ann"), header("Accept", "application/json"))
			switch res.StatusCode {
			case 201:
				ok.Add(1)
			case 413:
			default:
				t.Errorf("stream status %d %s", res.StatusCode, b)
			}
		}()
	}
	wg.Wait()
	used, reserved := e.usage(1)
	if ok.Load() != 3 || used != 900_000 || reserved != 0 {
		t.Errorf("streamed: %d accepted, used %d, reserved %d", ok.Load(), used, reserved)
	}
}

func multipartBody(t *testing.T, fields map[string]string, filename string, content io.Reader) (io.Reader, string) {
	t.Helper()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		if filename != "" {
			fw, err := mw.CreateFormFile("file", filename)
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(fw, content); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		_ = pw.CloseWithError(mw.Close())
	}()
	return pr, mw.FormDataContentType()
}

func TestMaxBytes(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.setting("uploads.max_bytes", 100_000)
	big := randomText(150_000)
	if res, _ := e.initUpload("ann", "big.txt", len(big), nil); res.StatusCode != 413 {
		t.Errorf("chunked init over max: %d", res.StatusCode)
	}
	if res, _ := e.do("PUT", "/api/v1/upload/big.txt", strings.NewReader(big), as("ann")); res.StatusCode != 413 {
		t.Errorf("PUT over max: %d", res.StatusCode)
	}
	// No Content-Length: the limit applies while streaming.
	if res, _ := e.do("PUT", "/api/v1/upload/big.txt", io.MultiReader(strings.NewReader(big)), as("ann")); res.StatusCode != 413 {
		t.Errorf("chunked-encoding PUT over max: %d", res.StatusCode)
	}
	body, ct := multipartBody(t, map[string]string{"expires": "1d"}, "big.txt", strings.NewReader(big))
	if res, b := e.do("POST", "/api/v1/upload", body, as("ann"), header("Content-Type", ct)); res.StatusCode != 413 {
		t.Errorf("multipart over max: %d %s", res.StatusCode, b)
	}
	// The user's own limit applies when lower than the site's.
	e.exec(`UPDATE users SET max_upload_bytes = 50000 WHERE id = 1`)
	if res, _ := e.do("PUT", "/api/v1/upload/mid.txt", strings.NewReader(big[:60_000]), as("ann")); res.StatusCode != 413 {
		t.Errorf("PUT over user max: %d", res.StatusCode)
	}
	if res, _ := e.do("PUT", "/api/v1/upload/ok.txt", strings.NewReader(big[:40_000]), as("ann")); res.StatusCode != 201 {
		t.Errorf("PUT under user max: %d", res.StatusCode)
	}
	used, reserved := e.usage(1)
	if used != 40_000 || reserved != 0 {
		t.Errorf("used %d reserved %d", used, reserved)
	}
	var n int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM uploads`).Scan(&n)
	if n != 0 || len(e.tmpFiles()) != 0 {
		t.Errorf("leftovers: %d uploads rows, temp %v", n, e.tmpFiles())
	}
}

func TestMultipartStreamsWithoutTempFiles(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if os.TempDir() != tmp {
		t.Fatalf("TempDir %s", os.TempDir())
	}
	// Larger than ParseMultipartForm's 32 MiB memory budget, which would spool to disk.
	const size = 34 << 20
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	var src bytes.Buffer
	for src.Len() < size {
		src.Write(chunk)
	}
	want := sha(src.String())
	body, ct := multipartBody(t, map[string]string{"name": "big.log", "visibility": "private"}, "ignored.bin", bytes.NewReader(src.Bytes()))
	res, b := e.do("POST", "/api/v1/upload", body, as("ann"), header("Content-Type", ct), header("Accept", "application/json"))
	if res.StatusCode != 201 {
		t.Fatalf("multipart: %d %s", res.StatusCode, b)
	}
	it := decode(t, b)
	if str(it, "sha256") != want || str(it, "name") != "big.log" || str(it, "visibility") != "private" {
		t.Errorf("item %v", it)
	}
	ents, _ := os.ReadDir(tmp)
	if len(ents) != 0 {
		t.Errorf("files written under os.TempDir(): %v", ents)
	}
}

func TestMultipartAndPutResponses(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	png := "\x89PNG\r\n\x1a\n" + randomText(200)
	body, ct := multipartBody(t, map[string]string{"naming": "original"}, "Screen Shot.png", strings.NewReader(png))
	res, b := e.do("POST", "/api/v1/upload", body, as("ann"), header("Content-Type", ct))
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	it := decode(t, b)
	if str(it, "slug") != "Screen-Shot" || str(it, "type") != "image/png" || str(it, "thumbnail_url") != str(it, "raw_url") ||
		!strings.HasSuffix(str(it, "raw_url"), "/Screen-Shot.png") || !strings.Contains(str(it, "deletion_url"), "/x#") {
		t.Errorf("multipart item: %v", it)
	}
	// Same name again: suffix on collision.
	if second := e.put("ann", "Screen%20Shot.png", png, "naming=original"); str(second, "slug") != "Screen-Shot-2" {
		t.Errorf("collision slug %q", str(second, "slug"))
	}
	// curl: just the URL.
	res, b = e.do("PUT", "/api/v1/upload/a.txt?format=url", strings.NewReader("hi"), as("ann"))
	if res.StatusCode != 201 || !strings.HasPrefix(b, e.s.URL+"/") || !strings.HasSuffix(b, "\n") || strings.Count(b, "\n") != 1 {
		t.Errorf("format=url: %d %q", res.StatusCode, b)
	}
	res, b = e.do("PUT", "/api/v1/upload/b.txt", strings.NewReader("hi"), as("ann"), header("Accept", "text/plain"))
	if res.StatusCode != 201 || strings.Contains(b, "{") {
		t.Errorf("Accept text/plain: %q", b)
	}
	words := e.put("ann", "w.txt", "hi", "naming=words")
	if strings.Count(str(words, "slug"), "-") < 2 {
		t.Errorf("words slug %q", str(words, "slug"))
	}
	custom := e.put("ann", "c.txt", "hi", "slug=my-file")
	if str(custom, "slug") != "my-file" {
		t.Errorf("custom slug %q", str(custom, "slug"))
	}
	for q, want := range map[string]int{"slug=MY-FILE": 409, "slug=login": 400, "slug=a.b": 400, "naming=bogus": 400, "expires=2d": 400, "visibility=public": 400, "length=4": 400} {
		if res, b := e.do("PUT", "/api/v1/upload/x.txt?"+q, strings.NewReader("hi"), as("ann")); res.StatusCode != want {
			t.Errorf("%s: %d %s", q, res.StatusCode, b)
		}
	}
	long := e.put("ann", "l.txt", "hi", "length=20")
	if len(str(long, "slug")) != 20 {
		t.Errorf("length=20 slug %q", str(long, "slug"))
	}
	// Multipart without a file field.
	body, ct = multipartBody(t, map[string]string{"name": "x"}, "", nil)
	if res, _ := e.do("POST", "/api/v1/upload", body, as("ann"), header("Content-Type", ct)); res.StatusCode != 400 {
		t.Errorf("no file: %d", res.StatusCode)
	}
	if used, reserved := e.usage(1); reserved != 0 || used == 0 {
		t.Errorf("used %d reserved %d", used, reserved)
	}
}

func TestBlockedExtensions(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.setting("uploads.blocked_extensions", []string{".EXE", "bat"})
	if res, _ := e.do("PUT", "/api/v1/upload/setup.exe", strings.NewReader("MZ"), as("ann")); res.StatusCode != 415 {
		t.Errorf("blocked PUT: %d", res.StatusCode)
	}
	if res, _ := e.initUpload("ann", "run.BAT", 5, nil); res.StatusCode != 415 {
		t.Errorf("blocked chunked: %d", res.StatusCode)
	}
	body, ct := multipartBody(t, nil, "x.exe", strings.NewReader("MZ"))
	if res, _ := e.do("POST", "/api/v1/upload", body, as("ann"), header("Content-Type", ct)); res.StatusCode != 415 {
		t.Errorf("blocked multipart: %d", res.StatusCode)
	}
	if res, _ := e.do("PUT", "/api/v1/upload/fine.txt", strings.NewReader("ok"), as("ann")); res.StatusCode != 201 {
		t.Errorf("allowed: %d", res.StatusCode)
	}
	if used, reserved := e.usage(1); used != 2 || reserved != 0 {
		t.Errorf("used %d reserved %d", used, reserved)
	}
}

func TestReapUploads(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	_, up := e.initUpload("ann", "a.txt", 100, nil)
	_, _ = e.putPart("ann", str(up, "id"), 0, "abc")
	_, fresh := e.initUpload("ann", "b.txt", 50, nil)
	e.exec(`UPDATE uploads SET expires_at = ? WHERE id = ?`, db.Millis(time.Now().Add(-time.Minute)), str(up, "id"))
	if err := e.svc.ReapUploads(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, reserved := e.usage(1); reserved != 50 {
		t.Errorf("reserved %d", reserved)
	}
	if res, _ := e.do("HEAD", "/api/v1/uploads/"+str(up, "id"), nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("reaped upload: %d", res.StatusCode)
	}
	if res, _ := e.do("HEAD", "/api/v1/uploads/"+str(fresh, "id"), nil, as("ann")); res.StatusCode != 200 {
		t.Errorf("fresh upload: %d", res.StatusCode)
	}
	if n := len(e.tmpFiles()); n != 1 {
		t.Errorf("%d temp files", n)
	}
}

func TestReconcile(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.put("ann", "a.txt", "12345", "")
	_, _ = e.initUpload("ann", "b.txt", 70, nil)
	e.exec(`UPDATE users SET used_bytes = 999, reserved_bytes = 5`)
	if err := e.svc.Reconcile(e.ctx); err != nil {
		t.Fatal(err)
	}
	if used, reserved := e.usage(1); used != 5 || reserved != 70 {
		t.Errorf("used %d reserved %d", used, reserved)
	}
}
