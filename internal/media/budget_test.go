package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// flatJPEG builds a w×h JPEG with one scan that codes every coefficient as
// zero, so it is tiny at any size. comps are the components' sampling
// factors (0x11, 0x22, ...), ids 1, 2, 3, 4. adobe >= 0 adds an Adobe APP14
// segment with that colour transform (0: RGB/CMYK, 1: YCbCr, 2: YCCK).
func flatJPEG(w, h int, progressive bool, comps []byte, adobe int) []byte {
	seg := func(m byte, p []byte) []byte {
		return append([]byte{0xff, m, byte((len(p) + 2) >> 8), byte(len(p) + 2)}, p...)
	}
	b := []byte{0xff, 0xd8}
	if adobe >= 0 {
		b = append(b, seg(0xee, append([]byte("Adobe\x00\x64\x00\x00\x00\x00"), byte(adobe)))...)
	}
	b = append(b, seg(0xdb, append([]byte{0}, bytes.Repeat([]byte{1}, 64)...))...)
	sof := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(len(comps))}
	maxH, maxV := 1, 1
	for i, c := range comps {
		sof = append(sof, byte(i+1), c, 0)
		maxH, maxV = max(maxH, int(c>>4)), max(maxV, int(c&15))
	}
	marker := byte(0xc0)
	if progressive {
		marker = 0xc2
	}
	b = append(b, seg(marker, sof)...)
	// Table 0 has a single 1-bit code: category 0 (DC) / EOB (AC).
	b = append(b, seg(0xc4, append(append([]byte{0x00, 1}, make([]byte, 15)...), 0))...)
	b = append(b, seg(0xc4, append(append([]byte{0x10, 1}, make([]byte, 15)...), 0))...)
	sos := []byte{byte(len(comps))}
	for i := range comps {
		sos = append(sos, byte(i+1), 0x00)
	}
	bitsPerBlock := 2 // DC 0, then EOB
	if progressive {
		sos = append(sos, 0, 0, 0) // DC first scan
		bitsPerBlock = 1
	} else {
		sos = append(sos, 0, 63, 0)
	}
	b = append(b, seg(0xda, sos)...)
	var blocks int
	if len(comps) == 1 {
		blocks = ((w + 7) / 8) * ((h + 7) / 8)
	} else {
		mcus := ((w + 8*maxH - 1) / (8 * maxH)) * ((h + 8*maxV - 1) / (8 * maxV))
		for _, c := range comps {
			blocks += mcus * int(c>>4) * int(c&15)
		}
	}
	b = append(b, make([]byte, (blocks*bitsPerBlock+7)/8)...)
	return append(b, 0xff, 0xd9)
}

func encGIF(b *bytes.Buffer, m image.Image) error { return gif.Encode(b, m, nil) }

