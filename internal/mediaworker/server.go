package mediaworker

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/version"
)

// Runner is what the worker runs: the media.Runner operations. *media.Runner
// (with Config.Direct inside the container) implements it.
type Runner interface {
	Probe(ctx context.Context, path string, t media.Type) (media.Info, error)
	Thumbnail(ctx context.Context, src string, t media.Type, w io.Writer) (media.Thumb, error)
	Poster(ctx context.Context, src string, t media.Type, dst string) error
	Teaser(ctx context.Context, src string, t media.Type, info media.Info, dst string) error
}

var _ Runner = (*media.Runner)(nil)

// Files opens blobs read-only; *local.ReadOnly implements it.
type Files interface {
	// Path returns the absolute path of a regular file, or blob.ErrNotFound.
	Path(key string) (string, error)
	Open(ctx context.Context, key string) (blob.File, error)
}

// DefaultRequestTimeout caps one request, whatever the Runner's per-call
// timeout. snap's job timeout (5 minutes) is longer, so the worker gives up
// first and says so.
const DefaultRequestTimeout = 4 * time.Minute

// ServerConfig configures a Server.
type ServerConfig struct {
	Files Files
	// Scratch holds one temp directory per request, removed after it. It
	// is emptied by NewServer.
	Scratch string
	// Token must be sent as "Authorization: Bearer <token>" on every request.
	Token string
	Log   *slog.Logger // nil: discard
	// RequestTimeout caps one request; zero uses DefaultRequestTimeout.
	RequestTimeout time.Duration
}

// Server is the worker's HTTP handler. It answers 503 "starting" until
// Start is called.
type Server struct {
	files   Files
	scratch string
	token   []byte
	log     *slog.Logger
	timeout time.Duration
	ready   atomic.Pointer[ready]
	mux     *http.ServeMux
}

type ready struct {
	run    Runner
	health Health
}

// NewServer checks cfg and empties the scratch directory.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Files == nil {
		return nil, errors.New("mediaworker: no files")
	}
	if len(cfg.Token) < 32 {
		return nil, errors.New("mediaworker: token must be at least 32 characters")
	}
	if cfg.Scratch == "" {
		return nil, errors.New("mediaworker: no scratch directory")
	}
	scratch, err := filepath.Abs(cfg.Scratch)
	if err != nil {
		return nil, fmt.Errorf("mediaworker: scratch directory: %w", err)
	}
	// Leftovers from a crash are never needed again.
	if err := os.RemoveAll(scratch); err != nil {
		return nil, fmt.Errorf("mediaworker: clear scratch: %w", err)
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return nil, fmt.Errorf("mediaworker: %w", err)
	}
	s := &Server{
		files: cfg.Files, scratch: scratch, token: []byte("Bearer " + cfg.Token),
		log: cfg.Log, timeout: cfg.RequestTimeout, mux: http.NewServeMux(),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.timeout <= 0 {
		s.timeout = DefaultRequestTimeout
	}
	s.mux.HandleFunc("GET /v1/health", s.health)
	s.mux.HandleFunc("POST /v1/{op}", s.op)
	return s, nil
}

// Start makes the server ready: requests run on run, and health reports
// the ffmpeg version given.
func (s *Server) Start(run Runner, ffmpeg string) {
	s.ready.Store(&ready{run: run, health: Health{Protocol: Protocol, Version: version.Version, FFmpeg: ffmpeg}})
}

// ServeHTTP checks the token on every request, before anything else.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			s.log.Error("media worker panic", "request_id", requestID(r), "err", v)
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		}
	}()
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), s.token) != 1 {
		s.log.Warn("media worker request", "request_id", requestID(r), "result", CodeUnauthorized)
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "missing or wrong token")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	st := s.ready.Load()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, CodeStarting, "starting")
		return
	}
	s.log.Debug("media worker health", "request_id", requestID(r))
	writeJSON(w, st.health)
}

// requestID is the caller's X-Request-Id, limited to a safe alphabet and
// length.
func requestID(r *http.Request) string {
	id := r.Header.Get(HeaderRequestID)
	if len(id) > 64 {
		id = id[:64]
	}
	return strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			return c
		}
		return -1
	}, id)
}

// failure is an error with its response code.
type failure struct {
	status int
	code   string
	err    error
}

func (f *failure) Error() string { return f.err.Error() }

func fail(status int, code string, format string, a ...any) *failure {
	return &failure{status, code, fmt.Errorf(format, a...)}
}

