package media

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

var (
	red   = color.RGBA{255, 0, 0, 255}
	green = color.RGBA{0, 255, 0, 255}
	blue  = color.RGBA{0, 0, 255, 255}
	white = color.RGBA{255, 255, 255, 255}
)

func near(a, b color.Color) bool {
	r1, g1, b1, a1 := a.RGBA()
	r2, g2, b2, a2 := b.RGBA()
	d := func(x, y uint32) bool { return max(x, y)-min(x, y) < 0x3000 }
	return d(r1, r2) && d(g1, g2) && d(b1, b2) && d(a1, a2)
}

func encode(t *testing.T, img image.Image, enc func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := enc(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(b *bytes.Buffer, m image.Image) error {
	return jpeg.Encode(b, m, &jpeg.Options{Quality: 95})
}
func encPNG(b *bytes.Buffer, m image.Image) error  { return png.Encode(b, m) }
func encBMP(b *bytes.Buffer, m image.Image) error  { return bmp.Encode(b, m) }
func encTIFF(b *bytes.Buffer, m image.Image) error { return tiff.Encode(b, m, nil) }

// quadrants is w×h with red, green / blue, white quadrants.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := [2][2]color.RGBA{{red, green}, {blue, white}}[y*2/h][x*2/w]
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// quadColors samples the centres of the four quadrants: TL, TR, BL, BR.
func quadColors(m image.Image) [4]color.Color {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	at := func(fx, fy float64) color.Color {
		return m.At(b.Min.X+int(fx*float64(w)), b.Min.Y+int(fy*float64(h)))
	}
	return [4]color.Color{at(.25, .25), at(.75, .25), at(.25, .75), at(.75, .75)}
}

func thumb(t *testing.T, r *Runner, path string) (Thumb, image.Image) {
	t.Helper()
	var buf bytes.Buffer
	th, err := r.Thumbnail(testCtx(t), path, sniffPath(t, path), &buf)
	if err != nil {
		t.Fatalf("Thumbnail(%s): %v", path, err)
	}
	var img image.Image
	switch th.MIME {
	case "image/jpeg":
		img, err = jpeg.Decode(&buf)
	case "image/png":
		img, err = png.Decode(&buf)
	default:
		t.Fatalf("thumbnail MIME %q", th.MIME)
	}
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != th.Width || b.Dy() != th.Height {
		t.Fatalf("Thumb says %dx%d, image is %v", th.Width, th.Height, b)
	}
	return th, img
}

func TestJPEGOrientation(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	src := encode(t, quadrants(200, 100), encJPEG)
	R, G, B, W := red, green, blue, white
	want := map[uint16][4]color.Color{ // display TL, TR, BL, BR
		1: {R, G, B, W},
		2: {G, R, W, B},
		3: {W, B, G, R},
		4: {B, W, R, G},
		5: {R, B, G, W},
		6: {B, R, W, G},
		7: {W, G, B, R},
		8: {G, W, R, B},
	}
	for o, quads := range want {
		path := putFile(t, "o.jpg", insertAfterSOI(src, segment(mAPP1, exifTIFF(o%2 == 0, o, true))))
		info, err := r.Probe(testCtx(t), path, sniffPath(t, path))
		if err != nil {
			t.Fatal(err)
		}
		wantW, wantH := 200, 100
		if o >= 5 {
			wantW, wantH = 100, 200
		}
		if info.Orientation != int(o) || info.Width != wantW || info.Height != wantH {
			t.Errorf("orientation %d: Probe = %+v", o, info)
		}
		th, img := thumb(t, r, path)
		if th.MIME != "image/jpeg" || th.Width != wantW || th.Height != wantH {
			t.Errorf("orientation %d: thumb %+v", o, th)
		}
		got := quadColors(img)
		for i := range got {
			if !near(got[i], quads[i]) {
				t.Errorf("orientation %d quadrant %d = %v, want %v", o, i, got[i], quads[i])
			}
		}
	}
}

func gifFrames(cols []color.Color, w, h int, delay int) *gif.GIF {
	g := &gif.GIF{}
	for _, c := range cols {
		m := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
		for i := range m.Pix {
			m.Pix[i] = uint8(m.Palette.Index(c))
		}
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, delay)
	}
	return g
}

func encodeGIF(t *testing.T, g *gif.GIF) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestThumbnails(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})

	alpha := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := range 40 {
		for x := 40; x < 80; x++ {
			alpha.SetNRGBA(x, y, color.NRGBA{0, 0, 255, 255})
		}
	}
	opaquePNG := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for i := 3; i < len(opaquePNG.Pix); i += 4 {
		opaquePNG.Pix[i] = 255
	}
	// First GIF frame covers only the left half of the canvas.
	partial := gifFrames([]color.Color{red}, 32, 48, 10)
	partial.Config = image.Config{Width: 64, Height: 48, ColorModel: color.Palette(palette.Plan9)}

	v := lavfi("testsrc2=s=160x120:r=10:d=1")
	cases := []struct {
		name     string
		path     string
		mime     string
		w, h     int
		animated bool
		dur      time.Duration // checked to ±150ms when animated
		check    func(t *testing.T, img image.Image)
	}{
		{name: "large jpeg", path: putFile(t, "big.jpg", encode(t, quadrants(1600, 800), encJPEG)),
			mime: "image/jpeg", w: 640, h: 320,
			check: func(t *testing.T, img image.Image) { wantQuads(t, img, red, green, blue, white) }},
		{name: "png alpha", path: putFile(t, "a.png", encode(t, alpha, encPNG)), mime: "image/png", w: 80, h: 40,
			check: func(t *testing.T, img image.Image) {
				if _, _, _, a := img.At(10, 20).RGBA(); a != 0 {
					t.Errorf("transparent half has alpha %d", a)
				}
				if !near(img.At(60, 20), blue) {
					t.Errorf("opaque half = %v", img.At(60, 20))
				}
			}},
		{name: "png opaque", path: putFile(t, "o.png", encode(t, opaquePNG, encPNG)), mime: "image/jpeg", w: 80, h: 40},
		{name: "gif animated", path: putFile(t, "a.gif", encodeGIF(t, gifFrames([]color.Color{red, green, blue}, 64, 48, 10))),
			mime: "image/jpeg", w: 64, h: 48, animated: true, dur: 300 * time.Millisecond,
			check: func(t *testing.T, img image.Image) { wantAt(t, img, 32, 24, red) }},
		{name: "gif zero delay", path: putFile(t, "z.gif", encodeGIF(t, gifFrames([]color.Color{red, green}, 8, 8, 0))),
			mime: "image/jpeg", w: 8, h: 8, animated: true, dur: 200 * time.Millisecond},
		{name: "gif partial first frame", path: putFile(t, "p.gif", encodeGIF(t, partial)), mime: "image/png", w: 64, h: 48,
			check: func(t *testing.T, img image.Image) {
				wantAt(t, img, 10, 10, red)
				if _, _, _, a := img.At(50, 10).RGBA(); a != 0 {
					t.Errorf("uncovered canvas alpha = %d", a)
				}
			}},
		{name: "gif still", path: putFile(t, "s.gif", encodeGIF(t, gifFrames([]color.Color{blue}, 16, 16, 0))), mime: "image/jpeg", w: 16, h: 16},
		{name: "bmp", path: putFile(t, "q.bmp", encode(t, quadrants(64, 32), encBMP)), mime: "image/jpeg", w: 64, h: 32,
			check: func(t *testing.T, img image.Image) { wantQuads(t, img, red, green, blue, white) }},
		{name: "tiff", path: putFile(t, "q.tiff", encode(t, quadrants(64, 32), encTIFF)), mime: "image/jpeg", w: 64, h: 32,
			check: func(t *testing.T, img image.Image) { wantQuads(t, img, red, green, blue, white) }},
		{name: "webp still", path: gen(t, "s.webp", cat(lavfi("color=c=blue:s=64x48"), "-frames:v", "1", "-c:v", "libwebp")...),
			mime: "image/jpeg", w: 64, h: 48, check: func(t *testing.T, img image.Image) { wantAt(t, img, 32, 24, blue) }},
		{name: "webp animated", path: gen(t, "a.webp", cat(lavfi("color=c=red:s=64x48:r=10:d=1"), lavfi("color=c=blue:s=64x48:r=10:d=1"),
			"-filter_complex", "[0][1]concat=n=2:v=1", "-c:v", "libwebp_anim", "-lossless", "1")...),
			mime: "image/jpeg", w: 64, h: 48, animated: true, dur: 2 * time.Second,
			check: func(t *testing.T, img image.Image) { wantAt(t, img, 32, 24, red) }},
		{name: "webp animated alpha", path: gen(t, "aa.webp", cat(lavfi("color=c=red@0.5:s=64x48:r=10:d=1,format=rgba"),
			lavfi("color=c=blue@0.5:s=64x48:r=10:d=1,format=rgba"),
			"-filter_complex", "[0][1]concat=n=2:v=1", "-c:v", "libwebp_anim", "-lossless", "0")...),
			mime: "image/png", w: 64, h: 48, animated: true, dur: 2 * time.Second,
			check: func(t *testing.T, img image.Image) {
				if _, _, _, a := img.At(32, 24).RGBA(); a < 0x6000 || a > 0xa000 {
					t.Errorf("alpha = %#x, want about half", a)
				}
			}},
		{name: "apng", path: gen(t, "a.png", cat(v, "-f", "apng", "-plays", "0")...),
			mime: "image/jpeg", w: 160, h: 120, animated: true, dur: time.Second},
		{name: "avif", path: gen(t, "s.avif", cat(lavfi("color=c=green:s=160x120"), "-frames:v", "1", "-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "8")...),
			mime: "image/jpeg", w: 160, h: 120, check: func(t *testing.T, img image.Image) { wantAt(t, img, 80, 60, color.RGBA{0, 128, 0, 255}) }},
		{name: "avif large", path: gen(t, "l.avif", cat(lavfi("testsrc2=s=1280x720"), "-frames:v", "1", "-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "8")...),
			mime: "image/jpeg", w: 640, h: 360},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			typ := sniffPath(t, c.path)
			info, err := r.Probe(testCtx(t), c.path, typ)
			if err != nil {
				t.Fatal(err)
			}
			if info.Animated != c.animated {
				t.Errorf("Animated = %v, want %v", info.Animated, c.animated)
			}
			if c.animated && (info.Duration < c.dur-150*time.Millisecond || info.Duration > c.dur+150*time.Millisecond) {
				t.Errorf("Duration = %s, want about %s", info.Duration, c.dur)
			}
			th, img := thumb(t, r, c.path)
			if th.MIME != c.mime || th.Width != c.w || th.Height != c.h {
				t.Errorf("thumb = %+v, want %s %dx%d", th, c.mime, c.w, c.h)
			}
			if c.check != nil {
				c.check(t, img)
			}
		})
	}
}

func wantAt(t *testing.T, img image.Image, x, y int, c color.Color) {
	t.Helper()
	if got := img.At(x, y); !near(got, c) {
		t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, c)
	}
}

