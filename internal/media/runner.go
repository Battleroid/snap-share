package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config sets the binaries and limits of a Runner. Zero values take the
// defaults noted on each field.
type Config struct {
	// FFmpeg and FFprobe are binary names or paths, resolved through PATH by
	// NewRunner. Defaults "ffmpeg" and "ffprobe".
	FFmpeg  string
	FFprobe string

	// Timeout is the wall-clock limit per ffmpeg/ffprobe invocation. The
	// whole process group is killed when it expires. Default 60s.
	Timeout time.Duration
	// MemLimit is RLIMIT_AS in bytes for each child. ffmpeg 8.1 on Alpine
	// needs roughly 200 MiB of address space just to load. Default 1 GiB.
	MemLimit uint64
	// CPULimit is RLIMIT_CPU for each child, counted across its threads and
	// rounded up to whole seconds. Default 120s.
	CPULimit time.Duration
	// Threads bounds decoder, filter and encoder threads per invocation.
	// Default 2.
	Threads int
	// MaxParallel bounds concurrent ffmpeg/ffprobe children, and separately
	// concurrent in-process image decodes. Default 2.
	MaxParallel int

	// MaxPixels rejects images (and animated image canvases) larger than
	// this many pixels before decoding. Default 100 million.
	MaxPixels int64
	// ThumbWidth and PosterWidth are maximum output widths; smaller inputs
	// are not upscaled. Default 640.
	ThumbWidth  int
	PosterWidth int
	// TeaserWidth is the maximum teaser width, rounded down to even.
	// Default 480.
	TeaserWidth int
	// TeaserMaxBytes fails a teaser that comes out larger. Default 1 MiB.
	TeaserMaxBytes int64
}

func (c Config) withDefaults() Config {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	if c.FFprobe == "" {
		c.FFprobe = "ffprobe"
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
	if c.MemLimit == 0 {
		c.MemLimit = 1 << 30
	}
	if c.CPULimit <= 0 {
		c.CPULimit = 120 * time.Second
	}
	def(&c.Threads, 2)
	def(&c.MaxParallel, 2)
	def(&c.ThumbWidth, 640)
	def(&c.PosterWidth, 640)
	def(&c.TeaserWidth, 480)
	if c.MaxPixels <= 0 {
		c.MaxPixels = 100_000_000
	}
	if c.TeaserMaxBytes <= 0 {
		c.TeaserMaxBytes = 1 << 20
	}
	return c
}

// Runner runs the media operations. It is safe for concurrent use.
type Runner struct {
	cfg        Config
	ffmpegBin  string // absolute paths
	ffprobeBin string
	self       string // re-exec target, see MaybeRunLimited
	sem        chan struct{}
	imgSem     chan struct{}
}

// NewRunner resolves the binaries and the re-exec target. It fails if
// ffmpeg or ffprobe cannot be found.
func NewRunner(cfg Config) (*Runner, error) {
	cfg = cfg.withDefaults()
	r := &Runner{
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.MaxParallel),
		imgSem: make(chan struct{}, cfg.MaxParallel),
	}
	var err error
	if r.ffmpegBin, err = lookAbs(cfg.FFmpeg); err != nil {
		return nil, err
	}
	if r.ffprobeBin, err = lookAbs(cfg.FFprobe); err != nil {
		return nil, err
	}
	if r.self, err = os.Executable(); err != nil {
		return nil, fmt.Errorf("media: locate own executable: %w", err)
	}
	return r, nil
}

func lookAbs(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("media: %w", err)
	}
	if p, err = filepath.Abs(p); err != nil {
		return "", fmt.Errorf("media: %w", err)
	}
	return p, nil
}

type tool string

const (
	toolFFmpeg  tool = "ffmpeg"
	toolFFprobe tool = "ffprobe"
)

// demuxers are the containers ffmpeg may be forced to. None of them can
// reference other files once the mov options below are set. Playlist and
// script demuxers (hls, concat, sdp, image2, ...) are deliberately absent.
var demuxers = map[string]bool{
	"mov": true, "matroska": true, "avi": true, "mpegts": true, "mpeg": true,
	"flv": true, "asf": true, "ogg": true, "mp3": true, "flac": true,
	"wav": true, "aac": true, "aiff": true, "gif": true, "apng": true,
}

// input is one ffmpeg input: a sniffed file, or raw RGBA frames that this
// package generates and feeds on stdin.
type input struct {
	path string
	typ  Type
	pre  []string // options placed before the hardened flags, e.g. -ss/-t
	raw  *rawVideo
}

type rawVideo struct {
	w, h, fps int
	r         io.Reader
}

// job is one ffmpeg or ffprobe invocation.
type job struct {
	tool   tool
	inputs []input
	args   []string // output options (ffmpeg) or print options (ffprobe)
	// output is an absolute path, or "-" for stdout. ffmpeg only.
	output    string
	stdout    io.Writer
	stdoutMax int64
}

