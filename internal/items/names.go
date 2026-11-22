package items

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxFilenameBytes caps stored filenames.
const MaxFilenameBytes = 255

// SanitizeFilename NFC-normalises name and removes control characters, bidi
// overrides and path separators, trims surrounding spaces and dots, and caps
// the result at MaxFilenameBytes (keeping a short extension). It may return "".
func SanitizeFilename(name string) string {
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "")
	}
	name = norm.NFC.String(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
		case unicode.IsControl(r):
		case isBidiControl(r):
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	s = strings.TrimRight(s, ". ")
	if strings.Trim(s, ".") == "" {
		return ""
	}
	if len(s) > MaxFilenameBytes {
		ext := path.Ext(s)
		if len(ext) > 32 {
			ext = ""
		}
		stem := s[:MaxFilenameBytes-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		s = stem + ext
	}
	return s
}

func isBidiControl(r rune) bool {
	return r == 0x061C || r == 0x200E || r == 0x200F || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

// attrChar is RFC 5987 attr-char.
func attrChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// contentDisposition builds an RFC 6266/5987 header value with an ASCII
// fallback filename and the UTF-8 one.
func contentDisposition(disp, name string) string {
	if name == "" {
		name = "download"
	}
	var ascii strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' || r == ';' || r == '%':
			ascii.WriteByte('_')
		case r < 0x80:
			ascii.WriteRune(r)
		default:
			ascii.WriteByte('_')
		}
	}
	var enc strings.Builder
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(name); i++ {
		c := name[i]
		if attrChar(c) {
			enc.WriteByte(c)
			continue
		}
		enc.WriteByte('%')
		enc.WriteByte(hex[c>>4])
		enc.WriteByte(hex[c&15])
	}
	return disp + `; filename="` + ascii.String() + `"; filename*=UTF-8''` + enc.String()
}

// textTypes refine a sniffed text/plain by extension. Code files stay
// text/plain; the highlighter picks their language from the name.
var textTypes = map[string]string{
	"md": "text/markdown", "markdown": "text/markdown", "mdown": "text/markdown",
	"html": "text/html", "htm": "text/html", "xhtml": "application/xhtml+xml",
	"css": "text/css", "js": "text/javascript", "mjs": "text/javascript", "cjs": "text/javascript",
	"json": "application/json", "xml": "application/xml", "csv": "text/csv", "tsv": "text/tab-separated-values",
}

// sniff picks the stored MIME type from the first bytes of content (magic
// numbers first, then http.DetectContentType). The filename only refines
// text into a more specific text type; it never makes content an image or video.
func sniff(head []byte, filename string) string {
	if t := magic(head); t != "" {
		return t
	}
	t := http.DetectContentType(head)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if strings.HasPrefix(t, "text/") && looksLikeSVG(head) {
		return "image/svg+xml"
	}
	if t == "text/plain" {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
		if tt, ok := textTypes[ext]; ok {
			return tt
		}
	}
	return t
}