// classify maps a Runner error to its response code.
func classify(ctx context.Context, err error) *failure {
	var f *failure
	switch {
	case errors.As(err, &f):
		return f
	case errors.Is(err, media.ErrUnsupported):
		return &failure{http.StatusUnprocessableEntity, CodeUnsupported, err}
	case errors.Is(err, media.ErrTooLarge):
		return &failure{http.StatusRequestEntityTooLarge, CodeTooLarge, err}
	case errors.Is(err, media.ErrTimeout), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &failure{http.StatusGatewayTimeout, CodeTimeout, err}
	}
	return &failure{http.StatusInternalServerError, CodeInternal, err}
}

func (s *Server) op(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	op := r.PathValue("op")
	attrs := []any{"request_id", requestID(r), "op", op}
	st := s.ready.Load()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, CodeStarting, "starting")
		s.log.Info("media worker request", append(attrs, "result", CodeStarting)...)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	out, t, err := s.run(ctx, w, r, st.run, op)
	attrs = append(attrs, "mime", t.MIME)
	if err != nil {
		f := classify(ctx, err)
		writeError(w, f.status, f.code, f.err.Error())
		attrs = append(attrs, "ms", time.Since(start).Milliseconds(), "result", f.code, "err", f.err)
		if f.code == CodeInternal {
			s.log.Warn("media worker request", attrs...)
		} else {
			s.log.Info("media worker request", attrs...)
		}
		return
	}
	attrs = append(attrs, "ms", time.Since(start).Milliseconds())
	result := "ok"
	if out != nil {
		attrs = append(attrs, "bytes", out.size)
		if err := out.send(w); err != nil {
			result = "write_failed"
			attrs = append(attrs, "err", err)
		}
	}
	s.log.Info("media worker request", append(attrs, "result", result)...)
}

// output is a successful response body, held in a scratch file so that
// errors can still be reported until the operation has finished.
type output struct {
	file *os.File
	size int64
	hdr  http.Header
}