// TestDecodeCostJPEG checks the estimate against what image/jpeg allocates:
// never below it, and not far above.
func TestDecodeCostJPEG(t *testing.T) {
	const w, h = 1600, 1200
	cases := []struct {
		name        string
		progressive bool
		comps       []byte
		adobe       int
	}{
		{"grey", false, []byte{0x11}, -1},
		{"grey progressive, nominal 2x2", true, []byte{0x22}, -1},
		{"4:2:0", false, []byte{0x22, 0x11, 0x11}, -1},
		{"4:2:0 progressive", true, []byte{0x22, 0x11, 0x11}, -1},
		{"4:4:4 progressive", true, []byte{0x11, 0x11, 0x11}, -1},
		{"rgb", false, []byte{0x11, 0x11, 0x11}, 0},
		{"flex", false, []byte{0x22, 0x21, 0x12}, -1},
		{"cmyk progressive", true, []byte{0x11, 0x11, 0x11, 0x11}, 0},
		{"ycck", false, []byte{0x22, 0x11, 0x11, 0x22}, 2},
	}
	for _, c := range cases {
		data := flatJPEG(w, h, c.progressive, c.comps, c.adobe)
		l, err := readJPEGLayout(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var derr error
		alloc, _ := allocDuring(func() { _, derr = jpeg.Decode(bytes.NewReader(data)) })
		if derr != nil {
			t.Fatalf("%s: decode: %v", c.name, derr)
		}
		est := l.cost()
		t.Logf("%-32s estimate %4d MiB, allocated %4d MiB", c.name, est>>20, alloc>>20)
		if int64(alloc) > est+1<<20 {
			t.Errorf("%s: allocated %d bytes, estimate %d is too low", c.name, alloc, est)
		}
		if est > int64(alloc)*13/10+1<<20 {
			t.Errorf("%s: estimate %d is far above the %d allocated", c.name, est, alloc)
		}
	}
}

func TestDecodeCostOtherFormats(t *testing.T) {
	const w, h = 1000, 800
	rect := image.Rect(0, 0, w, h)
	opaque := image.NewNRGBA(rect)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 0xff
	}
	pal := image.NewPaletted(rect, palette.Plan9)
	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"png grey", "g.png", encode(t, image.NewGray(rect), encPNG)},
		{"png grey 16", "g16.png", encode(t, image.NewGray16(rect), encPNG)},
		{"png rgb", "rgb.png", encode(t, opaque, encPNG)},
		{"png rgba", "rgba.png", encode(t, image.NewNRGBA(rect), encPNG)},
		{"png rgba 16", "rgba16.png", encode(t, image.NewNRGBA64(rect), encPNG)},
		{"png paletted", "p.png", encode(t, pal, encPNG)},
		{"gif", "a.gif", encode(t, pal, encGIF)},
		{"bmp", "a.bmp", encode(t, opaque, encBMP)},
		{"tiff", "a.tiff", encode(t, opaque, encTIFF)},
		{"tiff 16", "a16.tiff", encode(t, image.NewRGBA64(rect), encTIFF)},
	}
	for _, c := range cases {
		path := putFile(t, c.file, c.data)
		typ := sniffPath(t, path)
		codec, ok := goCodec(typ.MIME)
		if !ok {
			t.Fatalf("%s: no codec for %s", c.name, typ.MIME)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := codec.config(codecReader(f, typ.MIME))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		est, err := decodeCost(codecReader(f, typ.MIME), typ.MIME, cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		alloc, _ := allocDuring(func() { _, err = codec.decode(codecReader(f, typ.MIME)) })
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s: decode: %v", c.name, err)
		}
		t.Logf("%-14s estimate %5.1f MiB, allocated %5.1f MiB", c.name, float64(est)/(1<<20), float64(alloc)/(1<<20))
		if int64(alloc) > est+1<<20 {
			t.Errorf("%s: allocated %d bytes, estimate %d is too low", c.name, alloc, est)
		}
	}
}

// TestScaleCost: a whole thumbnail (decode, CatmullRom scale, encode) stays
// within the estimate stillGo uses.
func TestScaleCost(t *testing.T) {
	for _, c := range []struct {
		w, h, o int
	}{{4000, 3000, 1}, {3000, 4000, 6}, {1000, 12000, 1}, {600, 400, 1}} {
		data := flatJPEG(c.w, c.h, false, []byte{0x22, 0x11, 0x11}, -1)
		if c.o != 1 {
			data = insertAfterSOI(data, segment(mAPP1, exifTIFF(false, uint16(c.o), false)))
		}
		path := putFile(t, "s.jpg", data)
		l, err := readJPEGLayout(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		est := l.cost() + scaleCost(c.w, c.h, c.o, 640)
		r := NewImageRunner(Config{DecodeMemory: 1 << 30})
		var th Thumb
		alloc, _ := allocDuring(func() { th, err = r.Thumbnail(testCtx(t), path, sniffPath(t, path), &bytes.Buffer{}) })
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%dx%d o=%d -> %dx%d: estimate %d MiB, allocated %d MiB", c.w, c.h, c.o, th.Width, th.Height, est>>20, alloc>>20)
		if int64(alloc) > est+4<<20 {
			t.Errorf("%dx%d: allocated %d MiB, estimate %d MiB", c.w, c.h, alloc>>20, est>>20)
		}
	}
}

// TestDecodeMemoryBudget: images whose decode estimate is over DecodeMemory
// are refused without ffmpeg, before anything large is allocated, while
// cheaper images of the same pixel count are decoded.
func TestDecodeMemoryBudget(t *testing.T) {
	const w, h = 2000, 2000
	rect := image.Rect(0, 0, w, h)
	r := NewImageRunner(Config{DecodeMemory: 64 << 20})
	cases := []struct {
		name string
		file string
		data []byte
		ok   bool
	}{
		// 4 MB decoded, 41 MB scaler buffer.
		{"grey png", "g.png", encode(t, image.NewGray(rect), encPNG), true},
		// 32 MB decoded: over the budget with the scaler.
		{"16-bit png", "rgba16.png", encode(t, image.NewNRGBA64(rect), encPNG), false},
		{"baseline 4:2:0 jpeg", "b.jpg", flatJPEG(w, h, false, []byte{0x22, 0x11, 0x11}, -1), true},
		// Coefficients alone are 64 MB.
		{"progressive cmyk jpeg", "p.jpg", flatJPEG(w, h, true, []byte{0x11, 0x11, 0x11, 0x11}, 0), false},
	}
	for _, c := range cases {
		path := putFile(t, c.file, c.data)
		typ := sniffPath(t, path)
		var err error
		alloc, _ := allocDuring(func() { _, err = r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}) })
		if c.ok {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var ob *overBudget
		if !errors.Is(err, ErrTooLarge) || !errors.As(err, &ob) {
			t.Errorf("%s: Thumbnail = %v, want an over-budget ErrTooLarge", c.name, err)
		}
		if alloc > 4<<20 {
			t.Errorf("%s: allocated %d MiB before refusing", c.name, alloc>>20)
		}
		if err := r.Poster(testCtx(t), path, typ, putFile(t, "p.jpg", nil)); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: Poster = %v, want ErrTooLarge", c.name, err)
		}
	}
}

