package mediaworker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/version"
)

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

func lavfi(src string) []string { return []string{"-f", "lavfi", "-i", src} }

type fixture struct {
	name string // the item's filename, for snap's sniff
	data []byte
}

func fixtures(t *testing.T) []fixture {
	return []fixture{
		{"clip.mp4", gen(t, "clip.mp4", append(append(lavfi("testsrc=size=320x240:rate=24"), lavfi("sine=frequency=440")...),
			"-t", "3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", "-movflags", "+faststart")...)},
		{"clip.webm", gen(t, "clip.webm", append(lavfi("testsrc=size=160x120:rate=10:duration=2"), "-c:v", "libvpx-vp9", "-b:v", "200k")...)},
		// Audio in an isom MP4: only the .m4a name makes it audio/mp4.
		{"song.m4a", gen(t, "song.mp4", append(lavfi("sine=frequency=440:duration=1"), "-c:a", "aac")...)},
		{"photo.jpg", jpegBytes(t, 320, 200)},
		{"wide.png", pngBytes(t, 1000, 500)},
		{"loop.gif", animatedGIF(t)},
	}
}

// Every operation through the worker gives what the local runner gives.
func TestOpsMatchLocalRunner(t *testing.T) {
	local := directRunner(t)
	w := newWorker(t, directRunner(t))
	ctx := ctxT(t)
	out := t.TempDir()
	for _, fx := range fixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			src := w.put(fx.data)
			typ := sniff(t, src, fx.name)
			if fx.name == "song.m4a" && typ.MIME != "audio/mp4" {
				t.Fatalf("fixture sniffed as %+v", typ)
			}
			want, werr := local.Probe(ctx, src, typ)
			got, gerr := w.client.Probe(ctx, src, typ)
			if (werr == nil) != (gerr == nil) || got != want {
				t.Fatalf("Probe: worker %+v %v, local %+v %v", got, gerr, want, werr)
			}

			var wb, gb bytes.Buffer
			wth, werr := local.Thumbnail(ctx, src, typ, &wb)
			gth, gerr := w.client.Thumbnail(ctx, src, typ, &gb)
			if (werr == nil) != (gerr == nil) || gth != wth || !bytes.Equal(gb.Bytes(), wb.Bytes()) {
				t.Errorf("Thumbnail: worker %+v %d bytes %v, local %+v %d bytes %v", gth, gb.Len(), gerr, wth, wb.Len(), werr)
			}
			if werr != nil && !sameClass(gerr, werr) {
				t.Errorf("Thumbnail errors differ: worker %v, local %v", gerr, werr)
			}

			lp, gp := filepath.Join(out, fx.name+".l.jpg"), filepath.Join(out, fx.name+".w.jpg")
			werr = local.Poster(ctx, src, typ, lp)
			gerr = w.client.Poster(ctx, src, typ, gp)
			if (werr == nil) != (gerr == nil) || !sameClass(gerr, werr) {
				t.Errorf("Poster: worker %v, local %v", gerr, werr)
			} else if werr == nil {
				lb, _ := os.ReadFile(lp)
				gb, _ := os.ReadFile(gp)
				if !bytes.Equal(lb, gb) || len(gb) == 0 {
					t.Errorf("Poster: worker %d bytes, local %d", len(gb), len(lb))
				}
			} else if _, err := os.Stat(gp); err == nil {
				t.Error("failed Poster left its output")
			}

			lt, gt := filepath.Join(out, fx.name+".l.mp4"), filepath.Join(out, fx.name+".w.mp4")
			werr = local.Teaser(ctx, src, typ, want, lt)
			gerr = w.client.Teaser(ctx, src, typ, want, gt)
			if (werr == nil) != (gerr == nil) || !sameClass(gerr, werr) {
				t.Errorf("Teaser: worker %v, local %v", gerr, werr)
			} else if werr == nil {
				if got := sniff(t, gt, "t.mp4"); got.MIME != "video/mp4" {
					t.Errorf("Teaser output sniffed as %+v", got)
				}
			}
		})
	}
	// Each request's scratch directory is gone.
	ents, err := os.ReadDir(w.scratch)
	if err != nil || len(ents) != 0 {
		t.Errorf("scratch left: %v", ents)
	}
}

