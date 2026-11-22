package e2e

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"unicode/utf8"
)

// Metadata framing constants.
const (
	MetaVersion    = 1
	MetaSize       = 1024                           // metadata plaintext: uint16 length || JSON || zero padding
	SealedMetaSize = NonceSize + MetaSize + TagSize // random nonce || AES-GCM(K_meta, plaintext)
	MaxMetaJSON    = MetaSize - 2                   // longest JSON that fits
	MinTextPad     = 1024                           // smallest padded text size

	// MaxSize is the largest size metadata can carry: 2^53-1, the largest
	// integer a JavaScript number holds exactly.
	MaxSize = 1<<53 - 1
)

// Kinds of secret.
const (
	KindText = "text"
	KindFile = "file"
)

// ErrMeta reports metadata that cannot be encoded or that decrypted but is
// malformed.
var ErrMeta = errors.New("e2e: invalid metadata")

// Meta is the encrypted description of a secret. For text, Size is the
// unpadded byte length and the reader truncates the padded content to it.
type Meta struct {
	V    int
	Kind string
	Name string
	Type string
	Size int64
}

// SealMeta encodes m into the fixed 1024-byte record and seals it under
// K_meta with a random nonce, giving SealedMetaSize bytes. m.V must be 0 or
// 1 (1 is written). Name is truncated on a UTF-8 boundary to the longest
// prefix that fits; any other overflow is an error.
func SealMeta(key [KeySize]byte, m Meta) ([]byte, error) {
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("e2e: random: %w", err)
	}
	return sealMetaNonce(key, m, nonce)
}

// sealMetaNonce is SealMeta with a caller-chosen nonce, for test vectors.
func sealMetaNonce(key [KeySize]byte, m Meta, nonce [NonceSize]byte) ([]byte, error) {
	pt, _, err := encodeMeta(m)
	if err != nil {
		return nil, err
	}
	return sealMetaRaw(key, pt[:], nonce)
}

func sealMetaRaw(key [KeySize]byte, pt []byte, nonce [NonceSize]byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, NonceSize+len(pt)+TagSize)
	out = append(out, nonce[:]...)
	return aead.Seal(out, nonce[:], pt, nil), nil
}

// encodeMeta returns the metadata plaintext and the Meta as it will read
// back (version set, strings made valid UTF-8, name truncated).
func encodeMeta(m Meta) ([MetaSize]byte, Meta, error) {
	var pt [MetaSize]byte
	if m.V != 0 && m.V != MetaVersion {
		return pt, Meta{}, fmt.Errorf("%w: version %d", ErrMeta, m.V)
	}
	if m.Kind != KindText && m.Kind != KindFile {
		return pt, Meta{}, fmt.Errorf("%w: kind %q", ErrMeta, m.Kind)
	}
	if m.Size < 0 || m.Size > MaxSize {
		return pt, Meta{}, fmt.Errorf("%w: size %d", ErrMeta, m.Size)
	}
	stored := Meta{V: MetaVersion, Kind: m.Kind, Type: validUTF8(m.Type), Size: m.Size}

	budget := MaxMetaJSON - len(metaJSON(stored))
	if budget < 0 {
		return pt, Meta{}, fmt.Errorf("%w: type too long", ErrMeta)
	}
	name := make([]byte, 0, len(m.Name))
	for _, r := range m.Name {
		w := jsonRuneLen(r)
		if w > budget {
			break
		}
		budget -= w
		name = utf8.AppendRune(name, r)
	}
	stored.Name = string(name)

	js := metaJSON(stored)
	binary.BigEndian.PutUint16(pt[:2], uint16(len(js))) //nolint:gosec // len(js) <= MaxMetaJSON
	copy(pt[2:], js)
	return pt, stored, nil
}

// metaJSON writes {"v":1,"kind":…,"name":…,"type":…,"size":…} with no
// whitespace, escaping strings exactly as ECMAScript JSON.stringify does for
// well-formed strings, so Go and JS produce identical bytes.
func metaJSON(m Meta) []byte {
	b := make([]byte, 0, MetaSize)
	b = append(b, `{"v":`...)
	b = strconv.AppendInt(b, int64(m.V), 10)
	b = append(b, `,"kind":`...)
	b = appendJSONString(b, m.Kind)
	b = append(b, `,"name":`...)
	b = appendJSONString(b, m.Name)
	b = append(b, `,"type":`...)
	b = appendJSONString(b, m.Type)
	b = append(b, `,"size":`...)
	b = strconv.AppendInt(b, m.Size, 10)
	return append(b, '}')
}

