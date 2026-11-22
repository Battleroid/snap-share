package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/version"
)

// secretClient talks to a snap server's secrets API. The API key is sent
// only by the create calls, never when opening a link (whose server may not
// be yours). Redirects are not followed, so tickets and tokens never travel
// to another host.
type secretClient struct {
	base string
	key  string
	hc   *http.Client
}

func newSecretClient(base, key string, hc *http.Client) (*secretClient, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("the server URL must be an http(s) URL")
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if hc != nil {
		c.Transport = hc.Transport
	}
	return &secretClient{base: strings.TrimRight(base, "/"), key: key, hc: c}, nil
}

// secretAPIError is a non-2xx answer: the server's sentence and its JSON body.
type secretAPIError struct {
	Status int
	Msg    string
	Body   map[string]any
}

func (e *secretAPIError) Error() string { return e.Msg }

const (
	secretUploadHeader = "X-Snap-Upload-Token" // #nosec G101 -- a header name
	secretTicketHeader = "X-Snap-Ticket"       // #nosec G101 -- a header name
)

func (c *secretClient) request(ctx context.Context, method, path string, body []byte, ctype string, hdr map[string]string, useKey bool) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	// The user's own server, or the server named in the link they open.
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd) // #nosec G704 -- see above
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sp/"+version.Version)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if useKey && c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.hc.Do(req) // #nosec G704 -- see above
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.base, secretStripURL(err))
	}
	if res.StatusCode >= 300 {
		defer func() { _ = res.Body.Close() }()
		ae := &secretAPIError{Status: res.StatusCode, Msg: fmt.Sprintf("server answered %d", res.StatusCode)}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		if json.Unmarshal(b, &ae.Body) == nil {
			if m, ok := ae.Body["error"].(string); ok && m != "" {
				ae.Msg = m
			}
		}
		return nil, ae
	}
	return res, nil
}

// secretStripURL drops the request URL from transport errors: it can carry a
// secret id.
func secretStripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (c *secretClient) json(ctx context.Context, method, path string, in, out any, hdr map[string]string, useKey bool) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	res, err := c.request(ctx, method, path, body, "application/json", hdr, useKey)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("unexpected answer from the server: %w", err)
	}
	return nil
}

// secretRetry runs fn up to four times while it fails with a network error, a 5xx,
// 408 or 429. fn must send the same bytes each time.
func secretRetry(ctx context.Context, fn func() error) error {
	var err error
	for i := range 4 {
		if err = fn(); err == nil {
			return nil
		}
		var ae *secretAPIError
		if errors.As(err, &ae) && ae.Status < 500 && ae.Status != 408 && ae.Status != 429 {
			return err
		}
		if i < 3 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(500<<i) * time.Millisecond):
			}
		}
	}
	return err
}

type secretCreateReq struct {
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Views     int    `json:"views"`
	ExpiresIn int64  `json:"expires_in"`
	Note      string `json:"note"`
}

type secretCreateResp struct {
	ID          string `json:"id"`
	UploadToken string `json:"upload_token"`
	Chunks      int64  `json:"chunks"`
	CTSize      int64  `json:"ct_size"`
}

func (c *secretClient) create(ctx context.Context, r secretCreateReq) (secretCreateResp, error) {
	var out secretCreateResp
	err := c.json(ctx, http.MethodPost, "/api/v1/secrets", r, &out, nil, true)
	return out, err
}

func (c *secretClient) putChunk(ctx context.Context, id, token string, n int64, ct []byte) error {
	return secretRetry(ctx, func() error {
		res, err := c.request(ctx, http.MethodPut, fmt.Sprintf("/api/v1/secrets/%s/chunks/%d", id, n), ct,
			"application/octet-stream", map[string]string{secretUploadHeader: token}, false)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		return res.Body.Close()
	})
}

type secretFinalizeReq struct {
	Salt   string         `json:"salt"`
	KDF    *e2e.KDFParams `json:"kdf"`
	HProbe string         `json:"h_probe"`
	HAuth  string         `json:"h_auth"`
	Commit string         `json:"commit"`
	MetaCT string         `json:"meta_ct"`
}

type secretFinalizeResp struct {
	ExpiresAt string `json:"expires_at"`
	Views     int    `json:"views"`
}

