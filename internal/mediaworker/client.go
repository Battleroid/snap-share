package mediaworker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/media"
)

// DefaultDefer is how long a job waits when the worker is unreachable or
// still starting.
const DefaultDefer = time.Minute

// ClientConfig configures a Client.
type ClientConfig struct {
	// URL is unix:///path/to/socket or http://host:port.
	URL   string
	Token string
	// TeaserMaxBytes refuses a larger teaser (media.ErrTooLarge). Zero uses
	// media.Config's default, 1 MiB.
	TeaserMaxBytes int64
	// Defer is how long jobs.Defer asks to wait when the worker cannot be
	// reached. Zero uses DefaultDefer.
	Defer time.Duration
}

// Client calls a media worker. It implements mediajobs.Runner: paths are
// blob paths of the files store, which the worker opens by key.
//
// Errors: the worker's codes come back as media.ErrUnsupported, ErrTooLarge
// and ErrTimeout. A worker that cannot be dialled, or answers that it is
// still starting, gives jobs.Defer, so the job waits without spending an
// attempt. Anything else, including a connection that drops mid-request
// (an input that crashes the worker), is a plain error that counts.
type Client struct {
	base      string
	token     string
	hc        *http.Client
	teaserMax int64
	deferFor  time.Duration
}

// NewClient checks cfg. It does not contact the worker.
func NewClient(cfg ClientConfig) (*Client, error) {
	if len(cfg.Token) < 32 {
		return nil, errors.New("mediaworker: token must be at least 32 characters")
	}
	tr := &http.Transport{
		Proxy:             nil, // never through an environment proxy
		DisableKeepAlives: true,
		// Every dial is fresh, so a dial error always means "not reachable
		// now", and never hides a request the worker already received.
	}
	c := &Client{token: cfg.Token, teaserMax: cfg.TeaserMaxBytes, deferFor: cfg.Defer}
	switch {
	case strings.HasPrefix(cfg.URL, "unix://"):
		sock := strings.TrimPrefix(cfg.URL, "unix://")
		if !filepath.IsAbs(sock) || filepath.Clean(sock) != sock {
			return nil, fmt.Errorf("mediaworker: %q: unix:// needs an absolute socket path", cfg.URL)
		}
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
		c.base = "http://media-worker"
	case strings.HasPrefix(cfg.URL, "http://"):
		u, err := url.Parse(cfg.URL)
		if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("mediaworker: %q must be http://host:port", cfg.URL)
		}
		c.base = "http://" + u.Host
	default:
		return nil, fmt.Errorf("mediaworker: %q must be unix:///path or http://host:port", cfg.URL)
	}
	c.hc = &http.Client{
		Transport: tr,
		// The token goes to the configured worker only.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if c.teaserMax <= 0 {
		c.teaserMax = 1 << 20
	}
	if c.deferFor <= 0 {
		c.deferFor = DefaultDefer
	}
	return c, nil
}

// HasFFmpeg is true: a configured worker has ffmpeg, whether or not it is
// up at the moment.
func (c *Client) HasFFmpeg() bool { return true }

type requestIDKey struct{}

// WithRequestID sets the X-Request-Id of the calls made with ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func reqID(ctx context.Context) string {
	if id, _ := ctx.Value(requestIDKey{}).(string); id != "" {
		return id
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "mw-" + hex.EncodeToString(b[:])
}

// keyOf returns the blob key of a files-store path (<dir>/ab/cd/<key>).
func keyOf(path string) (string, error) {
	key := filepath.Base(path)
	if !local.ValidKey(key) || filepath.Base(filepath.Dir(path)) != key[2:4] ||
		filepath.Base(filepath.Dir(filepath.Dir(path))) != key[:2] {
		return "", fmt.Errorf("mediaworker: %q is not a blob path", path)
	}
	return key, nil
}

// call sends one request and returns the successful response, whose body
// the caller closes.
func (c *Client) call(ctx context.Context, method, op string, body *Request) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/v1/"+op, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(HeaderRequestID, reqID(ctx))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req) // #nosec G704 -- the configured worker
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("media worker %s: %w", op, ctx.Err())
		}
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Op == "dial" {
			return nil, jobs.Defer(fmt.Errorf("media worker unreachable: %w", err), c.deferFor)
		}
		return nil, fmt.Errorf("media worker %s: %w", op, err)
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	defer func() { _ = res.Body.Close() }()
	return nil, c.errorOf(op, res)
}