func sameClass(a, b error) bool {
	for _, s := range []error{media.ErrUnsupported, media.ErrTooLarge, media.ErrTimeout} {
		if errors.Is(a, s) != errors.Is(b, s) {
			return false
		}
	}
	return true
}

// fakeRunner fails every call with err, or writes body.
type fakeRunner struct {
	err  error
	body []byte
}

func (f fakeRunner) Probe(context.Context, string, media.Type) (media.Info, error) {
	return media.Info{Width: 1, Height: 1}, f.err
}

func (f fakeRunner) Thumbnail(_ context.Context, _ string, _ media.Type, w io.Writer) (media.Thumb, error) {
	if f.err != nil {
		return media.Thumb{}, f.err
	}
	_, err := w.Write(f.body)
	return media.Thumb{MIME: "image/png", Width: 2, Height: 3}, err
}

func (f fakeRunner) Poster(_ context.Context, _ string, _ media.Type, dst string) error {
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dst, f.body, 0o600)
}

func (f fakeRunner) Teaser(_ context.Context, _ string, _ media.Type, _ media.Info, dst string) error {
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dst, f.body, 0o600)
}

var pngType = media.Type{MIME: "image/png", Kind: media.KindImage}

func TestErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
		want   error
		state  string // after a job returns the client's error through mediajobs' classify
	}{
		{fmt.Errorf("%w: nope", media.ErrUnsupported), 422, "unsupported", media.ErrUnsupported, "failed"},
		{fmt.Errorf("%w: big", media.ErrTooLarge), 413, "too_large", media.ErrTooLarge, "failed"},
		{fmt.Errorf("%w: slow", media.ErrTimeout), 504, "timeout", media.ErrTimeout, "failed"},
		{errors.New("ffmpeg: exit status 1: broken"), 500, "internal", nil, "queued"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			w := newWorker(t, fakeRunner{err: tc.err})
			src := w.put(pngBytes(t, 4, 4))
			key := filepath.Base(src)
			status, code := w.raw("probe", "Bearer "+token, `{"key":"`+key+`","type":{"mime":"image/png","kind":"image","container":""}}`)
			if status != tc.status || code != tc.code {
				t.Errorf("raw: %d %s, want %d %s", status, code, tc.status, tc.code)
			}
			ctx := ctxT(t)
			errs := map[string]error{}
			_, errs["probe"] = w.client.Probe(ctx, src, pngType)
			_, errs["thumbnail"] = w.client.Thumbnail(ctx, src, pngType, io.Discard)
			errs["poster"] = w.client.Poster(ctx, src, pngType, filepath.Join(t.TempDir(), "p"))
			errs["teaser"] = w.client.Teaser(ctx, src, pngType, media.Info{}, filepath.Join(t.TempDir(), "t"))
			for op, err := range errs {
				if err == nil {
					t.Errorf("%s: no error", op)
					continue
				}
				if tc.want != nil && !errors.Is(err, tc.want) {
					t.Errorf("%s: %v, want %v", op, err, tc.want)
				}
				if tc.want == nil && !plain(err) {
					t.Errorf("%s: %v has a media class", op, err)
				}
				if !strings.Contains(err.Error(), strings.SplitN(tc.err.Error(), ": ", 2)[1]) {
					t.Errorf("%s: %v lost the worker's message", op, err)
				}
			}
			// The class survives into the job queue as mediajobs classifies it.
			herr := errs["probe"]
			if tc.want != nil {
				herr = jobs.Permanent(herr)
			}
			if state, attempts := jobOutcome(t, herr); state != tc.state || attempts != 1 {
				t.Errorf("job %s with %d attempts, want %s with 1", state, attempts, tc.state)
			}
		})
	}
}

