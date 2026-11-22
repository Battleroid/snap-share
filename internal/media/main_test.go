package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// The Runner re-executes the test binary to apply rlimits.
	MaybeRunLimited()
	os.Exit(m.Run())
}

func lookTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s not found; run the tests with `make test-go`: %v", name, err)
	}
	return p
}

// gen writes a fixture with ffmpeg (not through the Runner) and returns its
// path.
func gen(t *testing.T, name string, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	full = append(full, out)
	cmd := exec.Command(lookTool(t, "ffmpeg"), full...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, b)
	}
	return out
}

func lavfi(src string) []string { return []string{"-f", "lavfi", "-i", src} }

func putFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestRunner(t *testing.T, cfg Config) *Runner {
	t.Helper()
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sniffPath(t *testing.T, path string) Type {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	typ, err := Sniff(f, fi.Size(), filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// probed is an independent ffprobe of an output file.
type probed struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		PixFmt    string `json:"pix_fmt"`
		FrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (p probed) duration() float64 {
	d, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return d
}

func probeFile(t *testing.T, path string) probed {
	t.Helper()
	out, err := exec.Command(lookTool(t, "ffprobe"), "-v", "error", "-print_format", "json",
		"-show_entries", "format=duration:stream=codec_type,codec_name,width,height,pix_fmt,avg_frame_rate",
		path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var p probed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// halvesJPEG encodes a w×h image whose left half is red and right half blue.
func halvesJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// insertAfterSOI inserts marker segments right after a JPEG's SOI.
func insertAfterSOI(j []byte, segs ...[]byte) []byte {
	out := append([]byte(nil), j[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, j[2:]...)
}

func segment(marker byte, payload []byte) []byte {
	s := []byte{0xff, marker}
	s = binary.BigEndian.AppendUint16(s, uint16(len(payload)+2))
	return append(s, payload...)
}

// exifTIFF builds an Exif block with IFD0 = {Orientation, GPSInfo} and a GPS
// IFD with a latitude.
func exifTIFF(littleEndian bool, orientation uint16, gps bool) []byte {
	var bo binary.AppendByteOrder = binary.BigEndian
	b := []byte("MM")
	if littleEndian {
		bo, b = binary.LittleEndian, []byte("II")
	}
	b = bo.AppendUint16(b, 42)
	b = bo.AppendUint32(b, 8)
	n := uint16(1)
	if gps {
		n = 2
	}
	b = bo.AppendUint16(b, n)
	// Orientation, SHORT, 1
	b = bo.AppendUint16(b, 0x0112)
	b = bo.AppendUint16(b, 3)
	b = bo.AppendUint32(b, 1)
	b = bo.AppendUint16(b, orientation)
	b = append(b, 0, 0)
	gpsIFD := uint32(8 + 2 + 12*int(n) + 4)
	if gps {
		// GPSInfo, LONG, 1, offset
		b = bo.AppendUint16(b, 0x8825)
		b = bo.AppendUint16(b, 4)
		b = bo.AppendUint32(b, 1)
		b = bo.AppendUint32(b, gpsIFD)
	}
	b = bo.AppendUint32(b, 0) // no IFD1
	if gps {
		rationals := gpsIFD + 2 + 2*12 + 4
		b = bo.AppendUint16(b, 2)
		// GPSLatitudeRef, ASCII, 2, "N"
		b = bo.AppendUint16(b, 0x0001)
		b = bo.AppendUint16(b, 2)
		b = bo.AppendUint32(b, 2)
		b = append(b, 'N', 0, 0, 0)
		// GPSLatitude, RATIONAL, 3, offset
		b = bo.AppendUint16(b, 0x0002)
		b = bo.AppendUint16(b, 5)
		b = bo.AppendUint32(b, 3)
		b = bo.AppendUint32(b, rationals)
		b = bo.AppendUint32(b, 0)
		for _, v := range []uint32{52, 1, 31, 1, 1234, 100} {
			b = bo.AppendUint32(b, v)
		}
	}
	return append([]byte("Exif\x00\x00"), b...)
}
