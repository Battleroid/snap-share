// Package media sniffs uploaded files, reads their dimensions and duration,
// and makes image thumbnails, video posters and hover teasers.
//
// Images that Go can decode (JPEG, PNG, GIF, WebP, BMP, TIFF) are handled in
// process. Everything else goes through ffmpeg/ffprobe, always via one
// internal function: the demuxer is forced from [Sniff], only the file
// protocol is allowed, and the child gets an empty environment, its own
// process group, a timeout and rlimits (see [MaybeRunLimited]).
package media

import "errors"

// Sentinel errors. Returned errors wrap these; test with errors.Is.
var (
	// ErrUnsupported means the input type cannot be processed.
	ErrUnsupported = errors.New("media: unsupported")
	// ErrTooLarge means the input or the output exceeds a configured limit.
	ErrTooLarge = errors.New("media: too large")
	// ErrTimeout means ffmpeg/ffprobe hit the time or CPU limit and was killed.
	ErrTimeout = errors.New("media: timeout")
)

// Kind is a coarse content class.
type Kind string

// Kinds returned by Sniff.
const (
	KindImage   Kind = "image"
	KindVideo   Kind = "video"
	KindAudio   Kind = "audio"
	KindText    Kind = "text"
	KindPDF     Kind = "pdf"
	KindArchive Kind = "archive"
	KindOther   Kind = "other"
)

// Type is the result of Sniff.
type Type struct {
	// MIME is the detected media type, e.g. "video/mp4". Text is always
	// "text/plain" with a charset; Sniff never returns HTML, script or SVG
	// types.
	MIME string
	Kind Kind
	// Container is the ffmpeg demuxer forced with -f when ffmpeg reads the
	// file (e.g. "mov", "matroska", "gif"). Empty means ffmpeg must not be
	// used on it.
	Container string
}