func TestDialFailureDefers(t *testing.T) {
	for name, url := range map[string]string{
		"no socket": "unix://" + filepath.Join(socketDir(t), "none.sock"),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: url, Token: token})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Probe(ctxT(t), "/files/aa/bb/aabbccddeeff00112233445566778899", pngType)
			if err == nil || !plain(err) {
				t.Fatalf("Probe = %v", err)
			}
			// Wrapped, as mediajobs' Process wraps step errors.
			if state, attempts := jobOutcome(t, fmt.Errorf("poster: %w", err)); state != "queued" || attempts != 0 {
				t.Errorf("job %s with %d attempts, want deferred (queued, 0)", state, attempts)
			}
		})
	}
}

// A socket whose worker has gone (a stale socket file) also defers.
func TestStaleSocketDefers(t *testing.T) {
	sock := filepath.Join(socketDir(t), "w.sock")
	ln, err := mediaworker.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ul := ln.(*net.UnixListener)
	ul.SetUnlinkOnClose(false)
	_ = ln.Close()
	c, _ := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + sock, Token: token})
	_, err = c.Probe(ctxT(t), "/files/aa/bb/aabbccddeeff00112233445566778899", pngType)
	if state, attempts := jobOutcome(t, err); state != "queued" || attempts != 0 {
		t.Errorf("%v: job %s with %d attempts, want deferred", err, state, attempts)
	}
}

func TestStartingDefers(t *testing.T) {
	w := newWorker(t, nil) // not started
	if status, code := w.raw("health", "Bearer "+token, ""); status != 503 || code != "starting" {
		t.Errorf("health while starting: %d %s", status, code)
	}
	src := w.put(pngBytes(t, 4, 4))
	_, err := w.client.Probe(ctxT(t), src, pngType)
	if state, attempts := jobOutcome(t, err); state != "queued" || attempts != 0 {
		t.Errorf("%v: job %s with %d attempts, want deferred", err, state, attempts)
	}
	w.srv.Start(fakeRunner{}, "test")
	if _, err := w.client.Probe(ctxT(t), src, pngType); err != nil {
		t.Errorf("after Start: %v", err)
	}
}

// A connection that drops mid-response (the worker crashed on this input)
// is a counted error, never a deferral: an input that kills the worker must
// run out of attempts.
func TestDroppedConnectionCounts(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"mid-body": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "100000")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			conn, _, _ := http.NewResponseController(w).Hijack()
			_ = conn.Close()
		},
		"before headers": func(w http.ResponseWriter, _ *http.Request) {
			conn, _, _ := http.NewResponseController(w).Hijack()
			_ = conn.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorker(t, fakeRunner{})
			w.sock = filepath.Join(socketDir(t), "drop.sock")
			w.serve(h)
			c, _ := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + w.sock, Token: token})
			src := w.put(pngBytes(t, 4, 4))
			dst := filepath.Join(t.TempDir(), "poster.jpg")
			err := c.Poster(ctxT(t), src, pngType, dst)
			if err == nil || !plain(err) {
				t.Fatalf("Poster = %v, want a plain error", err)
			}
			if _, serr := os.Stat(dst); serr == nil {
				t.Error("partial poster left at dst")
			}
			if state, attempts := jobOutcome(t, err); state != "queued" || attempts != 1 {
				t.Errorf("job %s with %d attempts, want a counted retry", state, attempts)
			}
		})
	}
}

