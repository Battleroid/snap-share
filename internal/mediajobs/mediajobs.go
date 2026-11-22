// Package mediajobs connects items to the media library. When a file item is
// created it strips JPEG location data (if the owner asked for it) and makes
// image thumbnails synchronously; everything that needs ffmpeg runs later as a
// durable "media.process" job: dimensions and duration, AVIF/HEIC thumbnails,
// video posters and hover teasers. See docs/media.md.
package mediajobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/settings"
)

// Teasers turns hover teasers for video and animated images on or off
// (admin label "Video hover previews"). Existing teasers stay when it is
// turned off.
var Teasers = settings.Define("media.teasers", true, nil)

// JobKind is the durable job that runs the ffmpeg steps for one item.
const JobKind = "media.process"

// Defaults for Deps.
const (
	DefaultConcurrency = 2 // media.Config's default MaxParallel
	DefaultJobTimeout  = 5 * time.Minute
	createTimeout      = time.Minute // the synchronous work at upload
)

// Deps are the service's dependencies.
type Deps struct {
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue // nil: nothing is queued
	Blobs    blob.Store  // the items' files store
	Items    *items.Service
	// Runner runs ffmpeg. Nil (no ffmpeg on the host) still gives Go image
	// thumbnails; video and the other ffmpeg steps are skipped.
	Runner *media.Runner
	// WorkDir holds ffmpeg outputs before they are stored as blobs. It is
	// emptied at start.
	WorkDir string
	// Concurrency is the number of job workers; match the Runner's
	// MaxParallel. Zero uses DefaultConcurrency.
	Concurrency int
	// JobTimeout bounds one job. Zero uses DefaultJobTimeout.
	JobTimeout time.Duration
}

// Service runs the media steps for items.
type Service struct {
	d      Deps
	log    *slog.Logger
	run    *media.Runner
	ffmpeg bool
}

// New builds the service and registers the job handler on d.Jobs.
func New(d Deps) (*Service, error) {
	if d.DB == nil || d.Settings == nil || d.Blobs == nil || d.Items == nil {
		return nil, errors.New("mediajobs: missing dependency")
	}
	s := &Service{d: d, log: d.Log, run: d.Runner}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.ffmpeg = s.run != nil && s.run.HasFFmpeg()
	if s.run == nil {
		s.run = media.NewImageRunner(media.Config{})
	}
	if s.ffmpeg {
		if d.WorkDir == "" {
			return nil, errors.New("mediajobs: WorkDir is required with ffmpeg")
		}
		dir, err := filepath.Abs(d.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("mediajobs: %w", err)
		}
		// Leftovers from a crash are never needed again.
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("mediajobs: clear work dir: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("mediajobs: %w", err)
		}
		s.d.WorkDir = dir
	}
	if d.Jobs != nil {
		n := d.Concurrency
		if n <= 0 {
			n = DefaultConcurrency
		}
		timeout := d.JobTimeout
		if timeout <= 0 {
			timeout = DefaultJobTimeout
		}
		d.Jobs.Handle(JobKind, n, timeout, s.handle)
	}
	return s, nil
}

// FFmpeg reports whether ffmpeg is available.
func (s *Service) FFmpeg() bool { return s.ffmpeg }

// goImage reports the image types decoded in process (no ffmpeg).
func goImage(t media.Type) bool {
	if t.Kind != media.KindImage {
		return false
	}
	switch t.MIME {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff":
		return true
	}
	return false
}

