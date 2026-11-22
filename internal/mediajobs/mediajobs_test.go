package mediajobs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestMain(m *testing.M) {
	// The media runner re-executes the test binary to apply rlimits.
	media.MaybeRunLimited()
	os.Exit(m.Run())
}

type env struct {
	t   *testing.T
	s   *testutil.Server
	ctx context.Context
	c   *http.Client
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
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
	return &env{t: t, s: s, ctx: ctx, c: s.Client()}
}

// get fetches path anonymously (or as the owner) and returns status, type and body.
func (e *env) get(path string, owner bool) (int, string, []byte) {
	e.t.Helper()
	req := e.s.Request(e.t, "GET", path, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 test")
	if owner {
		authtest.Session(req, "ann")
	}
	res, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), b
}

// put uploads content as ann and returns the item JSON.
func (e *env) put(name string, content []byte) map[string]any {
	e.t.Helper()
	req := e.s.Request(e.t, "PUT", "/api/v1/upload/"+name, bytes.NewReader(content))
	req.Header.Set("Accept", "application/json")
	authtest.Session(req, "ann")
	res, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("PUT %s: %d %s", name, res.StatusCode, b)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) item(id int64) *items.Item {
	e.t.Helper()
	it, err := e.s.App.Items.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it
}

func (e *env) jobs(id int64) int {
	e.t.Helper()
	var n int
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*) FROM jobs WHERE kind = ? AND payload = ?`,
		mediajobs.JobKind, `{"id":`+itoa(id)+`}`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) blobExists(key string) bool {
	e.t.Helper()
	f, err := e.s.App.Files.Open(e.ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	_ = f.Close()
	return true
}

func (e *env) usedBytes() int64 {
	e.t.Helper()
	var n int64
	if err := e.s.App.DB.R.QueryRow(`SELECT used_bytes FROM users WHERE id = 1`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func id(m map[string]any) int64 { v, _ := m["id"].(float64); return int64(v) }

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }

// path strips the origin from an absolute URL.
func path(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.IndexByte(u[i+3:], '/'); j >= 0 {
			return u[i+3+j:]
		}
	}
	return u
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withGPS inserts an Exif APP1 segment holding a GPS IFD (latitude N 52°)
// right after the JPEG's SOI marker.
func withGPS(j []byte) []byte {
	le := binary.LittleEndian
	tiff := []byte("II*\x00")
	tiff = le.AppendUint32(tiff, 8)
	// IFD0: one entry, GPSInfo (0x8825) -> offset 26.
	tiff = le.AppendUint16(tiff, 1)
	tiff = le.AppendUint16(tiff, 0x8825)
	tiff = le.AppendUint16(tiff, 4)
	tiff = le.AppendUint32(tiff, 1)
	tiff = le.AppendUint32(tiff, 26)
	tiff = le.AppendUint32(tiff, 0)
	// GPS IFD: GPSLatitudeRef "N", GPSLatitude 52/1 0/1 0/1.
	tiff = le.AppendUint16(tiff, 2)
	tiff = le.AppendUint16(tiff, 1)
	tiff = le.AppendUint16(tiff, 2)
	tiff = le.AppendUint32(tiff, 2)
	tiff = append(tiff, 'N', 0, 0, 0)
	tiff = le.AppendUint16(tiff, 2)
	tiff = le.AppendUint16(tiff, 5)
	tiff = le.AppendUint32(tiff, 3)
	tiff = le.AppendUint32(tiff, uint32(len(tiff)+4+4))
	tiff = le.AppendUint32(tiff, 0)
	for _, v := range []uint32{52, 1, 0, 1, 0, 1} {
		tiff = le.AppendUint32(tiff, v)
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte(nil), j[:2]...)
	out = append(out, seg...)
	return append(out, j[2:]...)
}

func animatedGIF(t *testing.T) []byte {
	t.Helper()
	g := &gif.GIF{}
	for i := range 6 {
		img := image.NewPaletted(image.Rect(0, 0, 64, 48), palette.Plan9)
		for p := range img.Pix {
			img.Pix[p] = uint8((p + i*40) % 200)
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 20)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
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
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-movflags", "+faststart", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// runJobs runs the job queue until the test ends.
func (e *env) runJobs() {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); e.s.App.Jobs.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); wg.Wait() })
}

func (e *env) waitFor(id int64, done func(*items.Item) bool) *items.Item {
	e.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		it := e.item(id)
		if done(it) {
			return it
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("item %d: media job did not finish: %+v", id, it)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestThumbnailAtUpload(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name    string
		content []byte
		w, h    int64
		mime    string
	}{
		{"wide.png", pngBytes(t, 1000, 500), 1000, 500, "image/png"}, // transparent pixels: PNG thumbnail
		{"photo.jpg", jpegBytes(t, 320, 200), 320, 200, "image/jpeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := e.put(tc.name, tc.content)
			thumb := str(j, "thumbnail_url")
			if !strings.HasSuffix(thumb, "/thumb") {
				t.Fatalf("thumbnail_url = %q, want the generated thumbnail", thumb)
			}
			if w, _ := j["width"].(float64); int64(w) != tc.w || int64(j["height"].(float64)) != tc.h {
				t.Errorf("dimensions %v×%v, want %d×%d", j["width"], j["height"], tc.w, tc.h)
			}
			status, ctype, body := e.get(path(thumb), false)
			if status != 200 || ctype != tc.mime {
				t.Fatalf("GET thumbnail: %d %s", status, ctype)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
			if err != nil || cfg.Width != min(640, int(tc.w)) {
				t.Errorf("thumbnail %+v, %v", cfg, err)
			}
			if n := e.jobs(id(j)); n != 0 {
				t.Errorf("still image queued %d media jobs", n)
			}
		})
	}
}

func TestStripJPEGLocation(t *testing.T) {
	e := newEnv(t)
	plain := jpegBytes(t, 64, 48)
	tagged := withGPS(plain)

	// Off by default: stored as uploaded.
	j := e.put("off.jpg", tagged)
	if str(j, "sha256") != sum(tagged) {
		t.Fatal("location stripped although the preference is off")
	}
	used := e.usedBytes()

	if err := items.SavePrefs(e.ctx, e.s.App.DB, 1, items.Prefs{StripLocation: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	j = e.put("on.jpg", tagged)
	it := e.item(id(j))
	f, err := e.s.App.Files.Open(e.ctx, it.BlobKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(f)
	_ = f.Close()
	if bytes.Contains(stored, []byte("Exif")) || !bytes.Equal(stored, plain) {
		t.Fatalf("stored %d bytes, want the %d bytes without the Exif segment", len(stored), len(plain))
	}
	if str(j, "sha256") != sum(plain) || int64(j["size"].(float64)) != int64(len(plain)) || it.Size != int64(len(plain)) {
		t.Errorf("response sha256/size not updated: %v %v", j["sha256"], j["size"])
	}
	if got := e.usedBytes() - used; got != int64(len(plain)) {
		t.Errorf("quota charged %d, want %d", got, len(plain))
	}
	if !strings.HasSuffix(str(j, "thumbnail_url"), "/thumb") {
		t.Error("no thumbnail for the stripped JPEG")
	}
	if _, _, body := e.get(path(str(j, "raw_url")), false); !bytes.Equal(body, plain) {
		t.Error("raw content is not the stripped file")
	}

	// A JPEG that cannot be parsed keeps its original bytes and charge.
	bad := append([]byte("\xff\xd8\xff\xe1\x00\x10Exif\x00\x00"), bytes.Repeat([]byte{0x42}, 64)...)
	used = e.usedBytes()
	j = e.put("bad.jpg", bad)
	if str(j, "sha256") != sum(bad) || e.usedBytes()-used != int64(len(bad)) {
		t.Errorf("malformed JPEG changed: sha %v, charged %d", j["sha256"], e.usedBytes()-used)
	}
	ents, _ := os.ReadDir(filepath.Join(e.s.App.Cfg.FilesDir, ".tmp"))
	if len(ents) != 0 {
		t.Errorf("temp files left: %v", ents)
	}
}

func TestVideoJob(t *testing.T) {
	e := newEnv(t)
	if !e.s.App.Media.FFmpeg() {
		t.Fatal("ffmpeg is not available; run the tests with `make test-go`")
	}
	e.runJobs()
	j := e.put("clip.mp4", clip(t))
	vid := id(j)
	it := e.waitFor(vid, func(it *items.Item) bool { return it.PreviewKey != "" && it.ThumbKey != "" })
	if it.Width != 320 || it.Height != 240 || it.DurationMS < 2900 || it.DurationMS > 3200 {
		t.Errorf("probe: %d×%d %d ms", it.Width, it.Height, it.DurationMS)
	}
	slug := "/" + it.Slug
	if status, ctype, _ := e.get(slug+"/thumb", false); status != 200 || ctype != "image/jpeg" {
		t.Errorf("poster: %d %s", status, ctype)
	}
	status, ctype, teaser := e.get(slug+"/preview", false)
	if status != 200 || ctype != "video/mp4" || len(teaser) == 0 || len(teaser) > 1<<20 {
		t.Errorf("teaser: %d %s %d bytes", status, ctype, len(teaser))
	}
	// The share page offers the poster and shows dimensions and duration.
	_, _, page := e.get(slug, false)
	for _, want := range []string{`poster="`, `preload="none"`, "320×240", "0:03"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("share page lacks %q", want)
		}
	}
	var state string
	if err := e.s.App.DB.R.QueryRow(`SELECT state FROM jobs WHERE kind = ?`, mediajobs.JobKind).Scan(&state); err != nil || state != "done" {
		t.Errorf("job state %q, %v", state, err)
	}

	// Deleting the item removes the poster and teaser too.
	req := e.s.Request(t, "DELETE", "/api/v1/items/"+itoa(vid), nil)
	authtest.Session(req, "ann")
	res, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	for _, k := range []string{it.BlobKey, it.ThumbKey, it.PreviewKey} {
		if e.blobExists(k) {
			t.Errorf("blob %s survived the item", k)
		}
	}
	// A job for a deleted item is a no-op.
	if err := e.s.App.Media.Process(e.ctx, vid); err != nil {
		t.Errorf("Process(deleted): %v", err)
	}
}

