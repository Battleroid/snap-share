package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// Info is what Probe learns about a file.
type Info struct {
	// Width and Height are the display size: after EXIF orientation for
	// JPEGs and rotation metadata for video. HEIF tile grids report the
	// whole grid.
	Width, Height int
	// Duration of video, audio and animated images; 0 for still images.
	Duration time.Duration
	// HasVideo is true for a video stream that is not cover art. Always
	// false for KindImage.
	HasVideo bool
	HasAudio bool
	// Animated is true for images with more than one frame (GIF, APNG,
	// WebP, AVIF sequences).
	Animated bool
	// Orientation is the EXIF orientation of a JPEG (1-8), 0 if absent.
	Orientation int
	// VideoCodec and AudioCodec are ffprobe codec names, e.g. "h264".
	VideoCodec, AudioCodec string
}

// Probe reads dimensions, duration and stream layout. Go-decodable images
// are read in process from their headers; video, audio, AVIF and HEIC go
// through ffprobe with the sniffed demuxer forced. path must be absolute.
func (r *Runner) Probe(ctx context.Context, path string, t Type) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if _, ok := goCodec(t.MIME); ok && t.Kind == KindImage {
		return probeImage(path, t)
	}
	switch t.Kind {
	case KindImage, KindVideo, KindAudio:
		if t.Container != "" {
			return r.ffprobe(ctx, path, t)
		}
	}
	return Info{}, fmt.Errorf("%w: cannot probe %s", ErrUnsupported, t.MIME)
}

func probeImage(path string, t Type) (Info, error) {
	c, _ := goCodec(t.MIME)
	f, err := os.Open(path) //nolint:gosec // server-generated blob path
	if err != nil {
		return Info{}, fmt.Errorf("media: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return Info{}, fmt.Errorf("media: %w", err)
	}
	cfg, err := c.config(bufio.NewReader(f))
	if err != nil {
		return Info{}, fmt.Errorf("media: %s header: %w", t.MIME, err)
	}
	info := Info{Width: cfg.Width, Height: cfg.Height}
	if _, err := f.Seek(0, 0); err != nil {
		return Info{}, fmt.Errorf("media: %w", err)
	}
	switch t.MIME {
	case "image/jpeg":
		info.Orientation = jpegOrientation(f)
		if info.Orientation >= 5 {
			info.Width, info.Height = info.Height, info.Width
		}
	case "image/gif":
		n, d := gifAnim(f)
		if n > 1 {
			info.Animated, info.Duration = true, d
		}
	case "image/png":
		if n, d := apngAnim(f, fi.Size()); n > 1 {
			info.Animated, info.Duration = true, d
		}
	case "image/webp":
		a, err := parseWebP(f, fi.Size())
		if err != nil {
			return Info{}, err
		}
		if a.animated && len(a.frames) > 1 {
			info.Animated, info.Duration = true, a.dur
		}
	}
	return info, nil
}

// probeMaxBytes bounds ffprobe's JSON output.
const probeMaxBytes = 1 << 20

// ffprobeShow limits ffprobe's output to the fields read below.
const ffprobeShow = "format=duration" +
	":stream=index,codec_type,codec_name,width,height,duration,nb_frames" +
	":stream_disposition=attached_pic:stream_side_data=rotation" +
	":stream_group=index,type:stream_group_component=width,height"

type ffprobeJSON struct {
	Streams []struct {
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Duration    string `json:"duration"`
		NbFrames    string `json:"nb_frames"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	StreamGroups []struct {
		Type       string `json:"type"`
		Components []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"components"`
	} `json:"stream_groups"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (r *Runner) ffprobe(ctx context.Context, path string, t Type) (Info, error) {
	var out bytes.Buffer
	err := r.run(ctx, job{
		tool:      toolFFprobe,
		inputs:    []input{{path: path, typ: t}},
		args:      []string{"-print_format", "json", "-show_entries", ffprobeShow},
		stdout:    &out,
		stdoutMax: probeMaxBytes,
	})
	if err != nil {
		return Info{}, err
	}
	var p ffprobeJSON
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		return Info{}, fmt.Errorf("media: ffprobe output: %w", err)
	}
	return p.info(t), nil
}

func (p *ffprobeJSON) info(t Type) Info {
	var info Info
	dur := parseSeconds(p.Format.Duration)
	frames := 0
	for _, s := range p.Streams {
		switch {
		case s.CodecType == "video" && s.Disposition.AttachedPic == 0 && !info.HasVideo:
			info.HasVideo, info.VideoCodec = true, s.CodecName
			info.Width, info.Height = s.Width, s.Height
			for _, sd := range s.SideData {
				if r := math.Mod(math.Abs(sd.Rotation), 180); r > 45 && r < 135 {
					info.Width, info.Height = info.Height, info.Width
				}
			}
			frames, _ = strconv.Atoi(s.NbFrames)
			dur = max(dur, parseSeconds(s.Duration))
		case s.CodecType == "audio" && !info.HasAudio:
			info.HasAudio, info.AudioCodec = true, s.CodecName
			dur = max(dur, parseSeconds(s.Duration))
		}
	}
	for _, g := range p.StreamGroups {
		if g.Type == "Tile Grid" && len(g.Components) > 0 {
			info.Width, info.Height = g.Components[0].Width, g.Components[0].Height
			break
		}
	}
	info.Duration = dur
	if t.Kind == KindImage {
		info.HasVideo = false
		info.Animated = frames > 1
		if !info.Animated {
			info.Duration = 0
		}
	}
	return info
}

func parseSeconds(s string) time.Duration {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || f > 1e9 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}