// path is the absolute path of a blob.
func (s *Service) path(key string) (string, error) {
	p, err := s.d.Blobs.Path(key)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// sniff reads the content type of a blob with media.Sniff, which also gives
// the ffmpeg demuxer (Container) if ffmpeg may read it at all.
func (s *Service) sniff(ctx context.Context, key, filename string) (media.Type, error) {
	f, err := s.d.Blobs.Open(ctx, key)
	if err != nil {
		return media.Type{}, err
	}
	defer func() { _ = f.Close() }()
	return media.Sniff(f, f.Size(), filename)
}

// OnCreate is the items OnCreate hook. It runs before the upload response:
// JPEG location stripping, then the thumbnail and dimensions of images Go
// decodes, so the response's thumbnail_url works at once. Slower work is
// queued as a job. Errors are logged; the upload itself has succeeded.
func (s *Service) OnCreate(ctx context.Context, it *items.Item) {
	if it.Kind != items.KindFile || it.BlobKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	t, err := s.sniff(ctx, it.BlobKey, it.Filename)
	if err != nil {
		s.log.Warn("media: sniff", "item", it.ID, "err", err)
		return
	}
	if t.MIME == "image/jpeg" {
		s.stripLocation(ctx, it)
	}
	var info media.Info
	if goImage(t) {
		info = s.imageMedia(ctx, it, t)
	}
	if s.ffmpeg && s.d.Jobs != nil && s.wantsJob(t, info) {
		if _, err := s.d.Jobs.Enqueue(ctx, JobKind, payload{ID: it.ID}); err != nil {
			s.log.Error("media: enqueue", "item", it.ID, "err", err)
		}
	}
}

// wantsJob reports whether a sniffed file has work that needs ffmpeg.
func (s *Service) wantsJob(t media.Type, info media.Info) bool {
	switch t.Kind {
	case media.KindVideo, media.KindAudio:
		return t.Container != ""
	case media.KindImage:
		if !goImage(t) {
			return t.Container != "" // AVIF, HEIC
		}
		return info.Animated && Teasers.Get(s.d.Settings)
	}
	return false
}

// imageMedia makes the thumbnail and records the dimensions of an image Go
// decodes. It returns what Probe found.
func (s *Service) imageMedia(ctx context.Context, it *items.Item, t media.Type) media.Info {
	p, err := s.path(it.BlobKey)
	if err != nil {
		s.log.Warn("media: blob path", "item", it.ID, "err", err)
		return media.Info{}
	}
	var m items.Media
	info, err := s.run.Probe(ctx, p, t)
	if err != nil {
		s.log.Info("media: probe image", "item", it.ID, "err", err)
		return info
	}
	m.Width, m.Height, m.DurationMS = int64(info.Width), int64(info.Height), info.Duration.Milliseconds()
	if key, err := s.storeWith(ctx, func(w io.Writer) error {
		_, err := s.run.Thumbnail(ctx, p, t, w)
		return err
	}); err != nil {
		s.log.Info("media: thumbnail", "item", it.ID, "err", err)
	} else {
		m.ThumbKey = key
	}
	if err := s.d.Items.SetMedia(ctx, it.ID, m); err != nil && !errors.Is(err, items.ErrNotFound) {
		s.log.Error("media: save thumbnail", "item", it.ID, "err", err)
	}
	return info
}

// stripLocation rewrites a JPEG without its location metadata when the
// owner's "Strip JPEG location" preference is on. The copy is written to a
// new blob first; on any error the original stays.
func (s *Service) stripLocation(ctx context.Context, it *items.Item) {
	prefs, err := items.LoadPrefs(ctx, s.d.DB, it.OwnerID)
	if err != nil || !prefs.StripLocation {
		return
	}
	src, err := s.d.Blobs.Open(ctx, it.BlobKey)
	if err != nil {
		s.log.Warn("media: strip location: open", "item", it.ID, "err", err)
		return
	}
	defer func() { _ = src.Close() }()
	h := sha256.New()
	var n int64
	key, err := s.storeWith(ctx, func(w io.Writer) error {
		return media.StripJPEGLocation(src, io.MultiWriter(w, h, countWriter{&n}))
	})
	if err != nil {
		s.log.Info("media: strip location; keeping the original", "item", it.ID, "err", err)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum == it.SHA256 { // nothing was removed
		_ = s.d.Blobs.Delete(ctx, key)
		return
	}
	if err := s.d.Items.ReplaceContent(ctx, it.ID, it.BlobKey, key, n, sum); err != nil {
		s.log.Warn("media: strip location: replace", "item", it.ID, "err", err)
		return
	}
	it.BlobKey, it.Size, it.SHA256 = key, n, sum
}

type countWriter struct{ n *int64 }

func (c countWriter) Write(p []byte) (int, error) {
	*c.n += int64(len(p))
	return len(p), nil
}

// storeWith writes a new blob through fn, discarding it if fn fails.
func (s *Service) storeWith(ctx context.Context, fn func(io.Writer) error) (string, error) {
	w, err := s.d.Blobs.Create(ctx)
	if err != nil {
		return "", err
	}
	if err := fn(w); err != nil {
		_ = w.Abort()
		return "", err
	}
	return w.Commit()
}

// storeFile copies a file into a new blob.
func (s *Service) storeFile(ctx context.Context, name string) (string, error) {
	f, err := os.Open(name) //nolint:gosec // a path in our own work dir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return s.storeWith(ctx, func(w io.Writer) error {
		_, err := io.Copy(w, f)
		return err
	})
}

