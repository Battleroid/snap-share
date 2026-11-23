package mediajobs_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// withMedia adjusts the media service's dependencies.
func withMedia(f func(*mediajobs.Deps)) testutil.Option {
	return testutil.AppOptions(func(o *app.Options) { o.MediaDeps = f })
}

// fakeRunner is a Runner that runs no ffmpeg: it records its calls and
// writes fixed outputs, as a remote media worker would.
type fakeRunner struct {
	t        *testing.T
	thumb    []byte // what Thumbnail writes
	thumbErr error  // returned by Thumbnail instead
	mu       sync.Mutex
	calls    []string
}

func (f *fakeRunner) record(op string, src string, t media.Type) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !filepath.IsAbs(src) {
		f.t.Errorf("%s: path %q is not absolute", op, src)
	}
	f.calls = append(f.calls, op+" "+t.MIME)
}

func (f *fakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeRunner) Probe(_ context.Context, path string, t media.Type) (media.Info, error) {
	f.record("probe", path, t)
	if t.Kind == media.KindVideo {
		return media.Info{Width: 320, Height: 240, Duration: 3 * time.Second, HasVideo: true}, nil
	}
	return media.Info{Width: 40, Height: 30}, nil
}

func (f *fakeRunner) Thumbnail(_ context.Context, src string, t media.Type, w io.Writer) (media.Thumb, error) {
	f.record("thumbnail", src, t)
	f.mu.Lock()
	err := f.thumbErr
	f.mu.Unlock()
	if err != nil {
		return media.Thumb{}, err
	}
	_, err = w.Write(f.thumb)
	return media.Thumb{MIME: "image/png", Width: 40, Height: 30}, err
}

func (f *fakeRunner) Poster(_ context.Context, src string, t media.Type, dst string) error {
	f.record("poster", src, t)
	return os.WriteFile(dst, f.thumb, 0o600)
}

func (f *fakeRunner) Teaser(_ context.Context, src string, t media.Type, info media.Info, dst string) error {
	f.record("teaser", src, t)
	if info.Width != 320 {
		f.t.Errorf("teaser got info %+v, want the probe's", info)
	}
	return os.WriteFile(dst, []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), 0o600)
}

func (f *fakeRunner) HasFFmpeg() bool { return true }

// The interface is all the service needs: with a fake Runner and no
// ffmpeg calls at all, uploads and jobs store what the Runner returns.
func TestFakeRunner(t *testing.T) {
	fake := &fakeRunner{t: t, thumb: pngBytes(t, 40, 30)}
	e := newEnv(t, withMedia(func(d *mediajobs.Deps) { d.Runner = fake }))
	if !e.s.App.Media.FFmpeg() {
		t.Fatal("FFmpeg() is false with a Runner that has it")
	}

	// Without Local, the upload-time work uses the Runner.
	j := e.put("pic.png", pngBytes(t, 40, 30))
	if got := fake.Calls(); !slices.Equal(got, []string{"probe image/png", "thumbnail image/png"}) {
		t.Errorf("upload calls %q", got)
	}
	if status, ctype, _ := e.get(path(str(j, "thumbnail_url")), false); status != 200 || ctype != "image/png" {
		t.Errorf("thumbnail: %d %s", status, ctype)
	}
	if e.jobs(id(j)) != 0 {
		t.Error("still image queued a job")
	}

	v := e.put("clip.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"+strings.Repeat("\x00", 200)))
	if e.jobs(id(v)) != 1 {
		t.Fatal("video did not queue a job")
	}
	before := len(fake.Calls())
	if err := e.s.App.Media.Process(e.ctx, id(v)); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls()[before:]; !slices.Equal(got, []string{"probe video/mp4", "poster video/mp4", "teaser video/mp4"}) {
		t.Errorf("job calls %q", got)
	}
	it := e.item(id(v))
	if it.Width != 320 || it.Height != 240 || it.DurationMS != 3000 || it.ThumbKey == "" || it.PreviewKey == "" {
		t.Errorf("after job: %+v", it)
	}
}