func wantQuads(t *testing.T, img image.Image, cols ...color.Color) {
	t.Helper()
	for i, got := range quadColors(img) {
		if !near(got, cols[i]) {
			t.Errorf("quadrant %d = %v, want %v", i, got, cols[i])
		}
	}
}

func pngChunk(b *bytes.Buffer, typ string, data []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	_ = binary.Write(b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

// pngBomb has a valid header claiming w×h and almost no pixel data.
func pngBomb(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	pngChunk(&b, "IHDR", append(ihdr, 8, 2, 0, 0, 0))
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(make([]byte, 1024))
	_ = zw.Close()
	pngChunk(&b, "IDAT", z.Bytes())
	pngChunk(&b, "IEND", nil)
	return b.Bytes()
}

func TestPixelBombsRejectedBeforeDecode(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	jpg := encode(t, quadrants(16, 16), encJPEG)
	sof := bytes.Index(jpg, []byte{0xff, 0xc0})
	binary.BigEndian.PutUint16(jpg[sof+5:], 60000) // height
	binary.BigEndian.PutUint16(jpg[sof+7:], 60000) // width
	gifBomb := []byte("GIF89a\xff\xff\xff\xff\x00\x00\x00\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00\x3b")
	webpBomb := []byte("RIFF\x3c\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x12\x00\x00\x00\xff\x3f\x00\xff\x3f\x00" +
		"ANIM\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00ANMF\x10\x00\x00\x00" + strings.Repeat("\x00", 16))
	bombs := map[string][]byte{
		"bomb.png":  pngBomb(30000, 30000),
		"bomb.jpg":  jpg,
		"bomb.gif":  gifBomb,
		"bomb.webp": webpBomb,
	}
	for name, data := range bombs {
		path := putFile(t, name, data)
		typ := sniffPath(t, path)
		info, err := r.Probe(testCtx(t), path, typ)
		if err != nil {
			t.Fatalf("%s: Probe: %v", name, err)
		}
		if int64(info.Width)*int64(info.Height) <= 100_000_000 {
			t.Fatalf("%s: header claims only %dx%d", name, info.Width, info.Height)
		}
		start := time.Now()
		_, err = r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{})
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: Thumbnail = %v, want ErrTooLarge", name, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: took %s; decoded before checking?", name, d)
		}
		if err := r.Poster(testCtx(t), path, typ, putFile(t, "p.jpg", nil)); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: Poster = %v, want ErrTooLarge", name, err)
		}
	}

	// The cap is configurable and also guards the ffmpeg path.
	small := newTestRunner(t, Config{MaxPixels: 1000})
	for _, path := range []string{
		putFile(t, "q.png", encode(t, quadrants(100, 100), encPNG)),
		gen(t, "s.avif", cat(lavfi("color=c=green:s=160x120"), "-frames:v", "1", "-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "8")...),
	} {
		if _, err := small.Thumbnail(testCtx(t), path, sniffPath(t, path), &bytes.Buffer{}); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s with MaxPixels 1000: %v", path, err)
		}
	}
}

