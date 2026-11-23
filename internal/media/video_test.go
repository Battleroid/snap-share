package media

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// twoColors is a clip that is red for the first `at` and blue after.
func twoColors(t *testing.T, name string, size string, at, total float64, extra ...string) string {
	t.Helper()
	red := lavfi("color=c=red:s=" + size + ":r=24:d=" + ftoa(at))
	blue := lavfi("color=c=blue:s=" + size + ":r=24:d=" + ftoa(total-at))
	return gen(t, name, cat(red, blue, "-filter_complex", "[0][1]concat=n=2:v=1", extra)...)
}

func ftoa(f float64) string { return time.Duration(f * float64(time.Second)).String() }

func readJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("poster is not a JPEG: %v", err)
	}
	return img
}

func TestPoster(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast"}
	mp4 := gen(t, "v.mp4", cat(lavfi("testsrc2=s=320x240:r=24:d=2"), x264)...)
	cover := withCover(t, mp4)
	cases := []struct {
		name string
		path string
		w, h int
		col  color.Color // centre pixel, if set
	}{
		{"mp4 720p", gen(t, "hd.mp4", cat(lavfi("testsrc2=s=1280x720:r=24:d=3"), x264)...), 640, 360, nil},
		{"webm vp9", gen(t, "v.webm", cat(lavfi("testsrc2=s=320x240:r=24:d=3"), "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8")...), 320, 240, nil},
		// 10% of 10 s is past the red first half second.
		{"seeks to 10%", twoColors(t, "seek.mp4", "160x120", 0.5, 10, x264...), 160, 120, blue},
		// Under 2 s the first frame is used.
		{"short clip", twoColors(t, "short.mp4", "160x120", 0.5, 1.5, x264...), 160, 120, red},
		{"rotated", gen(t, "r.mp4", "-display_rotation", "90", "-i", mp4, "-c", "copy"), 240, 320, nil},
		{"with cover art", cover, 320, 240, nil},
		{"gif", putFile(t, "a.gif", encodeGIF(t, gifFrames([]color.Color{red, green, blue}, 64, 48, 10))), 64, 48, red},
		{"webp animated", gen(t, "a.webp", cat(lavfi("color=c=red:s=64x48:r=10:d=1"), lavfi("color=c=blue:s=64x48:r=10:d=1"),
			"-filter_complex", "[0][1]concat=n=2:v=1", "-c:v", "libwebp_anim", "-lossless", "1")...), 64, 48, red},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dst := filepath.Join(t.TempDir(), "poster.jpg")
			if err := r.Poster(testCtx(t), c.path, sniffPath(t, c.path), dst); err != nil {
				t.Fatal(err)
			}
			img := readJPEG(t, dst)
			if b := img.Bounds(); b.Dx() != c.w || b.Dy() != c.h {
				t.Errorf("poster %v, want %dx%d", b, c.w, c.h)
			}
			if c.col != nil {
				wantAt(t, img, c.w/2, c.h/2, c.col)
			}
		})
	}

	audio := gen(t, "a.m4a", cat(lavfi("sine=d=1"), "-c:a", "aac")...)
	if err := r.Poster(testCtx(t), audio, sniffPath(t, audio), filepath.Join(t.TempDir(), "p.jpg")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("audio poster = %v, want ErrUnsupported", err)
	}
}

// withCover remuxes a video with a 32x32 cover picture. The mp4 muxer
// stores it after the video package.
func withCover(t *testing.T, video string) string {
	t.Helper()
	cover := putFile(t, "cover.png", encode(t, quadrants(32, 32), encPNG))
	out := gen(t, "cover.mp4", "-i", cover, "-i", video, "-map", "0", "-map", "1",
		"-c:v:0", "png", "-c:v:1", "copy", "-disposition:v:0", "attached_pic")
	if p := probeFile(t, out); len(p.Streams) != 2 {
		t.Fatalf("streams: %+v", p.Streams)
	}
	return out
}