// A still image the upload-time runner refuses as over its memory budget
// is queued, and the job makes its thumbnail with the Runner.
func TestOverBudgetStillQueued(t *testing.T) {
	e := newEnv(t, withMedia(func(d *mediajobs.Deps) {
		// What snap keeps in worker mode: Go only, here with a tiny budget.
		d.Local = media.NewImageRunner(media.Config{DecodeMemory: 1 << 20})
	}))
	if !e.s.App.Media.FFmpeg() {
		t.Fatal("ffmpeg is not available; run the tests with `make test-go`")
	}
	j := e.put("big.jpg", jpegBytes(t, 1200, 900))
	if thumb := str(j, "thumbnail_url"); strings.HasSuffix(thumb, "/thumb") {
		t.Fatalf("thumbnail_url = %q at upload, want none yet", thumb)
	}
	if w, _ := j["width"].(float64); int64(w) != 1200 {
		t.Errorf("width %v at upload, want 1200", j["width"])
	}
	if n := e.jobs(id(j)); n != 1 {
		t.Fatalf("over-budget still queued %d media jobs, want 1", n)
	}
	e.runJobs()
	it := e.waitFor(id(j), func(it *items.Item) bool { return it.ThumbKey != "" })
	status, ctype, body := e.get("/"+it.Slug+"/thumb", false)
	if status != 200 || ctype != "image/jpeg" {
		t.Fatalf("GET thumbnail: %d %s", status, ctype)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil || cfg.Width != 640 {
		t.Errorf("thumbnail %+v, %v", cfg, err)
	}
	if it.Width != 1200 || it.Height != 900 {
		t.Errorf("dimensions %d×%d", it.Width, it.Height)
	}

	// Under the budget, nothing changes: thumbnail at upload, no job.
	small := e.put("small.jpg", jpegBytes(t, 64, 48))
	if !strings.HasSuffix(str(small, "thumbnail_url"), "/thumb") || e.jobs(id(small)) != 0 {
		t.Errorf("small still: thumbnail %q, %d jobs", str(small, "thumbnail_url"), e.jobs(id(small)))
	}
}

// The same with a fake (remote) Runner: the Local refusal is queued and the
// job asks the Runner, not Local, for the thumbnail.
func TestOverBudgetStillFakeRunner(t *testing.T) {
	local := &fakeRunner{t: t, thumbErr: fmt.Errorf("%w: over the budget", media.ErrTooLarge)}
	remote := &fakeRunner{t: t, thumb: pngBytes(t, 40, 30)}
	e := newEnv(t, withMedia(func(d *mediajobs.Deps) { d.Runner, d.Local = remote, local }))
	j := e.put("pic.png", pngBytes(t, 40, 30))
	if e.jobs(id(j)) != 1 {
		t.Fatal("refused still did not queue a job")
	}
	if got := remote.Calls(); len(got) != 0 {
		t.Errorf("Runner used at upload: %q", got)
	}
	if err := e.s.App.Media.Process(e.ctx, id(j)); err != nil {
		t.Fatal(err)
	}
	if got := remote.Calls(); !slices.Equal(got, []string{"probe image/png", "thumbnail image/png"}) {
		t.Errorf("job calls %q", got)
	}
	if it := e.item(id(j)); it.ThumbKey == "" {
		t.Error("no thumbnail after the job")
	}
	// A rerun of the job does not make the thumbnail again.
	if err := e.s.App.Media.Process(e.ctx, id(j)); err != nil {
		t.Fatal(err)
	}
	if got := remote.Calls(); slices.Contains(got[2:], "thumbnail image/png") {
		t.Errorf("rerun made another thumbnail: %q", got)
	}

	// Other errors at upload are not queued.
	local.mu.Lock()
	local.thumbErr = fmt.Errorf("%w: bad", media.ErrUnsupported)
	local.mu.Unlock()
	if g := e.put("other.png", pngBytes(t, 40, 30)); e.jobs(id(g)) != 0 {
		t.Error("an unsupported still queued a job")
	}
}
