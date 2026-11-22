package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

// sniffLen is how much of the file Sniff reads. Enough for every signature
// below, including three 192-byte MPEG-TS packets and an EBML header.
const sniffLen = 4096

const mimeOctet = "application/octet-stream"

var typeOther = Type{MIME: mimeOctet, Kind: KindOther}

// Sniff classifies a file by its content.
//
// Magic bytes decide first, then http.DetectContentType. The filename
// extension is used only to break ties the bytes cannot settle (weak MP3/AAC
// frame syncs, audio-only Matroska/MP4); it never turns text or unknown
// bytes into a media or executable type. Text, including HLS, ffconcat and
// SDP playlists, always comes back as KindText "text/plain" with an empty
// Container, so it is never handed to ffmpeg.
func Sniff(r io.ReaderAt, size int64, filename string) (Type, error) {
	if size < 0 {
		return Type{}, errors.New("media: sniff: negative size")
	}
	buf := make([]byte, min(size, sniffLen))
	if len(buf) > 0 {
		n, err := r.ReadAt(buf, 0)
		if err != nil && (!errors.Is(err, io.EOF) || n == 0) {
			return Type{}, fmt.Errorf("media: sniff: %w", err)
		}
		buf = buf[:n]
	}
	if len(buf) == 0 {
		return typeOther, nil
	}
	ext := extOf(filename)
	if t, ok := sniffMagic(r, size, buf, ext); ok {
		return t, nil
	}
	return sniffFallback(buf, int64(len(buf)) < size), nil
}

// extOf returns the lowercased extension of a client-supplied filename,
// which may use either slash.
func extOf(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(name[i:])
}

func typ(m string, kind Kind, container string) Type {
	return Type{MIME: m, Kind: kind, Container: container}
}

func sniffMagic(r io.ReaderAt, size int64, b []byte, ext string) (Type, bool) {
	has := func(off int, sig string) bool {
		return len(b) >= off+len(sig) && string(b[off:off+len(sig)]) == sig
	}
	switch {
	case has(0, "\xff\xd8\xff"):
		return typ("image/jpeg", KindImage, ""), true
	case has(0, "\x89PNG\r\n\x1a\n"):
		if isAPNG(b) {
			return typ("image/png", KindImage, "apng"), true
		}
		return typ("image/png", KindImage, ""), true
	case has(0, "GIF87a"), has(0, "GIF89a"):
		return typ("image/gif", KindImage, "gif"), true
	case has(0, "RIFF") && len(b) >= 12:
		switch string(b[8:12]) {
		case "WEBP":
			// ffmpeg 8.1 cannot decode animated WebP; it is handled in Go.
			return typ("image/webp", KindImage, ""), true
		case "WAVE":
			return typ("audio/wav", KindAudio, "wav"), true
		case "AVI ":
			return typ("video/x-msvideo", KindVideo, "avi"), true
		}
		return typeOther, true
	case has(4, "ftyp") && boxOK(b, size, 16):
		return sniffFtyp(b, ext)
	case boxOK(b, size, 8) &&
		(has(4, "moov") || has(4, "mdat") || has(4, "wide") || has(4, "free") || has(4, "skip") || has(4, "pnot")):
		// QuickTime files without an ftyp box.
		return typ("video/quicktime", KindVideo, "mov"), true
	case has(0, "\x1a\x45\xdf\xa3"):
		return sniffEBML(b, ext)
	case has(0, "OggS"):
		return sniffOgg(b), true
	case has(0, "fLaC"):
		return typ("audio/flac", KindAudio, "flac"), true
	case has(0, "ID3"):
		return sniffID3(r, size, b), true
	case has(0, "FLV\x01"):
		return typ("video/x-flv", KindVideo, "flv"), true
	case has(0, "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c"):
		if ext == ".wma" {
			return typ("audio/x-ms-wma", KindAudio, "asf"), true
		}
		return typ("video/x-ms-asf", KindVideo, "asf"), true
	case has(0, "\x00\x00\x01\xba"):
		return typ("video/mpeg", KindVideo, "mpeg"), true
	case isMPEGTS(b):
		return typ("video/mp2t", KindVideo, "mpegts"), true
	case has(0, "FORM") && (has(8, "AIFF") || has(8, "AIFC")):
		return typ("audio/aiff", KindAudio, "aiff"), true
	case has(0, "MThd"):
		return typ("audio/midi", KindAudio, ""), true
	case len(b) >= 7 && b[0] == 0xff && b[1]&0xf6 == 0xf0:
		if isADTS(b, ext == ".aac") {
			return typ("audio/aac", KindAudio, "aac"), true
		}
	case len(b) >= 4 && b[0] == 0xff && b[1]&0xe0 == 0xe0:
		if isMP3(b, ext == ".mp3") {
			return typ("audio/mpeg", KindAudio, "mp3"), true
		}
	case has(0, "BM") && len(b) >= 18:
		switch binary.LittleEndian.Uint32(b[14:]) {
		case 12, 40, 52, 56, 64, 108, 124:
			return typ("image/bmp", KindImage, ""), true
		}
	case has(0, "II*\x00"), has(0, "MM\x00*"):
		return typ("image/tiff", KindImage, ""), true
	case has(0, "\x00\x00\x01\x00") && len(b) >= 22 && b[4] > 0 && b[5] == 0:
		return typ("image/x-icon", KindImage, ""), true
	case has(0, "\xff\x0a"), has(0, "\x00\x00\x00\x0cJXL \r\n\x87\n"):
		return typ("image/jxl", KindImage, ""), true
	case has(0, "%PDF-"):
		return typ("application/pdf", KindPDF, ""), true
	case has(0, "PK\x03\x04"), has(0, "PK\x05\x06"), has(0, "PK\x07\x08"):
		return typ("application/zip", KindArchive, ""), true
	case has(0, "\x1f\x8b"):
		return typ("application/gzip", KindArchive, ""), true
	case has(0, "BZh") && len(b) >= 4 && b[3] >= '1' && b[3] <= '9':
		return typ("application/x-bzip2", KindArchive, ""), true
	case has(0, "\xfd7zXZ\x00"):
		return typ("application/x-xz", KindArchive, ""), true
	case has(0, "\x28\xb5\x2f\xfd"):
		return typ("application/zstd", KindArchive, ""), true
	case has(0, "7z\xbc\xaf\x27\x1c"):
		return typ("application/x-7z-compressed", KindArchive, ""), true
	case has(0, "Rar!\x1a\x07"):
		return typ("application/vnd.rar", KindArchive, ""), true
	case has(257, "ustar"):
		return typ("application/x-tar", KindArchive, ""), true
	case has(0, "\x7fELF"), isPE(b), has(0, "\x00asm"),
		has(0, "\xfe\xed\xfa\xce"), has(0, "\xfe\xed\xfa\xcf"),
		has(0, "\xce\xfa\xed\xfe"), has(0, "\xcf\xfa\xed\xfe"), has(0, "\xca\xfe\xba\xbe"):
		return typeOther, true
	}
	return Type{}, false
}

