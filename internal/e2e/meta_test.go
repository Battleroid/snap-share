package e2e

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMetaRoundTrip(t *testing.T) {
	key := tb32("meta rt")
	tests := []Meta{
		{Kind: KindText, Type: "text/plain; charset=utf-8", Size: 11},
		{V: 1, Kind: KindFile, Name: "photo.jpg", Type: "image/jpeg", Size: 123456},
		{Kind: KindFile, Name: "\u00FCber \"quoted\" \\ <tag> & \u2028 \U0001F600\x00", Type: "", Size: 0},
		{Kind: KindFile, Name: "max", Size: MaxSize},
	}
	for _, m := range tests {
		ct, err := SealMeta(key, m)
		if err != nil {
			t.Fatalf("%+v: %v", m, err)
		}
		if len(ct) != SealedMetaSize {
			t.Fatalf("sealed size %d", len(ct))
		}
		got, err := OpenMeta(key, ct)
		want := m
		want.V = 1
		if err != nil || got != want {
			t.Fatalf("OpenMeta = %+v, %v; want %+v", got, err, want)
		}
		ct2, err := SealMeta(key, m)
		if err != nil || bytes.Equal(ct[:NonceSize], ct2[:NonceSize]) {
			t.Fatal("SealMeta reused a nonce")
		}
	}
}

// The hand-written encoder must agree with encoding/json on the value it
// writes, and with JSON.stringify on the bytes (checked by the JS tests).
func TestMetaJSONMatchesStdlib(t *testing.T) {
	m := Meta{V: 1, Kind: KindFile, Name: "a\"b\\c\n\t\b\f\r\x01\x1f\x7f<>&\u2028\u2029\u00E9\U0001F600", Type: "x/y", Size: 42}
	js := metaJSON(m)
	var back struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(js, &back); err != nil {
		t.Fatal(err)
	}
	if Meta(back) != m {
		t.Fatalf("round trip through encoding/json: %+v", back)
	}
	want := `{"v":1,"kind":"file","name":"a\"b\\c\n\t\b\f\r\u0001\u001f` + "\x7f<>&\u2028\u2029\u00E9\U0001F600" + `","type":"x/y","size":42}`
	if string(js) != want {
		t.Fatalf("metaJSON = %s\nwant      %s", js, want)
	}
	if got := string(metaJSON(Meta{V: 1, Kind: KindText, Name: "\xffa"})); got != `{"v":1,"kind":"text","name":"`+"\uFFFD"+`a","type":"","size":0}` {
		t.Fatalf("invalid UTF-8 not replaced: %s", got)
	}
}

func TestMetaTruncation(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"ascii", strings.Repeat("a", 2000)},
		{"two-byte", strings.Repeat("\u00E9", 900)},
		{"three-byte", strings.Repeat("\u20AC", 700)},
		{"four-byte", strings.Repeat("\U0001F600", 500)},
		{"escaped quotes", strings.Repeat("\"", 1000)},
		{"control chars", strings.Repeat("\x01", 500)},
		{"mixed", strings.Repeat("a\u00E9\u20AC\U0001F600\"\x02", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, stored, err := encodeMeta(Meta{Kind: KindFile, Name: tt.in, Type: "application/octet-stream", Size: 1})
			if err != nil {
				t.Fatal(err)
			}
			n := int(binary.BigEndian.Uint16(pt[:2]))
			if n > MaxMetaJSON {
				t.Fatalf("json length %d", n)
			}
			if !strings.HasPrefix(tt.in, stored.Name) || !utf8.ValidString(stored.Name) || len(stored.Name) == len(tt.in) {
				t.Fatalf("bad truncation to %d bytes", len(stored.Name))
			}
			// Longest prefix: one more rune would not fit.
			r, w := utf8.DecodeRuneInString(tt.in[len(stored.Name):])
			if n+jsonRuneLen(r) <= MaxMetaJSON {
				t.Fatalf("stopped early: %d + %d (rune %q, width %d)", n, jsonRuneLen(r), r, w)
			}
			got, err := parseMeta(pt[:])
			if err != nil || got != stored {
				t.Fatalf("parseMeta = %+v, %v", got, err)
			}
		})
	}
}