func (o *output) send(w http.ResponseWriter) error {
	defer func() { _ = o.file.Close() }()
	for k, v := range o.hdr {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Length", strconv.FormatInt(o.size, 10))
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	w.WriteHeader(http.StatusOK)
	if _, err := o.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(w, o.file)
	return err
}

// run decodes and checks the request and runs op. A nil output means the
// response (probe JSON) is written already.
func (s *Server) run(ctx context.Context, w http.ResponseWriter, r *http.Request, run Runner, op string) (*output, media.Type, error) {
	switch op {
	case OpProbe, OpThumbnail, OpPoster, OpTeaser:
	default:
		return nil, media.Type{}, fail(http.StatusNotFound, CodeNotFound, "unknown operation")
	}
	req, err := decode(w, r)
	if err != nil {
		return nil, media.Type{}, err
	}
	claimed := req.Type.media()
	if !local.ValidKey(req.Key) {
		return nil, claimed, fail(http.StatusBadRequest, CodeBadRequest, "key must be 32 lowercase hex digits")
	}
	if (op == OpTeaser) != (req.Info != nil) {
		return nil, claimed, fail(http.StatusBadRequest, CodeBadRequest, "info is required for the teaser and only for it")
	}
	if req.Info != nil {
		if err := req.Info.check(); err != nil {
			return nil, claimed, fail(http.StatusBadRequest, CodeBadRequest, "%v", err)
		}
	}
	src, t, err := s.open(ctx, req.Key, claimed)
	if err != nil {
		return nil, claimed, err
	}

	if op == OpProbe {
		info, err := run.Probe(ctx, src, t)
		if err != nil {
			return nil, t, err
		}
		writeJSON(w, wireInfo(info))
		return nil, t, nil
	}
	dir, err := os.MkdirTemp(s.scratch, "req-")
	if err != nil {
		return nil, t, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dst := filepath.Join(dir, op)
	hdr := http.Header{}
	switch op {
	case OpThumbnail:
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // our scratch dir
		if err != nil {
			return nil, t, err
		}
		th, err := run.Thumbnail(ctx, src, t, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, t, err
		}
		hdr.Set("Content-Type", th.MIME)
		hdr.Set(HeaderWidth, strconv.Itoa(th.Width))
		hdr.Set(HeaderHeight, strconv.Itoa(th.Height))
	case OpPoster:
		if err := run.Poster(ctx, src, t, dst); err != nil {
			return nil, t, err
		}
		hdr.Set("Content-Type", "image/jpeg")
	case OpTeaser:
		if err := run.Teaser(ctx, src, t, req.Info.media(), dst); err != nil {
			return nil, t, err
		}
		hdr.Set("Content-Type", "video/mp4")
	}
	// Opened before the directory is removed; the open file outlives it.
	f, err := os.Open(dst) //nolint:gosec // our scratch dir
	if err != nil {
		return nil, t, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, t, err
	}
	return &output{file: f, size: fi.Size(), hdr: hdr}, t, nil
}

// decode reads one Request object with no unknown fields.
func decode(w http.ResponseWriter, r *http.Request) (Request, error) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return req, fail(http.StatusBadRequest, CodeBadRequest, "bad request body: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return req, fail(http.StatusBadRequest, CodeBadRequest, "bad request body: more than one object")
	}
	return req, nil
}

// open resolves key to a regular file in the files directory, sniffs it
// and returns its path and the sniffed type, which must equal the claimed
// one. The Runner then forces the sniffed demuxer and refuses any outside
// its whitelist.
func (s *Server) open(ctx context.Context, key string, claimed media.Type) (string, media.Type, error) {
	src, err := s.files.Path(key)
	if errors.Is(err, blob.ErrNotFound) {
		return "", claimed, fail(http.StatusNotFound, CodeNotFound, "no such file")
	}
	if err != nil {
		return "", claimed, err
	}
	f, err := s.files.Open(ctx, key)
	if errors.Is(err, blob.ErrNotFound) {
		return "", claimed, fail(http.StatusNotFound, CodeNotFound, "no such file")
	}
	if err != nil {
		return "", claimed, err
	}
	defer func() { _ = f.Close() }()
	t, err := media.Sniff(f, f.Size(), sniffHint(claimed.MIME))
	if err != nil {
		return "", claimed, err
	}
	if t != claimed {
		return "", claimed, fail(http.StatusUnprocessableEntity, CodeUnsupported,
			"type mismatch: claimed %s (%s), the file is %s (%s)", claimed.MIME, claimed.Container, t.MIME, t.Container)
	}
	return src, t, nil
}

// sniffHint is a filename for media.Sniff that gives the claimed type the
// tie-breaks snap's sniff had from the item's real filename: Sniff uses the
// extension only between media types the bytes already allow (audio or
// video in MP4, WebM, Matroska, ASF; weak MP3 and AAC frame syncs), never to
// turn text or unknown bytes into media.
func sniffHint(mime string) string {
	switch mime {
	case "audio/mp4":
		return "a.m4a"
	case "audio/webm":
		return "a.weba"
	case "audio/x-matroska":
		return "a.mka"
	case "audio/x-ms-wma":
		return "a.wma"
	case "audio/mpeg":
		return "a.mp3"
	case "audio/aac":
		return "a.aac"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	if len(msg) > maxErrorBytes/2 {
		msg = msg[:maxErrorBytes/2]
	}
	b, _ := json.Marshal(ErrorBody{Code: code, Message: msg})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// Listen opens the worker's listener: "unix" makes the socket's directory
// 0700 and the socket 0600 (snap and the worker share a uid), replacing a
// stale socket; "tcp" listens on host:port.
func Listen(ctx context.Context, network, address string) (net.Listener, error) {
	var lc net.ListenConfig
	if network != "unix" {
		return lc.Listen(ctx, network, address)
	}
	dir := filepath.Dir(address)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mediaworker: socket directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a directory needs x
		return nil, fmt.Errorf("mediaworker: socket directory must be the worker's own: %w", err)
	}
	if fi, err := os.Lstat(address); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("mediaworker: %s exists and is not a socket", address)
		}
		if err := os.Remove(address); err != nil {
			return nil, fmt.Errorf("mediaworker: remove stale socket: %w", err)
		}
	}
	ln, err := lc.Listen(ctx, "unix", address)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// FFmpegVersion returns the version ffmpeg reports, e.g. "8.1", or "" if it
// cannot be run. It runs `ffmpeg -version` with an empty environment and
// no input.
func FFmpegVersion(ctx context.Context, ffmpeg string) string {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	bin, err := exec.LookPath(ffmpeg)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-version") //nolint:gosec // our ffmpeg, fixed arguments
	cmd.Env = []string{}
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line, _, _ := bytes.Cut(out, []byte("\n"))
	f := strings.Fields(string(line)) // "ffmpeg version 8.1 Copyright ..."
	if len(f) >= 3 && f[0] == "ffmpeg" && f[1] == "version" {
		return f[2]
	}
	return ""
}