// sniffFtyp classifies ISO base media files by their ftyp brands.
// boxOK reports whether b starts with an ISO BMFF box header whose size is
// at least minSize and fits the file, so text that happens to contain
// "ftyp" or "free" at offset 4 is not taken for a video.
func boxOK(b []byte, size int64, minSize uint32) bool {
	if len(b) < 8 {
		return false
	}
	n := binary.BigEndian.Uint32(b)
	return n >= minSize && int64(n) <= size
}

// isPE reports a Windows PE executable ("MZ" plus the "PE" header it points to).
func isPE(b []byte) bool {
	if len(b) < 0x40 || string(b[:2]) != "MZ" {
		return false
	}
	off := int(binary.LittleEndian.Uint32(b[0x3c:]))
	return off+4 <= len(b) && string(b[off:off+4]) == "PE\x00\x00"
}

func sniffFtyp(b []byte, ext string) (Type, bool) {
	if len(b) < 16 {
		return typeOther, true
	}
	end := len(b)
	if n := int(binary.BigEndian.Uint32(b)); n < end {
		end = n
	}
	if end < 16 {
		return typeOther, true
	}
	major := string(b[8:12])
	brands := []string{major}
	for i := 16; i+4 <= end; i += 4 {
		brands = append(brands, string(b[i:i+4]))
	}
	hasBrand := func(set ...string) bool {
		for _, br := range brands {
			for _, s := range set {
				if br == s {
					return true
				}
			}
		}
		return false
	}
	heif := major == "mif1" || major == "msf1" || major == "miaf"
	switch {
	case major == "avif" || major == "avis" || (heif && hasBrand("avif", "avis")):
		return typ("image/avif", KindImage, "mov"), true
	case major == "heic" || major == "heix" || major == "heim" || major == "heis" ||
		major == "hevc" || major == "hevx" || (heif && hasBrand("heic", "heix", "hevc", "hevx")):
		return typ("image/heic", KindImage, "mov"), true
	case heif:
		return typ("image/heif", KindImage, "mov"), true
	case major == "M4A " || major == "M4B " || major == "M4P " || major == "F4A " || major == "F4B ":
		return typ("audio/mp4", KindAudio, "mov"), true
	case major == "crx ":
		return typeOther, true // Canon raw
	case major == "qt  ":
		return typ("video/quicktime", KindVideo, "mov"), true
	case strings.HasPrefix(major, "3g2"):
		return typ("video/3gpp2", KindVideo, "mov"), true
	case strings.HasPrefix(major, "3g"):
		return typ("video/3gpp", KindVideo, "mov"), true
	case major == "M4V " || major == "M4VH" || major == "M4VP":
		return typ("video/x-m4v", KindVideo, "mov"), true
	}
	if ext == ".m4a" || ext == ".m4b" {
		return typ("audio/mp4", KindAudio, "mov"), true
	}
	return typ("video/mp4", KindVideo, "mov"), true
}