func magic(b []byte) string {
	switch {
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		switch string(b[8:12]) {
		case "avif", "avis":
			return "image/avif"
		case "heic", "heix", "heim", "heis", "mif1", "msf1":
			return "image/heic"
		case "M4A ", "M4B ":
			return "audio/mp4"
		case "qt  ":
			return "video/quicktime"
		}
		return "video/mp4"
	case bytes.HasPrefix(b, []byte("\x1a\x45\xdf\xa3")):
		if bytes.Contains(b, []byte("webm")) {
			return "video/webm"
		}
		return "video/x-matroska"
	case bytes.HasPrefix(b, []byte("fLaC")):
		return "audio/flac"
	case bytes.HasPrefix(b, []byte("OggS")):
		switch {
		case bytes.Contains(b, []byte("theora")):
			return "video/ogg"
		case bytes.Contains(b, []byte("OpusHead")), bytes.Contains(b, []byte("vorbis")):
			return "audio/ogg"
		}
		return "application/ogg"
	case bytes.HasPrefix(b, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return "application/zstd"
	case bytes.HasPrefix(b, []byte{0xfd, '7', 'z', 'X', 'Z', 0}):
		return "application/x-xz"
	case bytes.HasPrefix(b, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return "application/x-7z-compressed"
	case len(b) >= 262 && string(b[257:262]) == "ustar":
		return "application/x-tar"
	}
	return ""
}

func looksLikeSVG(b []byte) bool {
	l := bytes.ToLower(b)
	i := bytes.Index(l, []byte("<svg"))
	return i >= 0 && !bytes.Contains(l[:i], []byte("<html")) && !bytes.Contains(l[:i], []byte("<body"))
}

// inlineTypes are served as stored. Everything else is text/plain (anything
// textual, scripts, markup, styles, JSON, wasm) or application/octet-stream,
// so nothing uploaded can run as a script, style, worker or page on the app origin.
var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"image/bmp": true, "image/x-icon": true, "image/vnd.microsoft.icon": true, "image/heic": true,
	"image/svg+xml": true, // sandboxed by the raw CSP; never text/html
	"video/mp4":     true, "video/webm": true, "video/ogg": true, "video/quicktime": true, "video/x-matroska": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/wav": true, "audio/wave": true, "audio/x-wav": true,
	"audio/webm": true, "audio/flac": true, "audio/aac": true, "audio/mp4": true, "audio/x-m4a": true,
	"application/pdf": true, "application/ogg": true,
	"application/zip": true, "application/gzip": true, "application/x-gzip": true, "application/x-tar": true,
	"application/zstd": true, "application/x-xz": true, "application/x-7z-compressed": true,
	"application/x-rar-compressed": true, "application/vnd.rar": true,
}

const textPlain = "text/plain; charset=utf-8"

// servedType maps a stored MIME type to the Content-Type sent for raw content.
func servedType(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch {
	case m == "text/plain":
		return textPlain
	case inlineTypes[m]:
		return m
	case strings.HasPrefix(m, "text/"), strings.HasSuffix(m, "+xml"), strings.HasSuffix(m, "+json"),
		strings.Contains(m, "javascript"), strings.Contains(m, "ecmascript"),
		m == "application/json", m == "application/xml", m == "application/wasm", m == "application/x-sh",
		m == "application/yaml", m == "application/toml", m == "application/sql":
		return textPlain
	}
	return "application/octet-stream"
}

// Viewer kinds for share pages.
const (
	viewImage    = "image"
	viewVideo    = "video"
	viewAudio    = "audio"
	viewPDF      = "pdf"
	viewText     = "text"
	viewMarkdown = "markdown"
	viewFile     = "file"
)

func viewKind(kind Kind, mime, lang string) string {
	if kind == KindPaste {
		if isMarkdownLang(lang) {
			return viewMarkdown
		}
		return viewText
	}
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon", "image/svg+xml":
		return viewImage
	case "video/mp4", "video/webm", "video/ogg", "video/quicktime":
		return viewVideo
	case "application/pdf":
		return viewPDF
	case "text/markdown":
		return viewMarkdown
	}
	switch {
	case strings.HasPrefix(mime, "audio/"):
		return viewAudio
	case servedType(mime) == textPlain:
		return viewText
	}
	return viewFile
}

func isMarkdownLang(lang string) bool {
	switch strings.ToLower(lang) {
	case "markdown", "md", "mkd", "gfm":
		return true
	}
	return false
}

// typeLabel is a short lowercase description for meta lists ("png image").
func typeLabel(it *Item) string {
	switch it.Kind {
	case KindLink:
		return "link"
	case KindPaste:
		if l := it.MetaString("lang"); l != "" && l != "plaintext" {
			return strings.ToLower(l)
		}
		return "text"
	}
	if e := it.Ext(); e != "" {
		return e
	}
	if i := strings.IndexByte(it.MIME, '/'); i > 0 {
		return it.MIME[i+1:]
	}
	return "file"
}
