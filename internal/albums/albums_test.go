package albums_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type env struct {
	t      *testing.T
	s      *testutil.Server
	client *http.Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := testutil.NewServer(t)
	if err := local.MinFreeBytes.Set(context.Background(), s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	base := s.Client()
	return &env{t: t, s: s, client: &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect}}
}

func (e *env) user(name string, id int64, scopes ...auth.Scope) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (?, ?, ?, 1, 1, 1)`,
		id, name, name+"@example.test"); err != nil {
		e.t.Fatal(err)
	}
	e.s.Auth.Add(name, auth.Principal{UserID: id, EmailVerified: true})
	if len(scopes) == 0 {
		scopes = []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopeItemsDelete, auth.ScopeAlbumsRead, auth.ScopeAlbumsWrite}
	}
	e.s.Auth.Add(name+"-key", auth.Principal{UserID: id, EmailVerified: true, KeyID: id * 100, Scopes: scopes})
}

type opt func(*http.Request)

func as(name string) opt {
	return func(r *http.Request) {
		if strings.HasSuffix(name, "-key") {
			authtest.Bearer(r, name)
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Del("Origin")
			return
		}
		authtest.Session(r, name)
	}
}

func withCookies(cs []*http.Cookie) opt {
	return func(r *http.Request) {
		for _, c := range cs {
			r.AddCookie(c)
		}
	}
}

type resp struct {
	code    int
	header  http.Header
	cookies []*http.Cookie
}

func (e *env) do(method, path string, body io.Reader, opts ...opt) (*resp, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	for _, o := range opts {
		o(req)
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return &resp{code: res.StatusCode, header: res.Header, cookies: res.Cookies()}, string(b)
}

func (e *env) json(method, path string, v any, opts ...opt) (int, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	opts = append([]opt{func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
	}}, opts...)
	res, s := e.do(method, path, body, opts...)
	m := map[string]any{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			e.t.Fatalf("%s %s: not JSON: %q", method, path, s)
		}
	}
	return res.code, m
}

func (e *env) put(user, name, content string) map[string]any {
	e.t.Helper()
	res, body := e.do("PUT", "/api/v1/upload/"+url.PathEscape(name), strings.NewReader(content), as(user), func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	if res.code != 201 {
		e.t.Fatalf("upload %s: %d %s", name, res.code, body)
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(body), &m)
	return m
}

func id(m map[string]any) int64 { v, _ := m["id"].(float64); return int64(v) }
func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func memberNames(m map[string]any) []string {
	var out []string
	list, _ := m["items"].([]any)
	for _, x := range list {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

const pngData = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00"
const mp4Data = "\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"

func TestAlbumAPI(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1)
	e.user("bob", 2)
	a := e.put("ann", "a.png", pngData)
	b := e.put("ann", "b.png", pngData)
	c := e.put("ann", "c.txt", "c")
	bobItem := e.put("bob", "bob.png", pngData)

	code, al := e.json("POST", "/api/v1/albums", map[string]any{"title": "Trip", "items": []int64{id(b), id(a)}, "expires": "7d", "naming": "words"}, as("ann"))
	if code != 201 || str(al, "kind") != "album" || str(al, "name") != "Trip" || al["count"].(float64) != 2 ||
		!reflect.DeepEqual(memberNames(al), []string{"b.png", "a.png"}) || al["expires_at"] == nil || strings.Count(str(al, "slug"), "-") != 2 {
		t.Fatalf("create: %d %v", code, al)
	}
	albumPath := fmt.Sprintf("/api/v1/albums/%d", id(al))
	// The members' own settings are untouched.
	if _, m := e.json("GET", fmt.Sprintf("/api/v1/items/%d", id(a)), nil, as("ann")); m["expires_at"] != nil || str(m, "visibility") != "unlisted" {
		t.Errorf("member changed: %v", m)
	}

	// Add, remove, reorder.
	code, al = e.json("POST", albumPath+"/items", map[string]any{"add": []int64{id(c), id(a)}}, as("ann"))
	if code != 200 || !reflect.DeepEqual(memberNames(al), []string{"b.png", "a.png", "c.txt"}) {
		t.Errorf("add: %d %v", code, memberNames(al))
	}
	_, al = e.json("POST", albumPath+"/items", map[string]any{"order": []int64{id(c)}}, as("ann"))
	if !reflect.DeepEqual(memberNames(al), []string{"c.txt", "b.png", "a.png"}) {
		t.Errorf("order: %v", memberNames(al))
	}
	_, al = e.json("POST", albumPath+"/items", map[string]any{"remove": []int64{id(b)}, "order": []int64{id(a), id(c)}}, as("ann"))
	if !reflect.DeepEqual(memberNames(al), []string{"a.png", "c.txt"}) {
		t.Errorf("remove+order: %v", memberNames(al))
	}
	for _, body := range []map[string]any{
		{"order": []int64{id(b)}},        // not a member
		{"order": []int64{id(a), id(a)}}, // twice
		{"add": []int64{id(al)}},         // an album
		{"add": []int64{id(a), id(a)}},
		{"bogus": true},
	} {
		if code, m := e.json("POST", albumPath+"/items", body, as("ann")); code != 400 {
			t.Errorf("%v: %d %v", body, code, m)
		}
	}

	// List and get.
	code, list := e.json("GET", "/api/v1/albums", nil, as("ann"))
	if code != 200 || list["total"].(float64) != 1 || list["albums"].([]any)[0].(map[string]any)["count"].(float64) != 2 {
		t.Errorf("list: %v", list)
	}
	if code, m := e.json("GET", albumPath, nil, as("ann-key")); code != 200 || len(memberNames(m)) != 2 {
		t.Errorf("get: %d", code)
	}
	// Items that are not albums are not albums here.
	if code, _ := e.json("GET", fmt.Sprintf("/api/v1/albums/%d", id(a)), nil, as("ann")); code != 404 {
		t.Errorf("item as album: %d", code)
	}

	// Patch: as for items, answering with the album.
	code, m := e.json("PATCH", albumPath, map[string]any{"title": "Trip 2026", "visibility": "private", "slug": "trip-2026", "password": "pw-123456"}, as("ann"))
	if code != 200 || str(m, "name") != "Trip 2026" || str(m, "slug") != "trip-2026" || str(m, "visibility") != "private" || m["password"] != true || len(memberNames(m)) != 2 {
		t.Errorf("patch: %d %v", code, m)
	}
	if code, m := e.json("PATCH", albumPath, map[string]any{"slug": "dashboard"}, as("ann")); code != 400 {
		t.Errorf("reserved slug: %d %v", code, m)
	}
	if code, _ := e.json("PATCH", albumPath, map[string]any{"slug": str(a, "slug")}, as("ann")); code != 409 {
		t.Errorf("taken slug: %d", code)
	}

	// Other users: 404 for everything, and their items cannot be added.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", albumPath, nil},
		{"PATCH", albumPath, map[string]any{"title": "pwned"}},
		{"DELETE", albumPath, nil},
		{"POST", albumPath + "/items", map[string]any{"add": []int64{id(bobItem)}}},
		{"POST", albumPath + "/items", map[string]any{"remove": []int64{id(a)}}},
	} {
		for _, who := range []string{"bob", "bob-key"} {
			if code, _ := e.json(c.method, c.path, c.body, as(who)); code != 404 {
				t.Errorf("%s %s %s: %d", who, c.method, c.path, code)
			}
		}
	}
	if _, list := e.json("GET", "/api/v1/albums", nil, as("bob")); list["total"].(float64) != 0 {
		t.Errorf("bob's list: %v", list)
	}
	if code, _ := e.json("POST", albumPath+"/items", map[string]any{"add": []int64{id(bobItem)}}, as("ann")); code != 404 {
		t.Errorf("ann adds bob's item: %d", code)
	}
	if code, _ := e.json("POST", "/api/v1/albums", map[string]any{"title": "x", "items": []int64{id(a)}}, as("bob")); code != 404 {
		t.Errorf("bob creates with ann's item: %d", code)
	}
	if _, m := e.json("GET", albumPath, nil, as("ann")); str(m, "name") != "Trip 2026" || len(memberNames(m)) != 2 {
		t.Errorf("album changed by others: %v", m)
	}

	// Scopes.
	e.s.Auth.Add("reader", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 9, Scopes: []auth.Scope{auth.ScopeAlbumsRead}})
	e.s.Auth.Add("items", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 10, Scopes: []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite}})
	bearer := func(n string) opt {
		return func(r *http.Request) { authtest.Bearer(r, n); r.Header.Del("Origin"); r.Header.Del("Sec-Fetch-Site") }
	}
	if code, _ := e.json("GET", albumPath, nil, bearer("reader")); code != 200 {
		t.Errorf("reader get: %d", code)
	}
	if code, _ := e.json("POST", albumPath+"/items", map[string]any{"add": []int64{id(b)}}, bearer("reader")); code != 403 {
		t.Errorf("reader write: %d", code)
	}
	if code, _ := e.json("GET", "/api/v1/albums", nil, bearer("items")); code != 403 {
		t.Errorf("items key lists albums: %d", code)
	}

	// Delete: the album goes, its members stay.
	if code, _ := e.json("DELETE", albumPath, nil, as("ann")); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := e.json("GET", fmt.Sprintf("/api/v1/items/%d", id(a)), nil, as("ann")); code != 200 {
		t.Errorf("member after album delete: %d", code)
	}
	var n int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM album_items`).Scan(&n)
	if n != 0 {
		t.Errorf("%d album rows left", n)
	}
	// Deleting a member drops it from albums.
	_, al = e.json("POST", "/api/v1/albums", map[string]any{"title": "Again", "items": []int64{id(a), id(b)}}, as("ann"))
	e.json("DELETE", fmt.Sprintf("/api/v1/items/%d", id(b)), nil, as("ann"))
	if _, m := e.json("GET", fmt.Sprintf("/api/v1/albums/%d", id(al)), nil, as("ann")); !reflect.DeepEqual(memberNames(m), []string{"a.png"}) {
		t.Errorf("after member delete: %v", memberNames(m))
	}
}