// sniffEBML reads the DocType from a Matroska/WebM EBML header.
func sniffEBML(b []byte, ext string) (Type, bool) {
	p := b[4:]
	hsize, n := ebmlVint(p, false)
	if n == 0 {
		return typeOther, true
	}
	p = p[n:]
	if int64(len(p)) > hsize {
		p = p[:hsize]
	}
	for len(p) > 0 {
		id, n := ebmlVint(p, true)
		if n == 0 {
			break
		}
		p = p[n:]
		sz, n := ebmlVint(p, false)
		if n == 0 || sz > int64(len(p)-n) {
			break
		}
		data := p[n : int64(n)+sz]
		p = p[int64(n)+sz:]
		if id != 0x4282 { // DocType
			continue
		}
		switch string(bytes.TrimRight(data, "\x00")) {
		case "webm":
			if ext == ".weba" {
				return typ("audio/webm", KindAudio, "matroska"), true
			}
			return typ("video/webm", KindVideo, "matroska"), true
		case "matroska":
			if ext == ".mka" {
				return typ("audio/x-matroska", KindAudio, "matroska"), true
			}
			return typ("video/x-matroska", KindVideo, "matroska"), true
		}
		return typeOther, true
	}
	return typeOther, true
}

// ebmlVint decodes an EBML variable-length integer. IDs keep their length
// marker; sizes drop it. n is 0 if p is too short or invalid, or the value
// does not fit in 56 bits.
func ebmlVint(p []byte, keepMarker bool) (v int64, n int) {
	if len(p) == 0 || p[0] == 0 {
		return 0, 0
	}
	n = 1
	for mask := byte(0x80); p[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 7 || len(p) < n {
		return 0, 0
	}
	v = int64(p[0])
	if !keepMarker {
		v &= int64(0xff >> n)
	}
	for i := 1; i < n; i++ {
		v = v<<8 | int64(p[i])
	}
	return v, n
}

// sniffOgg looks at the first packet of the first page.
func sniffOgg(b []byte) Type {
	if len(b) < 27 {
		return typeOther
	}
	data := b[min(27+int(b[26]), len(b)):]
	has := func(sig string) bool { return bytes.HasPrefix(data, []byte(sig)) }
	switch {
	case has("\x01vorbis"), has("OpusHead"), has("\x7fFLAC"), has("Speex   "):
		return typ("audio/ogg", KindAudio, "ogg")
	case has("\x80theora"), has("fishead\x00"), has("\x01video\x00\x00\x00"):
		return typ("video/ogg", KindVideo, "ogg")
	}
	return typeOther
}

// sniffID3 skips an ID3v2 tag and classifies what follows it.
func sniffID3(r io.ReaderAt, size int64, b []byte) Type {
	mp3 := typ("audio/mpeg", KindAudio, "mp3")
	if len(b) < 10 {
		return mp3
	}
	tag := int64(b[6]&0x7f)<<21 | int64(b[7]&0x7f)<<14 | int64(b[8]&0x7f)<<7 | int64(b[9]&0x7f)
	off := 10 + tag
	if b[5]&0x10 != 0 {
		off += 10 // footer
	}
	var next [8]byte
	if off+int64(len(next)) > size {
		return mp3
	}
	if _, err := r.ReadAt(next[:], off); err != nil {
		return mp3
	}
	switch {
	case string(next[:4]) == "fLaC":
		return typ("audio/flac", KindAudio, "flac")
	case next[0] == 0xff && next[1]&0xf6 == 0xf0:
		return typ("audio/aac", KindAudio, "aac")
	}
	return mp3
}

// isAPNG reports whether a PNG has an acTL chunk before its first IDAT.
func isAPNG(b []byte) bool {
	for p := 8; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		switch string(b[p+4 : p+8]) {
		case "acTL":
			return true
		case "IDAT":
			return false
		}
		if n < 0 || n > len(b) {
			return false
		}
		p += 12 + n
	}
	return false
}

