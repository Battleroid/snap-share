package media

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"slices"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"
)

// codec is an in-process image decoder.
type codec struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

// goCodec returns the Go decoder for a sniffed MIME type.
func goCodec(mime string) (codec, bool) {
	switch mime {
	case "image/jpeg":
		return codec{jpeg.DecodeConfig, jpeg.Decode}, true
	case "image/png":
		return codec{png.DecodeConfig, png.Decode}, true
	case "image/gif":
		return codec{gif.DecodeConfig, gif.Decode}, true
	case "image/webp":
		return codec{webp.DecodeConfig, webp.Decode}, true
	case "image/bmp":
		return codec{bmp.DecodeConfig, bmp.Decode}, true
	case "image/tiff":
		return codec{tiff.DecodeConfig, tiff.Decode}, true
	}
	return codec{}, false
}

// Thumb describes a thumbnail written by Thumbnail.
type Thumb struct {
	// MIME is "image/jpeg", or "image/png" when the source has transparency.
	MIME          string
	Width, Height int
}

// Thumbnail writes a thumbnail of the image at src (an absolute path) to w:
// the first frame, EXIF orientation applied, at most ThumbWidth wide and
// never upscaled. Opaque images become JPEG (quality 82); images with
// transparency become PNG.
//
// JPEG, PNG, GIF, WebP (including animated), BMP and TIFF are decoded in
// process after a header check against MaxPixels. AVIF and HEIC go through
// ffmpeg with the mov demuxer forced. Other kinds return ErrUnsupported.
func (r *Runner) Thumbnail(ctx context.Context, src string, t Type, w io.Writer) (Thumb, error) {
	img, opaque, err := r.still(ctx, src, t, r.cfg.ThumbWidth)
	if err != nil {
		return Thumb{}, err
	}
	th := Thumb{MIME: "image/jpeg", Width: img.Rect.Dx(), Height: img.Rect.Dy()}
	if opaque {
		err = jpeg.Encode(w, img, &jpeg.Options{Quality: 82})
	} else {
		th.MIME = "image/png"
		err = png.Encode(w, img)
	}
	if err != nil {
		return Thumb{}, fmt.Errorf("media: encode thumbnail: %w", err)
	}
	return th, nil
}

// still returns the first frame of an image, oriented and scaled to at most
// maxW wide, and whether the source is opaque.
func (r *Runner) still(ctx context.Context, path string, t Type, maxW int) (*image.RGBA, bool, error) {
	if t.Kind != KindImage {
		return nil, false, fmt.Errorf("%w: %s is not an image", ErrUnsupported, t.MIME)
	}
	if _, ok := goCodec(t.MIME); ok {
		img, opaque, err := r.stillGo(ctx, path, t, maxW)
		var ob *overBudget
		if errors.As(err, &ob) && !ob.animated && r.HasFFmpeg() {
			return r.stillLarge(ctx, path, t, ob, maxW)
		}
		return img, opaque, err
	}
	if t.Container == "mov" { // AVIF, HEIC
		info, err := r.ffprobe(ctx, path, t)
		if err != nil {
			return nil, false, err
		}
		if err := r.checkPixels(info.Width, info.Height); err != nil {
			return nil, false, err
		}
		tw, th := fitWidth(info.Width, info.Height, maxW)
		return r.stillFFmpeg(ctx, path, t, nil, tw, th, 0)
	}
	return nil, false, fmt.Errorf("%w: no decoder for %s", ErrUnsupported, t.MIME)
}

// imagePipes are the ffmpeg demuxers for images Go decodes, used for those
// whose decode would need more memory than DecodeMemory. They read the one
// input file and nothing else. GIF and APNG already have a Container.
var imagePipes = map[string]string{
	"image/jpeg": "jpeg_pipe",
	"image/png":  "png_pipe",
	"image/bmp":  "bmp_pipe",
	"image/tiff": "tiff_pipe",
	"image/webp": "webp_pipe", // still images only: ffmpeg 8.1 cannot decode animated WebP
}

// stillLarge decodes an image that is over the in-process memory budget
// with ffmpeg, which runs under RLIMIT_AS (MemLimit). ffmpeg scales it to
// the stored orientation; EXIF orientation is applied here, as for Go
// decodes.
func (r *Runner) stillLarge(ctx context.Context, path string, t Type, ob *overBudget, maxW int) (*image.RGBA, bool, error) {
	if t.Container == "" {
		t.Container = imagePipes[t.MIME]
	}
	if t.Container == "" {
		return nil, false, ob
	}
	swap := ob.orientation >= 5 && ob.orientation <= 8
	dw, dh := ob.w, ob.h
	if swap {
		dw, dh = dh, dw
	}
	tw, th := fitWidth(dw, dh, maxW)
	if swap {
		tw, th = th, tw
	}
	img, opaque, err := r.stillFFmpeg(ctx, path, t, []string{"-noautorotate"}, tw, th, 0)
	if err != nil {
		return nil, false, err
	}
	return orient(img, ob.orientation), opaque, nil
}

