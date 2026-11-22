package keys_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/sharex"
)

// shareX replays a custom uploader the way ShareX sends it: RequestMethod to
// RequestURL with Headers, and a body built from Body (MultipartFormData puts
// the file in FileFormName; JSON substitutes {input}, JSON-escaped, into Data).
type shareX struct {
	t  *testing.T
	e  *env
	up sharex.Uploader
}

type shareXResult struct {
	status                            int
	url, thumbnail, deletion, errText string
}

var jsonSyntax = regexp.MustCompile(`\{json:([^{}]+)\}`)

// resolve fills {json:path} references from the response body.
func resolve(tmpl string, body []byte) string {
	var doc any
	_ = json.Unmarshal(body, &doc)
	return jsonSyntax.ReplaceAllStringFunc(tmpl, func(m string) string {
		v := doc
		for _, k := range strings.Split(jsonSyntax.FindStringSubmatch(m)[1], ".") {
			obj, _ := v.(map[string]any)
			v = obj[k]
		}
		if v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		b, _ := json.Marshal(v)
		return string(b)
	})
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func (x *shareX) send(fileName, contentType string, input []byte) shareXResult {
	x.t.Helper()
	var body io.Reader
	reqCT := ""
	switch x.up.Body {
	case "MultipartFormData":
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		for k, v := range x.up.Arguments {
			_ = mw.WriteField(k, v)
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+x.up.FileFormName+`"; filename="`+fileName+`"`)
		h.Set("Content-Type", contentType)
		pw, _ := mw.CreatePart(h)
		_, _ = pw.Write(input)
		_ = mw.Close()
		body, reqCT = &b, mw.FormDataContentType()
	case "JSON":
		data := strings.NewReplacer("{input}", jsonEscape(string(input)), "{filename}", jsonEscape(fileName)).Replace(x.up.Data)
		body, reqCT = strings.NewReader(data), "application/json"
	default:
		x.t.Fatalf("body %q not replayed", x.up.Body)
	}
	req, err := http.NewRequestWithContext(x.e.ctx, x.up.RequestMethod, x.up.RequestURL, body)
	if err != nil {
		x.t.Fatal(err)
	}
	req.Header.Set("Content-Type", reqCT)
	req.Header.Set("User-Agent", "ShareX/15.0.0")
	for k, v := range x.up.Headers {
		req.Header.Set(k, v)
	}
	res, err := x.e.client.Do(req)
	if err != nil {
		x.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	out := shareXResult{status: res.StatusCode}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		out.url, out.thumbnail, out.deletion = resolve(x.up.URL, b), resolve(x.up.ThumbnailURL, b), resolve(x.up.DeletionURL, b)
	} else {
		out.errText = resolve(x.up.ErrorMessage, b)
	}
	return out
}

// download fetches a .sxcu from the integrations page like the browser does.
func download(t *testing.T, e *env, sess opt, kind sharex.Kind) sharex.Uploader {
	t.Helper()
	res, body := e.do("POST", "/settings/integrations/sharex/"+string(kind), nil, sess)
	if res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", kind, res.StatusCode, body)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.HasSuffix(cd, `-`+string(kind)+`.sxcu"`) {
		t.Errorf("%s: Content-Disposition %q", kind, cd)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("%s: Cache-Control %q", kind, res.Header.Get("Cache-Control"))
	}
	// Exactly ShareX's field names, nothing unknown.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"Version": true, "Name": true, "DestinationType": true, "RequestMethod": true, "RequestURL": true,
		"Parameters": true, "Headers": true, "Body": true, "Arguments": true, "FileFormName": true, "Data": true,
		"URL": true, "ThumbnailURL": true, "DeletionURL": true, "ErrorMessage": true}
	for k := range raw {
		if !allowed[k] {
			t.Errorf("%s: field %q is not a ShareX field", kind, k)
		}
	}
	var up sharex.Uploader
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&up); err != nil {
		t.Fatal(err)
	}
	return up
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestShareXReplay(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	sess := withCookie(e.session(ann))

	files := download(t, e, sess, sharex.Files)
	want := sharex.Uploader{
		Version: sharex.Version, DestinationType: "ImageUploader, FileUploader", RequestMethod: "POST",
		RequestURL: e.s.URL + "/api/v1/upload", Body: "MultipartFormData", FileFormName: "file",
		URL: "{json:url}", ThumbnailURL: "{json:thumbnail_url}", DeletionURL: "{json:deletion_url}", ErrorMessage: "{json:error}",
	}
	tok := strings.TrimPrefix(files.Headers["Authorization"], "Bearer ")
	got := files
	got.Name, got.Headers = "", nil
	if !equalUploader(got, want) || !strings.HasPrefix(tok, "snap_") || len(files.Headers) != 1 || files.Name == "" {
		t.Fatalf("files uploader:\n got %+v\nwant %+v", files, want)
	}

	// The minted key can only create items, and is listed with a dated name.
	list, _ := e.s.App.Keys.Store().List(e.ctx, ann.ID)
	if len(list) != 1 || len(list[0].Scopes) != 1 || list[0].Scopes[0] != auth.ScopeItemsWrite ||
		list[0].Name != "ShareX uploader "+time.Now().UTC().Format("2006-01-02") || list[0].ExpiresAt != nil {
		t.Fatalf("minted key %+v", list[0])
	}
	if res, _ := e.do("GET", "/api/v1/items", nil, bearer(tok)); res.StatusCode != http.StatusForbidden {
		t.Errorf("ShareX key reading items: %d", res.StatusCode)
	}

	// Image upload: URL, thumbnail and deletion URL all work.
	x := &shareX{t: t, e: e, up: files}
	img := pngBytes()
	r := x.send("ShareX_2026-11-22.png", "image/png", img)
	if r.status != http.StatusCreated || !strings.HasPrefix(r.url, e.s.URL+"/") || r.thumbnail == "" || !strings.Contains(r.deletion, "/x#") {
		t.Fatalf("image: %+v", r)
	}
	if res, _ := e.do("GET", strings.TrimPrefix(r.url, e.s.URL), nil); res.StatusCode != 200 {
		t.Errorf("image page: %d", res.StatusCode)
	}
	res, body := e.do("GET", strings.TrimPrefix(r.thumbnail, e.s.URL), nil)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		t.Errorf("thumbnail: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if r.thumbnail == r.url || len(body) == 0 {
		t.Error("thumbnail is the page")
	}
	// The deletion URL works without signing in (the page posts the fragment).
	token := r.deletion[strings.Index(r.deletion, "#")+1:]
	if res, m := e.doJSON("POST", "/api/v1/delete", map[string]string{"token": token}); res.StatusCode != 200 || m["deleted"] != true {
		t.Errorf("delete by token: %d %v", res.StatusCode, m)
	}
	if res, _ := e.do("GET", strings.TrimPrefix(r.url, e.s.URL), nil); res.StatusCode != 404 {
		t.Errorf("deleted image: %d", res.StatusCode)
	}

	// Any other file.
	r = x.send("report.pdf", "application/pdf", []byte("%PDF-1.4\n%fake\n"))
	if r.status != http.StatusCreated || r.url == "" || r.deletion == "" {
		t.Errorf("file: %+v", r)
	}

	// Errors surface through ErrorMessage.
	bad := files
	bad.Headers = map[string]string{"Authorization": "Bearer " + flipAt(tok, 30)}
	if r := (&shareX{t: t, e: e, up: bad}).send("a.png", "image/png", img); r.status != 401 || r.errText != "Invalid credentials." {
		t.Errorf("bad key: %+v", r)
	}

	// Text uploader → a paste with exactly the text, quotes and newlines included.
	text := download(t, e, sess, sharex.Text)
	if text.DestinationType != "TextUploader" || text.RequestURL != e.s.URL+"/api/v1/pastes" || text.Body != "JSON" ||
		text.Data != `{"content":"{input}"}` || text.FileFormName != "" {
		t.Fatalf("text uploader %+v", text)
	}
	content := "first \"line\"\n\tsecond\\line ünïcode {input}\n"
	r = (&shareX{t: t, e: e, up: text}).send("ShareX_text.txt", "text/plain", []byte(content))
	if r.status != http.StatusCreated || r.url == "" || r.deletion == "" {
		t.Fatalf("text: %+v", r)
	}
	res, body = e.do("GET", strings.TrimPrefix(r.url, e.s.URL)+"/raw", nil)
	if res.StatusCode != 200 || body != content {
		t.Errorf("paste content: %d %q", res.StatusCode, body)
	}

	// URL shortener → a short link that redirects.
	links := download(t, e, sess, sharex.Links)
	if links.DestinationType != "URLShortener" || links.RequestURL != e.s.URL+"/api/v1/links" || links.Data != `{"url":"{input}"}` {
		t.Fatalf("links uploader %+v", links)
	}
	target := "https://example.com/a/path?q=1&r=\"2\""
	r = (&shareX{t: t, e: e, up: links}).send("", "", []byte(target))
	if r.status != http.StatusCreated || r.url == "" {
		t.Fatalf("link: %+v", r)
	}
	res, _ = e.do("GET", strings.TrimPrefix(r.url, e.s.URL), nil)
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "https://example.com/a/path?q=1&r=") {
		t.Errorf("link redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// Three downloads, three separate keys; unknown kinds and cross-site posts mint nothing.
	if res, _ := e.do("POST", "/settings/integrations/sharex/everything", nil, sess); res.StatusCode != 404 {
		t.Errorf("unknown kind: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/settings/integrations/sharex/files", nil, sess, crossSite()); res.StatusCode != 403 {
		t.Errorf("cross-site download: %d", res.StatusCode)
	}
	if list, _ := e.s.App.Keys.Store().List(e.ctx, ann.ID); len(list) != 3 {
		t.Errorf("keys after downloads: %d", len(list))
	}
}

func equalUploader(a, b sharex.Uploader) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}
