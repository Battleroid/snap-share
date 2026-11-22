package items

import (
	"database/sql"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Battleroid/snap-share/internal/web"
)

// MaxLinkLength caps short link targets.
const MaxLinkLength = 2048

var errLinkScheme = web.Errorf(http.StatusBadRequest, "Links must be http or https URLs.")

// NormalizeLink validates a short link target and returns its canonical form:
// http or https only, a host, no user info, lowercase scheme and host, no
// default port, at most MaxLinkLength characters.
func NormalizeLink(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", web.Errorf(http.StatusBadRequest, "Enter a URL.")
	}
	if len(raw) > MaxLinkLength {
		return "", web.Errorf(http.StatusBadRequest, "The URL is longer than %d characters.", MaxLinkLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", web.Errorf(http.StatusBadRequest, "That is not a valid URL.")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errLinkScheme
	}
	if u.Opaque != "" || u.Host == "" {
		return "", web.Errorf(http.StatusBadRequest, "The URL needs a host.")
	}
	if u.User != nil {
		return "", web.Errorf(http.StatusBadRequest, "Links with a user name or password are not allowed.")
	}
	host, port := u.Hostname(), u.Port()
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || strings.ContainsAny(host, " \\%") {
		return "", web.Errorf(http.StatusBadRequest, "The URL needs a host.")
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	u.Scheme = scheme
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	out := u.String()
	if len(out) > MaxLinkLength {
		return "", web.Errorf(http.StatusBadRequest, "The URL is longer than %d characters.", MaxLinkLength)
	}
	return out, nil
}

type linkReq struct {
	URL string `json:"url"`
	options
}

// createLink handles POST /api/v1/links (JSON, a form, or the URL as text/plain).
func (s *Service) createLink(w http.ResponseWriter, r *http.Request) error {
	p, err := creator(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var req linkReq
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mt {
	case "application/json":
		if err := web.DecodeJSON(w, r, 16<<10, &req); err != nil {
			return err
		}
	case "application/x-www-form-urlencoded":
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			return web.Errorf(http.StatusBadRequest, "Malformed form body.")
		}
		req.URL = r.PostForm.Get("url")
		formOptions(&req.options, r.PostForm)
	default:
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			return web.Errorf(http.StatusRequestEntityTooLarge, "The URL is too long.")
		}
		req.URL = string(b)
		req.options = queryOptions(r)
	}
	target, err := NormalizeLink(req.URL)
	if err != nil {
		return err
	}
	if _, err := s.userLimits(ctx, s.d.DB.R, p.UserID); err != nil {
		return err
	}
	prefs, err := LoadPrefs(ctx, s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	opts, err := s.resolve(ctx, prefs, req.options, KindLink)
	if err != nil {
		return err
	}
	exp, err := s.expiresAt(opts)
	if err != nil {
		return err
	}
	n := NewItem{
		OwnerID: p.UserID, Kind: KindLink, Title: opts.Title, TargetURL: target, Visibility: opts.Visibility,
		PasswordHash: opts.PasswordHash, ExpiresAt: exp, ExpiryAction: opts.ExpiryAction, Naming: opts.Naming,
	}
	var it *Item
	var tok string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		it, tok, err = s.InsertTx(ctx, tx, n)
		return err
	})
	if err != nil {
		var ae *web.APIError
		if errors.As(err, &ae) {
			return ae
		}
		return err
	}
	s.respondCreated(w, r, it, tok)
	return nil
}
