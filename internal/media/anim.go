package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Frame delays at or under 10ms play at 100ms in browsers; do the same.
func frameDelay(d time.Duration) time.Duration {
	if d <= 10*time.Millisecond {
		return 100 * time.Millisecond
	}
	return d
}

// maxFrames bounds the frame tables built from animated images.
const maxFrames = 100_000

// gifAnim walks GIF blocks without decoding pixels and returns the frame
// count and total duration. A truncated file yields what was read.
func gifAnim(r io.Reader) (frames int, dur time.Duration) {
	br := bufio.NewReader(r)
	var hdr [13]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return 0, 0
	}
	skip := func(n int) bool { _, err := br.Discard(n); return err == nil }
	if hdr[10]&0x80 != 0 && !skip(3<<((hdr[10]&7)+1)) {
		return 0, 0
	}
	subBlocks := func() bool {
		for {
			n, err := br.ReadByte()
			if err != nil {
				return false
			}
			if n == 0 {
				return true
			}
			if !skip(int(n)) {
				return false
			}
		}
	}
	delay := 0
	for frames < maxFrames {
		b, err := br.ReadByte()
		if err != nil {
			return frames, dur
		}
		switch b {
		case 0x21: // extension
			label, err := br.ReadByte()
			if err != nil {
				return frames, dur
			}
			if label == 0xf9 { // graphic control: size 4, flags, delay (LE, 1/100 s)
				gce, err := br.Peek(4)
				if err != nil || gce[0] != 4 {
					return frames, dur
				}
				delay = int(binary.LittleEndian.Uint16(gce[2:]))
			}
			if !subBlocks() {
				return frames, dur
			}
		case 0x2c: // image descriptor
			var desc [9]byte
			if _, err := io.ReadFull(br, desc[:]); err != nil {
				return frames, dur
			}
			if desc[8]&0x80 != 0 && !skip(3<<((desc[8]&7)+1)) {
				return frames, dur
			}
			if !skip(1) || !subBlocks() { // LZW minimum code size, data
				return frames, dur
			}
			frames++
			dur += frameDelay(time.Duration(delay) * 10 * time.Millisecond)
			delay = 0
		default: // 0x3b trailer, or garbage
			return frames, dur
		}
	}
	return frames, dur
}

// apngAnim reads acTL/fcTL chunks of a PNG and returns the animation frame
// count and total duration. Plain PNGs return 0 frames.
func apngAnim(r io.ReaderAt, size int64) (frames int, dur time.Duration) {
	var h [8]byte
	for off := int64(8); off+12 <= size && frames < maxFrames; {
		if _, err := r.ReadAt(h[:], off); err != nil {
			break
		}
		n := int64(binary.BigEndian.Uint32(h[:]))
		switch string(h[4:]) {
		case "fcTL":
			var fc [26]byte
			if n < 26 {
				return frames, dur
			}
			if _, err := r.ReadAt(fc[:], off+8); err != nil {
				return frames, dur
			}
			num, den := binary.BigEndian.Uint16(fc[20:]), binary.BigEndian.Uint16(fc[22:])
			if den == 0 {
				den = 100
			}
			frames++
			dur += frameDelay(time.Duration(num) * time.Second / time.Duration(den))
		case "IEND":
			return frames, dur
		}
		off += 12 + n
	}
	return frames, dur
}

// webpAnim is the frame table of a WebP file.
type webpAnim struct {
	w, h     int // canvas
	animated bool
	frames   []webpFrame
	dur      time.Duration
}

type webpFrame struct {
	rect    image.Rectangle // on the canvas
	dur     time.Duration
	blend   bool // alpha-blend over the canvas; otherwise replace
	dispose bool // clear the frame's rectangle after it is shown
	off, n  int64
	// wm1, hm1 are the raw 24-bit width-1 and height-1 fields, reused for
	// the VP8X header of the rewrapped frame.
	wm1, hm1 [3]byte
}

var errBadWebP = errors.New("media: malformed webp")