func isMPEGTS(b []byte) bool {
	for _, pkt := range []int{188, 192} {
		off := 0
		if pkt == 192 {
			off = 4 // M2TS timestamp prefix
		}
		if len(b) > off+2*pkt && b[off] == 0x47 && b[off+pkt] == 0x47 && b[off+2*pkt] == 0x47 {
			return true
		}
	}
	return false
}

// isADTS validates an AAC ADTS header and, when the buffer allows, the
// header of the following frame. A lone header is accepted only with the
// .aac extension.
func isADTS(b []byte, extOK bool) bool {
	ok := func(h []byte) (int, bool) {
		if len(h) < 7 || h[0] != 0xff || h[1]&0xf6 != 0xf0 || (h[2]>>2)&0xf >= 13 {
			return 0, false
		}
		n := int(h[3]&3)<<11 | int(h[4])<<3 | int(h[5])>>5
		return n, n >= 7
	}
	n, valid := ok(b)
	if !valid {
		return false
	}
	if n+7 <= len(b) {
		_, valid = ok(b[n:])
		return valid
	}
	return extOK
}

var (
	mp3Bitrates = [2][16]int{
		{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}, // MPEG-1 layer III
		{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},     // MPEG-2/2.5 layer III
	}
	mp3Rates = [4][3]int{
		{11025, 12000, 8000},  // MPEG-2.5
		{},                    // reserved
		{22050, 24000, 16000}, // MPEG-2
		{44100, 48000, 32000}, // MPEG-1
	}
)

// mp3Frame returns the length of the MPEG audio layer III frame whose
// header starts h, or 0.
func mp3Frame(h []byte) int {
	if len(h) < 4 || h[0] != 0xff || h[1]&0xe0 != 0xe0 {
		return 0
	}
	ver, layer := (h[1]>>3)&3, (h[1]>>1)&3
	bri, sri, pad := h[2]>>4, (h[2]>>2)&3, int(h[2]>>1)&1
	if ver == 1 || layer != 1 || sri == 3 {
		return 0
	}
	table, coef := 1, 72
	if ver == 3 {
		table, coef = 0, 144
	}
	br := mp3Bitrates[table][bri]
	if br == 0 {
		return 0
	}
	return coef*br*1000/mp3Rates[ver][sri] + pad
}

// isMP3 accepts an MPEG audio layer III frame followed by a second one, or a
// single valid frame header with the .mp3 extension.
func isMP3(b []byte, extOK bool) bool {
	n := mp3Frame(b)
	if n == 0 {
		return false
	}
	if n+4 <= len(b) {
		return mp3Frame(b[n:]) != 0
	}
	return extOK
}

// sniffFallback handles content without a known signature. truncated says
// the buffer is a prefix of a longer file.
func sniffFallback(b []byte, truncated bool) Type {
	if isText(b, truncated) {
		return Type{MIME: "text/plain; charset=utf-8", Kind: KindText}
	}
	ct := http.DetectContentType(b)
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return typeOther
	}
	switch {
	case mt == "text/plain" && strings.HasPrefix(params["charset"], "utf-16"):
		return Type{MIME: ct, Kind: KindText}
	case mt == "text/plain":
		// Not UTF-8 (isText said no), so the charset is unknown.
		return Type{MIME: "text/plain", Kind: KindText}
	case strings.HasPrefix(mt, "image/"):
		return typ(mt, KindImage, "")
	case strings.HasPrefix(mt, "audio/"):
		return typ(mt, KindAudio, "")
	case strings.HasPrefix(mt, "video/"):
		return typ(mt, KindVideo, "")
	case strings.HasPrefix(mt, "font/"), mt == "application/vnd.ms-fontobject":
		return typ(mt, KindOther, "")
	case mt == "application/x-rar-compressed":
		return typ("application/vnd.rar", KindArchive, "")
	}
	// Includes text/html and text/xml whose prefix looked like markup but
	// whose bytes are not text.
	return typeOther
}

// isText reports whether b is UTF-8 without control characters other than
// common whitespace and ESC. A rune cut off at the end of a truncated buffer
// is allowed.
func isText(b []byte, truncated bool) bool {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	for i := 0; i < len(b); {
		c := b[i]
		if c < utf8.RuneSelf {
			if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != '\v' && c != 0x1b || c == 0x7f {
				return false
			}
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n <= 1 {
			return truncated && len(b)-i < utf8.UTFMax && !utf8.FullRune(b[i:])
		}
		i += n
	}
	return true
}
