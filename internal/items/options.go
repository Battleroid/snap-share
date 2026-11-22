package items

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

var (
	errSlugInvalid  = web.Errorf(http.StatusBadRequest, "Use 1-64 letters, digits, - and _ for the name.")
	errSlugReserved = web.Errorf(http.StatusBadRequest, "That name is reserved.")
	errExpiry       = web.Errorf(http.StatusBadRequest, "Expiry must be never, 1h, 1d, 7d, 30d, 90d or an RFC 3339 time.")
)

// Prefs are a user's upload defaults, stored in users.prefs.
type Prefs struct {
	Naming        string `json:"naming"`         // random | words | original
	RandomLength  int    `json:"random_length"`  // 8-32
	Visibility    string `json:"visibility"`     // unlisted | private
	Expiry        string `json:"expiry"`         // never | 1h | 1d | 7d | 30d | 90d
	StripLocation bool   `json:"strip_location"` // stored; the media track applies it
	EmbedTitle    string `json:"embed_title"`    // stored; the embeds track uses it
	EmbedColor    string `json:"embed_color"`    // #rrggbb
	DirectLink    bool   `json:"direct_link"`    // API "url" is the raw link for files
}

// ExpiryChoices are the relative expiries offered in forms.
var ExpiryChoices = []string{"never", "1h", "1d", "7d", "30d", "90d"}

var expiryDurations = map[string]time.Duration{
	"1h": time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (p Prefs) normalized() Prefs {
	switch p.Naming {
	case NamingRandom, NamingWords, NamingOriginal:
	default:
		p.Naming = NamingRandom
	}
	if p.RandomLength < MinRandomLength || p.RandomLength > MaxRandomLength {
		p.RandomLength = DefaultRandomLength
	}
	if p.Visibility != string(Private) {
		p.Visibility = string(Unlisted)
	}
	if _, ok := expiryDurations[p.Expiry]; !ok {
		p.Expiry = "never"
	}
	if !colorRe.MatchString(p.EmbedColor) {
		p.EmbedColor = ""
	}
	p.EmbedTitle = cleanText(p.EmbedTitle, 120)
	return p
}

// LoadPrefs reads a user's defaults (normalized; defaults when unset or the user is missing).
func LoadPrefs(ctx context.Context, d *db.DB, userID int64) (Prefs, error) {
	var raw string
	err := d.R.QueryRowContext(ctx, `SELECT prefs FROM users WHERE id = ?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Prefs{}.normalized(), nil
	}
	if err != nil {
		return Prefs{}.normalized(), err
	}
	var p Prefs
	_ = json.Unmarshal([]byte(raw), &p)
	return p.normalized(), nil
}

// SavePrefs merges p into users.prefs, keeping keys other packages store there.
func SavePrefs(ctx context.Context, d *db.DB, userID int64, p Prefs, now time.Time) error {
	b, err := json.Marshal(p.normalized())
	if err != nil {
		return err
	}
	return d.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET prefs = json_patch(prefs, ?), updated_at = ? WHERE id = ?`, string(b), db.Millis(now), userID)
		return err
	})
}

// parseExpiry resolves an expiry spec at now, capped by maxDays (0: no cap).
// It returns nil for never.
func parseExpiry(spec string, now time.Time, maxDays int) (*time.Time, error) {
	spec = strings.TrimSpace(spec)
	var t time.Time
	switch d, ok := expiryDurations[spec]; {
	case spec == "" || spec == "never":
	case ok:
		t = now.Add(d)
	default:
		var err error
		if t, err = time.Parse(time.RFC3339, spec); err != nil {
			return nil, errExpiry
		}
		if !t.After(now) {
			return nil, web.Errorf(http.StatusBadRequest, "That expiry is in the past.")
		}
	}
	if maxDays > 0 {
		limit := now.Add(time.Duration(maxDays) * 24 * time.Hour)
		if t.IsZero() || t.After(limit) {
			t = limit
		}
	}
	if t.IsZero() {
		return nil, nil
	}
	t = t.UTC().Truncate(time.Millisecond)
	return &t, nil
}

func validExpirySpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if _, ok := expiryDurations[spec]; ok || spec == "" || spec == "never" {
		return true
	}
	_, err := time.Parse(time.RFC3339, spec)
	return err == nil
}