func (c *secretClient) finalize(ctx context.Context, id, token string, r secretFinalizeReq) (secretFinalizeResp, error) {
	var out secretFinalizeResp
	err := secretRetry(ctx, func() error {
		return c.json(ctx, http.MethodPost, "/api/v1/secrets/"+id+"/finalize", r, &out, map[string]string{secretUploadHeader: token}, false)
	})
	return out, err
}

type secretInfo struct {
	State     string         `json:"state"`
	Mode      string         `json:"mode"`
	Kind      string         `json:"kind"`
	Salt      string         `json:"salt"`
	KDF       *e2e.KDFParams `json:"kdf"`
	Size      int64          `json:"size"`
	CTSize    int64          `json:"ct_size"`
	ViewsLeft int            `json:"views_left"`
	ExpiresAt string         `json:"expires_at"`
}

// secretGone is a tombstone: the secret was opened, burned, expired or revoked.
type secretGone struct {
	State, Reason, OpenedAt string
}

func (g *secretGone) Error() string {
	switch {
	case g.State == "expired":
		return "this secret has expired"
	case g.State == "revoked":
		return "the sender revoked this secret"
	case g.Reason == "failed attempts":
		return "this secret was destroyed after too many wrong passphrases"
	case g.OpenedAt != "":
		if t, err := time.Parse(time.RFC3339, g.OpenedAt); err == nil {
			return "this secret was already opened, at " + t.Local().Format("2006-01-02 15:04")
		}
	}
	return "this secret was already opened"
}

var errNoSecret = errors.New("nothing here: the link is wrong, or the secret was deleted")

// secretAsGone turns 404 and 410 answers into errNoSecret and *secretGone.
func secretAsGone(err error) error {
	var ae *secretAPIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Status {
	case http.StatusNotFound:
		return errNoSecret
	case http.StatusGone:
		s, _ := ae.Body["state"].(string)
		r, _ := ae.Body["burn_reason"].(string)
		o, _ := ae.Body["opened_at"].(string)
		return &secretGone{State: s, Reason: r, OpenedAt: o}
	}
	return err
}

func (c *secretClient) info(ctx context.Context, id, probe string) (secretInfo, error) {
	var out secretInfo
	err := c.json(ctx, http.MethodPost, "/api/v1/secrets/"+id+"/info", map[string]string{"probe": probe}, &out, nil, false)
	return out, secretAsGone(err)
}

type secretClaim struct {
	Kind      string `json:"kind"`
	Commit    string `json:"commit"`
	MetaCT    string `json:"meta_ct"`
	ViewsLeft int    `json:"views_left"`
	CTSize    int64  `json:"ct_size"`
	Content   string `json:"content"`
	Ticket    string `json:"ticket"`
}

// secretWrongPass is a refused claim.
type secretWrongPass struct {
	left   int
	burned bool
}

func (w *secretWrongPass) Error() string {
	if w.burned || w.left <= 0 {
		return "wrong passphrase; the secret has now been destroyed"
	}
	return fmt.Sprintf("wrong passphrase; %d %s left", w.left, secretPlural(w.left, "attempt", "attempts"))
}

func (c *secretClient) claim(ctx context.Context, id, probe, auth string) (secretClaim, error) {
	var out secretClaim
	err := c.json(ctx, http.MethodPost, "/api/v1/secrets/"+id+"/claim", map[string]string{"probe": probe, "auth": auth}, &out, nil, false)
	var ae *secretAPIError
	if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
		left, _ := ae.Body["attempts_left"].(float64)
		state, _ := ae.Body["state"].(string)
		return out, &secretWrongPass{left: int(left), burned: state == "burned"}
	}
	return out, secretAsGone(err)
}

func (c *secretClient) blob(ctx context.Context, id, ticket string) (io.ReadCloser, error) {
	res, err := c.request(ctx, http.MethodGet, "/api/v1/secrets/"+id+"/blob", nil, "", map[string]string{secretTicketHeader: ticket}, false)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (c *secretClient) done(ctx context.Context, id, ticket string) error {
	res, err := c.request(ctx, http.MethodPost, "/api/v1/secrets/"+id+"/done", nil, "", map[string]string{secretTicketHeader: ticket}, false)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func secretPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
