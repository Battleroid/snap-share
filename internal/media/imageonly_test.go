package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"path/filepath"
	"testing"
)

func TestImageRunner(t *testing.T) {
	t.Parallel()
	r := NewImageRunner(Config{})
	if r.HasFFmpeg() {
		t.Fatal("image runner claims ffmpeg")
	}
	gifPath := putFile(t, "a.gif", encodeGIF(t, gifFrames([]color.Color{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}, 64, 48, 10)))
	typ := sniffPath(t, gifPath)
	info, err := r.Probe(testCtx(t), gifPath, typ)
	if err != nil || !info.Animated || info.Width != 64 {
		t.Fatalf("Probe: %+v, %v", info, err)
	}
	var b bytes.Buffer
	if _, err := r.Thumbnail(testCtx(t), gifPath, typ, &b); err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if cfg, _, err := image.DecodeConfig(&b); err != nil || cfg.Width != 64 {
		t.Errorf("thumbnail %+v, %v", cfg, err)
	}
	dst := filepath.Join(t.TempDir(), "t.mp4")
	if err := r.Teaser(testCtx(t), gifPath, typ, info, dst); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Teaser without ffmpeg: %v, want ErrUnsupported", err)
	}
	mp4 := putFile(t, "v.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2\x00\x00\x00\x08free"))
	if _, err := r.Probe(testCtx(t), mp4, sniffPath(t, mp4)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Probe(mp4) without ffmpeg: %v, want ErrUnsupported", err)
	}
}
