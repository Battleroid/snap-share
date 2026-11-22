package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"sync"
)

// In-process decodes are bounded by memory, not only by pixel count: the
// same number of pixels costs 1 byte each for a grey baseline JPEG and over
// 20 for a progressive CMYK one. Before decoding, the header is read and
// the peak memory of the decoder plus the thumbnail scaler is estimated.
// An image whose estimate is over Config.DecodeMemory is never decoded in
// process (it goes to ffmpeg, which runs under RLIMIT_AS, or is refused);
// the others run only while the estimates of all running decodes together
// stay within DecodeMemory.

// memBudget is a weighted semaphore over bytes.
type memBudget struct {
	mu    sync.Mutex
	limit int64
	used  int64
	freed chan struct{} // closed (and replaced) whenever memory is released
}

func newMemBudget(limit int64) *memBudget {
	return &memBudget{limit: limit, freed: make(chan struct{})}
}

// acquire waits until n bytes fit, or ctx ends. n must not exceed the limit.
func (m *memBudget) acquire(ctx context.Context, n int64) error {
	for {
		m.mu.Lock()
		if m.used+n <= m.limit {
			m.used += n
			m.mu.Unlock()
			return nil
		}
		freed := m.freed
		m.mu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *memBudget) release(n int64) {
	m.mu.Lock()
	m.used -= n
	close(m.freed)
	m.freed = make(chan struct{})
	m.mu.Unlock()
}

// overBudget is returned (wrapping ErrTooLarge) when an image's estimated
// decode memory is over DecodeMemory. It carries what the ffmpeg fallback
// needs.
type overBudget struct {
	w, h        int // stored size
	orientation int
	animated    bool
	cost, limit int64
}

func (e *overBudget) Error() string {
	return fmt.Sprintf("%s: decoding needs about %d MiB, over the %d MiB in-process budget",
		ErrTooLarge, e.cost>>20, e.limit>>20)
}

func (e *overBudget) Unwrap() error { return ErrTooLarge }

// lockDecode takes a decode slot and cost bytes of the memory budget. The
// returned func releases both.
func (r *Runner) lockDecode(ctx context.Context, cost int64) (func(), error) {
	select {
	case r.imgSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := r.mem.acquire(ctx, cost); err != nil {
		<-r.imgSem
		return nil, err
	}
	return func() {
		r.mem.release(cost)
		<-r.imgSem
	}, nil
}

// scaleCost is the memory scaleOrient needs to turn a sw×sh image with EXIF
// orientation o into one at most maxW wide: the destination, the
// CatmullRom scaler's temporary buffer (4 float64 per destination column
// per source row) and the rotated copy.
func scaleCost(sw, sh, o, maxW int) int64 {
	swap := o >= 5 && o <= 8
	dw, dh := sw, sh
	if swap {
		dw, dh = sh, sw
	}
	tw, th := fitWidth(dw, dh, maxW)
	rw, rh := tw, th
	if swap {
		rw, rh = th, tw
	}
	n := 4 * int64(rw) * int64(rh)
	if rw != sw || rh != sh {
		n += 32 * int64(rw) * int64(sh)
	}
	if o >= 2 && o <= 8 {
		n += 4 * int64(rw) * int64(rh)
	}
	return n
}

// bytesPer returns the size of a decoded pixel in the given colour model.
func bytesPer(m color.Model) int64 {
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// decodeCost estimates the peak memory of decoding the image in r (positioned
// at the start) whose header gave cfg. It covers the decoder's own buffers,
// not the scaler (see scaleCost).
func decodeCost(r io.Reader, mime string, cfg image.Config) (int64, error) {
	px := int64(cfg.Width) * int64(cfg.Height)
	switch mime {
	case "image/jpeg":
		l, err := readJPEGLayout(r)
		if err != nil {
			return 0, err
		}
		return l.cost(), nil
	case "image/png":
		l, err := readPNGLayout(r)
		if err != nil {
			return 0, err
		}
		return l.cost(), nil
	case "image/gif":
		// The first frame (paletted, plus a copy when interlaced; frames
		// must lie within the screen) and the RGBA canvas it is drawn on.
		return 6 * px, nil
	case "image/bmp":
		return bytesPer(cfg.ColorModel) * px, nil
	case "image/tiff":
		// The image, plus one decompressed strip or tile, which
		// x/image/tiff allows to reach 8 bytes per pixel of the block; a
		// single strip can cover the whole image.
		return (bytesPer(cfg.ColorModel) + 8) * px, nil
	case "image/webp":
		return webpStillCost(cfg.ColorModel, px), nil
	}
	return 0, fmt.Errorf("%w: no decode estimate for %s", ErrUnsupported, mime)
}

// webpStillCost estimates x/image/webp: lossless (VP8L) decodes into 4
// bytes per pixel and the colour-indexing transform can add another 4;
// lossy (VP8) is YCbCr 4:2:0 on macroblock-aligned planes, with an alpha
// plane that may itself be VP8L-compressed.
func webpStillCost(m color.Model, px int64) int64 {
	switch m {
	case color.YCbCrModel:
		return 2 * px
	case color.NYCbCrAModel:
		return (2 + 1 + 8) * px
	}
	return 8 * px // NRGBA: lossless
}

// webpFrameCost is the worst case of one animation frame of px pixels:
// lossy with VP8L-compressed alpha.
func webpFrameCost(px int64) int64 { return webpStillCost(color.NYCbCrAModel, px) }

// jpegLayout is what image/jpeg allocates by: the frame header (SOF) and
// the markers that decide the output colour model.
type jpegLayout struct {
	w, h        int
	progressive bool
	comps       []jpegComp
	jfif        bool
	adobe       bool
	transform   byte // Adobe APP14 colour transform; 0 means RGB or CMYK
}

type jpegComp struct {
	id   byte
	h, v int
}

var errJPEGHeader = fmt.Errorf("%w: no supported jpeg frame header", ErrUnsupported)

// readJPEGLayout reads the segments before the first scan.
func readJPEGLayout(r io.Reader) (jpegLayout, error) {
	var l jpegLayout
	br := bufio.NewReader(r)
	if m, err := readMarker(br); err != nil || m != mSOI {
		return l, errBadJPEG
	}
	for {
		m, err := readMarker(br)
		if err != nil {
			return l, errBadJPEG
		}
		if m == mSOS || m == mEOI {
			break
		}
		if standalone(m) {
			continue
		}
		seg, err := readSegment(br)
		if err != nil {
			return l, errBadJPEG
		}
		switch {
		case m == 0xc0 || m == 0xc1 || m == 0xc2:
			if l.comps != nil {
				return l, errBadJPEG // image/jpeg refuses several frames too
			}
			if len(seg) < 6 {
				return l, errBadJPEG
			}
			l.progressive = m == 0xc2
			l.h, l.w = int(binary.BigEndian.Uint16(seg[1:])), int(binary.BigEndian.Uint16(seg[3:]))
			n := int(seg[5])
			if n != 1 && n != 3 && n != 4 || len(seg) < 6+3*n {
				return l, errBadJPEG
			}
			for i := range n {
				c := seg[6+3*i:]
				h, v := int(c[1]>>4), int(c[1]&0x0f)
				if h < 1 || h > 4 || v < 1 || v > 4 {
					return l, errBadJPEG
				}
				if n == 1 {
					h, v = 1, 1 // image/jpeg ignores the factors of a grey image
				}
				l.comps = append(l.comps, jpegComp{id: c[0], h: h, v: v})
			}
		case m >= 0xc3 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc:
			return l, errJPEGHeader // lossless, hierarchical, arithmetic
		case m == 0xe0 && bytes.HasPrefix(seg, []byte("JFIF\x00")):
			l.jfif = true
		case m == 0xee && len(seg) >= 12 && bytes.HasPrefix(seg, []byte("Adobe")):
			l.adobe, l.transform = true, seg[11]
		}
	}
	if l.comps == nil {
		return l, errJPEGHeader
	}
	return l, nil
}

// cost follows image/jpeg's allocations (Go 1.27): planes sized in whole
// MCUs (grey; YCbCr at its subsampling, or 4:4:4 for unusual factors; a
// separate K plane), a [64]int32 coefficient block per 8×8 block of every
// component for progressive images, and an RGBA/CMYK copy for RGB and
// 4-component images.
func (l jpegLayout) cost() int64 {
	maxH, maxV := 1, 1
	for _, c := range l.comps {
		maxH, maxV = max(maxH, c.h), max(maxV, c.v)
	}
	mxx := int64((l.w + 8*maxH - 1) / (8 * maxH))
	myy := int64((l.h + 8*maxV - 1) / (8 * maxV))
	full := 64 * mxx * int64(maxH) * myy * int64(maxV)
	samples := func(c jpegComp) int64 { return 64 * mxx * int64(c.h) * myy * int64(c.v) }
	out := 4 * int64(l.w) * int64(l.h)

	var n int64
	switch len(l.comps) {
	case 1:
		n = full
	default:
		c := l.comps
		flex := c[1].h != c[2].h || c[1].v != c[2].v || c[0].h != maxH || c[0].v != maxV
		if flex {
			n = 3 * full
		} else {
			n = full + samples(c[1]) + samples(c[2])
		}
		if len(c) == 4 {
			n += samples(c[3]) + out
		} else if l.rgb() {
			n += out
		}
	}
	if l.progressive {
		for _, c := range l.comps {
			n += 4 * samples(c) // 64 int32 per 64 samples
		}
	}
	return n
}

// rgb mirrors image/jpeg's isRGB for 3-component images.
func (l jpegLayout) rgb() bool {
	if l.jfif {
		return false
	}
	if l.adobe && l.transform == 0 {
		return true
	}
	c := l.comps
	return len(c) == 3 && c[0].id == 'R' && c[1].id == 'G' && c[2].id == 'B'
}

// pngLayout is the PNG header as far as image/png's allocations go.
type pngLayout struct {
	w, h       int64
	depth      byte
	colorType  byte
	interlaced bool
	trns       bool
}

var errBadPNG = errors.New("media: malformed png")

// readPNGLayout reads IHDR and the chunks before the first IDAT.
func readPNGLayout(r io.Reader) (pngLayout, error) {
	var l pngLayout
	br := bufio.NewReader(r)
	var sig [8]byte
	if _, err := io.ReadFull(br, sig[:]); err != nil || string(sig[:]) != "\x89PNG\r\n\x1a\n" {
		return l, errBadPNG
	}
	for first := true; ; first = false {
		var h [8]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			return l, errBadPNG
		}
		n := int64(binary.BigEndian.Uint32(h[:4]))
		typ := string(h[4:])
		if first != (typ == "IHDR") {
			return l, errBadPNG
		}
		switch typ {
		case "IHDR":
			var p [13]byte
			if n != 13 {
				return l, errBadPNG
			}
			if _, err := io.ReadFull(br, p[:]); err != nil {
				return l, errBadPNG
			}
			l.w, l.h = int64(binary.BigEndian.Uint32(p[0:])), int64(binary.BigEndian.Uint32(p[4:]))
			l.depth, l.colorType, l.interlaced = p[8], p[9], p[12] == 1
			n = 0
		case "tRNS":
			l.trns = true
		case "IDAT", "IEND":
			return l, nil
		}
		if _, err := br.Discard(int(n) + 4); err != nil { // data and CRC
			return l, errBadPNG
		}
	}
}

// cost is the decoded image (image/png picks Gray, Gray16, RGBA, RGBA64,
// NRGBA, NRGBA64 or Paletted from the colour type, bit depth and tRNS) and,
// for interlaced images, the seven pass images merged into it.
func (l pngLayout) cost() int64 {
	var bpp int64
	switch l.colorType {
	case 0: // grey
		bpp = 1
		if l.trns {
			bpp = 4
		}
	case 3: // paletted
		bpp = 1
	default: // RGB, grey+alpha, RGBA
		bpp = 4
	}
	if l.depth == 16 {
		bpp *= 2
		if l.colorType == 0 && !l.trns {
			bpp = 2
		}
	}
	n := bpp * l.w * l.h
	if l.interlaced {
		n *= 2
	}
	return n
}