func TestThumbnailUnsupported(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	for _, typ := range []Type{
		{"image/x-icon", KindImage, ""},
		{"image/jxl", KindImage, ""},
		{"video/mp4", KindVideo, "mov"},
		{"text/plain; charset=utf-8", KindText, ""},
	} {
		path := putFile(t, "x", []byte("whatever"))
		if _, err := r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", typ.MIME, err)
		}
	}
}

func TestProbeAV(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{})
	av := cat(lavfi("testsrc2=s=320x240:r=24:d=2"), lavfi("sine=d=2"), "-shortest")
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast"}
	mp4 := gen(t, "v.mp4", cat(av, x264, "-c:a", "aac")...)
	rotated := gen(t, "r.mp4", "-display_rotation", "90", "-i", mp4, "-c", "copy")
	cover := putFile(t, "c.png", encode(t, quadrants(32, 32), encPNG))
	mp3 := gen(t, "a.mp3", cat(lavfi("sine=d=2"), "-i", cover, "-map", "0", "-map", "1", "-c:a", "libmp3lame",
		"-c:v", "png", "-disposition:v", "attached_pic")...)
	cases := []struct {
		name string
		path string
		want Info
	}{
		{"mp4", mp4, Info{Width: 320, Height: 240, HasVideo: true, HasAudio: true, VideoCodec: "h264", AudioCodec: "aac"}},
		{"rotated", rotated, Info{Width: 240, Height: 320, HasVideo: true, HasAudio: true, VideoCodec: "h264", AudioCodec: "aac"}},
		{"webm", gen(t, "v.webm", cat(av, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus")...),
			Info{Width: 320, Height: 240, HasVideo: true, HasAudio: true, VideoCodec: "vp9", AudioCodec: "opus"}},
		{"m4a", gen(t, "a.m4a", cat(lavfi("sine=d=2"), "-c:a", "aac")...), Info{HasAudio: true, AudioCodec: "aac"}},
		{"mp3 with cover art", mp3, Info{HasAudio: true, AudioCodec: "mp3"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := r.Probe(testCtx(t), c.path, sniffPath(t, c.path))
			if err != nil {
				t.Fatal(err)
			}
			if got.Duration < 1900*time.Millisecond || got.Duration > 2200*time.Millisecond {
				t.Errorf("Duration = %s", got.Duration)
			}
			got.Duration = 0
			if got != c.want {
				t.Errorf("Probe = %+v, want %+v", got, c.want)
			}
		})
	}
}
