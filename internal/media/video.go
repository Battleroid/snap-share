package media

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// Teaser shape.
const (
	teaserMax = 8 * time.Second
	teaserSeg = 1500 * time.Millisecond
	teaserFPS = 24
)

// Poster writes a JPEG frame of a video, or the first frame of an image, to
// dst (absolute path), at most PosterWidth wide. Videos use the frame at
// 10% of the duration, or the first frame for clips under 2 s.
// Transparency is flattened onto black, as in the teaser.
func (r *Runner) Poster(ctx context.Context, src string, t Type, dst string) error {
	if err := checkPath(dst); err != nil {
		return err
	}
	var (
		img    *image.RGBA
		opaque bool
		err    error
	)
	switch {
	case t.Kind == KindImage:
		img, opaque, err = r.still(ctx, src, t, r.cfg.PosterWidth)
	case t.Kind == KindVideo && demuxers[t.Container]:
		img, opaque, err = r.videoPoster(ctx, src, t)
	default:
		err = fmt.Errorf("%w: no poster for %s", ErrUnsupported, t.MIME)
	}
	if err != nil {
		return err
	}
	if !opaque {
		// Pixels are premultiplied, so opaque alpha is "over black".
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
	}
	return writeFile(dst, func(w io.Writer) error {
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 82})
	})
}

func (r *Runner) videoPoster(ctx context.Context, src string, t Type) (*image.RGBA, bool, error) {
	info, err := r.ffprobe(ctx, src, t)
	if err != nil {
		return nil, false, err
	}
	if !info.HasVideo {
		return nil, false, fmt.Errorf("%w: no video stream", ErrUnsupported)
	}
	at := time.Duration(0)
	if info.Duration >= 2*time.Second {
		at = info.Duration / 10
	}
	img, opaque, err := r.stillFFmpeg(ctx, src, t, info, r.cfg.PosterWidth, at)
	if err != nil && at > 0 && ctx.Err() == nil && !errors.Is(err, ErrTimeout) {
		// The container may claim more duration than it has frames for.
		img, opaque, err = r.stillFFmpeg(ctx, src, t, info, r.cfg.PosterWidth, 0)
	}
	return img, opaque, err
}

// writeFile writes dst through fn and removes it on error.
func writeFile(dst string, fn func(io.Writer) error) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // server-generated path
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	err = fn(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("media: write %s: %w", dst, err)
	}
	return nil
}

// Teaser writes a short muted H.264 MP4 preview of a video or animated
// image to dst (absolute path), using info from Probe.
//
// Clips of 8 s or more give five 1.5 s segments starting at 10, 30, 50, 70
// and 90% of the duration, joined with the concat filter; shorter clips are
// used whole. Output is at most TeaserWidth wide (even dimensions), 24 fps,
// yuv420p, libx264 CRF 30 veryfast with a rate cap derived from
// TeaserMaxBytes, no audio, +faststart, and at most 8 s. A result over
// TeaserMaxBytes is deleted and ErrTooLarge returned.
//
// Animated WebP is composited in Go (ffmpeg 8.1 cannot decode it) and fed
// to ffmpeg as raw frames.
func (r *Runner) Teaser(ctx context.Context, src string, t Type, info Info, dst string) error {
	if err := checkPath(dst); err != nil {
		return err
	}
	var err error
	switch {
	case t.Kind == KindImage && !info.Animated:
		err = fmt.Errorf("%w: still image", ErrUnsupported)
	case t.Kind == KindImage && t.MIME == "image/webp":
		err = r.teaserWebP(ctx, src, dst)
	case t.Kind == KindImage && demuxers[t.Container]:
		err = r.teaserFFmpeg(ctx, src, t, info, dst, true)
	case t.Kind == KindVideo && demuxers[t.Container] && info.HasVideo:
		err = r.teaserFFmpeg(ctx, src, t, info, dst, false)
	default:
		err = fmt.Errorf("%w: no teaser for %s", ErrUnsupported, t.MIME)
	}
	if err == nil {
		err = r.checkTeaser(dst)
	}
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func (r *Runner) checkTeaser(dst string) error {
	fi, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("media: teaser: %w", err)
	}
	switch {
	case fi.Size() == 0:
		return errors.New("media: teaser is empty")
	case fi.Size() > r.cfg.TeaserMaxBytes:
		return fmt.Errorf("%w: teaser is %d bytes, cap %d", ErrTooLarge, fi.Size(), r.cfg.TeaserMaxBytes)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return fmt.Errorf("media: teaser: %w", err)
	}
	return nil
}

type span struct{ start, length time.Duration }

// teaserSpans picks the sampled segments. Unknown or short durations use
// the clip from the start, capped at teaserMax.
func teaserSpans(d time.Duration) []span {
	if d < teaserMax {
		if d <= 0 {
			d = teaserMax
		}
		return []span{{0, d}}
	}
	points := [...]float64{0.1, 0.3, 0.5, 0.7, 0.9}
	spans := make([]span, len(points))
	for i, p := range points {
		s := time.Duration(p * float64(d))
		spans[i] = span{min(s, d-teaserSeg), teaserSeg}
	}
	return spans
}

