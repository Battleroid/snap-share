package media

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestSniffGenerated(t *testing.T) {
	t.Parallel()
	v := lavfi("testsrc2=s=160x120:r=10:d=1")
	a := lavfi("sine=d=1")
	av := append(append(append([]string{}, v...), a...), "-shortest")
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast"}
	cases := []struct {
		name string
		args []string
		want Type
	}{
		{"clip.mp4", cat(av, x264, "-c:a", "aac"), Type{"video/mp4", KindVideo, "mov"}},
		{"clip.mov", cat(av, x264, "-c:a", "aac"), Type{"video/quicktime", KindVideo, "mov"}},
		{"clip.3gp", cat(av, x264, "-c:a", "aac", "-ar", "16000"), Type{"video/3gpp", KindVideo, "mov"}},
		{"audio.m4a", cat(a, "-c:a", "aac"), Type{"audio/mp4", KindAudio, "mov"}},
		{"clip.mkv", cat(av, x264, "-c:a", "aac"), Type{"video/x-matroska", KindVideo, "matroska"}},
		{"audio.mka", cat(a, "-c:a", "libopus"), Type{"audio/x-matroska", KindAudio, "matroska"}},
		{"clip.webm", cat(av, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-c:a", "libopus"), Type{"video/webm", KindVideo, "matroska"}},
		{"clip.avi", cat(av, "-c:v", "mpeg4", "-c:a", "mp3"), Type{"video/x-msvideo", KindVideo, "avi"}},
		{"clip.ts", cat(av, x264, "-c:a", "aac"), Type{"video/mp2t", KindVideo, "mpegts"}},
		{"clip.mpg", cat(av, "-r", "25", "-c:v", "mpeg1video", "-c:a", "mp2"), Type{"video/mpeg", KindVideo, "mpeg"}},
		{"clip.flv", cat(av, x264, "-c:a", "aac"), Type{"video/x-flv", KindVideo, "flv"}},
		{"clip.wmv", cat(av, "-c:v", "wmv2", "-c:a", "wmav2", "-f", "asf"), Type{"video/x-ms-asf", KindVideo, "asf"}},
		{"clip.ogv", cat(av, "-c:v", "libtheora", "-c:a", "libvorbis"), Type{"video/ogg", KindVideo, "ogg"}},
		{"id3.mp3", cat(a, "-c:a", "libmp3lame"), Type{"audio/mpeg", KindAudio, "mp3"}},
		{"bare.bin", cat(a, "-c:a", "libmp3lame", "-f", "mp3", "-id3v2_version", "0", "-write_xing", "0"), Type{"audio/mpeg", KindAudio, "mp3"}},
		{"audio.aac", cat(a, "-c:a", "aac", "-f", "adts"), Type{"audio/aac", KindAudio, "aac"}},
		{"audio.flac", a, Type{"audio/flac", KindAudio, "flac"}},
		{"audio.wav", a, Type{"audio/wav", KindAudio, "wav"}},
		{"audio.ogg", cat(a, "-c:a", "libvorbis"), Type{"audio/ogg", KindAudio, "ogg"}},
		{"audio.opus", a, Type{"audio/ogg", KindAudio, "ogg"}},
		{"audio.aiff", a, Type{"audio/aiff", KindAudio, "aiff"}},
		{"anim.gif", v, Type{"image/gif", KindImage, "gif"}},
		{"anim.png", cat(v, "-f", "apng", "-plays", "0"), Type{"image/png", KindImage, "apng"}},
		{"still.png", cat(v, "-frames:v", "1"), Type{"image/png", KindImage, ""}},
		{"still.jpg", cat(v, "-frames:v", "1"), Type{"image/jpeg", KindImage, ""}},
		{"still.webp", cat(v, "-frames:v", "1", "-c:v", "libwebp"), Type{"image/webp", KindImage, ""}},
		{"anim.webp", cat(v, "-c:v", "libwebp_anim"), Type{"image/webp", KindImage, ""}},
		{"still.avif", cat(v, "-frames:v", "1", "-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "8"), Type{"image/avif", KindImage, "mov"}},
		{"still.bmp", cat(v, "-frames:v", "1"), Type{"image/bmp", KindImage, ""}},
		{"still.tiff", cat(v, "-frames:v", "1"), Type{"image/tiff", KindImage, ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := gen(t, c.name, c.args...)
			if got := sniffPath(t, p); got != c.want {
				t.Errorf("Sniff = %+v, want %+v", got, c.want)
			}
		})
	}
}

func cat(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch p := p.(type) {
		case string:
			out = append(out, p)
		case []string:
			out = append(out, p...)
		}
	}
	return out
}

func ftyp(major string, compat ...string) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(16+4*len(compat)))
	b = append(b, "ftyp"+major+"\x00\x00\x00\x00"...)
	for _, c := range compat {
		b = append(b, c...)
	}
	return append(b, make([]byte, 64)...)
}

func ebml(doctype string) []byte {
	body := []byte{0x42, 0x86, 0x81, 0x01} // EBMLVersion 1
	body = append(body, 0x42, 0x82, byte(0x80|len(doctype)))
	body = append(body, doctype...)
	b := append([]byte{0x1a, 0x45, 0xdf, 0xa3, byte(0x80 | len(body))}, body...)
	return append(b, make([]byte, 32)...)
}

func TestSniffBytes(t *testing.T) {
	t.Parallel()
	text := Type{"text/plain; charset=utf-8", KindText, ""}
	mp3Frame := []byte{0xff, 0xfb, 0x90, 0x64} // MPEG-1 layer III 128 kbit/s 44.1 kHz
	pe := make([]byte, 0x80)
	copy(pe, "MZ")
	pe[0x3c] = 0x40
	copy(pe[0x40:], "PE\x00\x00")
	var zipBuf, gzBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	if _, err := zw.Create("a.txt"); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("hello"))
	_ = gw.Close()

	cases := []struct {
		name string
		data []byte
		want Type
	}{
		// Playlists and scripts ffmpeg could follow must stay text.
		{"hls.mp4", []byte("#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nfile:///etc/passwd\n#EXT-X-ENDLIST\n"), text},
		{"bom-hls.mp4", []byte("\xef\xbb\xbf#EXTM3U\n#EXTINF:10,\n/etc/passwd\n"), text},
		{"concat.mp4", []byte("ffconcat version 1.0\nfile /etc/passwd\n"), text},
		{"stream.sdp.mp4", []byte("v=0\no=- 0 0 IN IP4 127.0.0.1\ns=x\nc=IN IP4 127.0.0.1\nm=video 5004 RTP/AVP 96\n"), text},
		{"page.mp4", []byte("<!DOCTYPE html><script>alert(1)</script>"), text},
		{"x.js", []byte("export default 1;\n"), text},
		{"x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), text},
		{"x.html", []byte("<html><body>hi</body></html>"), text},
		{"free.mov", []byte("Fun free stuff, nothing else\n"), text},
		{"ftyp.mp4", []byte("abcdftypisom and more text"), text},
		{"latin1.txt", []byte("caf\xe9 au lait\n"), Type{"text/plain", KindText, ""}},
		{"utf16.txt", []byte("\xff\xfeh\x00i\x00"), Type{"text/plain; charset=utf-16le", KindText, ""}},
		// Empty and truncated.
		{"empty.mp4", nil, typeOther},
		{"short.mp4", []byte("\x00\x00\x00"), typeOther},
		{"trunc-ftyp.mp4", []byte("\x00\x00\x00\x18ftyp"), typeOther},
		{"trunc.png", []byte("\x89PNG\r\n\x1a\n"), Type{"image/png", KindImage, ""}},
		{"trunc.ebml.webm", []byte{0x1a, 0x45, 0xdf, 0xa3}, typeOther},
		{"riff.bin", []byte("RIFF\x10\x00\x00\x00XXXX\x00\x00\x00\x00"), typeOther},
		// ISO BMFF brands.
		{"x.heic", ftyp("heic", "mif1", "heic"), Type{"image/heic", KindImage, "mov"}},
		{"x.heif", ftyp("mif1", "heic"), Type{"image/heic", KindImage, "mov"}},
		{"x.avif", ftyp("mif1", "avif", "miaf"), Type{"image/avif", KindImage, "mov"}},
		{"plain.heif", ftyp("mif1", "miaf"), Type{"image/heif", KindImage, "mov"}},
		{"x.m4a", ftyp("isom", "mp42"), Type{"audio/mp4", KindAudio, "mov"}},
		{"x.mp4", ftyp("isom", "mp42"), Type{"video/mp4", KindVideo, "mov"}},
		{"x.cr3", ftyp("crx ", "isom"), typeOther},
		// EBML doc types.
		{"x.webm", ebml("webm"), Type{"video/webm", KindVideo, "matroska"}},
		{"x.mkv", ebml("matroska"), Type{"video/x-matroska", KindVideo, "matroska"}},
		{"x.mka", ebml("matroska"), Type{"audio/x-matroska", KindAudio, "matroska"}},
		{"x.weba", ebml("webm"), Type{"audio/webm", KindAudio, "matroska"}},
		{"odd.mkv", ebml("evil"), typeOther},
		// Weak audio syncs need a second frame or the extension.
		{"one.mp3", mp3Frame, Type{"audio/mpeg", KindAudio, "mp3"}},
		{"one.bin", mp3Frame, Type{"text/plain", KindText, ""}}, // legacy-charset text, not audio
		{"id3flac.mp3", append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), "fLaCxxxx"...), Type{"audio/flac", KindAudio, "flac"}},
		// Other kinds.
		{"x.pdf", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"), Type{"application/pdf", KindPDF, ""}},
		{"x.zip", zipBuf.Bytes(), Type{"application/zip", KindArchive, ""}},
		{"x.gz", gzBuf.Bytes(), Type{"application/gzip", KindArchive, ""}},
		{"x.elf", []byte("\x7fELF\x02\x01\x01\x00"), typeOther},
		{"x.exe", pe, typeOther},
		{"mz.txt", []byte("MZ is a postcode\n"), text},
		{"x.bin", []byte{0x00, 0x01, 0x02, 0x03, 0xfe}, typeOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := Sniff(bytes.NewReader(c.data), int64(len(c.data)), c.name)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Sniff = %+v, want %+v", got, c.want)
			}
			if got.Kind == KindText && got.Container != "" {
				t.Errorf("text with a container: %+v", got)
			}
		})
	}
}

func TestSniffNeverReturnsActiveTypes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a.html", "a.htm", "a.svg", "a.js", "a.mjs", "a.xml", "a.xhtml", "a.css", "a.wasm", "a.m3u8"} {
		for _, body := range []string{"<html>", "<?xml version=\"1.0\"?><svg/>", "alert(1)", "#EXTM3U\n"} {
			got, err := Sniff(strings.NewReader(body), int64(len(body)), name)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got.MIME, "text/plain") || got.Container != "" {
				t.Errorf("Sniff(%q, %q) = %+v", body, name, got)
			}
		}
	}
}

type failReader struct{}

func (failReader) ReadAt([]byte, int64) (int, error) { return 0, errors.New("disk on fire") }

func TestSniffReadError(t *testing.T) {
	t.Parallel()
	if _, err := Sniff(failReader{}, 100, "x"); err == nil {
		t.Fatal("want error")
	}
	if _, err := Sniff(failReader{}, -1, "x"); err == nil {
		t.Fatal("want error for negative size")
	}
}