func TestOversizedBody(t *testing.T) {
	big := bytes.Repeat([]byte{0xff}, 1<<20+1) // one byte over the default teaser cap
	w := newWorker(t, fakeRunner{body: big})
	src := w.put(pngBytes(t, 4, 4))
	ctx := ctxT(t)
	dst := filepath.Join(t.TempDir(), "teaser.mp4")
	if err := w.client.Teaser(ctx, src, pngType, media.Info{}, dst); !errors.Is(err, media.ErrTooLarge) {
		t.Errorf("Teaser = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("oversized teaser left at dst")
	}
	// The same body is fine as a poster (cap 32 MiB) ...
	if err := w.client.Poster(ctx, src, pngType, filepath.Join(t.TempDir(), "p.jpg")); err != nil {
		t.Errorf("Poster: %v", err)
	}
	// ... and a body over that is refused, whatever Content-Length says.
	huge := newWorker(t, fakeRunner{})
	huge.sock = filepath.Join(socketDir(t), "huge.sock")
	huge.serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set(mediaworker.HeaderWidth, "1")
		w.Header().Set(mediaworker.HeaderHeight, "1")
		chunk := make([]byte, 1<<20)
		for range 33 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	c, _ := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + huge.sock, Token: token})
	if _, err := c.Thumbnail(ctx, src, pngType, io.Discard); !errors.Is(err, media.ErrTooLarge) {
		t.Errorf("Thumbnail of 33 MiB = %v, want ErrTooLarge", err)
	}
}

func TestToken(t *testing.T) {
	w := newWorker(t, fakeRunner{})
	key := filepath.Base(w.put(pngBytes(t, 4, 4)))
	body := `{"key":"` + key + `","type":{"mime":"image/png","kind":"image","container":""}}`
	for _, auth := range []string{"", "Bearer", "Bearer " + token[:len(token)-1], "Bearer " + token + "x", "bearer " + token, token, "Basic " + token} {
		for _, op := range []string{"probe", "health", "nope"} {
			if status, code := w.raw(op, auth, body); status != 401 || code != "unauthorized" {
				t.Errorf("%s with %q: %d %s", op, auth, status, code)
			}
		}
	}
	if status, _ := w.raw("probe", "Bearer "+token, body); status != 200 {
		t.Errorf("right token: %d", status)
	}
	other, _ := mediaworker.NewClient(mediaworker.ClientConfig{URL: "unix://" + w.sock, Token: strings.Repeat("w", 40)})
	if _, err := other.Health(ctxT(t)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token Health = %v", err)
	}
}

func TestHealth(t *testing.T) {
	w := newWorker(t, fakeRunner{})
	h, err := w.client.Health(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if h != (mediaworker.Health{Protocol: mediaworker.Protocol, Version: version.Version, FFmpeg: "test"}) {
		t.Errorf("health %+v", h)
	}
	if v := mediaworker.FFmpegVersion(ctxT(t), ""); v == "" || strings.ContainsAny(v, " \n") {
		t.Errorf("FFmpegVersion = %q", v)
	}
}

func TestBadRequests(t *testing.T) {
	w := newWorker(t, fakeRunner{})
	key := filepath.Base(w.put(pngBytes(t, 4, 4)))
	typ := `"type":{"mime":"image/png","kind":"image","container":""}`
	for name, tc := range map[string]struct {
		op, body string
		status   int
	}{
		"not json":      {"probe", `nope`, 400},
		"two objects":   {"probe", `{"key":"` + key + `",` + typ + `}{}`, 400},
		"upper hex":     {"probe", `{"key":"` + strings.ToUpper(key) + `",` + typ + `}`, 400},
		"missing blob":  {"probe", `{"key":"00000000000000000000000000000000",` + typ + `}`, 404},
		"info on probe": {"probe", `{"key":"` + key + `",` + typ + `,"info":{}}`, 400},
		"no info":       {"teaser", `{"key":"` + key + `",` + typ + `}`, 400},
		"huge info":     {"teaser", `{"key":"` + key + `",` + typ + `,"info":{"width":100000}}`, 400},
		"negative dur":  {"teaser", `{"key":"` + key + `",` + typ + `,"info":{"duration_ns":-1}}`, 400},
		"unknown op":    {"exec", `{"key":"` + key + `",` + typ + `}`, 404},
		"big body":      {"probe", `{"key":"` + key + `",` + typ + `,"x":"` + strings.Repeat("a", 70<<10) + `"}`, 400},
	} {
		if status, _ := w.raw(tc.op, "Bearer "+token, tc.body); status != tc.status {
			t.Errorf("%s: %d, want %d", name, status, tc.status)
		}
	}
}

// The client turns only blob paths into keys.
func TestClientPaths(t *testing.T) {
	w := newWorker(t, fakeRunner{})
	ctx := ctxT(t)
	for _, p := range []string{"/etc/passwd", "/files/aabbccddeeff00112233445566778899",
		"/files/aa/cc/aabbccddeeff00112233445566778899", "/files/aa/bb/../bb/AABBCCDDEEFF00112233445566778899"} {
		if _, err := w.client.Probe(ctx, p, pngType); err == nil || !strings.Contains(err.Error(), "not a blob path") {
			t.Errorf("Probe(%q) = %v", p, err)
		}
	}
	for _, u := range []string{"", "unix://rel.sock", "https://w:1", "http://w:1/x", "http://u:p@w:1", "tcp://w:1"} {
		if _, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: u, Token: token}); err == nil {
			t.Errorf("NewClient(%q) accepted", u)
		}
	}
	if _, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: "http://127.0.0.1:1", Token: "short"}); err == nil {
		t.Error("short token accepted")
	}
}