func TestSealMetaErrors(t *testing.T) {
	key := tb32("meta errors")
	tests := []struct {
		name string
		m    Meta
	}{
		{"version 2", Meta{V: 2, Kind: KindFile}},
		{"no kind", Meta{}},
		{"unknown kind", Meta{Kind: "link"}},
		{"negative size", Meta{Kind: KindFile, Size: -1}},
		{"size above 2^53-1", Meta{Kind: KindFile, Size: MaxSize + 1}},
		{"type too long", Meta{Kind: KindFile, Type: strings.Repeat("t", 1000)}},
	}
	for _, tt := range tests {
		if _, err := SealMeta(key, tt.m); !errors.Is(err, ErrMeta) {
			t.Errorf("%s: SealMeta = %v", tt.name, err)
		}
	}
}

func TestOpenMetaErrors(t *testing.T) {
	key := tb32("open errors")
	good, err := SealMeta(key, Meta{Kind: KindFile, Name: "a", Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	other := key
	other[0] ^= 1
	tests := []struct {
		name string
		key  [32]byte
		ct   []byte
		want error
	}{
		{"empty", key, nil, ErrMeta},
		{"short", key, good[:len(good)-1], ErrMeta},
		{"long", key, append(bytes.Clone(good), 0), ErrMeta},
		{"wrong key", other, good, ErrDecrypt},
		{"flipped", key, func() []byte { b := bytes.Clone(good); b[100] ^= 1; return b }(), ErrDecrypt},
	}
	for _, tt := range tests {
		if _, err := OpenMeta(tt.key, tt.ct); !errors.Is(err, tt.want) {
			t.Errorf("%s: OpenMeta = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestPadText(t *testing.T) {
	tests := []struct{ n, want int }{
		{0, 1024}, {1, 1024}, {1024, 1024}, {1025, 2048}, {2048, 2048}, {2049, 4096}, {65536, 65536}, {65537, 131072},
	}
	for _, tt := range tests {
		if got := PaddedTextLen(tt.n); got != tt.want {
			t.Errorf("PaddedTextLen(%d) = %d, want %d", tt.n, got, tt.want)
		}
		in := bytes.Repeat([]byte{0xab}, tt.n)
		out := PadText(in)
		if len(out) != tt.want || !bytes.Equal(out[:tt.n], in) || bytes.ContainsFunc(out[tt.n:], func(r rune) bool { return r != 0 }) {
			t.Errorf("PadText(%d bytes) wrong", tt.n)
		}
		back, err := UnpadText(out, int64(tt.n))
		if err != nil || !bytes.Equal(back, in) {
			t.Errorf("UnpadText(%d) = %v", tt.n, err)
		}
	}
	in := []byte("abc")
	PadText(in)[0] = 'x'
	if in[0] != 'a' {
		t.Fatal("PadText aliased its input")
	}
	for _, size := range []int64{-1, 1025} {
		if _, err := UnpadText(make([]byte, 1024), size); !errors.Is(err, ErrMeta) {
			t.Errorf("UnpadText(size %d) = %v", size, err)
		}
	}
}

func FuzzParseMeta(f *testing.F) {
	pt, _, err := encodeMeta(Meta{Kind: KindFile, Name: "seed", Type: "a/b", Size: 9})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(pt[:])
	f.Add(rawMeta(`{"v":1}`, 7))
	f.Add(make([]byte, MetaSize))
	f.Fuzz(func(t *testing.T, b []byte) {
		pt := make([]byte, MetaSize)
		copy(pt, b)
		m, err := parseMeta(pt)
		if err != nil {
			return
		}
		if m.V != 1 || m.Kind != KindText && m.Kind != KindFile || m.Size < 0 || m.Size > MaxSize {
			t.Fatalf("accepted %+v", m)
		}
	})
}