// parseWebP reads the RIFF chunk headers of a WebP file. For still images
// it fills only the canvas size (from VP8X if present).
func parseWebP(r io.ReaderAt, size int64) (webpAnim, error) {
	var a webpAnim
	var h [12]byte
	if size < 12 {
		return a, errBadWebP
	}
	if _, err := r.ReadAt(h[:], 0); err != nil {
		return a, errBadWebP
	}
	if string(h[:4]) != "RIFF" || string(h[8:]) != "WEBP" {
		return a, errBadWebP
	}
	end := min(size, 8+int64(binary.LittleEndian.Uint32(h[4:])))
	u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	for off := int64(12); off+8 <= end; {
		if _, err := r.ReadAt(h[:8], off); err != nil {
			return a, errBadWebP
		}
		n := int64(binary.LittleEndian.Uint32(h[4:8]))
		data := off + 8
		if data+n > end {
			return a, errBadWebP
		}
		switch string(h[:4]) {
		case "VP8X":
			var p [10]byte
			if n < 10 {
				return a, errBadWebP
			}
			if _, err := r.ReadAt(p[:], data); err != nil {
				return a, errBadWebP
			}
			a.animated = p[0]&0x02 != 0
			a.w, a.h = 1+u24(p[4:]), 1+u24(p[7:])
		case "ANMF":
			var p [16]byte
			if n < 16 || len(a.frames) >= maxFrames {
				return a, errBadWebP
			}
			if _, err := r.ReadAt(p[:], data); err != nil {
				return a, errBadWebP
			}
			x, y := 2*u24(p[0:]), 2*u24(p[3:])
			f := webpFrame{
				rect:    image.Rect(x, y, x+1+u24(p[6:]), y+1+u24(p[9:])),
				dur:     frameDelay(time.Duration(u24(p[12:])) * time.Millisecond),
				blend:   p[15]&0x02 == 0,
				dispose: p[15]&0x01 != 0,
				off:     data + 16,
				n:       n - 16,
				wm1:     [3]byte(p[6:9]),
				hm1:     [3]byte(p[9:12]),
			}
			// Frames must lie on the canvas (the WebP container spec says
			// so, and libwebp refuses others). The canvas is what the pixel
			// limit was checked against.
			if !f.rect.In(image.Rect(0, 0, a.w, a.h)) {
				return a, fmt.Errorf("%w: webp frame %v is outside the %dx%d canvas", ErrTooLarge, f.rect, a.w, a.h)
			}
			a.frames = append(a.frames, f)
			a.dur += f.dur
		}
		off = data + n + n&1
	}
	if a.animated && len(a.frames) == 0 {
		return a, errBadWebP
	}
	return a, nil
}

// maxFrameBytes bounds one ANMF frame's compressed data.
const maxFrameBytes = 64 << 20

// One player decodes at most this many frames, and this many pixels summed
// over its frames. The canvas and each frame are within MaxPixels; these
// bound the work of a long animation of large frames.
const (
	maxPlayFrames = 10_000
	maxPlayPixels = 2_000_000_000
)