// TestOverBudgetUsesFFmpeg: with ffmpeg, an image over the in-process
// budget is thumbnailed by ffmpeg (under RLIMIT_AS), oriented like a Go
// decode.
func TestOverBudgetUsesFFmpeg(t *testing.T) {
	r := newTestRunner(t, Config{DecodeMemory: 16 << 20})
	src := encode(t, quadrants(2000, 1000), encJPEG)
	R, G, B, W := red, green, blue, white
	for o, want := range map[uint16][4]color.Color{1: {R, G, B, W}, 6: {B, R, W, G}, 3: {W, B, G, R}} {
		path := putFile(t, "o.jpg", insertAfterSOI(src, segment(mAPP1, exifTIFF(false, o, false))))
		th, img := thumb(t, r, path)
		ww, wh := 640, 320
		if o >= 5 {
			ww, wh = 640, 1280
		}
		if th.MIME != "image/jpeg" || th.Width != ww || th.Height != wh {
			t.Errorf("orientation %d: thumb %+v", o, th)
		}
		for i, got := range quadColors(img) {
			if !near(got, want[i]) {
				t.Errorf("orientation %d quadrant %d = %v, want %v", o, i, got, want[i])
			}
		}
	}

	// The progressive CMYK bomb: in process it would need 445 MiB.
	path := putFile(t, "p.jpg", progressiveFlatJPEG(4000, 4000))
	typ := sniffPath(t, path)
	var th Thumb
	var err error
	alloc, _ := allocDuring(func() { th, err = newTestRunner(t, Config{}).Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}) })
	t.Logf("progressive cmyk 4000x4000 through ffmpeg: %+v, %v, %d MiB allocated in process", th, err, alloc>>20)
	if err != nil && !errors.Is(err, ErrTooLarge) {
		t.Errorf("Thumbnail = %v", err)
	}
	if alloc > 64<<20 {
		t.Errorf("allocated %d MiB in process", alloc>>20)
	}
}

// TestMemBudget: acquisitions wait for releases and give up with ctx.
func TestMemBudget(t *testing.T) {
	m := newMemBudget(100)
	ctx := t.Context()
	if err := m.acquire(ctx, 60); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := m.acquire(short, 50); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("over-budget acquire = %v, want DeadlineExceeded", err)
	}
	got := make(chan error, 1)
	go func() { got <- m.acquire(ctx, 50) }()
	select {
	case err := <-got:
		t.Fatalf("acquired before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	m.release(60)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	if m.used != 50 {
		t.Errorf("used = %d, want 50", m.used)
	}
}

// TestDecodesShareTheBudget: parallel thumbnails never hold more estimated
// memory than DecodeMemory at once.
func TestDecodesShareTheBudget(t *testing.T) {
	data := flatJPEG(2000, 1500, false, []byte{0x22, 0x11, 0x11}, -1) // about 35 MiB each
	path := putFile(t, "b.jpg", data)
	typ := sniffPath(t, path)
	r := NewImageRunner(Config{DecodeMemory: 80 << 20, MaxParallel: 8})
	var peak atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.mem.mu.Lock()
			if u := r.mem.used; u > peak.Load() {
				peak.Store(u)
			}
			r.mem.mu.Unlock()
		}
	}()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(stop)
	if p := peak.Load(); p > 80<<20 || p == 0 {
		t.Errorf("peak estimated memory %d MiB, budget 80 MiB", p>>20)
	}
	if r.mem.used != 0 {
		t.Errorf("%d bytes still held", r.mem.used)
	}
}

// vp8Header is a VP8 key frame header declaring w×h, enough for
// webp.DecodeConfig.
func vp8Header(w, h int) []byte {
	b := []byte{0x10, 0x00, 0x00, 0x9d, 0x01, 0x2a} // key frame, version 0, shown
	b = binary.LittleEndian.AppendUint16(b, uint16(w))
	b = binary.LittleEndian.AppendUint16(b, uint16(h))
	return append(b, make([]byte, 10)...)
}

