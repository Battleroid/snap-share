package media

import "fmt"

// NewImageRunner returns a Runner for hosts without ffmpeg. Thumbnail,
// Poster and Probe work for the images Go decodes (JPEG, PNG, GIF, WebP,
// BMP, TIFF); anything that would start ffmpeg or ffprobe returns
// ErrUnsupported.
func NewImageRunner(cfg Config) *Runner {
	cfg = cfg.withDefaults()
	return &Runner{
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.MaxParallel),
		imgSem: make(chan struct{}, cfg.MaxParallel),
	}
}

// HasFFmpeg reports whether r can run ffmpeg and ffprobe (false for
// NewImageRunner).
func (r *Runner) HasFFmpeg() bool { return r.ffmpegBin != "" && r.ffprobeBin != "" }

var errNoFFmpeg = fmt.Errorf("%w: ffmpeg is not available", ErrUnsupported)