// The request id reaches the worker's log; the token never does.
func TestRequestIDLogged(t *testing.T) {
	w := newWorker(t, fakeRunner{err: fmt.Errorf("%w: nope", media.ErrUnsupported)})
	src := w.put(pngBytes(t, 4, 4))
	_, _ = w.client.Probe(mediaworker.WithRequestID(ctxT(t), "job-7-2"), src, pngType)
	log := w.log.String()
	for _, want := range []string{`"request_id":"job-7-2"`, `"op":"probe"`, `"mime":"image/png"`, `"result":"unsupported"`, `"ms":`} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %s:\n%s", want, log)
		}
	}
}

// The listener's socket is private and its directory is the worker's own.
func TestListenPermissions(t *testing.T) {
	sock := filepath.Join(socketDir(t), "w.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mediaworker.Listen("unix", sock); err == nil {
		t.Fatal("Listen replaced a regular file")
	}
	_ = os.Remove(sock)
	ln, err := mediaworker.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	for p, want := range map[string]os.FileMode{filepath.Dir(sock): 0o700, sock: 0o600} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", p, fi.Mode().Perm(), err, want)
		}
	}
}

// The server's own cap ends a request whatever the Runner does.
func TestRequestTimeout(t *testing.T) {
	w := newWorker(t, blockingRunner{}, func(c *mediaworker.ServerConfig) { c.RequestTimeout = 200 * time.Millisecond })
	src := w.put(pngBytes(t, 4, 4))
	start := time.Now()
	_, err := w.client.Probe(ctxT(t), src, pngType)
	if !errors.Is(err, media.ErrTimeout) || time.Since(start) > 10*time.Second {
		t.Errorf("Probe = %v after %s, want ErrTimeout", err, time.Since(start))
	}
}

// blockingRunner waits for its context, as ffmpeg killed on cancel does.
type blockingRunner struct{ fakeRunner }

func (blockingRunner) Probe(ctx context.Context, _ string, _ media.Type) (media.Info, error) {
	<-ctx.Done()
	return media.Info{}, fmt.Errorf("media: ffprobe: %w", ctx.Err())
}

// Thumbnail headers come back as media.Thumb.
func TestThumbnailHeaders(t *testing.T) {
	w := newWorker(t, fakeRunner{body: []byte("img")})
	src := w.put(pngBytes(t, 4, 4))
	var b bytes.Buffer
	th, err := w.client.Thumbnail(ctxT(t), src, pngType, &b)
	if err != nil || th != (media.Thumb{MIME: "image/png", Width: 2, Height: 3}) || b.String() != "img" {
		t.Errorf("Thumbnail = %+v %q %v", th, b.String(), err)
	}
}

// plain reports whether err has none of the media error classes.
func plain(err error) bool {
	return !errors.Is(err, media.ErrUnsupported) && !errors.Is(err, media.ErrTooLarge) && !errors.Is(err, media.ErrTimeout)
}
