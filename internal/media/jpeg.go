package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// JPEG markers used here.
const (
	mSOI  = 0xd8
	mEOI  = 0xd9
	mSOS  = 0xda
	mAPP1 = 0xe1
	mAPP2 = 0xe2
	mAPPD = 0xed
	mTEM  = 0x01
)

var (
	sigExif   = []byte("Exif\x00\x00")
	sigXMP    = []byte("http://ns.adobe.com/xap/1.0/\x00")
	sigXMPExt = []byte("http://ns.adobe.com/xmp/extension/\x00")
	sigPS     = []byte("Photoshop 3.0\x00")
	sigMPF    = []byte("MPF\x00")
)

var errBadJPEG = errors.New("media: malformed jpeg")

// readMarker reads 0xFF, any fill bytes, and the marker code.
func readMarker(br *bufio.Reader) (byte, error) {
	b, err := br.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != 0xff {
		return 0, errBadJPEG
	}
	for b == 0xff {
		if b, err = br.ReadByte(); err != nil {
			return 0, err
		}
	}
	if b == 0 {
		return 0, errBadJPEG
	}
	return b, nil
}

// readSegment reads a marker segment's length and payload.
func readSegment(br *bufio.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(br, l[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(l[:]))
	if n < 2 {
		return nil, errBadJPEG
	}
	seg := make([]byte, n-2)
	if _, err := io.ReadFull(br, seg); err != nil {
		return nil, err
	}
	return seg, nil
}

func standalone(m byte) bool {
	return m == mSOI || m == mEOI || m == mTEM || (m >= 0xd0 && m <= 0xd7)
}

// jpegOrientation returns the EXIF orientation (1-8) from the segments
// before the first scan, or 0 if there is none.
func jpegOrientation(r io.Reader) int {
	br := bufio.NewReader(r)
	if m, err := readMarker(br); err != nil || m != mSOI {
		return 0
	}
	for {
		m, err := readMarker(br)
		if err != nil || m == mSOS || m == mEOI {
			return 0
		}
		if standalone(m) {
			continue
		}
		seg, err := readSegment(br)
		if err != nil {
			return 0
		}
		if m == mAPP1 && bytes.HasPrefix(seg, sigExif) {
			return exifOrientation(seg[len(sigExif):])
		}
	}
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF-structured EXIF
// block. It returns 0 if absent or invalid.
func exifOrientation(tiff []byte) int {
	return int(exifOrientationTag(tiff))
}

func exifOrientationTag(tiff []byte) uint16 {
	if len(tiff) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return 0
	}
	ifd := int64(bo.Uint32(tiff[4:]))
	if ifd+2 > int64(len(tiff)) {
		return 0
	}
	n := int64(bo.Uint16(tiff[ifd:]))
	for i := range n {
		e := ifd + 2 + 12*i
		if e+12 > int64(len(tiff)) {
			return 0
		}
		if bo.Uint16(tiff[e:]) != 0x0112 {
			continue
		}
		if bo.Uint16(tiff[e+2:]) != 3 || bo.Uint32(tiff[e+4:]) < 1 { // SHORT
			return 0
		}
		if v := bo.Uint16(tiff[e+8:]); v >= 1 && v <= 8 {
			return v
		}
		return 0
	}
	return 0
}

// orientationAPP1 is a minimal Exif APP1 payload holding only the
// orientation tag: big-endian TIFF header, IFD0 with one SHORT entry.
func orientationAPP1(o uint16) []byte {
	b := append([]byte(nil), sigExif...)
	b = append(b, "MM\x00\x2a\x00\x00\x00\x08"...) // header, IFD0 at 8
	b = append(b, 0x00, 0x01)                      // one entry
	b = append(b, 0x01, 0x12, 0x00, 0x03)          // tag 0x0112, SHORT
	b = append(b, 0x00, 0x00, 0x00, 0x01)          // count 1
	b = binary.BigEndian.AppendUint16(b, o)        // value
	b = append(b, 0, 0)                            // padding
	b = append(b, 0, 0, 0, 0)                      // no next IFD
	return b
}

// StripJPEGLocation copies a JPEG from r to w without location metadata.
// It is lossless: scan data and every other segment are copied byte for
// byte; nothing is decoded or re-encoded.
//
// Removed: all Exif APP1 segments (GPS lives in an Exif sub-IFD), XMP and
// extended XMP APP1, Photoshop APP13 (IPTC location fields), MPF APP2, and
// anything after the end-of-image marker (MPF secondary images and motion
// photo videos carry their own metadata). If the Exif had an orientation, a
// minimal Exif APP1 holding only that tag takes its place. Other Exif
// fields (camera, date, colour space hints) are lost with it.
//
// Input that does not start with a JPEG SOI returns ErrUnsupported. On a
// malformed JPEG the output is incomplete and an error is returned, so
// callers should write to a temporary file and keep the original on error.
func StripJPEGLocation(r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64<<10)
	if soi, err := br.Peek(3); err != nil || !bytes.Equal(soi, []byte{0xff, mSOI, 0xff}) {
		return fmt.Errorf("%w: not a jpeg", ErrUnsupported)
	}
	if err := stripJPEG(br, w); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("media: strip jpeg location: %w", err)
	}
	return nil
}

