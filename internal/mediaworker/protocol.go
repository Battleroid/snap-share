// Package mediaworker runs the ffmpeg steps of media processing in a
// separate process (the `snap-server media-worker` container) and calls them
// from snap.
//
// The split is at the public methods of media.Runner: snap sends a blob key,
// the sniffed type and, for the teaser, what the probe found; the worker
// opens the blob read-only, sniffs it again, refuses a mismatch and runs its
// own media.Runner. No argument, filter graph or path crosses the wire. The
// protocol is described in docs/media.md.
package mediaworker

import (
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/media"
)

// Protocol is the version of the wire protocol below. /v1 stays the path
// while it is compatible; snap compares Health.Protocol with its own.
const Protocol = 1

// Operations, the path segment after /v1/.
const (
	OpProbe     = "probe"
	OpThumbnail = "thumbnail"
	OpPoster    = "poster"
	OpTeaser    = "teaser"
)

// Error codes in an error response. The HTTP status matches the code.
const (
	CodeUnsupported  = "unsupported"  // 422, media.ErrUnsupported
	CodeTooLarge     = "too_large"    // 413, media.ErrTooLarge
	CodeTimeout      = "timeout"      // 504, media.ErrTimeout
	CodeInternal     = "internal"     // 500
	CodeStarting     = "starting"     // 503, the worker is not ready yet
	CodeBadRequest   = "bad_request"  // 400
	CodeUnauthorized = "unauthorized" // 401
	CodeNotFound     = "not_found"    // 404, no such blob or operation
)

// Thumbnail response headers: the size of the image in the body. Its type
// is the Content-Type.
const (
	HeaderWidth  = "X-Media-Width"
	HeaderHeight = "X-Media-Height"
)

// HeaderRequestID carries the caller's request id, logged by the worker.
const HeaderRequestID = "X-Request-Id"

// Limits of the protocol.
const (
	maxRequestBytes = 64 << 10 // a request is a key, a type and an info
	maxErrorBytes   = 64 << 10
	maxProbeBytes   = 1 << 20
	// MaxImageBytes caps a thumbnail or poster response.
	MaxImageBytes = 32 << 20
	// infoMaxSide and infoMaxDuration bound a teaser request's Info.
	infoMaxSide     = 1 << 16
	infoMaxDuration = 1_000_000 * time.Second
)

// Request is the body of POST /v1/{op}.
type Request struct {
	Key  string    `json:"key"`
	Type WireType  `json:"type"`
	Info *WireInfo `json:"info,omitempty"` // teaser only
}

// WireType is a media.Type.
type WireType struct {
	MIME      string `json:"mime"`
	Kind      string `json:"kind"`
	Container string `json:"container"`
}

func wireType(t media.Type) WireType {
	return WireType{MIME: t.MIME, Kind: string(t.Kind), Container: t.Container}
}

func (w WireType) media() media.Type {
	return media.Type{MIME: w.MIME, Kind: media.Kind(w.Kind), Container: w.Container}
}

// WireInfo is a media.Info, the probe response and the teaser's input.
type WireInfo struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	DurationNS  int64  `json:"duration_ns"`
	HasVideo    bool   `json:"has_video"`
	HasAudio    bool   `json:"has_audio"`
	Animated    bool   `json:"animated"`
	Orientation int    `json:"orientation"`
	VideoCodec  string `json:"video_codec"`
	AudioCodec  string `json:"audio_codec"`
}

func wireInfo(i media.Info) WireInfo {
	return WireInfo{
		Width: i.Width, Height: i.Height, DurationNS: int64(i.Duration),
		HasVideo: i.HasVideo, HasAudio: i.HasAudio, Animated: i.Animated, Orientation: i.Orientation,
		VideoCodec: i.VideoCodec, AudioCodec: i.AudioCodec,
	}
}

func (w WireInfo) media() media.Info {
	return media.Info{
		Width: w.Width, Height: w.Height, Duration: time.Duration(w.DurationNS),
		HasVideo: w.HasVideo, HasAudio: w.HasAudio, Animated: w.Animated, Orientation: w.Orientation,
		VideoCodec: w.VideoCodec, AudioCodec: w.AudioCodec,
	}
}

// check bounds the numbers the teaser derives its size and spans from.
func (w WireInfo) check() error {
	switch {
	case w.Width < 0 || w.Height < 0 || w.Width > infoMaxSide || w.Height > infoMaxSide:
		return fmt.Errorf("info size %d×%d out of range", w.Width, w.Height)
	case w.DurationNS < 0 || w.DurationNS > int64(infoMaxDuration):
		return fmt.Errorf("info duration %d out of range", w.DurationNS)
	case w.Orientation < 0 || w.Orientation > 8:
		return fmt.Errorf("info orientation %d out of range", w.Orientation)
	}
	return nil
}

// Health is the body of GET /v1/health.
type Health struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"` // snap-server's version
	FFmpeg   string `json:"ffmpeg"`  // ffmpeg's version
}

// ErrorBody is the body of every error response.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
