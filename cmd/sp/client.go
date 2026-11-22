package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/version"
)

// env is what a command talks to. main uses the process's streams and a
// default HTTP client; tests put their own in the context.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	http           *http.Client
}

type envKey struct{}

func withEnv(ctx context.Context, e *env) context.Context { return context.WithValue(ctx, envKey{}, e) }

func envFrom(ctx context.Context) *env {
	if e, ok := ctx.Value(envKey{}).(*env); ok {
		return e
	}
	return &env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, http: defaultHTTP()}
}

// defaultHTTP has connect and header timeouts but no overall timeout, so large
// uploads can take as long as they need. It never follows redirects: the API
// does not send any, and a redirect must not carry the key elsewhere.
func defaultHTTP() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.ResponseHeaderTimeout = 5 * time.Minute
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func userAgent() string { return "sp/" + version.Version }

// client calls the snap API with the configured instance and key.
type client struct {
	base string // scheme://host[:port]
	key  string
	hc   *http.Client
	env  *env
}

func newClient(ctx context.Context) (*client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	base, err := normalizeBase(cfg.URL)
	if err != nil {
		return nil, err
	}
	e := envFrom(ctx)
	return &client{base: base, key: strings.TrimSpace(cfg.Key), hc: e.http, env: e}, nil
}

// normalizeBase turns "share.example.com" or "https://share.example.com/" into
// "https://share.example.com".
func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("no server URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", raw)
	}
	if u.User != nil {
		return "", errors.New("the server URL must not contain a user name or password")
	}
	return u.Scheme + "://" + u.Host, nil
}

// apiError is an error response from the server.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func (c *client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// do sends req and decodes a JSON success body into out (unless nil). Error
// responses become *apiError with the server's message.
func (c *client) do(req *http.Request, out any) error {
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return readError(res)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(out); err != nil {
		return fmt.Errorf("unexpected response from %s: %w", c.base, err)
	}
	return nil
}

func readError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var body struct {
		Error string `json:"error"`
	}
	msg := ""
	if json.Unmarshal(b, &body) == nil && body.Error != "" {
		msg = body.Error
	} else {
		msg = fmt.Sprintf("HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	return &apiError{status: res.StatusCode, msg: msg}
}

// call sends a JSON request (in may be nil) and decodes the JSON response.
func (c *client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

// item is the part of the API's item JSON the CLI shows.
type item struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Slug        string     `json:"slug"`
	URL         string     `json:"url"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	Visibility  string     `json:"visibility"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletionURL string     `json:"deletion_url"`
}

// me is GET /api/v1/me.
type me struct {
	Username      string `json:"username"`
	Role          string `json:"role"`
	EmailVerified bool   `json:"email_verified"`
	Quota         struct {
		QuotaBytes     int64 `json:"quota_bytes"`
		UsedBytes      int64 `json:"used_bytes"`
		MaxUploadBytes int64 `json:"max_upload_bytes"`
	} `json:"quota"`
	Key *struct {
		Name      string     `json:"name"`
		Prefix    string     `json:"prefix"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at"`
	} `json:"key"`
}

// humanBytes formats a size with SI units, like the web UI.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}

// stamp formats an optional time in UTC; nil is "never".
func stamp(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// fprintf writes to a terminal stream; there is nothing useful to do when that fails.
func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

// isTerminal reports whether stream is a character device (prompts and
// progress output only go to terminals).
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