// decodeWebPFrame decodes one ANMF frame by rewrapping its ALPH/VP8/VP8L
// sub-chunks as a still WebP for x/image/webp. Lossless and plain lossy
// frames get no VP8X header, so x/image/webp does not compare the
// bitstream's size with the frame's; the header is read first and must
// match the frame rectangle (which parseWebP keeps on the canvas).
func (r *Runner) decodeWebPFrame(src io.ReaderAt, f webpFrame) (image.Image, error) {
	if f.n > maxFrameBytes {
		return nil, fmt.Errorf("%w: webp frame data", ErrTooLarge)
	}
	buf := make([]byte, f.n)
	if _, err := src.ReadAt(buf, f.off); err != nil {
		return nil, errBadWebP
	}
	var alph, bits []byte
	var lossless bool
	for p := buf; len(p) >= 8; {
		n := int(binary.LittleEndian.Uint32(p[4:8]))
		if n < 0 || n > len(p)-8 {
			return nil, errBadWebP
		}
		chunk := p[:8+n]
		switch string(p[:4]) {
		case "ALPH":
			alph = chunk
		case "VP8 ":
			bits = chunk
		case "VP8L":
			bits, lossless = chunk, true
		}
		p = p[min(len(p), 8+n+n&1):]
	}
	if bits == nil {
		return nil, errBadWebP
	}
	var body bytes.Buffer
	body.WriteString("RIFF\x00\x00\x00\x00WEBP")
	if alph != nil && !lossless {
		var x [18]byte
		copy(x[:], "VP8X")
		binary.LittleEndian.PutUint32(x[4:], 10)
		x[8] = 0x10 // alpha
		copy(x[12:15], f.wm1[:])
		copy(x[15:18], f.hm1[:])
		body.Write(x[:])
		body.Write(alph)
		if len(alph)&1 == 1 {
			body.WriteByte(0)
		}
	}
	body.Write(bits)
	data := body.Bytes()
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8)) //nolint:gosec // bounded by maxFrameBytes

	cfg, err := webp.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("media: webp frame: %w", err)
	}
	if err := r.checkPixels(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	if fw, fh := f.rect.Dx(), f.rect.Dy(); cfg.Width != fw || cfg.Height != fh {
		if cfg.Width > fw || cfg.Height > fh {
			return nil, fmt.Errorf("%w: webp frame bitstream is %dx%d, its frame %dx%d", ErrTooLarge, cfg.Width, cfg.Height, fw, fh)
		}
		return nil, fmt.Errorf("%w: frame bitstream is %dx%d, its frame %dx%d", errBadWebP, cfg.Width, cfg.Height, fw, fh)
	}
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("media: webp frame: %w", err)
	}
	if b := img.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return nil, fmt.Errorf("%w: frame decoded to %v, header %dx%d", errBadWebP, b, cfg.Width, cfg.Height)
	}
	return img, nil
}

// webpPlayer composites animated WebP frames onto a transparent canvas.
type webpPlayer struct {
	r       *Runner
	src     io.ReaderAt
	a       webpAnim
	canvas  *image.RGBA
	next    int             // index of the next frame to render
	cleared image.Rectangle // area to dispose before the next frame
	endsAt  time.Duration   // when the frame on the canvas is replaced
	pixels  int64           // decoded so far, summed over frames
}

func (r *Runner) newWebPPlayer(src io.ReaderAt, a webpAnim) *webpPlayer {
	return &webpPlayer{r: r, src: src, a: a, canvas: image.NewRGBA(image.Rect(0, 0, a.w, a.h))}
}

// step renders the next frame. It returns io.EOF after the last one.
func (p *webpPlayer) step() error {
	if p.next >= len(p.a.frames) {
		return io.EOF
	}
	f := p.a.frames[p.next]
	area := int64(f.rect.Dx()) * int64(f.rect.Dy())
	if p.next >= maxPlayFrames || p.pixels+area > maxPlayPixels {
		return fmt.Errorf("%w: webp animation is over %d frames or %d decoded pixels", ErrTooLarge, maxPlayFrames, int64(maxPlayPixels))
	}
	p.pixels += area
	if !p.cleared.Empty() {
		draw.Draw(p.canvas, p.cleared, image.Transparent, image.Point{}, draw.Src)
		p.cleared = image.Rectangle{}
	}
	img, err := p.r.decodeWebPFrame(p.src, f)
	if err != nil {
		return err
	}
	rect := f.rect.Intersect(p.canvas.Rect)
	op := draw.Src
	if f.blend {
		op = draw.Over
	}
	draw.Draw(p.canvas, rect, img, img.Bounds().Min, op)
	if f.dispose {
		p.cleared = rect
	}
	p.endsAt += f.dur
	p.next++
	return nil
}

// at advances until the canvas shows the frame visible at time t. It stops
// with ctx's error between frames.
func (p *webpPlayer) at(ctx context.Context, t time.Duration) error {
	if p.next == 0 {
		if err := p.step(); err != nil {
			return err
		}
	}
	for t >= p.endsAt && p.next < len(p.a.frames) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.step(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
