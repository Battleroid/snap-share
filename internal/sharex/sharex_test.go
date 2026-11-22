package sharex

import (
	"encoding/json"
	"testing"
)

func TestNew(t *testing.T) {
	for _, c := range []struct {
		kind                     Kind
		dest, url, body, fileFld string
		data, thumb              string
	}{
		{Files, "ImageUploader, FileUploader", "https://s.example/api/v1/upload", "MultipartFormData", "file", "", "{json:thumbnail_url}"},
		{Text, "TextUploader", "https://s.example/api/v1/pastes", "JSON", "", `{"content":"{input}"}`, ""},
		{Links, "URLShortener", "https://s.example/api/v1/links", "JSON", "", `{"url":"{input}"}`, ""},
	} {
		u, err := New(c.kind, "https://s.example/", "snap "+string(c.kind), "snap_TOKEN")
		if err != nil {
			t.Fatal(err)
		}
		if u.DestinationType != c.dest || u.RequestURL != c.url || u.Body != c.body || u.FileFormName != c.fileFld ||
			u.Data != c.data || u.ThumbnailURL != c.thumb || u.RequestMethod != "POST" || u.Version != Version ||
			u.Headers["Authorization"] != "Bearer snap_TOKEN" || u.URL != "{json:url}" ||
			u.DeletionURL != "{json:deletion_url}" || u.ErrorMessage != "{json:error}" {
			t.Errorf("%s: %+v", c.kind, u)
		}
		b, err := u.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]any
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if back["Data"] != nil && back["Data"] != c.data {
			t.Errorf("%s: Data round trip %q", c.kind, back["Data"])
		}
		if _, ok := back["Arguments"]; ok {
			t.Errorf("%s: empty Arguments written", c.kind)
		}
	}
	if _, err := New("albums", "https://s.example", "x", "t"); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestFilename(t *testing.T) {
	for host, want := range map[string]string{
		"share.example.com":   "share.example.com-files.sxcu",
		"203.0.113.5:8443":    "203.0.113.5-8443-files.sxcu",
		"[::1]:8443":          "---1--8443-files.sxcu",
		"we/ird\\name\x00.io": "we-ird-name-.io-files.sxcu",
	} {
		if got := Filename(host, Files); got != want {
			t.Errorf("%q: %q want %q", host, got, want)
		}
	}
}
