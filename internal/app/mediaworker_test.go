package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const mwToken = "app-test-media-worker-token-0123456789"

// mwSock is a Unix socket path short enough to bind, in a directory of
// its own (Listen makes it 0700).
func mwSock(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) > 80 {
		var err error
		if dir, err = os.MkdirTemp("", "mw"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	return filepath.Join(dir, "s", "w.sock")
}

// serveUnix serves h on a new Unix socket and returns its path.
func serveUnix(t *testing.T, h http.Handler) string {
	t.Helper()
	sock := mwSock(t)
	ln, err := mediaworker.Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(h)
	_ = hs.Listener.Close()
	hs.Listener = ln
	hs.Start()
	t.Cleanup(hs.Close)
	return sock
}

// worker is a real media worker over filesDir, recording request ids.
type worker struct {
	mu  sync.Mutex
	ids map[string][]string // op -> X-Request-Id
}

func startWorker(t *testing.T, filesDir, token string) (*worker, string) {
	t.Helper()
	ro, err := local.OpenReadOnly(filesDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	srv, err := mediaworker.NewServer(mediaworker.ServerConfig{
		Files: ro, Scratch: filepath.Join(t.TempDir(), "scratch"), Token: token, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := media.NewRunner(media.Config{Direct: true})
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	srv.Start(run, "test")
	w := &worker{ids: map[string][]string{}}
	sock := serveUnix(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		op := strings.TrimPrefix(r.URL.Path, "/v1/")
		w.ids[op] = append(w.ids[op], r.Header.Get(mediaworker.HeaderRequestID))
		w.mu.Unlock()
		srv.ServeHTTP(rw, r)
	}))
	return w, sock
}

func (w *worker) requests(op string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.ids[op])
}

// workerServer starts snap with SNAP_MEDIA_WORKER at sock and a user ann.
func workerServer(t *testing.T, sock, filesDir string, opts ...testutil.Option) *testutil.Server {
	t.Helper()
	opts = append(opts, testutil.Env("MEDIA_WORKER", "unix://"+sock), testutil.Env("MEDIA_WORKER_TOKEN", mwToken))
	if filesDir != "" {
		opts = append(opts, testutil.Env("FILES_DIR", filesDir))
	}
	s := testutil.NewServer(t, opts...)
	ctx := context.Background()
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.App.DB.W.Exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at)
		VALUES (1, 'ann', 'ann@example.test', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	s.Auth.Add("ann", auth.Principal{UserID: 1, EmailVerified: true})
	return s
}

func upload(t *testing.T, s *testutil.Server, name string, content []byte) int64 {
	t.Helper()
	req := s.Request(t, "PUT", "/api/v1/upload/"+name, bytes.NewReader(content))
	req.Header.Set("Accept", "application/json")
	authtest.Session(req, "ann")
	code, body := fetch(t, s, req)
	if code != http.StatusCreated {
		t.Fatalf("PUT %s: %d %s", name, code, body)
	}
	var it struct{ ID int64 }
	if err := json.Unmarshal([]byte(body), &it); err != nil || it.ID == 0 {
		t.Fatalf("upload response %s: %v", body, err)
	}
	return it.ID
}

func runJobs(t *testing.T, a *app.App) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a.Jobs.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
}

