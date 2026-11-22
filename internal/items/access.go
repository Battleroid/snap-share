package items

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Unlock cookie: one cookie holds a bounded, HMAC-signed list of
// "id:expiry:fingerprint" entries. The fingerprint is derived from the item's
// password hash and nonce, so changing the password invalidates earlier
// unlocks and an unlock never carries over to another item.

const (
	unlockTTL        = 24 * time.Hour
	maxUnlockEntries = 20
	unlockVersion    = "v1"
)

type unlockEntry struct {
	id  int64
	exp int64 // unix seconds
	fp  string
}

func (s *Service) unlockCookieName() string {
	if s.d.Cfg.Secure() {
		return "__Host-snap_unlock"
	}
	return "snap_unlock"
}

func unlockFingerprint(it *Item) string {
	sum := sha256.Sum256([]byte("unlock|" + it.PasswordHash + "|" + it.Nonce))
	return hex.EncodeToString(sum[:4])
}

func mac(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)[:16]
}

func encodeUnlocks(key []byte, list []unlockEntry) string {
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, strconv.FormatInt(e.id, 36)+":"+strconv.FormatInt(e.exp, 36)+":"+e.fp)
	}
	payload := unlockVersion + "|" + strings.Join(parts, ",")
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(mac(key, payload))
}

// decodeUnlocks verifies the value against keys and returns unexpired entries.
func decodeUnlocks(keys [][]byte, value string, now time.Time) []unlockEntry {
	p64, m64, ok := strings.Cut(value, ".")
	if !ok || len(value) > 4096 {
		return nil
	}
	enc := base64.RawURLEncoding
	payload, err1 := enc.DecodeString(p64)
	sig, err2 := enc.DecodeString(m64)
	if err1 != nil || err2 != nil {
		return nil
	}
	valid := false
	for _, k := range keys {
		if hmac.Equal(sig, mac(k, string(payload))) {
			valid = true
			break
		}
	}
	if !valid {
		return nil
	}
	body, ok := strings.CutPrefix(string(payload), unlockVersion+"|")
	if !ok || body == "" {
		return nil
	}
	var out []unlockEntry
	for _, f := range strings.Split(body, ",") {
		a := strings.Split(f, ":")
		if len(a) != 3 {
			return nil
		}
		id, err1 := strconv.ParseInt(a[0], 36, 64)
		exp, err2 := strconv.ParseInt(a[1], 36, 64)
		if err1 != nil || err2 != nil {
			return nil
		}
		if exp > now.Unix() {
			out = append(out, unlockEntry{id: id, exp: exp, fp: a[2]})
		}
	}
	return out
}

func (s *Service) readUnlocks(r *http.Request) []unlockEntry {
	c, err := r.Cookie(s.unlockCookieName())
	if err != nil {
		return nil
	}
	return decodeUnlocks(s.unlockKeys, c.Value, s.now())
}

// unlocked reports whether the request carries a valid unlock for it.
func (s *Service) unlocked(r *http.Request, it *Item) bool {
	fp := unlockFingerprint(it)
	for _, e := range s.readUnlocks(r) {
		if e.id == it.ID && hmac.Equal([]byte(e.fp), []byte(fp)) {
			return true
		}
	}
	return false
}

// addUnlock records an unlock for it in the cookie, dropping the oldest
// entries beyond maxUnlockEntries.
func (s *Service) addUnlock(w http.ResponseWriter, r *http.Request, it *Item) {
	now := s.now()
	list := slices.DeleteFunc(s.readUnlocks(r), func(e unlockEntry) bool { return e.id == it.ID })
	list = append(list, unlockEntry{id: it.ID, exp: now.Add(unlockTTL).Unix(), fp: unlockFingerprint(it)})
	if len(list) > maxUnlockEntries {
		list = list[len(list)-maxUnlockEntries:]
	}
	var last int64
	for _, e := range list {
		last = max(last, e.exp)
	}
	// #nosec G124 -- Secure follows the public URL scheme (off only for plain-http dev setups); HttpOnly and SameSite are set.
	http.SetCookie(w, &http.Cookie{
		Name:     s.unlockCookieName(),
		Value:    encodeUnlocks(s.unlockKeys[0], list),
		Path:     "/",
		MaxAge:   int(last - now.Unix()),
		Secure:   s.d.Cfg.Secure(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// Signed raw URLs: with a separate raw origin, cookies from the app origin do
// not reach it, so protected items get expiring HMAC-signed URLs.

const (
	rawURLTTL    = 6 * time.Hour
	rawURLWindow = time.Hour // expiries round up to this, so URLs stay stable for caching
)

// rawFingerprint binds a signature to the item's protection and to its
// random nonce, so it cannot validate for another item, even one at the
// same id.
func rawFingerprint(it *Item) string {
	sum := sha256.Sum256([]byte(string(it.Visibility) + "|" + it.PasswordHash + "|" + it.Nonce))
	return hex.EncodeToString(sum[:8])
}

func rawMessage(it *Item, exp int64) string {
	return "raw|" + strconv.FormatInt(it.ID, 10) + "|" + strconv.FormatInt(exp, 10) + "|" + rawFingerprint(it)
}

// sign returns the query string ("e=…&s=…") authorising raw access to it.
func (s *Service) sign(it *Item) string {
	exp := s.now().Add(rawURLTTL).Truncate(rawURLWindow).Add(rawURLWindow).Unix()
	sig := base64.RawURLEncoding.EncodeToString(mac(s.rawKeys[0], rawMessage(it, exp)))
	return "e=" + strconv.FormatInt(exp, 10) + "&s=" + sig
}

// validSignature checks the e/s query parameters for it.
func (s *Service) validSignature(q url.Values, it *Item) bool {
	exp, err := strconv.ParseInt(q.Get("e"), 10, 64)
	if err != nil || exp <= s.now().Unix() || exp > s.now().Add(rawURLTTL+2*rawURLWindow).Unix() {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(q.Get("s"))
	if err != nil {
		return false
	}
	msg := rawMessage(it, exp)
	for _, k := range s.rawKeys {
		if hmac.Equal(sig, mac(k, msg)) {
			return true
		}
	}
	return false
}

// onRawHost reports whether r arrived on the separate raw origin.
func (s *Service) onRawHost(r *http.Request) bool {
	return s.d.Cfg.RawURL != nil && strings.EqualFold(r.Host, s.d.Cfg.RawURL.Host)
}

// contentURL builds the URL of an item's content. For a viewer who may open
// a protected item and a separate raw origin, it is signed; otherwise
// protected items point at the app origin, which checks access and redirects.
func (s *Service) contentURL(it *Item, m mode, viewer bool) string {
	raw := s.d.Cfg.RawURL != nil
	base := s.origin()
	if raw && (!it.Protected() || viewer) {
		base = s.d.Cfg.RawOrigin()
	}
	p := "/" + it.Slug
	switch m {
	case modeRaw:
		if e := it.Ext(); e != "" {
			p += "." + e
		} else {
			p += "/raw"
		}
	case modeDownload:
		p += "/download"
	case modeThumb:
		p += "/thumb"
	case modePreview:
		p += "/preview"
	}
	u := base + p
	if raw && viewer && it.Protected() {
		u += "?" + s.sign(it)
	}
	return u
}