// appendJSONString escapes '"', '\\' and C0 controls (\b \t \n \f \r, else
// \u00xx in lowercase hex) and writes everything else as UTF-8. Invalid
// UTF-8 bytes become U+FFFD.
func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	for _, r := range s {
		b = appendJSONRune(b, r)
	}
	return append(b, '"')
}

func appendJSONRune(b []byte, r rune) []byte {
	const hex = "0123456789abcdef"
	switch r {
	case '"':
		return append(b, `\"`...)
	case '\\':
		return append(b, `\\`...)
	case '\b':
		return append(b, `\b`...)
	case '\t':
		return append(b, `\t`...)
	case '\n':
		return append(b, `\n`...)
	case '\f':
		return append(b, `\f`...)
	case '\r':
		return append(b, `\r`...)
	}
	if r < 0x20 {
		return append(b, '\\', 'u', '0', '0', hex[r>>4], hex[r&0xf])
	}
	return utf8.AppendRune(b, r)
}

func jsonRuneLen(r rune) int {
	var buf [6]byte
	return len(appendJSONRune(buf[:0], r))
}

func validUTF8(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = utf8.AppendRune(b, r)
	}
	return string(b)
}

// OpenMeta decrypts and parses sealed metadata. It rejects a wrong length,
// a failed tag (ErrDecrypt), a bad length prefix, non-zero padding, invalid
// UTF-8 or JSON, v other than 1, an unknown kind, non-string name or type,
// and a size that is not an integer in [0, 2^53-1]. Unknown JSON fields are
// ignored.
func OpenMeta(key [KeySize]byte, ct []byte) (Meta, error) {
	if len(ct) != SealedMetaSize {
		return Meta{}, fmt.Errorf("%w: %d bytes, want %d", ErrMeta, len(ct), SealedMetaSize)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return Meta{}, err
	}
	pt, err := aead.Open(nil, ct[:NonceSize], ct[NonceSize:], nil)
	if err != nil {
		return Meta{}, fmt.Errorf("%w: metadata", ErrDecrypt)
	}
	return parseMeta(pt)
}

func parseMeta(pt []byte) (Meta, error) {
	n := int(binary.BigEndian.Uint16(pt[:2]))
	if n == 0 || n > MaxMetaJSON {
		return Meta{}, fmt.Errorf("%w: json length %d", ErrMeta, n)
	}
	for _, c := range pt[2+n:] {
		if c != 0 {
			return Meta{}, fmt.Errorf("%w: non-zero padding", ErrMeta)
		}
	}
	js := pt[2 : 2+n]
	if !utf8.Valid(js) {
		return Meta{}, fmt.Errorf("%w: not valid UTF-8", ErrMeta)
	}
	var obj map[string]any
	if err := json.Unmarshal(js, &obj); err != nil || obj == nil {
		return Meta{}, fmt.Errorf("%w: not a JSON object", ErrMeta)
	}
	if v, ok := obj["v"].(float64); !ok || v != MetaVersion {
		return Meta{}, fmt.Errorf("%w: unsupported version", ErrMeta)
	}
	kind, ok := obj["kind"].(string)
	if !ok || (kind != KindText && kind != KindFile) {
		return Meta{}, fmt.Errorf("%w: bad kind", ErrMeta)
	}
	name, ok := obj["name"].(string)
	if !ok {
		return Meta{}, fmt.Errorf("%w: bad name", ErrMeta)
	}
	typ, ok := obj["type"].(string)
	if !ok {
		return Meta{}, fmt.Errorf("%w: bad type", ErrMeta)
	}
	size, ok := obj["size"].(float64)
	if !ok || size < 0 || size > MaxSize || size != math.Trunc(size) {
		return Meta{}, fmt.Errorf("%w: bad size", ErrMeta)
	}
	return Meta{V: MetaVersion, Kind: kind, Name: name, Type: typ, Size: int64(size)}, nil
}

// PaddedTextLen returns the padded size of n bytes of text: the next power
// of two that is at least max(n, 1024).
func PaddedTextLen(n int) int {
	if n <= MinTextPad {
		return MinTextPad
	}
	return 1 << bits.Len(uint(n-1))
}

// PadText returns a copy of b zero-padded to PaddedTextLen(len(b)). Store
// len(b) as Meta.Size.
func PadText(b []byte) []byte {
	out := make([]byte, PaddedTextLen(len(b)))
	copy(out, b)
	return out
}

// UnpadText returns the first size bytes of padded text.
func UnpadText(b []byte, size int64) ([]byte, error) {
	if size < 0 || size > int64(len(b)) {
		return nil, fmt.Errorf("%w: size %d exceeds content", ErrMeta, size)
	}
	return b[:size], nil
}