func (r *Runner) checkPixels(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("media: image has no pixels (%dx%d)", w, h)
	}
	if int64(w)*int64(h) > r.cfg.MaxPixels {
		return fmt.Errorf("%w: %dx%d is over %d pixels", ErrTooLarge, w, h, r.cfg.MaxPixels)
	}
	return nil
}

// codecReader is what a Go decoder reads f through. x/image/tiff reads a
// ReaderAt directly but buffers any other reader in memory up to the
// offsets the file points at, so it gets the file itself.
func codecReader(f *os.File, mime string) io.Reader {
	if mime == "image/tiff" {
		return f
	}
	return bufio.NewReader(f)
}

func (r *Runner) stillGo(ctx context.Context, path string, t Type, maxW int) (*image.RGBA, bool, error) {
	c, _ := goCodec(t.MIME)
	f, err := os.Open(path) //nolint:gosec // server-generated blob path
	if err != nil {
		return nil, false, fmt.Errorf("media: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("media: %w", err)
	}
	cfg, err := c.config(codecReader(f, t.MIME))
	if err != nil {
		return nil, false, fmt.Errorf("media: %s header: %w", t.MIME, err)
	}
	if err := r.checkPixels(cfg.Width, cfg.Height); err != nil {
		return nil, false, err
	}
	rewind := func() (io.Reader, error) {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("media: %w", err)
		}
		return codecReader(f, t.MIME), nil
	}

	// Estimate the memory before taking any.
	orientation := 1
	var anim *webpAnim
	var cost int64
	switch t.MIME {
	case "image/jpeg":
		rd, err := rewind()
		if err != nil {
			return nil, false, err
		}
		orientation = jpegOrientation(rd)
	case "image/webp":
		a, err := parseWebP(f, fi.Size())
		if err != nil {
			return nil, false, err
		}
		if a.animated {
			anim = &a
			// The canvas and the first frame (its data and its decode).
			f0 := a.frames[0]
			cost = 4*int64(a.w)*int64(a.h) + min(f0.n, maxFrameBytes) + webpFrameCost(int64(f0.rect.Dx())*int64(f0.rect.Dy()))
		}
	}
	if anim == nil {
		rd, err := rewind()
		if err != nil {
			return nil, false, err
		}
		if cost, err = decodeCost(rd, t.MIME, cfg); err != nil {
			return nil, false, err
		}
	}
	cost += scaleCost(cfg.Width, cfg.Height, orientation, maxW)
	if cost > r.cfg.DecodeMemory {
		return nil, false, &overBudget{w: cfg.Width, h: cfg.Height, orientation: orientation, animated: anim != nil,
			cost: cost, limit: r.cfg.DecodeMemory}
	}
	unlock, err := r.lockDecode(ctx, cost)
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	var img image.Image
	switch {
	case anim != nil:
		p := r.newWebPPlayer(f, *anim)
		if err := p.step(); err != nil {
			return nil, false, err
		}
		img = p.canvas
	case t.MIME == "image/gif":
		rd, err := rewind()
		if err != nil {
			return nil, false, err
		}
		m, err := c.decode(rd) // first frame only
		if err != nil {
			return nil, false, fmt.Errorf("media: decode %s: %w", t.MIME, err)
		}
		canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
		draw.Draw(canvas, m.Bounds(), m, m.Bounds().Min, draw.Src)
		img = canvas
	default:
		rd, err := rewind()
		if err != nil {
			return nil, false, err
		}
		if img, err = c.decode(rd); err != nil {
			return nil, false, fmt.Errorf("media: decode %s: %w", t.MIME, err)
		}
		if b := img.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
			return nil, false, fmt.Errorf("media: decode %s: %v does not match the %dx%d header", t.MIME, b, cfg.Width, cfg.Height)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	opaque := isOpaque(img)
	return scaleOrient(img, orientation, maxW, opaque), opaque, nil
}

// stillMaxBytes bounds the PNG frame ffmpeg writes to stdout.
const stillMaxBytes = 32 << 20

// stillFFmpeg grabs one frame at offset at, scaled by ffmpeg to tw×th, as
// RGBA. pre are extra input options. ffmpeg applies rotation metadata
// itself unless pre turns it off.
//
// Scaling uses the output options -s/-pix_fmt rather than -vf: for HEIF
// tile grids ffmpeg composes the tiles in a filtergraph it derives from the
// stream group, and -vf cannot be combined with that. Images therefore rely
// on default stream selection (which picks the grid); video maps the first
// video stream that is not cover art.
func (r *Runner) stillFFmpeg(ctx context.Context, path string, t Type, pre []string, tw, th int, at time.Duration) (*image.RGBA, bool, error) {
	if tw <= 0 || th <= 0 {
		return nil, false, fmt.Errorf("media: unknown frame size")
	}
	// The frame is decoded here: NRGBA, an RGBA copy and possibly a rotated
	// one, besides the PNG (at most stillMaxBytes).
	cost := 12 * int64(tw) * int64(th)
	if cost > r.cfg.DecodeMemory {
		return nil, false, fmt.Errorf("%w: a %dx%d frame needs about %d MiB, over the %d MiB in-process budget",
			ErrTooLarge, tw, th, cost>>20, r.cfg.DecodeMemory>>20)
	}
	var args []string
	if at > 0 {
		pre = append(slices.Clip(pre), "-ss", seconds(at))
	}
	if t.Kind != KindImage {
		args = []string{"-map", "0:V:0"}
	}
	args = append(args, "-frames:v", "1", "-an", "-sn", "-dn", "-map_metadata", "-1",
		"-s", fmt.Sprintf("%dx%d", tw, th), "-pix_fmt", "rgba", "-c:v", "png", "-f", "image2pipe")
	var out bytes.Buffer
	err := r.run(ctx, job{
		tool:      toolFFmpeg,
		inputs:    []input{{path: path, typ: t, pre: pre}},
		args:      args,
		output:    "-",
		stdout:    &out,
		stdoutMax: stillMaxBytes,
	})
	if err != nil {
		return nil, false, err
	}
	if out.Len() == 0 {
		return nil, false, fmt.Errorf("media: ffmpeg produced no frame")
	}
	unlock, err := r.lockDecode(ctx, min(cost+int64(out.Len()), r.cfg.DecodeMemory))
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	cfg, err := png.DecodeConfig(bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, false, fmt.Errorf("media: decode ffmpeg frame: %w", err)
	}
	if cfg.Width != tw || cfg.Height != th {
		return nil, false, fmt.Errorf("media: ffmpeg frame is %dx%d, want %dx%d", cfg.Width, cfg.Height, tw, th)
	}
	img, err := png.Decode(&out)
	if err != nil {
		return nil, false, fmt.Errorf("media: decode ffmpeg frame: %w", err)
	}
	opaque := isOpaque(img)
	return scaleOrient(img, 1, tw, opaque), opaque, nil
}

// fitWidth scales w×h down to at most maxW wide, keeping the aspect ratio.
// It returns 0, 0 for an unknown size.
func fitWidth(w, h, maxW int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	tw := min(maxW, w)
	return tw, max(1, int(math.Round(float64(h)*float64(tw)/float64(w))))
}

func seconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}

