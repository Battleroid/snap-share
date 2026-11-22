// Package sharex generates ShareX custom uploader files (.sxcu) for snap's
// upload, paste and short link endpoints.
//
// Field names follow ShareX's CustomUploaderItem (ShareX 13.7+ syntax, where
// response values are read with {json:path} and the uploaded text or URL is
// {input}).
package sharex

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind selects which uploader to generate.
type Kind string

// Kinds.
const (
	Files Kind = "files" // images and files: ImageUploader, FileUploader
	Text  Kind = "text"  // TextUploader → pastes
	Links Kind = "links" // URLShortener → short links
)

// Kinds lists every kind.
var Kinds = []Kind{Files, Text, Links}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == Files || k == Text || k == Links }

// Version is written into generated files. ShareX migrates files whose
// version is 13.7.1 or older from the old $json:…$ syntax, so it must be newer.
const Version = "15.0.0"

// Uploader is one .sxcu file.
type Uploader struct {
	Version         string            `json:"Version"`
	Name            string            `json:"Name"`
	DestinationType string            `json:"DestinationType"`
	RequestMethod   string            `json:"RequestMethod"`
	RequestURL      string            `json:"RequestURL"`
	Headers         map[string]string `json:"Headers"`
	Body            string            `json:"Body"`
	Arguments       map[string]string `json:"Arguments,omitempty"`
	FileFormName    string            `json:"FileFormName,omitempty"`
	Data            string            `json:"Data,omitempty"`
	URL             string            `json:"URL"`
	ThumbnailURL    string            `json:"ThumbnailURL,omitempty"`
	DeletionURL     string            `json:"DeletionURL,omitempty"`
	ErrorMessage    string            `json:"ErrorMessage"`
}

// New returns the uploader of kind for the instance at origin (scheme://host),
// authenticating with token. name labels it in ShareX's destination list.
func New(kind Kind, origin, name, token string) (Uploader, error) {
	origin = strings.TrimRight(origin, "/")
	u := Uploader{
		Version:       Version,
		Name:          name,
		RequestMethod: "POST",
		Headers:       map[string]string{"Authorization": "Bearer " + token},
		URL:           "{json:url}",
		DeletionURL:   "{json:deletion_url}",
		ErrorMessage:  "{json:error}",
	}
	switch kind {
	case Files:
		// Multipart with the file in the field "file"; the response is the item JSON.
		u.DestinationType = "ImageUploader, FileUploader"
		u.RequestURL = origin + "/api/v1/upload"
		u.Body = "MultipartFormData"
		u.FileFormName = "file"
		u.ThumbnailURL = "{json:thumbnail_url}"
	case Text:
		// ShareX JSON-escapes {input} in JSON bodies; the paste endpoint takes
		// {"content": …}. (With MultipartFormData it would send the text as a
		// file, which makes a file item rather than a paste.)
		u.DestinationType = "TextUploader"
		u.RequestURL = origin + "/api/v1/pastes"
		u.Body = "JSON"
		u.Data = `{"content":"{input}"}`
	case Links:
		u.DestinationType = "URLShortener"
		u.RequestURL = origin + "/api/v1/links"
		u.Body = "JSON"
		u.Data = `{"url":"{input}"}`
	default:
		return Uploader{}, fmt.Errorf("sharex: unknown kind %q", kind)
	}
	return u, nil
}

// Marshal encodes u as the indented JSON ShareX imports.
func (u Uploader) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Filename is the download name for kind on host, safe on every OS
// (a port's colon becomes "-").
func Filename(host string, kind Kind) string {
	host = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '-'
	}, host)
	return host + "-" + string(kind) + ".sxcu"
}