// The album page lists only members the viewer could open on their own.
func TestAlbumPageVisibility(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1)
	e.user("bob", 2)
	open := e.put("ann", "open.png", pngData)
	priv := e.put("ann", "private.png", pngData)
	locked := e.put("ann", "locked.png", pngData)
	gone := e.put("ann", "expired.png", pngData)
	video := e.put("ann", "clip.mp4", mp4Data)
	doc := e.put("ann", "doc.txt", "words")
	e.json("PATCH", fmt.Sprintf("/api/v1/items/%d", id(priv)), map[string]any{"visibility": "private"}, as("ann"))
	e.json("PATCH", fmt.Sprintf("/api/v1/items/%d", id(locked)), map[string]any{"password": "pw-123456"}, as("ann"))
	if _, err := e.s.App.DB.W.Exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(time.Now().Add(-time.Minute)), id(gone)); err != nil {
		t.Fatal(err)
	}
	if err := e.s.App.Items.SetMedia(context.Background(), id(video), items.Media{ThumbKey: "poster", PreviewKey: "teaser"}); err != nil {
		t.Fatal(err)
	}
	_, al := e.json("POST", "/api/v1/albums", map[string]any{"title": "Mixed", "items": []int64{id(open), id(priv), id(locked), id(gone), id(video), id(doc)}}, as("ann"))
	page := "/" + str(al, "slug")
	shown := func(body string) []string {
		var out []string
		for _, m := range regexp.MustCompile(`class="pb-media__title app-mtile__name" href="[^"]*" title="[^"]*" data-sort-label>([^<]+)<`).FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}

	// Anonymous and other users: unlisted, unexpired, unlocked only.
	for _, who := range []opt{func(*http.Request) {}, as("bob")} {
		res, body := e.do("GET", page, nil, who)
		if res.code != 200 || !reflect.DeepEqual(shown(body), []string{"open.png", "clip.mp4", "doc.txt"}) {
			t.Errorf("visitor sees %v (%d)", shown(body), res.code)
		}
		for _, hidden := range []string{"private.png", "locked.png", "expired.png", str(priv, "slug"), str(locked, "slug"), str(gone, "slug")} {
			if strings.Contains(body, hidden) {
				t.Errorf("visitor page names %q", hidden)
			}
		}
		// Stats are the owner's: no view counts on the page or its tiles.
		if !strings.Contains(body, "<dt>items</dt>") || strings.Contains(body, "<dt>views</dt>") || strings.Contains(body, "/stats") {
			t.Error("visitor album page shows a view count or a stats link")
		}
		if strings.Contains(body, "js/album.js") || strings.Contains(body, "album-remove") || strings.Contains(body, "data-sortable") {
			t.Error("owner tools shown to a visitor")
		}
	}
	// Viewer (the kit ImageViewer: members are a[data-lightbox-item] whose
	// href is the file) and hover-preview markup. A text file's tile links
	// to its page and is not a member.
	_, body := e.do("GET", page, nil)
	if !regexp.MustCompile(`href="[^"]*/`+str(open, "slug")+`\.png" data-lightbox-item data-kind="image" data-name="open.png"`).MatchString(body) ||
		!regexp.MustCompile(`href="[^"]*/`+str(video, "slug")+`\.mp4" data-lightbox-item data-kind="video" data-name="clip.mp4" data-poster="[^"]*/thumb" data-preview="[^"]*/`+str(video, "slug")+`/preview"`).MatchString(body) ||
		!strings.Contains(body, `href="`+e.s.URL+"/"+str(doc, "slug")+`" data-name="doc.txt"`) ||
		regexp.MustCompile(`href="[^"]*/`+str(doc, "slug")+`[^"]*" data-lightbox-item`).MatchString(body) ||
		strings.Count(body, "data-lightbox-item") != 2 || !strings.Contains(body, "js/share.js") || !strings.Contains(body, ` data-lightbox>`) {
		t.Errorf("viewer markup")
	}
	// Two pictures visible: a single cover, not the three-up grid.
	if strings.Contains(body, "pb-media__frame--grid") || strings.Count(body, "<img") != 3 {
		t.Error("visitor cover")
	}

	// After unlocking the password member in this browser, it shows.
	res, _ := e.do("POST", "/unlock/"+str(locked, "slug"), strings.NewReader("password=pw-123456"), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if res.code != 303 || len(res.cookies) == 0 {
		t.Fatalf("unlock: %d", res.code)
	}
	_, body = e.do("GET", page, nil, withCookies(res.cookies))
	if got := shown(body); !reflect.DeepEqual(got, []string{"open.png", "locked.png", "clip.mp4", "doc.txt"}) {
		t.Errorf("after unlock: %v", got)
	}

	// The owner sees everything, marked.
	_, body = e.do("GET", page, nil, as("ann"))
	if got := shown(body); len(got) != 6 || !strings.Contains(body, ">private<") || !strings.Contains(body, ">password<") ||
		!strings.Contains(body, ">expired<") || !strings.Contains(body, "js/album.js") || !strings.Contains(body, `data-row-action="album-remove"`) || !strings.Contains(body, "data-sortable") {
		t.Errorf("owner sees %v", got)
	}
	if !strings.Contains(body, `pb-media__frame--grid`) || !strings.Contains(body, `<span class="pb-media__more">+3</span>`) {
		t.Error("owner cover grid")
	}

	// The album's own password guards the album page only.
	e.json("PATCH", fmt.Sprintf("/api/v1/albums/%d", id(al)), map[string]any{"password": "album-pw"}, as("ann"))
	res, body = e.do("GET", page, nil)
	if res.code != 200 || !strings.Contains(body, `action="/unlock/`+str(al, "slug")+`"`) || strings.Contains(body, "open.png") {
		t.Errorf("album password: %d", res.code)
	}
	if res, _ := e.do("GET", "/"+str(open, "slug"), nil); res.code != 200 {
		t.Errorf("member behind album password: %d", res.code)
	}
	res, _ = e.do("POST", "/unlock/"+str(al, "slug"), strings.NewReader("password=album-pw"), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	})
	if _, body := e.do("GET", page, nil, withCookies(res.cookies)); !reflect.DeepEqual(shown(body), []string{"open.png", "clip.mp4", "doc.txt"}) {
		t.Errorf("unlocked album: %v", shown(body))
	}

	// A private or expired album is gone for others; members are unaffected.
	e.json("PATCH", fmt.Sprintf("/api/v1/albums/%d", id(al)), map[string]any{"password": nil, "visibility": "private"}, as("ann"))
	if res, _ := e.do("GET", page, nil, as("bob")); res.code != 404 {
		t.Errorf("private album: %d", res.code)
	}
	if res, _ := e.do("GET", page, nil, as("ann")); res.code != 200 {
		t.Errorf("owner private album: %d", res.code)
	}
	e.json("PATCH", fmt.Sprintf("/api/v1/albums/%d", id(al)), map[string]any{"visibility": "unlisted"}, as("ann"))
	if _, err := e.s.App.DB.W.Exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(time.Now().Add(-time.Second)), id(al)); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do("GET", page, nil); res.code != 404 {
		t.Errorf("expired album: %d", res.code)
	}
	if res, _ := e.do("GET", "/"+str(open, "slug"), nil); res.code != 200 {
		t.Errorf("member of expired album: %d", res.code)
	}
	// Albums have no raw content.
	if res, _ := e.do("GET", page+"/raw", nil, as("ann")); res.code != 404 {
		t.Errorf("album raw: %d", res.code)
	}
}