// topBoxes lists the top-level ISO BMFF box types of a file.
func topBoxes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var boxes []string
	for off := 0; off+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[off:]))
		boxes = append(boxes, string(b[off+4:off+8]))
		if n == 1 && off+16 <= len(b) {
			n = int(binary.BigEndian.Uint64(b[off+8:]))
		}
		if n < 8 {
			break
		}
		off += n
	}
	return boxes
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestTeaser(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast"}
	sine := func(d string) []string { return lavfi("sine=d=" + d) }
	small := gen(t, "s.mp4", cat(lavfi("testsrc2=s=320x240:r=24:d=3"), x264)...)
	cases := []struct {
		name string
		path string
		w, h int
		dur  float64
	}{
		{"mp4 60s segments", gen(t, "long.mp4", cat(lavfi("testsrc2=s=320x180:r=24:d=60"), sine("60"), "-shortest", x264, "-c:a", "aac")...), 320, 180, 7.5},
		{"mp4 5s whole", gen(t, "hd.mp4", cat(lavfi("testsrc2=s=1280x720:r=30:d=5"), sine("5"), "-shortest", x264, "-c:a", "aac")...), 480, 270, 5},
		{"webm vp9 10s", gen(t, "v.webm", cat(lavfi("testsrc2=s=320x240:r=24:d=10"), sine("10"), "-shortest",
			"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus")...), 320, 240, 7.5},
		{"odd size", gen(t, "odd.mkv", cat(lavfi("testsrc2=s=321x181:r=24:d=3"), "-c:v", "ffv1")...), 320, 180, 3},
		{"rotated", gen(t, "rot.mp4", "-display_rotation", "90", "-i", small, "-c", "copy"), 240, 320, 3},
		{"with cover art", withCover(t, small), 320, 240, 3},
		{"gif 3s whole", gen(t, "s.gif", lavfi("testsrc2=s=64x48:r=10:d=3")...), 64, 48, 3},
		{"gif 12s segments", gen(t, "l.gif", lavfi("testsrc2=s=64x48:r=10:d=12")...), 64, 48, 7.5},
		{"apng 2s", gen(t, "a.png", cat(lavfi("testsrc2=s=64x48:r=10:d=2"), "-f", "apng", "-plays", "0")...), 64, 48, 2},
		{"webp 2s whole", gen(t, "s.webp", cat(lavfi("testsrc2=s=64x48:r=10:d=2"), "-c:v", "libwebp_anim")...), 64, 48, 2},
		{"webp 10s segments", gen(t, "l.webp", cat(lavfi("testsrc2=s=66x50:r=5:d=10"), "-c:v", "libwebp_anim")...), 66, 50, 7.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			typ := sniffPath(t, c.path)
			info, err := r.Probe(ctx, c.path, typ)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "teaser.mp4")
			if err := r.Teaser(ctx, c.path, typ, info, dst); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Size() > 1<<20 {
				t.Errorf("size %d over 1 MiB", fi.Size())
			}
			p := probeFile(t, dst)
			if len(p.Streams) != 1 || p.Streams[0].CodecType != "video" {
				t.Fatalf("streams = %+v, want one video stream and no audio", p.Streams)
			}
			s := p.Streams[0]
			if s.CodecName != "h264" || s.PixFmt != "yuv420p" || s.FrameRate != "24/1" {
				t.Errorf("stream = %+v", s)
			}
			if s.Width != c.w || s.Height != c.h || s.Width > 480 || s.Width%2 != 0 || s.Height%2 != 0 {
				t.Errorf("size %dx%d, want %dx%d", s.Width, s.Height, c.w, c.h)
			}
			if d := p.duration(); d > 8 || math.Abs(d-c.dur) > 0.3 {
				t.Errorf("duration %.3f, want about %.1f and at most 8", d, c.dur)
			}
			boxes := topBoxes(t, dst)
			if m, d := indexOf(boxes, "moov"), indexOf(boxes, "mdat"); m < 0 || d < 0 || m > d {
				t.Errorf("not faststart: boxes %v", boxes)
			}
		})
	}
}

func TestTeaserByteCap(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{TeaserMaxBytes: 400})
	src := gen(t, "v.mp4", cat(lavfi("testsrc2=s=640x360:r=24:d=3"), "-c:v", "libx264", "-preset", "ultrafast")...)
	typ := sniffPath(t, src)
	info, err := r.Probe(testCtx(t), src, typ)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "teaser.mp4")
	if err := r.Teaser(testCtx(t), src, typ, info, dst); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Teaser = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("oversized teaser left behind: %v", err)
	}
}

func TestTeaserUnsupported(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	still := putFile(t, "s.png", encode(t, quadrants(32, 32), encPNG))
	stillGIF := putFile(t, "s.gif", encodeGIF(t, gifFrames([]color.Color{red}, 8, 8, 0)))
	audio := gen(t, "a.m4a", cat(lavfi("sine=d=1"), "-c:a", "aac")...)
	for _, path := range []string{still, stillGIF, audio} {
		typ := sniffPath(t, path)
		info, err := r.Probe(testCtx(t), path, typ)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(t.TempDir(), "teaser.mp4")
		if err := r.Teaser(testCtx(t), path, typ, info, dst); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Teaser = %v, want ErrUnsupported", path, err)
		}
	}
}

func TestTeaserSpans(t *testing.T) {
	t.Parallel()
	s := time.Second
	for _, c := range []struct {
		d    time.Duration
		want []span
	}{
		{0, []span{{0, 8 * s}}},
		{5 * s, []span{{0, 5 * s}}},
		{7999 * time.Millisecond, []span{{0, 7999 * time.Millisecond}}},
		{60 * s, []span{{6 * s, 1500 * time.Millisecond}, {18 * s, 1500 * time.Millisecond}, {30 * s, 1500 * time.Millisecond},
			{42 * s, 1500 * time.Millisecond}, {54 * s, 1500 * time.Millisecond}}},
		// The last segment is pulled back so it ends at the end.
		{8 * s, []span{{800 * time.Millisecond, 1500 * time.Millisecond}, {2400 * time.Millisecond, 1500 * time.Millisecond},
			{4 * s, 1500 * time.Millisecond}, {5600 * time.Millisecond, 1500 * time.Millisecond}, {6500 * time.Millisecond, 1500 * time.Millisecond}}},
	} {
		got := teaserSpans(c.d)
		if len(got) != len(c.want) {
			t.Fatalf("teaserSpans(%s) = %v", c.d, got)
		}
		for i := range got {
			if (got[i].start-c.want[i].start).Abs() > time.Millisecond || got[i].length != c.want[i].length {
				t.Errorf("teaserSpans(%s)[%d] = %v, want %v", c.d, i, got[i], c.want[i])
			}
		}
	}
}
