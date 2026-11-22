package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"testing"
)

// headerSegments returns the marker segments before the first scan.
func headerSegments(t *testing.T, j []byte) (markers []byte, payloads [][]byte) {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(j))
	if m, err := readMarker(br); err != nil || m != mSOI {
		t.Fatalf("no SOI: %v", err)
	}
	for {
		m, err := readMarker(br)
		if err != nil {
			t.Fatal(err)
		}
		if m == mSOS {
			return markers, payloads
		}
		seg, err := readSegment(br)
		if err != nil {
			t.Fatal(err)
		}
		markers, payloads = append(markers, m), append(payloads, seg)
	}
}

// ifd0Tags lists the tags in IFD0 of an Exif payload.
func ifd0Tags(t *testing.T, exif []byte) []uint16 {
	t.Helper()
	tiff := exif[len(sigExif):]
	var bo binary.ByteOrder = binary.BigEndian
	if string(tiff[:2]) == "II" {
		bo = binary.LittleEndian
	}
	ifd := bo.Uint32(tiff[4:])
	n := int(bo.Uint16(tiff[ifd:]))
	var tags []uint16
	for i := range n {
		tags = append(tags, bo.Uint16(tiff[int(ifd)+2+12*i:]))
	}
	return tags
}

func samePixels(t *testing.T, a, b []byte) {
	t.Helper()
	ia, err := jpeg.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	ib, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("stripped JPEG does not decode: %v", err)
	}
	ya, ok1 := ia.(*image.YCbCr)
	yb, ok2 := ib.(*image.YCbCr)
	if !ok1 || !ok2 || ya.Rect != yb.Rect || !bytes.Equal(ya.Y, yb.Y) || !bytes.Equal(ya.Cb, yb.Cb) || !bytes.Equal(ya.Cr, yb.Cr) {
		t.Fatal("decoded pixels differ")
	}
}

func withSig(sig []byte, rest string) []byte {
	return append(append([]byte(nil), sig...), rest...)
}

func TestStripJPEGLocation(t *testing.T) {
	t.Parallel()
	ffjpeg, err := os.ReadFile(gen(t, "ff.jpg", cat(lavfi("testsrc2=s=160x120"), "-frames:v", "1")...))
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{"go": halvesJPEG(t, 64, 32), "ffmpeg": ffjpeg}
	for name, base := range sources {
		for _, le := range []bool{true, false} {
			jfif := segment(0xe0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00"))
			icc := segment(mAPP2, withSig([]byte("ICC_PROFILE\x00"), "\x01\x01profile"))
			com := segment(0xfe, []byte("keep this comment"))
			in := insertAfterSOI(base,
				jfif,
				segment(mAPP1, exifTIFF(le, 6, true)),
				segment(mAPP1, withSig(sigXMP, `<x:xmpmeta><rdf:Description exif:GPSLatitude="52,31.2N"/></x:xmpmeta>`)),
				segment(mAPP1, withSig(sigXMPExt, "GPSLongitude 4,53.9E")),
				segment(mAPPD, withSig(sigPS, "8BIM\x04\x04 City: Amsterdam")),
				icc,
				segment(mAPP2, withSig(sigMPF, "MM\x00\x2a\x00\x00\x00\x08")),
				com,
			)
			in = append(in, "motion photo trailer with GPSLatitude"...)

			var out bytes.Buffer
			if err := StripJPEGLocation(bytes.NewReader(in), &out); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			o := out.Bytes()
			for _, needle := range []string{"GPS", "Amsterdam", "xap/1.0", "xmp/extension", "Photoshop", "MPF\x00", "trailer"} {
				if bytes.Contains(o, []byte(needle)) {
					t.Errorf("%s: output still contains %q", name, needle)
				}
			}
			for _, keep := range [][]byte{jfif, icc, com} {
				if !bytes.Contains(o, keep) {
					t.Errorf("%s: dropped a segment it should keep: %q", name, keep[:8])
				}
			}
			var exifs [][]byte
			markers, payloads := headerSegments(t, o)
			for i, m := range markers {
				if m == mAPP1 && bytes.HasPrefix(payloads[i], sigExif) {
					exifs = append(exifs, payloads[i])
				}
			}
			if len(exifs) != 1 {
				t.Fatalf("%s: %d Exif segments", name, len(exifs))
			}
			if tags := ifd0Tags(t, exifs[0]); len(tags) != 1 || tags[0] != 0x0112 {
				t.Errorf("%s: IFD0 tags %#x, want only orientation", name, tags)
			}
			if o := jpegOrientation(bytes.NewReader(o)); o != 6 {
				t.Errorf("%s: orientation %d, want 6", name, o)
			}
			// Scan data is copied byte for byte.
			scan := base[bytes.Index(base, []byte{0xff, mSOS}):]
			if !bytes.HasSuffix(o, scan) {
				t.Errorf("%s: scan data changed", name)
			}
			samePixels(t, base, o)
		}
	}
}

func TestStripJPEGLocationNoMetadata(t *testing.T) {
	t.Parallel()
	base := halvesJPEG(t, 32, 32)
	for _, in := range [][]byte{
		base,
		insertAfterSOI(base, segment(mAPP1, exifTIFF(false, 1, false))), // orientation 1 needs no tag
	} {
		var out bytes.Buffer
		if err := StripJPEGLocation(bytes.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), base) {
			t.Error("output differs from the metadata-free original")
		}
	}
}

func TestStripJPEGLocationScanMarkers(t *testing.T) {
	t.Parallel()
	sos := segment(mSOS, []byte{1, 1, 0, 0, 63, 0})
	dht := segment(0xc4, []byte{0x10, 1, 2, 3})
	scan1 := []byte{0x12, 0xff, 0x00, 0x34, 0xff, 0xd0, 0x56, 0xff, 0xd1, 0x78}
	scan2 := []byte{0x9a, 0xff, 0x00}
	var in, want []byte
	in = append(in, 0xff, mSOI)
	in = append(in, sos...)
	in = append(in, scan1...)
	in = append(in, 0xff, 0xff) // fill bytes before a marker
	in = append(in, dht...)
	in = append(in, sos...)
	in = append(in, scan2...)
	in = append(in, 0xff, mEOI)
	in = append(in, "junk"...)
	want = append(want, 0xff, mSOI)
	want = append(want, sos...)
	want = append(want, scan1...)
	want = append(want, dht...)
	want = append(want, sos...)
	want = append(want, scan2...)
	want = append(want, 0xff, mEOI)
	var out bytes.Buffer
	if err := StripJPEGLocation(bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("got  % x\nwant % x", out.Bytes(), want)
	}
}

func TestStripJPEGLocationBadInput(t *testing.T) {
	t.Parallel()
	png := encode(t, quadrants(8, 8), encPNG)
	for name, in := range map[string][]byte{"png": png, "empty": nil, "soi only": {0xff, 0xd8}} {
		if err := StripJPEGLocation(bytes.NewReader(in), &bytes.Buffer{}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
	base := halvesJPEG(t, 64, 64)
	for name, in := range map[string][]byte{
		"truncated scan":    base[:len(base)/2],
		"truncated segment": base[:6],
		"garbage marker":    append([]byte{0xff, 0xd8, 0xff, 0x00}, base[2:]...),
	} {
		err := StripJPEGLocation(bytes.NewReader(in), &bytes.Buffer{})
		if err == nil || errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v, want a malformed-JPEG error", name, err)
		}
	}
}