type payload struct {
	ID int64 `json:"id"`
}

func (s *Service) handle(ctx context.Context, raw json.RawMessage) error {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil || p.ID <= 0 {
		return jobs.Permanent(fmt.Errorf("media job: bad payload %q", raw))
	}
	return s.Process(ctx, p.ID)
}

// Process runs the ffmpeg steps for one item: probe dimensions and duration;
// for video a poster (stored as the thumbnail) and a teaser; for AVIF and
// HEIC a thumbnail; for animated images a teaser. Teasers follow the
// media.teasers setting. Without ffmpeg, or for an item that is gone or that
// ffmpeg may not read, it does nothing. Partial results are kept; media
// errors (unsupported, too large, timeout) are not retried.
func (s *Service) Process(ctx context.Context, id int64) error {
	if !s.ffmpeg {
		return nil
	}
	it, err := s.d.Items.Get(ctx, id)
	if errors.Is(err, items.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if it.Kind != items.KindFile || it.BlobKey == "" {
		return nil
	}
	t, err := s.sniff(ctx, it.BlobKey, it.Filename)
	if err != nil {
		return err
	}
	webp := t.MIME == "image/webp" // composited in Go; ffmpeg only encodes
	if t.Container == "" && !webp {
		return nil // ffmpeg must not read it
	}
	src, err := s.path(it.BlobKey)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.d.WorkDir, "job-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var (
		m    items.Media
		errs []error
	)
	teasers := Teasers.Get(s.d.Settings)
	switch t.Kind {
	case media.KindVideo, media.KindAudio:
		info, err := s.run.Probe(ctx, src, t)
		if err != nil {
			return classify(err)
		}
		m.Width, m.Height, m.DurationMS = int64(info.Width), int64(info.Height), info.Duration.Milliseconds()
		if t.Kind == media.KindVideo && info.HasVideo {
			if key, err := s.output(ctx, dir, "poster.jpg", func(dst string) error { return s.run.Poster(ctx, src, t, dst) }); err != nil {
				errs = append(errs, fmt.Errorf("poster: %w", err))
			} else {
				m.ThumbKey = key
			}
			if teasers {
				if key, err := s.output(ctx, dir, "teaser.mp4", func(dst string) error { return s.run.Teaser(ctx, src, t, info, dst) }); err != nil {
					errs = append(errs, fmt.Errorf("teaser: %w", err))
				} else {
					m.PreviewKey = key
				}
			}
		}
	case media.KindImage:
		info, err := s.run.Probe(ctx, src, t)
		if err != nil {
			return classify(err)
		}
		if !goImage(t) { // AVIF, HEIC: OnCreate could not decode it
			m.Width, m.Height = int64(info.Width), int64(info.Height)
			if key, err := s.storeWith(ctx, func(w io.Writer) error {
				_, err := s.run.Thumbnail(ctx, src, t, w)
				return err
			}); err != nil {
				errs = append(errs, fmt.Errorf("thumbnail: %w", err))
			} else {
				m.ThumbKey = key
			}
		}
		if info.Animated && teasers {
			m.DurationMS = info.Duration.Milliseconds()
			if key, err := s.output(ctx, dir, "teaser.mp4", func(dst string) error { return s.run.Teaser(ctx, src, t, info, dst) }); err != nil {
				errs = append(errs, fmt.Errorf("teaser: %w", err))
			} else {
				m.PreviewKey = key
			}
		}
	default:
		return nil
	}
	if m != (items.Media{}) {
		if err := s.d.Items.SetMedia(context.WithoutCancel(ctx), it.ID, m); err != nil {
			if errors.Is(err, items.ErrNotFound) {
				return nil
			}
			return err
		}
	}
	if err := errors.Join(errs...); err != nil {
		s.log.Info("media: job step failed", "item", it.ID, "mime", t.MIME, "err", err)
		return classify(err)
	}
	return nil
}

// output runs fn to write dir/name and stores the result as a blob.
func (s *Service) output(ctx context.Context, dir, name string, fn func(dst string) error) (string, error) {
	dst := filepath.Join(dir, name)
	if err := fn(dst); err != nil {
		return "", err
	}
	return s.storeFile(ctx, dst)
}

// classify marks errors that a retry cannot fix as permanent. Context errors
// stay retryable, so a job cut off by shutdown runs again.
func classify(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, media.ErrUnsupported), errors.Is(err, media.ErrTooLarge), errors.Is(err, media.ErrTimeout):
		return jobs.Permanent(err)
	}
	return err
}