func stripJPEG(br *bufio.Reader, w io.Writer) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	// Payloads come from readSegment or orientationAPP1, so the length
	// always fits the 16-bit field.
	writeSeg := func(m byte, payload []byte) {
		hdr := binary.BigEndian.AppendUint16([]byte{0xff, m}, uint16(len(payload)+2)) //nolint:gosec // see above
		_, _ = bw.Write(hdr)
		_, _ = bw.Write(payload)
	}
	if m, err := readMarker(br); err != nil || m != mSOI {
		return errBadJPEG
	}
	_, _ = bw.Write([]byte{0xff, mSOI})
	exifSeen := false
	m, err := readMarker(br)
	for err == nil {
		switch {
		case m == mEOI:
			_, _ = bw.Write([]byte{0xff, mEOI})
			return bw.Flush() // trailing data is dropped
		case m == mSOI:
			return errBadJPEG
		case standalone(m):
			_, _ = bw.Write([]byte{0xff, m})
			m, err = readMarker(br)
			continue
		}
		var seg []byte
		if seg, err = readSegment(br); err != nil {
			return err
		}
		switch {
		case m == mAPP1 && bytes.HasPrefix(seg, sigExif):
			if !exifSeen {
				exifSeen = true
				if o := exifOrientationTag(seg[len(sigExif):]); o > 1 {
					writeSeg(mAPP1, orientationAPP1(o))
				}
			}
		case m == mAPP1 && (bytes.HasPrefix(seg, sigXMP) || bytes.HasPrefix(seg, sigXMPExt)):
		case m == mAPPD && bytes.HasPrefix(seg, sigPS):
		case m == mAPP2 && bytes.HasPrefix(seg, sigMPF):
		default:
			writeSeg(m, seg)
		}
		if m == mSOS {
			m, err = copyScan(br, bw)
			continue
		}
		m, err = readMarker(br)
	}
	return err
}

// copyScan copies entropy-coded data up to the next marker that is not a
// stuffed 0xFF00 or a restart marker, and returns that marker.
func copyScan(br *bufio.Reader, bw *bufio.Writer) (byte, error) {
	for {
		data, err := br.ReadSlice(0xff)
		if len(data) > 0 {
			_, _ = bw.Write(data[:len(data)-1])
		}
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				// ReadSlice stopped without finding 0xFF; data was all scan bytes.
				_, _ = bw.Write(data[len(data)-1:])
				continue
			}
			return 0, err
		}
		next, err := br.ReadByte()
		for err == nil && next == 0xff { // fill bytes
			next, err = br.ReadByte()
		}
		if err != nil {
			return 0, err
		}
		if next == 0x00 || (next >= 0xd0 && next <= 0xd7) {
			_, _ = bw.Write([]byte{0xff, next})
			continue
		}
		return next, nil
	}
}
