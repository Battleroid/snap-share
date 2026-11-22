package keys_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

func multipartBody(t *testing.T, fields map[string]string, content string) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

// expiresWithin checks an item's expires_at is about d from now.
func expiresWithin(t *testing.T, what string, m map[string]any, d time.Duration) {
	t.Helper()
	s, _ := m["expires_at"].(string)
	exp, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Errorf("%s: expires_at %v", what, m["expires_at"])
		return
	}
	if diff := time.Until(exp) - d; diff > time.Minute || diff < -time.Minute {
		t.Errorf("%s: expires %v, want about %v from now", what, exp, d)
	}
}

// TestKeyConstraintsOnEveryCreationPath: a key that forces private, caps
// expiry at 1 day and uploads at 1000 bytes gets the same treatment from
// every endpoint that creates items.
func TestKeyConstraintsOnEveryCreationPath(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	tok := e.mint(ann, keys.NewKey{
		Scopes:      []auth.Scope{auth.ScopeItemsWrite, auth.ScopeItemsRead},
		Constraints: keys.Constraints{Visibility: "private", MaxExpirySeconds: 86400, MaxUploadBytes: 1000},
	})
	key := bearer(tok)
	day := 24 * time.Hour
	small, big := strings.Repeat("a", 500), strings.Repeat("b", 2000)
	created := func(what string, res *resp, m map[string]any) map[string]any {
		t.Helper()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("%s: %d %v", what, res.StatusCode, m)
		}
		if m["visibility"] != "private" {
			t.Errorf("%s: visibility %v", what, m["visibility"])
		}
		return m
	}

	// Multipart (ShareX, curl -F).
	body, ct := multipartBody(t, map[string]string{"visibility": "unlisted", "expires": "never"}, small)
	res, s := e.do("POST", "/api/v1/upload", body, key, header("Content-Type", ct))
	m := created("multipart", res, decode(t, s))
	expiresWithin(t, "multipart never", m, day)
	body, ct = multipartBody(t, nil, big)
	if res, s := e.do("POST", "/api/v1/upload", body, key, header("Content-Type", ct)); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("multipart over the key's cap: %d %s", res.StatusCode, s)
	}

	// Raw PUT (curl -T).
	res, s = e.do("PUT", "/api/v1/upload/a.txt?visibility=unlisted&expires=30d", strings.NewReader(small), key)
	m = created("raw put", res, decode(t, s))
	expiresWithin(t, "raw put 30d", m, day)
	if res, s := e.do("PUT", "/api/v1/upload/b.txt", strings.NewReader(big), key); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("raw put over the key's cap: %d %s", res.StatusCode, s)
	}

	// Resumable uploads: the cap applies at init, the options at completion.
	if res, m := e.doJSON("POST", "/api/v1/uploads", map[string]any{"name": "big.bin", "size": 2000}, key); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked init over the cap: %d %v", res.StatusCode, m)
	}
	res, m = e.doJSON("POST", "/api/v1/uploads", map[string]any{"name": "c.txt", "size": len(small),
		"options": map[string]any{"visibility": "unlisted", "expires": "never"}}, key)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("chunked init: %d %v", res.StatusCode, m)
	}
	id := m["id"].(string)
	if res, s := e.do("PUT", "/api/v1/uploads/"+id+"?offset=0", strings.NewReader(small), key); res.StatusCode != 200 {
		t.Fatalf("chunked put: %d %s", res.StatusCode, s)
	}
	res, s = e.do("POST", "/api/v1/uploads/"+id+"/complete", nil, key)
	m = created("chunked", res, decode(t, s))
	expiresWithin(t, "chunked never", m, day)

	// Pastes.
	res, m = e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "hello", "visibility": "unlisted", "expires": "never"}, key)
	created("paste", res, m)
	expiresWithin(t, "paste never", m, day)
	if res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": big}, key); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("paste over the cap: %d %v", res.StatusCode, m)
	}

	// Links; a shorter expiry than the cap is kept.
	res, m = e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.com/", "expires": "90d"}, key)
	link := created("link", res, m)
	expiresWithin(t, "link 90d", link, day)
	res, m = e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.com/b", "expires": "1h"}, key)
	created("short link", res, m)
	expiresWithin(t, "link 1h", m, time.Hour)

	// Edits cannot undo the restrictions.
	path := fmt.Sprintf("/api/v1/items/%d", int64(link["id"].(float64)))
	if res, m := e.doJSON("PATCH", path, map[string]any{"visibility": "unlisted"}, key); res.StatusCode != http.StatusForbidden {
		t.Errorf("patch to unlisted: %d %v", res.StatusCode, m)
	}
	res, m = e.doJSON("PATCH", path, map[string]any{"expires": "never"}, key)
	if res.StatusCode != 200 {
		t.Fatalf("patch expiry: %d %v", res.StatusCode, m)
	}
	expiresWithin(t, "patch never", m, day)

	// The same user's browser session is not restricted.
	sess := withCookie(e.session(ann))
	res, m = e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.com/c", "visibility": "unlisted"}, sess)
	if res.StatusCode != http.StatusCreated || m["visibility"] != "unlisted" || m["expires_at"] != nil {
		t.Errorf("session link: %d %v", res.StatusCode, m)
	}
	body, ct = multipartBody(t, nil, big)
	if res, s := e.do("POST", "/api/v1/upload", body, sess, header("Content-Type", ct)); res.StatusCode != http.StatusCreated {
		t.Errorf("session upload over the key's cap: %d %s", res.StatusCode, s)
	}
}