// args builds the full argument list. Every input gets the forced demuxer,
// the format and protocol whitelists, and (mov) external references off.
func (r *Runner) args(j job) ([]string, io.Reader, error) {
	threads := strconv.Itoa(r.cfg.Threads)
	var a []string
	switch j.tool {
	case toolFFmpeg:
		a = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-nostats",
			"-filter_threads", threads, "-filter_complex_threads", threads}
	case toolFFprobe:
		if len(j.inputs) != 1 || j.output != "" {
			return nil, nil, errors.New("media: ffprobe takes one input and no output")
		}
		a = []string{"-hide_banner", "-loglevel", "error"}
	default:
		return nil, nil, fmt.Errorf("media: unknown tool %q", j.tool)
	}
	var stdin io.Reader
	for _, in := range j.inputs {
		a = append(a, in.pre...)
		if in.raw != nil {
			if j.tool != toolFFmpeg || stdin != nil {
				return nil, nil, errors.New("media: one raw input, ffmpeg only")
			}
			stdin = in.raw.r
			a = append(a, "-f", "rawvideo", "-format_whitelist", "rawvideo", "-protocol_whitelist", "pipe",
				"-pixel_format", "rgba", "-video_size", fmt.Sprintf("%dx%d", in.raw.w, in.raw.h),
				"-framerate", strconv.Itoa(in.raw.fps), "-i", "pipe:0")
			continue
		}
		demux := in.typ.Container
		if !demuxers[demux] {
			return nil, nil, fmt.Errorf("%w: no ffmpeg demuxer for %q", ErrUnsupported, in.typ.MIME)
		}
		if err := checkPath(in.path); err != nil {
			return nil, nil, err
		}
		a = append(a, "-f", demux, "-format_whitelist", demux, "-protocol_whitelist", "file")
		if demux == "mov" {
			a = append(a, "-enable_drefs", "0", "-use_absolute_path", "0")
		}
		if j.tool == toolFFmpeg {
			a = append(a, "-threads", threads)
		}
		a = append(a, "-i", "file:"+in.path)
	}
	if j.tool == toolFFprobe {
		return append(a, j.args...), nil, nil
	}
	a = append(a, "-threads", threads)
	a = append(a, j.args...)
	switch j.output {
	case "-":
		a = append(a, "pipe:1")
	default:
		if err := checkPath(j.output); err != nil {
			return nil, nil, err
		}
		a = append(a, "-y", "file:"+j.output)
	}
	return a, stdin, nil
}

func checkPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("media: path must be absolute and clean: %q", p)
	}
	return nil
}

// run executes one ffmpeg/ffprobe job under the concurrency, time and
// resource limits. It is the only place a child process is started.
func (r *Runner) run(ctx context.Context, j job) error {
	args, stdin, err := r.args(j)
	if err != nil {
		return err
	}
	bin := r.ffmpegBin
	if j.tool == toolFFprobe {
		bin = r.ffprobeBin
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return fmt.Errorf("media: %s: %w", j.tool, ctx.Err())
	}
	tctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	cmd, err := r.command(tctx, bin, args)
	if err != nil {
		return err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	lw := &limitWriter{w: j.stdout, n: j.stdoutMax}
	if j.stdout != nil {
		cmd.Stdout = lw
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	err = cmd.Run()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("media: %s: %w", j.tool, ctx.Err())
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w: %s killed after %s", ErrTimeout, j.tool, r.cfg.Timeout)
	case lw.hit:
		return fmt.Errorf("%w: %s output over %d bytes", ErrTooLarge, j.tool, j.stdoutMax)
	}
	if cpuKilled(err) {
		return fmt.Errorf("%w: %s hit the CPU limit", ErrTimeout, j.tool)
	}
	return fmt.Errorf("media: %s: %w: %s", j.tool, err, stderr.String())
}

var errOutputLimit = errors.New("media: output limit exceeded")

// limitWriter fails once more than n bytes are written, which closes the
// pipe and ends the child with EPIPE.
type limitWriter struct {
	w   io.Writer
	n   int64
	hit bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		l.hit = true
		return 0, errOutputLimit
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

// tailBuffer keeps the last stderrMax bytes written to it.
type tailBuffer struct{ b []byte }

const stderrMax = 2048

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > stderrMax {
		t.b = slices.Clone(t.b[len(t.b)-stderrMax:])
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	s := strings.TrimSpace(string(bytes.ToValidUTF8(t.b, nil)))
	if s == "" {
		return "no stderr"
	}
	return strings.ReplaceAll(s, "\n", "; ")
}

// childLimits are passed to the re-exec helper as decimal arguments.
func (r *Runner) childLimits() []string {
	cpu := int64((r.cfg.CPULimit + time.Second - 1) / time.Second)
	// Outputs are thumbnails, posters and teasers; nothing legitimate
	// comes close to this.
	fsize := max(64<<20, 4*r.cfg.TeaserMaxBytes)
	return []string{
		strconv.FormatUint(r.cfg.MemLimit, 10),
		strconv.FormatInt(cpu, 10),
		strconv.FormatInt(fsize, 10),
		strconv.Itoa(childNoFile),
	}
}

// childNoFile is RLIMIT_NOFILE for ffmpeg/ffprobe.
const childNoFile = 64