// errorOf maps an error response to the error the media.Runner would have
// returned.
func (c *Client) errorOf(op string, res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBytes))
	var e ErrorBody
	if json.Unmarshal(b, &e) != nil || e.Code == "" {
		return fmt.Errorf("media worker %s: %s", op, res.Status)
	}
	msg := fmt.Sprintf("media worker %s: %s", op, e.Message)
	switch {
	case e.Code == CodeStarting && res.StatusCode == http.StatusServiceUnavailable:
		return jobs.Defer(errors.New("media worker is starting"), c.deferFor)
	case e.Code == CodeUnsupported:
		return fmt.Errorf("%w: %s", media.ErrUnsupported, msg)
	case e.Code == CodeTooLarge:
		return fmt.Errorf("%w: %s", media.ErrTooLarge, msg)
	case e.Code == CodeTimeout:
		return fmt.Errorf("%w: %s", media.ErrTimeout, msg)
	}
	return fmt.Errorf("media worker %s: %s (%s): %s", op, res.Status, e.Code, e.Message)
}

func (c *Client) post(ctx context.Context, op, src string, t media.Type, info *media.Info) (*http.Response, error) {
	key, err := keyOf(src)
	if err != nil {
		return nil, err
	}
	req := &Request{Key: key, Type: wireType(t)}
	if info != nil {
		wi := wireInfo(*info)
		req.Info = &wi
	}
	return c.call(ctx, http.MethodPost, op, req)
}

// copyMax copies the body to w, failing with media.ErrTooLarge past max
// bytes. A body cut short (the connection dropped) is a plain error.
func copyMax(op string, w io.Writer, res *http.Response, max int64) error {
	if res.ContentLength > max {
		return fmt.Errorf("%w: media worker %s: %d bytes, over %d", media.ErrTooLarge, op, res.ContentLength, max)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, max+1))
	if err != nil {
		return fmt.Errorf("media worker %s: read response: %w", op, err)
	}
	if n > max {
		return fmt.Errorf("%w: media worker %s: over %d bytes", media.ErrTooLarge, op, max)
	}
	return nil
}

// Probe reads dimensions, duration and streams on the worker.
func (c *Client) Probe(ctx context.Context, path string, t media.Type) (media.Info, error) {
	res, err := c.post(ctx, OpProbe, path, t, nil)
	if err != nil {
		return media.Info{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var b bytes.Buffer
	if err := copyMax(OpProbe, &b, res, maxProbeBytes); err != nil {
		return media.Info{}, err
	}
	var wi WireInfo
	if err := json.Unmarshal(b.Bytes(), &wi); err != nil {
		return media.Info{}, fmt.Errorf("media worker probe: %w", err)
	}
	return wi.media(), nil
}

// Thumbnail writes the worker's thumbnail of src to w.
func (c *Client) Thumbnail(ctx context.Context, src string, t media.Type, w io.Writer) (media.Thumb, error) {
	res, err := c.post(ctx, OpThumbnail, src, t, nil)
	if err != nil {
		return media.Thumb{}, err
	}
	defer func() { _ = res.Body.Close() }()
	th := media.Thumb{MIME: res.Header.Get("Content-Type")}
	th.Width, _ = strconv.Atoi(res.Header.Get(HeaderWidth))
	th.Height, _ = strconv.Atoi(res.Header.Get(HeaderHeight))
	if (th.MIME != "image/jpeg" && th.MIME != "image/png") || th.Width <= 0 || th.Height <= 0 {
		return media.Thumb{}, fmt.Errorf("media worker thumbnail: unexpected %q %d×%d", th.MIME, th.Width, th.Height)
	}
	if err := copyMax(OpThumbnail, w, res, MaxImageBytes); err != nil {
		return media.Thumb{}, err
	}
	return th, nil
}

// Poster writes the worker's JPEG poster of src to dst.
func (c *Client) Poster(ctx context.Context, src string, t media.Type, dst string) error {
	res, err := c.post(ctx, OpPoster, src, t, nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	return writeDst(OpPoster, dst, res, MaxImageBytes)
}

// Teaser writes the worker's MP4 teaser of src to dst. info is what Probe
// returned.
func (c *Client) Teaser(ctx context.Context, src string, t media.Type, info media.Info, dst string) error {
	res, err := c.post(ctx, OpTeaser, src, t, &info)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	return writeDst(OpTeaser, dst, res, c.teaserMax)
}

// writeDst stores the body at dst, removing it on any error.
func writeDst(op, dst string, res *http.Response, max int64) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the job's work dir
	if err != nil {
		return err
	}
	err = copyMax(op, f, res, max)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// Health asks the worker for its protocol and versions.
func (c *Client) Health(ctx context.Context) (Health, error) {
	res, err := c.call(ctx, http.MethodGet, "health", nil)
	if err != nil {
		return Health{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var b bytes.Buffer
	if err := copyMax("health", &b, res, maxErrorBytes); err != nil {
		return Health{}, err
	}
	var h Health
	if err := json.Unmarshal(b.Bytes(), &h); err != nil {
		return Health{}, fmt.Errorf("media worker health: %w", err)
	}
	return h, nil
}
