package e2e

// Red-team: STREAM tampering attempts. These PASS when every attack is
// rejected (they document attacks that did NOT work).

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
)

func rtEncrypt(t *testing.T, key [KeySize]byte, pt []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := NewEncryptor(&b, key)
	if _, err := w.Write(pt); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func rtDecrypt(key [KeySize]byte, ct []byte) ([]byte, error) {
	return io.ReadAll(NewDecryptor(bytes.NewReader(ct), key))
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestRedteamStreamTampering(t *testing.T) {
	var k1, k2 [KeySize]byte
	k1[0], k2[0] = 1, 2
	pt := bytes.Repeat([]byte{7}, 2*ChunkSize+5)
	ct := rtEncrypt(t, k1, pt)
	other := rtEncrypt(t, k2, pt)
	c0, c1, c2 := ct[:fullChunk], ct[fullChunk:2*fullChunk], ct[2*fullChunk:]
	emptyFinal, err := EncryptChunk(k1, 0, true, nil) // valid only as a lone chunk 0
	if err != nil {
		t.Fatal(err)
	}
	ctFull := rtEncrypt(t, k1, bytes.Repeat([]byte{7}, 2*ChunkSize)) // 2 full chunks, the second final
	attacks := map[string][]byte{
		"drop final chunk":                 cat(c0, c1),
		"drop middle":                      cat(c0, c2),
		"swap 0 and 1":                     cat(c1, c0, c2),
		"duplicate 0":                      cat(c0, c0, c1, c2),
		"extend after final":               cat(ct, []byte{0}),
		"append empty final":               cat(c0, c1, emptyFinal),
		"splice chunk from other key":      cat(c0, other[fullChunk:2*fullChunk], c2),
		"truncate full-final to non-final": ctFull[:fullChunk],
		"cut last byte":                    ct[:len(ct)-1],
		"empty input":                      nil,
		"15 bytes":                         make([]byte, 15),
	}
	for name, a := range attacks {
		if _, err := rtDecrypt(k1, a); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: accepted (err %v)", name, err)
		}
	}
	if got, err := rtDecrypt(k1, ct); err != nil || !bytes.Equal(got, pt) {
		t.Fatal("round trip")
	}
}

// HMAC zero-pads short keys, so the HKDF-Extract salts "snap-e2e-v1" and
// "snap-e2e-v1" || 0^16 give the same PRK: with S = 0^16 and no passphrase
// the main PRK equals the probe PRK. Every output still has its own info
// label, so no derived key equals K_probe: no practical impact.
func TestRedteamHMACSaltPadding(t *testing.T) {
	var key [KeySize]byte
	key[5] = 9
	var id [IDSize]byte
	var zero [SaltSize]byte
	a := hmac.New(sha256.New, []byte(Label))
	a.Write(key[:])
	b := hmac.New(sha256.New, append([]byte(Label), zero[:]...))
	b.Write(key[:])
	if !bytes.Equal(a.Sum(nil), b.Sum(nil)) {
		t.Fatal("PRKs differ")
	}
	ks, err := DeriveKeys(key, id, zero, nil, KDFParams{})
	if err != nil {
		t.Fatal(err)
	}
	probe := ProbeKey(key, id)
	for _, k := range [][32]byte{ks.Auth, ks.Enc, ks.Meta, ks.Commit} {
		if k == probe {
			t.Fatal("a derived key equals K_probe")
		}
	}
}
