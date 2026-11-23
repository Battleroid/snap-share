package mediaworker_test

import (
	"bytes"
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
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/mediaworker"
)

var _ mediajobs.Runner = (*mediaworker.Client)(nil)

const token = "test-token-0123456789abcdefghijklmnopqrstuvwxyz"

// syncBuffer is a log destination safe for the server's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// worker is a media worker on a Unix socket, its files store and a client.
type worker struct {
	t       *testing.T
	store   *local.Store
	srv     *mediaworker.Server
	sock    string
	scratch string
	client  *mediaworker.Client
	log     *syncBuffer
}

// socketDir is a short directory for a Unix socket (paths are limited to
// about 108 bytes).
func socketDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) > 80 {
		var err error
		if dir, err = os.MkdirTemp("", "mw"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	return filepath.Join(dir, "sock")
}

// directRunner is what the worker runs: media.Runner with Direct.
func directRunner(t *testing.T) *media.Runner {
	t.Helper()
	r, err := media.NewRunner(media.Config{Direct: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	return r
}

// newWorker starts the real server on a Unix socket; run nil leaves it
// "starting".
func newWorker(t *testing.T, run mediaworker.Runner, cfg ...func(*mediaworker.ServerConfig)) *worker {
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
	w := &worker{t: t, store: store, log: &syncBuffer{}, scratch: filepath.Join(t.TempDir(), "scratch")}
	sc := mediaworker.ServerConfig{
		Files: ro, Scratch: w.scratch, Token: token,
		Log: slog.New(slog.NewJSONHandler(w.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, f := range cfg {
		f(&sc)
	}
	if w.srv, err = mediaworker.NewServer(sc); err != nil {
		t.Fatal(err)
	}
	if run != nil {
		w.srv.Start(run, "test")
	}
	w.sock = filepath.Join(socketDir(t), "w.sock")
	w.serve(w.srv)
	if w.client, err = mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + w.sock, Token: token}); err != nil {
		t.Fatal(err)
	}
	return w
}

// serve serves h on the worker's socket.
func (w *worker) serve(h http.Handler) {
	ln, err := mediaworker.Listen(w.t.Context(), "unix", w.sock)
	if err != nil {
		w.t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(h)
	_ = hs.Listener.Close()
	hs.Listener = ln
	hs.Start()
	w.t.Cleanup(hs.Close)
}

// put stores b and returns its blob path.
func (w *worker) put(b []byte) string {
	w.t.Helper()
	bw, err := w.store.Create(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := bw.Write(b); err != nil {
		w.t.Fatal(err)
	}
	key, err := bw.Commit()
	if err != nil {
		w.t.Fatal(err)
	}
	p, err := w.store.Path(key)
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

// raw posts body to /v1/op with the given Authorization header ("" for
// none) and returns the status and the error code.
func (w *worker) raw(op, auth, body string) (int, string) {
	w.t.Helper()
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", w.sock)
		}}}
	method := http.MethodPost
	if op == "health" {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, "http://worker/v1/"+op, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := c.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var e mediaworker.ErrorBody
	_ = json.Unmarshal(b, &e)
	return res.StatusCode, e.Code
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// gen writes a fixture with ffmpeg and returns its bytes.
func gen(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	out := filepath.Join(t.TempDir(), name)
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	cmd := exec.Command(ff, append(full, out)...) //nolint:gosec // test fixture
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sniff(t *testing.T, path, name string) media.Type {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	typ, err := media.Sniff(f, fi.Size(), name)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

// jobOutcome hands err to a real job queue as a handler's result and
// returns the job's state and attempts afterwards: a deferred job is
// "queued" with 0 attempts, a counted failure "queued" with 1, a permanent
// one "failed".
func jobOutcome(t *testing.T, herr error) (string, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(d, slog.New(slog.DiscardHandler))
	ran := make(chan struct{}, 1)
	q.Handle("x", 1, time.Minute, func(context.Context, json.RawMessage) error {
		select {
		case ran <- struct{}{}:
		default:
		}
		return herr
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); q.Run(ctx) }()
	defer func() { cancel(); wg.Wait() }()
	id, err := q.Enqueue(ctx, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not run")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var state string
		var attempts int
		var lastErr *string
		if err := d.R.QueryRow(`SELECT state, attempts, last_error FROM jobs WHERE id = ?`, id).Scan(&state, &attempts, &lastErr); err != nil {
			t.Fatal(err)
		}
		if state != "running" && lastErr != nil {
			return state, attempts
		}
		if time.Now().After(deadline) {
			t.Fatalf("job outcome not recorded: %s %d", state, attempts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