// clip makes a short H.264 MP4 with ffmpeg (not through the Runner).
func clip(t *testing.T) []byte {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	out := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command(ff, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", //nolint:gosec // test fixture
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// metric returns the value of the series name{labels}, if exported.
func metric(t *testing.T, a *app.App, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := a.Metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue next
				}
			}
			switch {
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue(), true
			case m.GetCounter() != nil:
				return m.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

// Without SNAP_MEDIA_WORKER the job runs ffmpeg in process through one
// Runner; with it, the job calls the worker, uploads use a Go-only runner,
// and the worker's series exist.
func TestMediaWiring(t *testing.T) {
	var got mediajobs.Deps
	capture := testutil.AppOptions(func(o *app.Options) { o.MediaDeps = func(d *mediajobs.Deps) { got = *d } })

	s := testutil.NewServer(t, capture)
	if r, ok := got.Runner.(*media.Runner); !ok || !r.HasFFmpeg() {
		t.Errorf("local mode: Runner %T", got.Runner)
	}
	if got.Local != nil {
		t.Errorf("local mode: Local %T, want the Runner's own", got.Local)
	}
	if err := s.App.CheckMediaWorker(t.Context()); err == nil {
		t.Error("health check without a worker")
	}
	if _, ok := metric(t, s.App, "snap_media_worker_up", nil); ok {
		t.Error("snap_media_worker_up exported without a worker")
	}

	s = testutil.NewServer(t, capture, testutil.Env("MEDIA_WORKER", "unix://"+mwSock(t)), testutil.Env("MEDIA_WORKER_TOKEN", mwToken))
	if _, ok := got.Runner.(*media.Runner); ok || got.Runner == nil || !got.Runner.HasFFmpeg() {
		t.Errorf("worker mode: Runner %T", got.Runner)
	}
	if l, ok := got.Local.(*media.Runner); !ok || l.HasFFmpeg() {
		t.Errorf("worker mode: Local %T must be a Go-only image runner", got.Local)
	}
	if !s.App.Media.FFmpeg() {
		t.Error("a configured worker counts as ffmpeg")
	}
	if v, ok := metric(t, s.App, "snap_media_worker_up", nil); !ok || v != 0 {
		t.Errorf("snap_media_worker_up %v %v with no worker listening", v, ok)
	}
}

// The health check sets snap_media_worker_up: a wrong token, another
// protocol or no worker is 0, a good answer 1.
func TestMediaWorkerHealthGauge(t *testing.T) {
	var mu sync.Mutex
	status, body := http.StatusUnauthorized, `{"code":"unauthorized","message":"bad token"}`
	sock := serveUnix(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/health" || r.Header.Get("Authorization") != "Bearer "+mwToken {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	set := func(st int, b string) { mu.Lock(); status, body = st, b; mu.Unlock() }
	s := workerServer(t, sock, "")
	up := func() float64 {
		v, ok := metric(t, s.App, "snap_media_worker_up", nil)
		if !ok {
			t.Fatal("no snap_media_worker_up")
		}
		return v
	}
	if up() != 0 {
		t.Fatal("up with a refused token at startup")
	}
	set(http.StatusOK, `{"protocol":1,"version":"v1.2.3","ffmpeg":"8.1"}`)
	if err := s.App.CheckMediaWorker(t.Context()); err != nil || up() != 1 {
		t.Fatalf("good answer: %v, up %v", err, up())
	}
	if v, ok := metric(t, s.App, "snap_media_worker_info", map[string]string{"version": "v1.2.3", "protocol": "1"}); !ok || v != 1 {
		t.Error("no snap_media_worker_info for v1.2.3")
	}
	set(http.StatusOK, `{"protocol":2,"version":"v2.0.0","ffmpeg":"9"}`)
	if err := s.App.CheckMediaWorker(t.Context()); err == nil || !strings.Contains(err.Error(), "protocol 2") || up() != 0 {
		t.Errorf("protocol mismatch: %v, up %v", err, up())
	}
	if _, ok := metric(t, s.App, "snap_media_worker_info", map[string]string{"version": "v1.2.3"}); ok {
		t.Error("the old version's info series stayed")
	}
	set(http.StatusUnauthorized, `{"code":"unauthorized","message":"bad token"}`)
	if err := s.App.CheckMediaWorker(t.Context()); err == nil || up() != 0 {
		t.Errorf("wrong token: %v, up %v", err, up())
	}
	if n, _ := metric(t, s.App, "snap_media_worker_requests_total", map[string]string{"op": "health", "result": "error"}); n < 2 {
		t.Errorf("health errors counted %v", n)
	}
}

// A worker that refuses snap's token never costs a job an attempt: the
// job waits, and only health checks reach the worker.
func TestMediaWorkerWrongTokenDefers(t *testing.T) {
	files := t.TempDir()
	w, sock := startWorker(t, files, "a-different-token-0123456789abcdefgh")
	s := workerServer(t, sock, files)
	id := upload(t, s, "clip.mp4", clip(t))
	runJobs(t, s.App)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state string
		var attempts int
		var lastErr *string
		if err := s.App.DB.R.QueryRow(`SELECT state, attempts, last_error FROM jobs WHERE kind = ?`, mediajobs.JobKind).
			Scan(&state, &attempts, &lastErr); err != nil {
			t.Fatal(err)
		}
		if lastErr != nil && state != "running" {
			if state != "queued" || attempts != 0 || !strings.Contains(*lastErr, "not healthy") {
				t.Errorf("job %s, %d attempts, %q; want deferred", state, attempts, *lastErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, op := range []string{"probe", "poster", "teaser", "thumbnail"} {
		if n := len(w.requests(op)); n != 0 {
			t.Errorf("%d %s requests reached a worker that refuses the token", n, op)
		}
	}
	if len(w.requests("health")) == 0 {
		t.Error("no health check reached the worker")
	}
	if n, _ := metric(t, s.App, "snap_media_worker_requests_total", map[string]string{"op": "probe", "result": "unavailable"}); n < 1 {
		t.Errorf("probe unavailable counted %v", n)
	}
	it, err := s.App.Items.Get(t.Context(), id)
	if err != nil || it.ThumbKey != "" {
		t.Errorf("item %+v, %v", it, err)
	}
}

// End to end: snap with SNAP_MEDIA_WORKER on a Unix socket hands a video
// upload to the real worker handler, which makes its poster and teaser.
func TestMediaWorkerProcessesVideo(t *testing.T) {
	files := t.TempDir()
	w, sock := startWorker(t, files, mwToken)
	s := workerServer(t, sock, files)
	if v, _ := metric(t, s.App, "snap_media_worker_up", nil); v != 1 {
		t.Fatalf("worker not up after the startup check: %v", v)
	}
	id := upload(t, s, "clip.mp4", clip(t))
	runJobs(t, s.App)
	var it *items.Item
	deadline := time.Now().Add(90 * time.Second)
	for {
		var err error
		if it, err = s.App.Items.Get(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		if it.ThumbKey != "" && it.PreviewKey != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("media job did not finish: %+v", it)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if it.Width != 320 || it.Height != 240 {
		t.Errorf("probe: %d×%d", it.Width, it.Height)
	}
	req := s.Request(t, "GET", "/"+it.Slug+"/thumb", nil)
	if code, body := fetch(t, s, req); code != 200 || !bytes.HasPrefix([]byte(body), []byte{0xff, 0xd8, 0xff}) {
		t.Errorf("poster: %d, %d bytes", code, len(body))
	}
	var jobID int64
	if err := s.App.DB.R.QueryRow(`SELECT id FROM jobs WHERE kind = ?`, mediajobs.JobKind).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	want := "job-" + strconv.FormatInt(jobID, 10) + "-1"
	for _, op := range []string{"probe", "poster", "teaser"} {
		if ids := w.requests(op); len(ids) != 1 || ids[0] != want {
			t.Errorf("%s request ids %q, want [%s]", op, ids, want)
		}
		if n, _ := metric(t, s.App, "snap_media_worker_requests_total", map[string]string{"op": op, "result": "ok"}); n != 1 {
			t.Errorf("%s ok counted %v", op, n)
		}
	}
	found := false
	mfs, _ := s.App.Metrics.Gather()
	for _, mf := range mfs {
		found = found || mf.GetName() == "snap_media_step_duration_seconds"
	}
	if !found {
		t.Error("snap_media_step_duration_seconds missing in worker mode")
	}
}

// fetch sends req and returns the status and body.
func fetch(t *testing.T, s *testutil.Server, req *http.Request) (int, string) {
	t.Helper()
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}