// cleanText strips control characters and trims to max runes.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isBidiControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// options are the creation options every route accepts.
type options struct {
	Title        string `json:"title"`
	Expires      string `json:"expires"`
	ExpiryAction string `json:"expiry_action"`
	Visibility   string `json:"visibility"`
	Password     string `json:"password"`
	Naming       string `json:"naming"`
	Slug         string `json:"slug"`
	Length       int    `json:"length"`
}

// set assigns a form field by name (multipart, query or form bodies).
func (o *options) set(k, v string) bool {
	switch k {
	case "title":
		o.Title = v
	case "expires", "expiry":
		o.Expires = v
	case "expiry_action":
		o.ExpiryAction = v
	case "visibility":
		o.Visibility = v
	case "password":
		o.Password = v
	case "naming":
		o.Naming = v
	case "slug":
		o.Slug = v
	case "length":
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' || n > 1000 {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		o.Length = n
	default:
		return false
	}
	return true
}

// resolved are validated options; they are stored with chunked uploads, so
// the password is kept only as its hash.
type resolved struct {
	Title        string     `json:"title,omitempty"`
	Expires      string     `json:"expires,omitempty"` // spec, resolved again at creation
	ExpiryAction string     `json:"expiry_action"`
	Visibility   Visibility `json:"visibility"`
	PasswordHash string     `json:"password_hash,omitempty"`
	Naming       Naming     `json:"naming"`
}

func (s *Service) resolve(ctx context.Context, p Prefs, o options, kind Kind) (resolved, error) {
	r := resolved{Title: cleanText(o.Title, 200)}
	r.Expires = strings.TrimSpace(o.Expires)
	if r.Expires == "" {
		r.Expires = p.Expiry
	}
	if !validExpirySpec(r.Expires) {
		return r, errExpiry
	}
	if _, err := parseExpiry(r.Expires, s.now(), MaxExpiryDays.Get(s.d.Settings)); err != nil {
		return r, err
	}
	switch o.ExpiryAction {
	case "", ExpireDelete:
		r.ExpiryAction = ExpireDelete
	case ExpirePrivate:
		r.ExpiryAction = ExpirePrivate
	default:
		return r, web.Errorf(http.StatusBadRequest, "Expiry action must be delete or private.")
	}
	switch o.Visibility {
	case "":
		r.Visibility = Visibility(p.Visibility)
	case string(Unlisted), string(Private):
		r.Visibility = Visibility(o.Visibility)
	default:
		return r, web.Errorf(http.StatusBadRequest, "Visibility must be unlisted or private.")
	}
	if o.Password != "" {
		if len(o.Password) > 256 {
			return r, web.Errorf(http.StatusBadRequest, "The password is too long.")
		}
		h, err := s.passwords.Hash(o.Password)
		if err != nil {
			return r, err
		}
		r.PasswordHash = h
	}
	if o.Slug != "" {
		if !ValidSlug(o.Slug) {
			return r, errSlugInvalid
		}
		if IsReserved(o.Slug, s.d.ExtraReserved...) {
			return r, errSlugReserved
		}
		taken, err := s.slugTaken(ctx, s.d.DB.R, o.Slug)
		if err != nil {
			return r, err
		}
		if taken {
			return r, errSlugTaken
		}
		r.Naming = Naming{Scheme: NamingCustom, Custom: o.Slug}
		return r, nil
	}
	scheme := o.Naming
	if scheme == "" {
		scheme = p.Naming
	}
	switch scheme {
	case NamingRandom, NamingWords:
	case NamingOriginal:
		if kind == KindLink {
			scheme = NamingRandom
		}
	default:
		return r, web.Errorf(http.StatusBadRequest, "Naming must be random, words or original.")
	}
	length := o.Length
	if length == 0 {
		length = p.RandomLength
	}
	if length < MinRandomLength || length > MaxRandomLength {
		return r, web.Errorf(http.StatusBadRequest, "Random names are 8 to 32 characters long.")
	}
	r.Naming = Naming{Scheme: scheme, Length: length}
	return r, nil
}

// expiresAt resolves the stored spec at creation time.
func (s *Service) expiresAt(r resolved) (*time.Time, error) {
	return parseExpiry(r.Expires, s.now(), MaxExpiryDays.Get(s.d.Settings))
}
