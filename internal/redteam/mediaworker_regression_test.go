//go:build linux

package redteam_test

// Regression tests for the media worker (internal/mediaworker): the worker
// runs ffmpeg on files it is pointed at by a request, so a request must not
// be able to name another file, another demuxer or extra ffmpeg arguments,
// and its token must never reach the log.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediaworker"
)

const mwToken = "redteam-worker-token-Zq9xK2mW7vB4nR8tY1"

type mwEnv struct {
	t     *testing.T
	store *local.Store
	sock  string
	log   *syncBuf
	c     *mediaworker.Client
}

func newMWEnv(t *testing.T) *mwEnv {
	t.Helper()
	store, err := local.New(t.TempDir(), local.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ro, err := local.OpenReadOnly(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	e := &mwEnv{t: t, store: store, log: &syncBuf{}}
	srv, err := mediaworker.NewServer(mediaworker.ServerConfig{
		Files: ro, Scratch: filepath.Join(t.TempDir(), "scratch"), Token: mwToken,
		Log: slog.New(slog.NewJSONHandler(e.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := media.NewRunner(media.Config{Direct: true, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	srv.Start(run, "test")
	dir, err := os.MkdirTemp("", "rtmw") // Unix socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	e.sock = filepath.Join(dir, "s", "w.sock")
	ln, err := mediaworker.Listen(t.Context(), "unix", e.sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(srv)
	_ = hs.Listener.Close()
	hs.Listener = ln
	hs.Start()
	t.Cleanup(hs.Close)
	if e.c, err = mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + e.sock, Token: mwToken}); err != nil {
		t.Fatal(err)
	}
	return e
}

// put stores b as a blob and returns its key and path.
func (e *mwEnv) put(b []byte) (string, string) {
	e.t.Helper()
	w, err := e.store.Create(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = w.Write(b)
	key, err := w.Commit()
	if err != nil {
		e.t.Fatal(err)
	}
	p, _ := e.store.Path(key)
	return key, p
}

// post sends a raw request body and returns the status and error code.
func (e *mwEnv) post(op, auth, body string) (int, string) {
	e.t.Helper()
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", e.sock)
		}}}
	req, _ := http.NewRequest(http.MethodPost, "http://w/v1/"+op, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var eb mediaworker.ErrorBody
	_ = json.Unmarshal(b, &eb)
	return res.StatusCode, eb.Code
}

func body(key, mime, kind, container string, extra ...string) string {
	b, _ := json.Marshal(map[string]any{"key": key, "type": map[string]string{"mime": mime, "kind": kind, "container": container}})
	s := string(b)
	for _, x := range extra {
		s = strings.TrimSuffix(s, "}") + "," + x + "}"
	}
	return s
}

func mp4Clip(t *testing.T) []byte {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found: %v", err)
	}
	out := filepath.Join(t.TempDir(), "c.mp4")
	cmd := exec.Command(ff, "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10:duration=1", //nolint:gosec // fixture
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	b, _ := os.ReadFile(out)
	return b
}

// Keys that are not 32 lowercase hex digits never reach the filesystem.
func TestMediaWorkerKeysValidated(t *testing.T) {
	e := newMWEnv(t)
	for _, key := range []string{
		"../../../../etc/passwd", "/etc/passwd", "..%2f..%2fetc%2fpasswd",
		"../" + strings.Repeat("a", 29), strings.Repeat("A", 32), strings.Repeat("a", 31), strings.Repeat("a", 33),
		"aa/bb/" + strings.Repeat("a", 26), strings.Repeat("a", 31) + "\x00", strings.Repeat("g", 32), "",
	} {
		for _, op := range []string{"probe", "thumbnail", "poster"} {
			if status, code := e.post(op, "Bearer "+mwToken, body(key, "video/mp4", "video", "mov")); status != 400 || code != "bad_request" {
				t.Errorf("%s key %q: %d %s, want 400", op, key, status, code)
			}
		}
	}
}

// A blob that is a symlink (to a file outside the store, or inside it) is
// refused before ffmpeg sees it.
func TestMediaWorkerSymlinksRefused(t *testing.T) {
	e := newMWEnv(t)
	clip := mp4Clip(t)
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, clip, 0o600); err != nil {
		t.Fatal(err)
	}
	_, inside := e.put(clip)
	for key, target := range map[string]string{
		"0123456789abcdef0123456789abcdef": outside,
		"fedcba9876543210fedcba9876543210": inside,
		"00112233445566778899aabbccddeeff": "/etc/passwd",
	} {
		p, _ := e.store.Path(key)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		for _, op := range []string{"probe", "poster"} {
			if status, code := e.post(op, "Bearer "+mwToken, body(key, "video/mp4", "video", "mov")); status != 404 || code != "not_found" {
				t.Errorf("%s symlink to %s: %d %s, want 404", op, target, status, code)
			}
		}
	}
	// A shard directory that is a symlink is refused too.
	const key = "abababababababababababababababab"
	realDir := filepath.Join(e.store.Dir(), "real")
	if err := os.MkdirAll(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, key), clip, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.store.Dir(), "ab"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(e.store.Dir(), "ab", "ab")); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.post("probe", "Bearer "+mwToken, body(key, "video/mp4", "video", "mov")); status != 404 {
		t.Errorf("symlinked shard: %d, want 404", status)
	}
}

func TestMediaWorkerToken(t *testing.T) {
	e := newMWEnv(t)
	key, _ := e.put(mp4Clip(t))
	ok := body(key, "video/mp4", "video", "mov")
	for _, auth := range []string{"", "Bearer ", "Bearer " + strings.ToUpper(mwToken), "Bearer " + mwToken[:len(mwToken)-1], "Token " + mwToken, "Bearer  " + mwToken} {
		if status, code := e.post("probe", auth, ok); status != 401 || code != "unauthorized" {
			t.Errorf("Authorization %q: %d %s, want 401", auth, status, code)
		}
	}
	if status, _ := e.post("probe", "Bearer "+mwToken, ok); status != 200 {
		t.Errorf("control: %d", status)
	}
}

// The claimed type must be what the worker sniffs itself, so a request
// cannot force a demuxer outside the whitelist or one that does not match
// the bytes.
func TestMediaWorkerDemuxerAndSniff(t *testing.T) {
	e := newMWEnv(t)
	key, _ := e.put(mp4Clip(t))
	for _, c := range []string{"hls", "concat", "sdp", "image2", "rtp", "lavfi", "tee", "matroska", "mpegts", ""} {
		if status, code := e.post("poster", "Bearer "+mwToken, body(key, "video/mp4", "video", c)); status != 422 || code != "unsupported" {
			t.Errorf("container %q: %d %s, want 422", c, status, code)
		}
	}
	for _, typ := range [][3]string{{"audio/mpeg", "audio", "mp3"}, {"audio/aac", "audio", "aac"}, {"image/avif", "image", "mov"}, {"video/webm", "video", "matroska"}, {"text/plain", "text", ""}} {
		if status, code := e.post("probe", "Bearer "+mwToken, body(key, typ[0], typ[1], typ[2])); status != 422 || code != "unsupported" {
			t.Errorf("claimed %v: %d %s, want 422", typ, status, code)
		}
	}
	if status, _ := e.post("poster", "Bearer "+mwToken, body(key, "video/mp4", "video", "mov")); status != 200 {
		t.Errorf("control: %d", status)
	}
	// The one other claim allowed is the one snap's own sniff gives these
	// bytes under a .m4a name: same demuxer, audio.
	if status, _ := e.post("probe", "Bearer "+mwToken, body(key, "audio/mp4", "audio", "mov")); status != 200 {
		t.Errorf("audio/mp4 claim: %d", status)
	}
}

// The HLS, ffconcat and SDP files the in-process tests use, renamed to
// .mp4, through the worker: claimed as video they are a mismatch, claimed
// as what they are (text) ffmpeg refuses them, and the FIFO they point at
// is never opened.
func TestMediaWorkerPlaylistsRefused(t *testing.T) {
	e := newMWEnv(t)
	fifo := filepath.Join(t.TempDir(), "secret.ts")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"hls.mp4": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n" +
			"#EXTINF:1.0,\nfile://" + fifo + "\n#EXTINF:1.0,\nfile:///etc/passwd\n#EXT-X-ENDLIST\n",
		"concat.mp4":     "ffconcat version 1.0\nfile " + fifo + "\nfile /etc/passwd\n",
		"stream.sdp.mp4": "v=0\no=- 0 0 IN IP4 127.0.0.1\ns=x\nc=IN IP4 127.0.0.1\nm=video 5004 RTP/AVP 96\n",
	} {
		key, p := e.put([]byte(content))
		f, _ := os.Open(p)
		fi, _ := f.Stat()
		sniffed, err := media.Sniff(f, fi.Size(), name)
		_ = f.Close()
		if err != nil || sniffed.Kind != media.KindText || sniffed.Container != "" {
			t.Fatalf("%s sniffed as %+v %v", name, sniffed, err)
		}
		start := time.Now()
		for _, op := range []string{"probe", "thumbnail", "poster"} {
			for _, claim := range []media.Type{{MIME: "video/mp4", Kind: media.KindVideo, Container: "mov"}, sniffed} {
				if status, code := e.post(op, "Bearer "+mwToken, body(key, claim.MIME, string(claim.Kind), claim.Container)); status != 422 || code != "unsupported" {
					t.Errorf("%s %s as %s: %d %s, want 422", name, op, claim.MIME, status, code)
				}
			}
		}
		// And through the client, as snap would send it.
		if err := e.c.Poster(context.Background(), p, sniffed, filepath.Join(t.TempDir(), "p.jpg")); err == nil {
			t.Errorf("%s: client Poster succeeded", name)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s took %s: something opened the FIFO", name, d)
		}
	}
}

// Only key, type and info cross the wire: anything that could carry ffmpeg
// arguments, a path or an output is refused, at the top level and inside
// type and info.
func TestMediaWorkerNoRawArgs(t *testing.T) {
	e := newMWEnv(t)
	key, _ := e.put(mp4Clip(t))
	for _, extra := range []string{
		`"args":["-f","hls","-i","file:/etc/passwd"]`, `"argv":["-version"]`, `"path":"/etc/passwd"`,
		`"input":"/etc/passwd"`, `"output":"/tmp/x"`, `"filter":"movie=/etc/passwd"`, `"ffmpeg":"/bin/sh"`,
		`"options":{"protocol_whitelist":"file,http"}`,
	} {
		if status, _ := e.post("poster", "Bearer "+mwToken, body(key, "video/mp4", "video", "mov", extra)); status != 400 {
			t.Errorf("extra %s: %d, want 400", extra, status)
		}
	}
	nested := `{"key":"` + key + `","type":{"mime":"video/mp4","kind":"video","container":"mov","args":["-f","hls"]}}`
	if status, _ := e.post("poster", "Bearer "+mwToken, nested); status != 400 {
		t.Errorf("args inside type: %d, want 400", status)
	}
	info := `{"key":"` + key + `","type":{"mime":"video/mp4","kind":"video","container":"mov"},"info":{"width":64,"filter":"x"}}`
	if status, _ := e.post("teaser", "Bearer "+mwToken, info); status != 400 {
		t.Errorf("filter inside info: %d, want 400", status)
	}
}

// Across a rejected token, a success, an ffmpeg failure, a bad request and
// a configuration error, neither the token nor the Authorization header
// reaches the worker's log.
func TestMediaWorkerLogsNoToken(t *testing.T) {
	e := newMWEnv(t)
	key, p := e.put(mp4Clip(t))
	_, broken := e.put(append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), make([]byte, 300)...))
	ok := body(key, "video/mp4", "video", "mov")
	e.post("probe", "Bearer wrong-"+mwToken, ok)
	e.post("probe", "Bearer "+mwToken, `{"key":"../`+mwToken+`"}`)
	ctx := mediaworker.WithRequestID(context.Background(), "rt-1")
	if _, err := e.c.Probe(ctx, p, media.Type{MIME: "video/mp4", Kind: media.KindVideo, Container: "mov"}); err != nil {
		t.Fatalf("control probe: %v", err)
	}
	if err := e.c.Poster(ctx, broken, media.Type{MIME: "video/mp4", Kind: media.KindVideo, Container: "mov"}, filepath.Join(t.TempDir(), "x.jpg")); err == nil {
		t.Error("broken file made a poster")
	}
	if _, err := e.c.Health(ctx); err != nil {
		t.Fatal(err)
	}
	_, cfgErr := config.LoadWorker(func(k string) string {
		return map[string]string{"SNAP_MEDIA_WORKER_LISTEN": "nope", "SNAP_MEDIA_WORKER_TOKEN": mwToken[:20]}[k]
	})
	log := e.log.String()
	if !strings.Contains(log, `"result":"unauthorized"`) || !strings.Contains(log, `"result":"ok"`) || !strings.Contains(log, `"request_id":"rt-1"`) {
		t.Fatalf("requests not logged:\n%s", log)
	}
	for _, leak := range []string{mwToken, mwToken[:20], "Bearer", "Authorization"} {
		if strings.Contains(log, leak) {
			t.Errorf("worker log contains %q:\n%s", leak, log)
		}
	}
	if cfgErr == nil || strings.Contains(cfgErr.Error(), mwToken[:20]) {
		t.Errorf("config error %v: want an error without the token", cfgErr)
	}
}