// teaserSize returns even output dimensions at most maxW wide for a w×h
// display size, or 0, 0 if unknown.
func teaserSize(w, h, maxW int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	tw := max(2, min(maxW, w)&^1)
	th := max(2, int(math.Round(float64(h)*float64(tw)/float64(w)/2))*2)
	return tw, th
}

// teaserEncode is the output side shared by every teaser.
func (r *Runner) teaserEncode() []string {
	// 80% of the byte cap spread over the maximum duration, as a VBV cap
	// on top of CRF; most clips never reach it.
	rate := strconv.FormatInt(r.cfg.TeaserMaxBytes*8/int64(teaserMax/time.Second)*8/10, 10)
	return []string{
		"-an", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "30",
		"-maxrate", rate, "-bufsize", rate,
		"-pix_fmt", "yuv420p", "-movflags", "+faststart",
		"-t", seconds(teaserMax),
		"-fs", strconv.FormatInt(r.cfg.TeaserMaxBytes+1, 10),
		"-f", "mp4",
	}
}

func (r *Runner) teaserFFmpeg(ctx context.Context, src string, t Type, info Info, dst string, alpha bool) error {
	scale := fmt.Sprintf("scale=w='max(2,trunc(min(%d,iw)/2)*2)':h=-2", r.cfg.TeaserWidth)
	if tw, th := teaserSize(info.Width, info.Height, r.cfg.TeaserWidth); tw > 0 {
		// Fixed size, so segments always match for concat.
		scale = fmt.Sprintf("scale=%d:%d", tw, th)
	}
	// setpts first: a segment's first frame rarely lands exactly on its -ss
	// point, and an offset fps grid leaves one-frame gaps between segments.
	chain := fmt.Sprintf("setpts=PTS-STARTPTS,fps=%d,%s:flags=bicubic,setsar=1,format=yuv420p", teaserFPS, scale)
	if alpha {
		chain = "format=rgba,premultiply=inplace=1," + chain
	}
	// 0:V is the first video stream that is not cover art.
	spans := teaserSpans(info.Duration)
	var inputs []input
	var args []string
	if len(spans) == 1 {
		inputs = []input{{path: src, typ: t}}
		args = []string{"-map", "0:V:0", "-vf", chain}
	} else {
		var fc, labels strings.Builder
		for i, s := range spans {
			inputs = append(inputs, input{
				path: src, typ: t,
				pre: []string{"-ss", seconds(s.start), "-t", seconds(s.length)},
			})
			fmt.Fprintf(&fc, "[%d:V:0]%s[v%d];", i, chain, i)
			fmt.Fprintf(&labels, "[v%d]", i)
		}
		fmt.Fprintf(&fc, "%sconcat=n=%d:v=1:a=0[out]", labels.String(), len(spans))
		args = []string{"-filter_complex", fc.String(), "-map", "[out]"}
	}
	return r.run(ctx, job{
		tool:   toolFFmpeg,
		inputs: inputs,
		args:   append(args, r.teaserEncode()...),
		output: dst,
	})
}

// teaserWebP composites an animated WebP in Go and pipes RGBA frames at
// 24 fps into ffmpeg's rawvideo demuxer.
func (r *Runner) teaserWebP(ctx context.Context, src, dst string) error {
	select {
	case r.imgSem <- struct{}{}:
		defer func() { <-r.imgSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	f, err := os.Open(src) //nolint:gosec // server-generated blob path
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	a, err := parseWebP(f, fi.Size())
	if err != nil {
		return err
	}
	if !a.animated {
		return fmt.Errorf("%w: still image", ErrUnsupported)
	}
	if err := r.checkPixels(a.w, a.h); err != nil {
		return err
	}
	tw, th := teaserSize(a.w, a.h, r.cfg.TeaserWidth)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := webpFrames(ctx, f, a, teaserSpans(a.dur), tw, th, pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	err = r.run(ctx, job{
		tool:   toolFFmpeg,
		inputs: []input{{raw: &rawVideo{w: tw, h: th, fps: teaserFPS, r: pr}}},
		args:   append([]string{"-map", "0:v:0", "-vf", "setsar=1,format=yuv420p"}, r.teaserEncode()...),
		output: dst,
	})
	_ = pr.CloseWithError(io.ErrClosedPipe) // unblock the producer if ffmpeg quit early
	if perr := <-done; perr != nil && err == nil {
		// ffmpeg saw EOF and finished a truncated clip.
		err = perr
	}
	return err
}

// webpFrames writes tw×th RGBA frames for the spans to w.
func webpFrames(ctx context.Context, r io.ReaderAt, a webpAnim, spans []span, tw, th int, w io.Writer) error {
	p := newWebPPlayer(r, a)
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	shown := -1
	for _, s := range spans {
		n := int(math.Round(s.length.Seconds() * teaserFPS))
		for k := range n {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := p.at(s.start + time.Duration(k)*time.Second/teaserFPS); err != nil {
				return err
			}
			if p.next != shown {
				draw.BiLinear.Scale(out, out.Rect, p.canvas, p.canvas.Rect, draw.Src, nil)
				shown = p.next
			}
			if _, err := w.Write(out.Pix); err != nil {
				return err
			}
		}
	}
	return nil
}