func isOpaque(img image.Image) bool {
	o, ok := img.(interface{ Opaque() bool })
	return ok && o.Opaque()
}

// scaleOrient scales img so that, after EXIF orientation o is applied, it
// is at most maxW wide, then applies o. Opaque sources get alpha forced to
// 255 so resampling rounding cannot make them look transparent.
func scaleOrient(img image.Image, o, maxW int, opaque bool) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
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
	dst := image.NewRGBA(image.Rect(0, 0, rw, rh))
	if rw == sw && rh == sh {
		draw.Draw(dst, dst.Rect, img, b.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Rect, img, b, draw.Src, nil)
	}
	if opaque {
		for i := 3; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = 0xff
		}
	}
	return orient(dst, o)
}

// orient applies EXIF orientation o (2-8) to m.
func orient(m *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return m
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2: // mirror horizontal
				sx, sy = w-1-x, y
			case 3: // rotate 180
				sx, sy = w-1-x, h-1-y
			case 4: // mirror vertical
				sx, sy = x, h-1-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 clockwise
				sx, sy = y, h-1-x
			case 7: // transverse
				sx, sy = w-1-y, h-1-x
			case 8: // rotate 90 counter-clockwise
				sx, sy = w-1-y, x
			}
			copy(out.Pix[out.PixOffset(x, y):][:4], m.Pix[m.PixOffset(sx, sy):][:4])
		}
	}
	return out
}