// webpAnimFile is an animated WebP with a canvas and frames given as
// (rect, payload chunk).
func webpAnimFile(cw, ch int, frames ...func() (image.Rectangle, []byte)) []byte {
	vp8x := append([]byte{0x02, 0, 0, 0}, u24le(cw-1)...)
	vp8x = append(vp8x, u24le(ch-1)...)
	body := []byte("WEBP")
	body = append(body, riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("ANIM", make([]byte, 6))...)
	for _, fn := range frames {
		rect, payload := fn()
		anmf := append(u24le(rect.Min.X/2), u24le(rect.Min.Y/2)...)
		anmf = append(anmf, u24le(rect.Dx()-1)...)
		anmf = append(anmf, u24le(rect.Dy()-1)...)
		anmf = append(anmf, u24le(100)...)
		anmf = append(anmf, 0)
		body = append(body, riffChunk("ANMF", append(anmf, payload...))...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func TestWebPFrameChecks(t *testing.T) {
	r := NewImageRunner(Config{MaxPixels: 1_000_000})
	frame := func(x, y, w, h int, chunk []byte) func() (image.Rectangle, []byte) {
		return func() (image.Rectangle, []byte) { return image.Rect(x, y, x+w, y+h), chunk }
	}
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"lossless bitstream larger than its frame", webpFrameBomb(16, 4096, 4096, 1), ErrTooLarge},
		{"lossless bitstream over MaxPixels", webpFrameBomb(16, 16384, 16384, 1), ErrTooLarge},
		{"lossy bitstream larger than its frame", webpAnimFile(16, 16, frame(0, 0, 16, 16, riffChunk("VP8 ", vp8Header(16000, 16000)))), ErrTooLarge},
		{"lossless bitstream smaller than its frame", webpAnimFile(64, 64, frame(0, 0, 64, 64, riffChunk("VP8L", vp8lFlat(8, 8)))), errBadWebP},
		{"frame larger than the canvas", webpAnimFile(16, 16, frame(0, 0, 16, 16, riffChunk("VP8L", vp8lFlat(16, 16))),
			frame(0, 0, 900, 900, riffChunk("VP8L", vp8lFlat(900, 900)))), ErrTooLarge},
		{"frame off the canvas", webpAnimFile(16, 16, frame(8, 8, 16, 16, riffChunk("VP8L", vp8lFlat(16, 16)))), ErrTooLarge},
		{"fine", webpAnimFile(32, 16, frame(0, 0, 32, 16, riffChunk("VP8L", vp8lFlat(32, 16))),
			frame(16, 0, 16, 16, riffChunk("VP8L", vp8lFlat(16, 16)))), nil},
	}
	for _, c := range cases {
		path := putFile(t, "a.webp", c.data)
		typ := sniffPath(t, path)
		var err error
		alloc, _ := allocDuring(func() { _, err = r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}) })
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: Thumbnail = %v, want %v", c.name, err, c.want)
		}
		if alloc > 8<<20 {
			t.Errorf("%s: allocated %d MiB", c.name, alloc>>20)
		}
	}
}

// TestWebPPlayerCaps: a player stops at the frame and pixel caps, and with
// its context, instead of decoding a whole long animation.
func TestWebPPlayerCaps(t *testing.T) {
	frames := make([]func() (image.Rectangle, []byte), maxPlayFrames+5)
	for i := range frames {
		frames[i] = func() (image.Rectangle, []byte) { return image.Rect(0, 0, 4, 4), riffChunk("VP8L", vp8lFlat(4, 4)) }
	}
	data := webpAnimFile(4, 4, frames...)
	a, err := parseWebP(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	r := NewImageRunner(Config{})
	p := r.newWebPPlayer(bytes.NewReader(data), a)
	start := time.Now()
	if err := p.at(t.Context(), a.dur); !errors.Is(err, ErrTooLarge) {
		t.Errorf("playing %d frames: %v, want ErrTooLarge", len(frames), err)
	}
	if p.next != maxPlayFrames {
		t.Errorf("decoded %d frames, cap %d", p.next, maxPlayFrames)
	}
	t.Logf("%d frames in %s", p.next, time.Since(start))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p = r.newWebPPlayer(bytes.NewReader(data), a)
	if err := p.at(ctx, a.dur); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if p.next > 1 {
		t.Errorf("cancelled player decoded %d frames", p.next)
	}

	p = r.newWebPPlayer(bytes.NewReader(data), a)
	p.pixels = maxPlayPixels - 40 // two 4×4 frames left
	if err := p.at(t.Context(), a.dur); !errors.Is(err, ErrTooLarge) || p.next != 2 {
		t.Errorf("pixel cap: %v after %d frames", err, p.next)
	}
}