func TestAnimatedGIFTeaser(t *testing.T) {
	e := newEnv(t)
	j := e.put("loop.gif", animatedGIF(t))
	if !strings.HasSuffix(str(j, "thumbnail_url"), "/thumb") {
		t.Error("no synchronous thumbnail for the GIF")
	}
	if e.jobs(id(j)) != 1 {
		t.Fatal("animated GIF did not queue a media job")
	}
	if err := e.s.App.Media.Process(e.ctx, id(j)); err != nil {
		t.Fatal(err)
	}
	it := e.item(id(j))
	if it.PreviewKey == "" || it.ThumbKey == "" || it.DurationMS == 0 {
		t.Errorf("after job: thumb %q preview %q %d ms", it.ThumbKey, it.PreviewKey, it.DurationMS)
	}
}

func TestTeasersOff(t *testing.T) {
	e := newEnv(t)
	if err := mediajobs.Teasers.Set(e.ctx, e.s.App.Settings, false); err != nil {
		t.Fatal(err)
	}
	j := e.put("clip.mp4", clip(t))
	if e.jobs(id(j)) != 1 {
		t.Fatal("video did not queue a media job")
	}
	if err := e.s.App.Media.Process(e.ctx, id(j)); err != nil {
		t.Fatal(err)
	}
	it := e.item(id(j))
	if it.ThumbKey == "" || it.PreviewKey != "" {
		t.Errorf("teasers off: thumb %q preview %q", it.ThumbKey, it.PreviewKey)
	}
	if status, _, _ := e.get("/"+it.Slug+"/preview", false); status != 404 {
		t.Errorf("GET preview: %d", status)
	}
	// Animated images queue nothing at all.
	if g := e.put("loop.gif", animatedGIF(t)); e.jobs(id(g)) != 0 {
		t.Error("animated GIF queued a job with teasers off")
	}
}

func TestWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // media.NewRunner finds no ffmpeg
	e := newEnv(t)
	if e.s.App.Media.FFmpeg() {
		t.Fatal("ffmpeg found with an empty PATH")
	}
	j := e.put("pic.png", pngBytes(t, 40, 30))
	if status, ctype, _ := e.get(path(str(j, "thumbnail_url")), false); !strings.HasSuffix(str(j, "thumbnail_url"), "/thumb") || status != 200 || ctype != "image/png" {
		t.Errorf("image thumbnail without ffmpeg: %q %d %s", str(j, "thumbnail_url"), status, ctype)
	}
	v := e.put("clip.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"+strings.Repeat("\x00", 200)))
	if e.jobs(id(v)) != 0 {
		t.Error("video queued a media job without ffmpeg")
	}
	if err := e.s.App.Media.Process(e.ctx, id(v)); err != nil {
		t.Errorf("Process without ffmpeg: %v", err)
	}
	if it := e.item(id(v)); it.ThumbKey != "" || it.PreviewKey != "" {
		t.Errorf("video processed without ffmpeg: %+v", it)
	}
	if _, _, page := e.get("/"+e.item(id(v)).Slug, false); !bytes.Contains(page, []byte(`preload="metadata"`)) {
		t.Error("video without a poster should preload metadata")
	}
}

func TestNotForFFmpeg(t *testing.T) {
	e := newEnv(t)
	// A playlist named like a video is text: no job, and Process refuses it.
	j := e.put("evil.mp4", []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nfile:///etc/passwd\n"))
	if e.jobs(id(j)) != 0 {
		t.Error("playlist queued a media job")
	}
	if err := e.s.App.Media.Process(e.ctx, id(j)); err != nil {
		t.Errorf("Process(playlist): %v", err)
	}
	if it := e.item(id(j)); it.ThumbKey != "" || it.PreviewKey != "" || it.Width != 0 {
		t.Errorf("playlist processed: %+v", it)
	}
}
