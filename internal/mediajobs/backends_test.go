package mediajobs_test

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// backend is where the media.process job's ffmpeg steps run.
type backend struct {
	name string
	deps func(t *testing.T, d *mediajobs.Deps)
}

var backends = []backend{
	// The default: one in-process Runner for uploads and jobs.
	{"local", func(*testing.T, *mediajobs.Deps) {}},
	// Worker mode: a Go-only runner at upload, and the job calls a media
	// worker (the real handler and a Direct Runner) over a Unix socket.
	{"worker", func(t *testing.T, d *mediajobs.Deps) {
		d.Runner = startWorker(t, d)
		d.Local = media.NewImageRunner(media.Config{})
	}},
}

// forBackends runs test once per backend.
func forBackends(t *testing.T, test func(t *testing.T, b backend)) {
	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) { test(t, b) })
	}
}

// with gives the backend's dependencies, then more.
func (b backend) with(t *testing.T, more ...func(*mediajobs.Deps)) testutil.Option {
	return withMedia(func(d *mediajobs.Deps) {
		b.deps(t, d)
		for _, f := range more {
			f(d)
		}
	})
}

const workerToken = "mediajobs-test-token-0123456789abcdef"

// sockPath is a Unix socket path short enough to bind.
func sockPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) > 80 {
		var err error
		if dir, err = os.MkdirTemp("", "mj"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	return filepath.Join(dir, "s", "w.sock")
}

// startWorker serves the app's files store read-only from a media worker
// and returns a client for it.
func startWorker(t *testing.T, d *mediajobs.Deps) *mediaworker.Client {
	t.Helper()
	ds, ok := d.Blobs.(interface{ Dir() string })
	if !ok {
		t.Fatalf("files store %T has no directory", d.Blobs)
	}
	ro, err := local.OpenReadOnly(ds.Dir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	srv, err := mediaworker.NewServer(mediaworker.ServerConfig{
		Files: ro, Scratch: filepath.Join(t.TempDir(), "scratch"), Token: workerToken, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := media.NewRunner(media.Config{Direct: true})
	if err != nil {
		t.Fatalf("ffmpeg not found; run the tests with `make test-go`: %v", err)
	}
	srv.Start(run, "test")
	sock := sockPath(t)
	ln, err := mediaworker.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewUnstartedServer(srv)
	_ = hs.Listener.Close()
	hs.Listener = ln
	hs.Start()
	t.Cleanup(hs.Close)
	c, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + sock, Token: workerToken})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// With the worker down, a media job waits without spending attempts, and
// the item keeps showing as processing.
func TestWorkerDownDefers(t *testing.T) {
	c, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + sockPath(t), Token: workerToken})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, withMedia(func(d *mediajobs.Deps) {
		d.Runner, d.Local = c, media.NewImageRunner(media.Config{})
	}))
	if !e.s.App.Media.FFmpeg() {
		t.Fatal("a configured worker should count as ffmpeg")
	}
	j := e.put("clip.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"+strings.Repeat("\x00", 200)))
	if e.jobs(id(j)) != 1 {
		t.Fatal("video did not queue a job")
	}
	e.runJobs()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var state string
		var attempts int
		var lastErr *string
		if err := e.s.App.DB.R.QueryRow(`SELECT state, attempts, last_error FROM jobs WHERE kind = ?`, mediajobs.JobKind).
			Scan(&state, &attempts, &lastErr); err != nil {
			t.Fatal(err)
		}
		if lastErr != nil {
			if state != "queued" || attempts != 0 || !strings.Contains(*lastErr, "unreachable") {
				t.Errorf("job %s, %d attempts, %q; want deferred", state, attempts, *lastErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if it := e.item(id(j)); it.ThumbKey != "" {
		t.Errorf("thumbnail without a worker: %+v", it)
	}
}
