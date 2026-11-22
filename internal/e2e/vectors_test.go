package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

const vectorsPath = "testdata/vectors.json"

// hexBytes is []byte as a lowercase hex string in JSON.
type hexBytes []byte

func (h hexBytes) MarshalJSON() ([]byte, error) { return json.Marshal(hex.EncodeToString(h)) }

func (h *hexBytes) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	d, err := hex.DecodeString(s)
	*h = d
	return err
}

func hp(b []byte) *hexBytes { h := hexBytes(b); return &h }
func sp(s string) *string   { return &s }

type vectorFile struct {
	Comment        string           `json:"comment"`
	Constants      vConstants       `json:"constants"`
	KDFValid       []KDFParams      `json:"kdf_valid"`
	KDFInvalid     []KDFParams      `json:"kdf_invalid"`
	Links          []vLink          `json:"links"`
	LinksInvalid   []vLinkInvalid   `json:"links_invalid"`
	Keys           []vKeys          `json:"keys"`
	KeysMismatch   []vKeysMismatch  `json:"keys_mismatch"`
	Chunks         []vChunk         `json:"chunks"`
	ChunksInvalid  []vChunk         `json:"chunks_invalid"`
	Streams        []vStream        `json:"streams"`
	StreamsInvalid []vStreamInvalid `json:"streams_invalid"`
	TextPadding    []vPad           `json:"text_padding"`
	Meta           []vMeta          `json:"meta"`
	MetaOpen       []vMetaOpen      `json:"meta_open"`
	MetaInvalid    []vMetaInvalid   `json:"meta_invalid"`
	Envelopes      []vEnvelope      `json:"envelopes"`
}

type vConstants struct {
	Label          string    `json:"label"`
	IDSize         int       `json:"id_size"`
	KeySize        int       `json:"key_size"`
	SaltSize       int       `json:"salt_size"`
	ChunkSize      int       `json:"chunk_size"`
	TagSize        int       `json:"tag_size"`
	NonceSize      int       `json:"nonce_size"`
	MetaSize       int       `json:"meta_size"`
	SealedMetaSize int       `json:"sealed_meta_size"`
	MinTextPad     int       `json:"min_text_pad"`
	MaxSize        int64     `json:"max_size"`
	KDFDefault     KDFParams `json:"kdf_default"`
	KDFMin         KDFParams `json:"kdf_min"`
	KDFMax         KDFParams `json:"kdf_max"`
	PlaintextGen   string    `json:"plaintext_gen"`
}

type vLink struct {
	Base string   `json:"base"`
	ID   hexBytes `json:"id"`
	Key  hexBytes `json:"key"`
	Link string   `json:"link"`
}

type vLinkInvalid struct {
	Name string `json:"name"`
	Link string `json:"link"`
}

type vKeys struct {
	Name          string     `json:"name"`
	LinkKey       hexBytes   `json:"link_key"`
	ID            hexBytes   `json:"id"`
	Salt          hexBytes   `json:"salt"`
	Passphrase    *string    `json:"passphrase"`
	PassphraseNFC *hexBytes  `json:"passphrase_nfc_utf8,omitempty"`
	KDF           *KDFParams `json:"kdf"`
	PassphraseKey *hexBytes  `json:"k_pw,omitempty"`
	Probe         hexBytes   `json:"probe"`
	ProbeHash     hexBytes   `json:"probe_hash"`
	Auth          hexBytes   `json:"auth"`
	AuthHash      hexBytes   `json:"auth_hash"`
	Enc           hexBytes   `json:"enc"`
	Meta          hexBytes   `json:"meta"`
	Commit        hexBytes   `json:"commit"`
}

// vKeysMismatch: deriving from these inputs must give a commitment that
// does not match Commit (the one stored for the named keys vector).
type vKeysMismatch struct {
	Name       string     `json:"name"`
	Of         string     `json:"of"`
	LinkKey    hexBytes   `json:"link_key"`
	ID         hexBytes   `json:"id"`
	Salt       hexBytes   `json:"salt"`
	Passphrase *string    `json:"passphrase"`
	KDF        *KDFParams `json:"kdf"`
	Commit     hexBytes   `json:"commit"`
}

type vChunk struct {
	Name       string    `json:"name"`
	Key        hexBytes  `json:"key"`
	Counter    uint64    `json:"counter"`
	Last       bool      `json:"last"`
	Plaintext  *hexBytes `json:"plaintext,omitempty"`
	Ciphertext hexBytes  `json:"ciphertext"`
}

type vStream struct {
	Name             string         `json:"name"`
	Key              hexBytes       `json:"key"`
	PlaintextLen     int64          `json:"plaintext_len"`
	Plaintext        *hexBytes      `json:"plaintext,omitempty"`
	PlaintextGen     string         `json:"plaintext_gen,omitempty"`
	PlaintextSHA256  hexBytes       `json:"plaintext_sha256"`
	CiphertextLen    int64          `json:"ciphertext_len"`
	CiphertextSHA256 hexBytes       `json:"ciphertext_sha256"`
	Ciphertext       *hexBytes      `json:"ciphertext,omitempty"`
	Chunks           []vStreamChunk `json:"chunks"`
}

type vStreamChunk struct {
	Len    int      `json:"len"`
	SHA256 hexBytes `json:"sha256"`
	Tag    hexBytes `json:"tag"`
}

// vStreamInvalid: apply Mutations in order to the ciphertext of the named
// stream; decrypting the result (with Key if set) must fail.
type vStreamInvalid struct {
	Name      string      `json:"name"`
	Stream    string      `json:"stream"`
	Key       *hexBytes   `json:"key,omitempty"`
	Mutations []vMutation `json:"mutations,omitempty"`
}

// vMutation ops (missing numbers are 0):
//
//	flip     ct[offset] ^= mask
//	truncate keep the first length bytes
//	append   append hex
//	swap     swap full ciphertext chunks a and b
//	drop     remove full ciphertext chunk index
//	repeat   append a copy of ciphertext chunk index (full or final)
type vMutation struct {
	Op     string   `json:"op"`
	Offset int64    `json:"offset,omitempty"`
	Mask   byte     `json:"mask,omitempty"`
	Length int64    `json:"length,omitempty"`
	Hex    hexBytes `json:"hex,omitempty"`
	A      int      `json:"a,omitempty"`
	B      int      `json:"b,omitempty"`
	Index  int      `json:"index,omitempty"`
}

type vPad struct {
	Len       int `json:"len"`
	PaddedLen int `json:"padded_len"`
}

type vMetaJSON struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

func (m vMetaJSON) meta() Meta { return Meta(m) }

type vMeta struct {
	Name      string    `json:"name"`
	Key       hexBytes  `json:"key"`
	Nonce     hexBytes  `json:"nonce"`
	Input     vMetaJSON `json:"input"`
	Stored    vMetaJSON `json:"stored"`
	Plaintext hexBytes  `json:"plaintext"`
	Sealed    hexBytes  `json:"sealed"`
}

// vMetaOpen: metadata no conforming writer produces but readers must accept.
type vMetaOpen struct {
	Name   string    `json:"name"`
	Key    hexBytes  `json:"key"`
	Sealed hexBytes  `json:"sealed"`
	Meta   vMetaJSON `json:"meta"`
}

type vMetaInvalid struct {
	Name   string   `json:"name"`
	Key    hexBytes `json:"key"`
	Sealed hexBytes `json:"sealed"`
}

type vEnvelope struct {
	Name       string     `json:"name"`
	Link       string     `json:"link"`
	Salt       hexBytes   `json:"salt"`
	Passphrase *string    `json:"passphrase"`
	KDF        *KDFParams `json:"kdf"`
	ProbeHash  hexBytes   `json:"probe_hash"`
	AuthHash   hexBytes   `json:"auth_hash"`
	Commit     hexBytes   `json:"commit"`
	MetaSealed hexBytes   `json:"meta_sealed"`
	Content    hexBytes   `json:"content"`
	Meta       vMetaJSON  `json:"meta"`
	Plaintext  hexBytes   `json:"plaintext"`
}

// tb returns n deterministic test bytes (n <= 32) named by label.
func tb(label string, n int) []byte {
	s := sha256.Sum256([]byte("snap-e2e-v1 test vector " + label))
	return s[:n]
}

func tb16(label string) [16]byte { return [16]byte(tb(label, 16)) }
func tb32(label string) [32]byte { return [32]byte(tb(label, 32)) }
func tb12(label string) [12]byte { return [12]byte(tb(label, 12)) }

// genPlaintext is the "mod251" generator: byte i = i mod 251.
func genPlaintext(n int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

const smallHex = 4096 // include full plaintext/ciphertext hex up to this many bytes

func encryptAll(t testing.TB, key [32]byte, pt []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewEncryptor(&buf, key)
	if _, err := w.Write(pt); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decryptAll(key [32]byte, ct []byte) ([]byte, error) {
	return io.ReadAll(NewDecryptor(bytes.NewReader(ct), key))
}

func splitChunks(ct []byte) [][]byte {
	var out [][]byte
	for len(ct) > fullChunk {
		out = append(out, ct[:fullChunk])
		ct = ct[fullChunk:]
	}
	return append(out, ct)
}

// rawChunk seals a chunk without the framing checks, to build invalid
// streams.
func rawChunk(t testing.TB, key [32]byte, counter uint64, last bool, pt []byte) []byte {
	t.Helper()
	aead, err := newAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := chunkNonce(counter, last)
	return aead.Seal(nil, nonce[:], pt, nil)
}

func sum(b []byte) hexBytes { s := sha256.Sum256(b); return s[:] }

func mutate(ct []byte, muts []vMutation) ([]byte, error) {
	out := bytes.Clone(ct)
	for _, m := range muts {
		switch m.Op {
		case "flip":
			out[m.Offset] ^= m.Mask
		case "truncate":
			out = out[:m.Length]
		case "append":
			out = append(out, m.Hex...)
		case "swap":
			a := bytes.Clone(out[m.A*fullChunk : (m.A+1)*fullChunk])
			b := bytes.Clone(out[m.B*fullChunk : (m.B+1)*fullChunk])
			copy(out[m.A*fullChunk:], b)
			copy(out[m.B*fullChunk:], a)
		case "drop":
			out = append(out[:m.Index*fullChunk:m.Index*fullChunk], out[(m.Index+1)*fullChunk:]...)
		case "repeat":
			out = append(out, bytes.Clone(splitChunks(out)[m.Index])...)
		default:
			return nil, fmt.Errorf("unknown mutation %q", m.Op)
		}
	}
	return out, nil
}

func passBytes(p *string) []byte {
	if p == nil {
		return nil
	}
	return []byte(*p)
}

func kdfOrZero(p *KDFParams) KDFParams {
	if p == nil {
		return KDFParams{}
	}
	return *p
}

// rawMeta builds a metadata plaintext from js with length prefix n.
func rawMeta(js string, n int) []byte {
	pt := make([]byte, MetaSize)
	binary.BigEndian.PutUint16(pt, uint16(n))
	copy(pt[2:], js)
	return pt
}

func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	fast := &KDFParams{MemKiB: MinKDFMemKiB, Time: MinKDFTime, Threads: 1}
	def := DefaultKDF()
	vf := vectorFile{
		Comment: "snap E2E envelope v1 test vectors. Generated by internal/e2e (go test ./internal/e2e -run TestVectors -update). " +
			"Spec: docs/security.md. Bytes are hex; links are base64url as specified.",
		Constants: vConstants{
			Label: Label, IDSize: IDSize, KeySize: KeySize, SaltSize: SaltSize,
			ChunkSize: ChunkSize, TagSize: TagSize, NonceSize: NonceSize,
			MetaSize: MetaSize, SealedMetaSize: SealedMetaSize, MinTextPad: MinTextPad, MaxSize: MaxSize,
			KDFDefault:   def,
			KDFMin:       KDFParams{MemKiB: MinKDFMemKiB, Time: MinKDFTime, Threads: KDFThreads},
			KDFMax:       KDFParams{MemKiB: MaxKDFMemKiB, Time: MaxKDFTime, Threads: KDFThreads},
			PlaintextGen: "mod251: byte i = i mod 251",
		},
		KDFValid: []KDFParams{
			{MemKiB: 65536, Time: 3, Threads: 1},
			{MemKiB: 262144, Time: 10, Threads: 1},
			{MemKiB: 131072, Time: 4, Threads: 1},
			def,
		},
		KDFInvalid: []KDFParams{
			{MemKiB: 65535, Time: 3, Threads: 1},
			{MemKiB: 262145, Time: 3, Threads: 1},
			{MemKiB: 65536, Time: 2, Threads: 1},
			{MemKiB: 65536, Time: 0, Threads: 1},
			{MemKiB: 65536, Time: 11, Threads: 1},
			{MemKiB: 65536, Time: 3, Threads: 0},
			{MemKiB: 65536, Time: 3, Threads: 2},
			{MemKiB: 4294967295, Time: 3, Threads: 1},
			// The pre-review floor (m=19456 KiB, t=1) is no longer accepted.
			{MemKiB: 19456, Time: 1, Threads: 1},
			{MemKiB: 19456, Time: 3, Threads: 1},
			{MemKiB: 65536, Time: 1, Threads: 1},
			{},
		},
	}

	// Keys.
	type keyIn struct {
		name   string
		link   [32]byte
		id     [16]byte
		salt   [16]byte
		pass   *string
		kdf    *KDFParams
		nfcHex bool
	}
	nonNFC := "Cafe\u0301 \u212B \u1112\u1161\u11AB \uFB01"
	keyIns := []keyIn{
		{name: "no passphrase", link: tb32("link 1"), id: tb16("id 1"), salt: tb16("salt 1")},
		{name: "passphrase", link: tb32("link 1"), id: tb16("id 1"), salt: tb16("salt 1"), pass: sp("correct horse battery staple"), kdf: fast},
		{name: "passphrase not NFC", link: tb32("link 2"), id: tb16("id 2"), salt: tb16("salt 2"), pass: sp(nonNFC), kdf: &KDFParams{MemKiB: MinKDFMemKiB, Time: 4, Threads: 1}, nfcHex: true},
		{name: "passphrase default kdf", link: tb32("link 3"), id: tb16("id 3"), salt: tb16("salt 3"), pass: sp("p\u00E4ssw\u00F6rd"), kdf: &def},
	}
	keys := map[string]Keys{}
	for _, in := range keyIns {
		k, err := DeriveKeys(in.link, in.id, in.salt, passBytes(in.pass), kdfOrZero(in.kdf))
		if err != nil {
			t.Fatalf("%s: %v", in.name, err)
		}
		keys[in.name] = k
		probe := ProbeKey(in.link, in.id)
		v := vKeys{
			Name: in.name, LinkKey: in.link[:], ID: in.id[:], Salt: in.salt[:], Passphrase: in.pass, KDF: in.kdf,
			Probe: probe[:], ProbeHash: sum(probe[:]),
			Auth: k.Auth[:], AuthHash: sum(k.Auth[:]), Enc: k.Enc[:], Meta: k.Meta[:], Commit: k.Commit[:],
		}
		if in.pass != nil {
			kpw, err := PassphraseKey([]byte(*in.pass), in.salt, *in.kdf)
			if err != nil {
				t.Fatal(err)
			}
			v.PassphraseKey = hp(kpw[:])
		}
		if in.nfcHex {
			v.PassphraseNFC = hp([]byte("Caf\u00E9 \u00C5 \uD55C \uFB01"))
		}
		vf.Keys = append(vf.Keys, v)
	}
	flip := func(b [16]byte) [16]byte { b[15] ^= 1; return b }
	k1, k2 := keyIns[0], keyIns[1]
	for _, m := range []struct {
		name, of string
		in       keyIn
	}{
		{"wrong passphrase", "passphrase", keyIn{link: k2.link, id: k2.id, salt: k2.salt, pass: sp("correct horse battery stapler"), kdf: fast}},
		{"passphrase omitted", "passphrase", keyIn{link: k2.link, id: k2.id, salt: k2.salt}},
		{"unexpected passphrase", "no passphrase", keyIn{link: k1.link, id: k1.id, salt: k1.salt, pass: sp("x"), kdf: fast}},
		{"different kdf", "passphrase", keyIn{link: k2.link, id: k2.id, salt: k2.salt, pass: k2.pass, kdf: &KDFParams{MemKiB: MinKDFMemKiB, Time: 4, Threads: 1}}},
		{"wrong id", "no passphrase", keyIn{link: k1.link, id: flip(k1.id), salt: k1.salt}},
		{"wrong salt", "passphrase", keyIn{link: k2.link, id: k2.id, salt: flip(k2.salt), pass: k2.pass, kdf: fast}},
		{"wrong link key", "no passphrase", keyIn{link: tb32("link 9"), id: k1.id, salt: k1.salt}},
		{"NFKC is not NFC", "passphrase not NFC", keyIn{link: keyIns[2].link, id: keyIns[2].id, salt: keyIns[2].salt, pass: sp("Caf\u00E9 \u00C5 \uD55C fi"), kdf: keyIns[2].kdf}},
	} {
		c := keys[m.of].Commit
		vf.KeysMismatch = append(vf.KeysMismatch, vKeysMismatch{
			Name: m.name, Of: m.of, LinkKey: m.in.link[:], ID: m.in.id[:], Salt: m.in.salt[:],
			Passphrase: m.in.pass, KDF: m.in.kdf, Commit: c[:],
		})
	}

	// Links.
	for _, l := range []struct {
		base string
		id   [16]byte
		key  [32]byte
	}{
		{"https://snap.example", k1.id, k1.link},
		{"http://192.168.1.10:8080/snap", tb16("link id 2"), tb32("link key 2")},
	} {
		vf.Links = append(vf.Links, vLink{Base: l.base, ID: l.id[:], Key: l.key[:], Link: FormatLink(l.base, l.id, l.key)})
	}
	idS, keyS := Encode(k1.id[:]), Encode(k1.link[:])
	base := "https://snap.example"
	nonCanon := func(s string) string {
		v := strings.IndexByte(b64Alphabet, s[len(s)-1])
		return s[:len(s)-1] + string(b64Alphabet[v|1])
	}
	for _, l := range []vLinkInvalid{
		{"empty", ""},
		{"no fragment", base + "/s/" + idS},
		{"empty fragment", base + "/s/" + idS + "#"},
		{"no /s/ path", base + "/x/" + idS + "#" + keyS},
		{"short key", base + "/s/" + idS + "#" + keyS[:42]},
		{"long key", base + "/s/" + idS + "#" + keyS + "A"},
		{"padded key", base + "/s/" + idS + "#" + keyS + "="},
		{"short id", base + "/s/" + idS[:21] + "#" + keyS},
		{"long id", base + "/s/" + idS + "A#" + keyS},
		{"non-canonical key", base + "/s/" + idS + "#" + nonCanon(keyS)},
		{"non-canonical id", base + "/s/" + nonCanon(idS) + "#" + keyS},
		{"standard base64 alphabet", base + "/s/" + idS + "#+" + keyS[1:]},
		{"space in key", base + "/s/" + idS + "#" + keyS[:10] + " " + keyS[11:]},
		{"newline in key", base + "/s/" + idS + "#" + keyS[:10] + "\n" + keyS[11:]},
		{"query string", base + "/s/" + idS + "?x=1#" + keyS},
		{"trailing slash", base + "/s/" + idS + "/#" + keyS},
	} {
		if nonCanon(keyS) == keyS || nonCanon(idS) == idS {
			t.Fatal("nonCanon did not change the encoding")
		}
		vf.LinksInvalid = append(vf.LinksInvalid, l)
	}

	// Single chunks.
	ck := tb32("chunk key")
	for _, c := range []struct {
		name    string
		counter uint64
		pt      string
	}{
		{"empty final chunk", 0, ""},
		{"counter 1", 1, "chunk one"},
		{"counter 0x0102030405", 0x0102030405, "abc"},
		{"counter 2^53-1", 1<<53 - 1, "max safe counter"},
	} {
		ct, err := EncryptChunk(ck, c.counter, true, []byte(c.pt))
		if err != nil {
			t.Fatal(err)
		}
		vf.Chunks = append(vf.Chunks, vChunk{Name: c.name, Key: ck[:], Counter: c.counter, Last: true, Plaintext: hp([]byte(c.pt)), Ciphertext: ct})
	}
	c1 := vf.Chunks[1].Ciphertext
	flipped := bytes.Clone(c1)
	flipped[len(flipped)-1] ^= 0x80
	wrongKey := tb32("chunk key 2")
	vf.ChunksInvalid = []vChunk{
		{Name: "wrong counter", Key: ck[:], Counter: 0, Last: true, Ciphertext: c1},
		{Name: "not last", Key: ck[:], Counter: 1, Last: false, Ciphertext: c1},
		{Name: "counter off by one in a high byte", Key: ck[:], Counter: 0x0102030404, Last: true, Ciphertext: vf.Chunks[2].Ciphertext},
		{Name: "counter 2^53-2", Key: ck[:], Counter: 1<<53 - 2, Last: true, Ciphertext: vf.Chunks[3].Ciphertext},
		{Name: "flipped tag bit", Key: ck[:], Counter: 1, Last: true, Ciphertext: flipped},
		{Name: "wrong key", Key: wrongKey[:], Counter: 1, Last: true, Ciphertext: c1},
		{Name: "shorter than a tag", Key: ck[:], Counter: 0, Last: true, Ciphertext: vf.Chunks[0].Ciphertext[:15]},
		{Name: "empty final chunk at counter 1", Key: ck[:], Counter: 1, Last: true, Ciphertext: rawChunk(t, ck, 1, true, nil)},
	}

	// Streams, under K_enc of the "no passphrase" keys.
	sk := keys["no passphrase"].Enc
	streamIns := []struct {
		name string
		pt   []byte
		gen  bool
	}{
		{name: "empty", pt: []byte{}},
		{name: "short", pt: []byte("hello, world")},
		{name: "padded text", pt: PadText([]byte("The eagle has landed."))},
		{name: "exactly 1 MiB", pt: genPlaintext(ChunkSize), gen: true},
		{name: "1 MiB + 1", pt: genPlaintext(ChunkSize + 1), gen: true},
		{name: "multi-chunk", pt: genPlaintext(3*ChunkSize + 12345), gen: true},
	}
	cts := map[string][]byte{}
	for _, s := range streamIns {
		ct := encryptAll(t, sk, s.pt)
		cts[s.name] = ct
		v := vStream{
			Name: s.name, Key: sk[:], PlaintextLen: int64(len(s.pt)), PlaintextSHA256: sum(s.pt),
			CiphertextLen: int64(len(ct)), CiphertextSHA256: sum(ct),
		}
		if s.gen {
			v.PlaintextGen = "mod251"
		} else {
			v.Plaintext = hp(s.pt)
		}
		if len(ct) <= smallHex {
			v.Ciphertext = hp(ct)
		}
		for _, c := range splitChunks(ct) {
			v.Chunks = append(v.Chunks, vStreamChunk{Len: len(c), SHA256: sum(c), Tag: c[len(c)-TagSize:]})
		}
		if CiphertextSize(int64(len(s.pt))) != int64(len(ct)) {
			t.Fatalf("%s: CiphertextSize mismatch", s.name)
		}
		vf.Streams = append(vf.Streams, v)
	}
	otherKey := keys["passphrase"].Enc
	vf.StreamsInvalid = []vStreamInvalid{
		{Name: "no data", Stream: "empty", Mutations: []vMutation{{Op: "truncate", Length: 0}}},
		{Name: "shorter than a tag", Stream: "empty", Mutations: []vMutation{{Op: "truncate", Length: 15}}},
		{Name: "flipped tag of empty final chunk", Stream: "empty", Mutations: []vMutation{{Op: "flip", Offset: 0, Mask: 1}}},
		{Name: "flipped first byte", Stream: "short", Mutations: []vMutation{{Op: "flip", Offset: 0, Mask: 1}}},
		{Name: "flipped last byte", Stream: "short", Mutations: []vMutation{{Op: "flip", Offset: 27, Mask: 0x80}}},
		{Name: "truncated by one byte", Stream: "short", Mutations: []vMutation{{Op: "truncate", Length: 27}}},
		{Name: "extended by one byte", Stream: "short", Mutations: []vMutation{{Op: "append", Hex: []byte{0}}}},
		{Name: "extended by an empty final chunk", Stream: "short", Mutations: []vMutation{{Op: "append", Hex: cts["empty"]}}},
		{Name: "wrong key", Stream: "padded text", Key: hp(otherKey[:])},
		{Name: "flipped byte in full final chunk", Stream: "exactly 1 MiB", Mutations: []vMutation{{Op: "flip", Offset: 524288, Mask: 4}}},
		{Name: "full final chunk truncated", Stream: "exactly 1 MiB", Mutations: []vMutation{{Op: "truncate", Length: fullChunk - 1}}},
		{Name: "full final chunk extended", Stream: "exactly 1 MiB", Mutations: []vMutation{{Op: "append", Hex: make([]byte, TagSize)}}},
		{Name: "final chunk dropped", Stream: "1 MiB + 1", Mutations: []vMutation{{Op: "truncate", Length: fullChunk}}},
		{Name: "final chunk repeated", Stream: "1 MiB + 1", Mutations: []vMutation{{Op: "repeat", Index: 1}}},
		{Name: "first chunk repeated at end", Stream: "1 MiB + 1", Mutations: []vMutation{{Op: "repeat", Index: 0}}},
		{Name: "full chunk then empty final chunk", Stream: "1 MiB + 1", Mutations: []vMutation{{Op: "truncate", Length: fullChunk}, {Op: "append", Hex: rawChunk(t, sk, 1, true, nil)}}},
		{Name: "chunks 0 and 1 swapped", Stream: "multi-chunk", Mutations: []vMutation{{Op: "swap", A: 0, B: 1}}},
		{Name: "chunk 1 dropped", Stream: "multi-chunk", Mutations: []vMutation{{Op: "drop", Index: 1}}},
		{Name: "final chunk dropped (multi)", Stream: "multi-chunk", Mutations: []vMutation{{Op: "truncate", Length: 3 * fullChunk}}},
		{Name: "truncated mid-chunk", Stream: "multi-chunk", Mutations: []vMutation{{Op: "truncate", Length: 2*fullChunk + 1000}}},
		{Name: "flipped byte in chunk 2", Stream: "multi-chunk", Mutations: []vMutation{{Op: "flip", Offset: 2*fullChunk + 77, Mask: 0x10}}},
		{Name: "flipped tag of chunk 0", Stream: "multi-chunk", Mutations: []vMutation{{Op: "flip", Offset: fullChunk - 1, Mask: 1}}},
	}

	// Text padding.
	for _, n := range []int{0, 1, 1023, 1024, 1025, 2048, 2049, 5000, 65536, 65537} {
		vf.TextPadding = append(vf.TextPadding, vPad{Len: n, PaddedLen: PaddedTextLen(n)})
	}

	// Metadata, under K_meta of the "no passphrase" keys.
	mk := keys["no passphrase"].Meta
	emoji := strings.Repeat("\U0001F600", 300)
	for i, m := range []struct {
		name string
		in   Meta
	}{
		{"file", Meta{V: 1, Kind: KindFile, Name: "report.pdf", Type: "application/pdf", Size: 48213}},
		{"text", Meta{Kind: KindText, Type: "text/plain; charset=utf-8", Size: 21}},
		{"escapes", Meta{Kind: KindFile, Name: "q\"b\\s/\n\t\b\f\r\x01\x1f\x7f<>&\u2028\u2029\u00E9\u20AC\U0001F600", Type: "application/x-\"odd\"", Size: 0}},
		{"long name truncated", Meta{Kind: KindFile, Name: emoji, Type: "application/octet-stream", Size: 1048577}},
		{"escaped name truncated", Meta{Kind: KindFile, Name: strings.Repeat("\"", 600) + "tail", Size: 1}},
		{"max size", Meta{Kind: KindFile, Name: "big.bin", Size: MaxSize}},
	} {
		nonce := tb12(fmt.Sprintf("meta nonce %d", i))
		pt, stored, err := encodeMeta(m.in)
		if err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		sealed, err := sealMetaNonce(mk, m.in, nonce)
		if err != nil {
			t.Fatal(err)
		}
		if m.name == "long name truncated" {
			// The byte budget must not fall on a rune boundary, so a byte cut
			// would split a character.
			budget := MaxMetaJSON - len(metaJSON(Meta{V: 1, Kind: m.in.Kind, Type: m.in.Type, Size: m.in.Size}))
			if budget%4 == 0 || utf8.RuneStart(m.in.Name[budget]) {
				t.Fatalf("pick a type so the budget (%d) splits a rune", budget)
			}
		}
		vf.Meta = append(vf.Meta, vMeta{
			Name: m.name, Key: mk[:], Nonce: nonce[:],
			Input: vMetaJSON(m.in), Stored: vMetaJSON(stored), Plaintext: pt[:], Sealed: sealed,
		})
	}
	seal := func(pt []byte, key [32]byte) hexBytes {
		out, err := sealMetaRaw(key, pt, tb12("bad meta nonce"))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	okJSON := func(js string) []byte { return rawMeta(js, len(js)) }
	for _, o := range []struct {
		name string
		js   string
		want Meta
	}{
		{"unknown field ignored", `{"v":1,"kind":"file","name":"a","type":"b","size":3,"extra":{"x":[1,2]}}`, Meta{1, KindFile, "a", "b", 3}},
		{"other key order", `{"size":3,"type":"b","name":"a","kind":"text","v":1}`, Meta{1, KindText, "a", "b", 3}},
		{"whitespace and escapes", "{ \"v\" : 1.0 ,\n\"kind\":\"file\", \"name\":\"\\u00e9\\ud83d\\ude00\\/\", \"type\":\"\", \"size\": 1e3 }  ", Meta{1, KindFile, "\u00E9\U0001F600/", "", 1000}},
	} {
		vf.MetaOpen = append(vf.MetaOpen, vMetaOpen{Name: o.name, Key: mk[:], Sealed: seal(okJSON(o.js), mk), Meta: vMetaJSON(o.want)})
	}
	good := vf.Meta[0].Sealed
	goodJS := `{"v":1,"kind":"file","name":"a","type":"b","size":3}`
	short := bytes.Clone(good[:len(good)-1])
	long := append(bytes.Clone(good), 0)
	flipCT := bytes.Clone(good)
	flipCT[20] ^= 1
	flipNonce := bytes.Clone(good)
	flipNonce[0] ^= 1
	padded := okJSON(goodJS)
	padded[MetaSize-1] = 1
	overlong := rawMeta(goodJS, len(goodJS)+5)
	vf.MetaInvalid = []vMetaInvalid{
		{"too short", mk[:], short},
		{"too long", mk[:], long},
		{"flipped ciphertext bit", mk[:], flipCT},
		{"flipped nonce bit", mk[:], flipNonce},
		{"wrong key", mk[:], seal(okJSON(goodJS), keys["passphrase"].Meta)},
		{"version 2", mk[:], seal(okJSON(`{"v":2,"kind":"file","name":"a","type":"b","size":3}`), mk)},
		{"version as string", mk[:], seal(okJSON(`{"v":"1","kind":"file","name":"a","type":"b","size":3}`), mk)},
		{"version missing", mk[:], seal(okJSON(`{"kind":"file","name":"a","type":"b","size":3}`), mk)},
		{"unknown kind", mk[:], seal(okJSON(`{"v":1,"kind":"link","name":"a","type":"b","size":3}`), mk)},
		{"name not a string", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":5,"type":"b","size":3}`), mk)},
		{"name null", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":null,"type":"b","size":3}`), mk)},
		{"type missing", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","size":3}`), mk)},
		{"negative size", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","type":"b","size":-1}`), mk)},
		{"fractional size", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","type":"b","size":1.5}`), mk)},
		{"size above 2^53-1", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","type":"b","size":9007199254740992}`), mk)},
		{"size as string", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","type":"b","size":"3"}`), mk)},
		{"size overflows float", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"a","type":"b","size":1e400}`), mk)},
		{"truncated json", mk[:], seal(okJSON(`{"v":1,"kind":"file"`), mk)},
		{"json array", mk[:], seal(okJSON(`[1]`), mk)},
		{"json null", mk[:], seal(okJSON(`null`), mk)},
		{"trailing garbage", mk[:], seal(okJSON(goodJS+"x"), mk)},
		{"leading byte order mark", mk[:], seal(okJSON("\uFEFF"+goodJS), mk)},
		{"invalid utf-8", mk[:], seal(okJSON(`{"v":1,"kind":"file","name":"`+"\xff"+`","type":"b","size":3}`), mk)},
		{"length zero", mk[:], seal(rawMeta(goodJS, 0), mk)},
		{"length 1023", mk[:], seal(rawMeta(goodJS, 1023), mk)},
		{"length covers zero padding", mk[:], seal(overlong, mk)},
		{"non-zero padding", mk[:], seal(padded, mk)},
	}

	// Envelopes: complete secrets from link to plaintext.
	for i, e := range []struct {
		name string
		pass *string
		kdf  *KDFParams
		meta Meta
		body []byte
	}{
		{"text without passphrase", nil, nil, Meta{Kind: KindText, Type: "text/plain; charset=utf-8"}, []byte("Launch code: 0000\n")},
		{"file with passphrase", sp("tr0ub4dor&3"), fast, Meta{Kind: KindFile, Name: "notes.pdf", Type: "application/pdf"}, []byte("%PDF-1.7\n% not really\n")},
	} {
		id, key, salt := tb16(fmt.Sprintf("env id %d", i)), tb32(fmt.Sprintf("env link %d", i)), tb16(fmt.Sprintf("env salt %d", i))
		k, err := DeriveKeys(key, id, salt, passBytes(e.pass), kdfOrZero(e.kdf))
		if err != nil {
			t.Fatal(err)
		}
		e.meta.Size = int64(len(e.body))
		content := e.body
		if e.meta.Kind == KindText {
			content = PadText(e.body)
		}
		sealed, err := sealMetaNonce(k.Meta, e.meta, tb12(fmt.Sprintf("env nonce %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		probe := ProbeKey(key, id)
		e.meta.V = 1
		vf.Envelopes = append(vf.Envelopes, vEnvelope{
			Name: e.name, Link: FormatLink("https://snap.example", id, key), Salt: salt[:],
			Passphrase: e.pass, KDF: e.kdf,
			ProbeHash: sum(probe[:]), AuthHash: sum(k.Auth[:]), Commit: k.Commit[:],
			MetaSealed: sealed, Content: encryptAll(t, k.Enc, content), Meta: vMetaJSON(e.meta), Plaintext: e.body,
		})
	}
	return vf
}

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func marshalVectors(t *testing.T, vf vectorFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(vf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestVectors regenerates the vectors and compares them with the golden
// file (or rewrites it with -update).
func TestVectors(t *testing.T) {
	got := marshalVectors(t, buildVectors(t))
	if *update {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale or the implementation changed; diff it after go test -run TestVectors -update", vectorsPath)
	}
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	b, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vf vectorFile
	if err := json.Unmarshal(b, &vf); err != nil {
		t.Fatal(err)
	}
	return vf
}

// TestVectorsConsume checks the golden file the way another implementation
// would: positive vectors reproduce, negative vectors are rejected.
func TestVectorsConsume(t *testing.T) {
	vf := loadVectors(t)

	t.Run("kdf", func(t *testing.T) {
		for _, p := range vf.KDFValid {
			if err := p.Validate(); err != nil {
				t.Errorf("%+v: %v", p, err)
			}
		}
		for _, p := range vf.KDFInvalid {
			if err := p.Validate(); !errors.Is(err, ErrKDFParams) {
				t.Errorf("%+v: Validate = %v", p, err)
			}
			if _, err := DeriveKeys([32]byte{}, [16]byte{}, [16]byte{}, []byte("pw"), p); !errors.Is(err, ErrKDFParams) {
				t.Errorf("%+v: DeriveKeys = %v", p, err)
			}
		}
	})

	t.Run("links", func(t *testing.T) {
		for _, l := range vf.Links {
			if got := FormatLink(l.Base, [16]byte(l.ID), [32]byte(l.Key)); got != l.Link {
				t.Errorf("FormatLink = %q, want %q", got, l.Link)
			}
			base, id, key, err := ParseLink(l.Link)
			if err != nil || base != l.Base || !bytes.Equal(id[:], l.ID) || !bytes.Equal(key[:], l.Key) {
				t.Errorf("ParseLink(%q) = %q %x %x %v", l.Link, base, id, key, err)
			}
		}
		for _, l := range vf.LinksInvalid {
			if _, _, _, err := ParseLink(l.Link); !errors.Is(err, ErrLink) {
				t.Errorf("%s: ParseLink = %v", l.Name, err)
			}
		}
	})

	t.Run("keys", func(t *testing.T) {
		for _, v := range vf.Keys {
			k, err := DeriveKeys([32]byte(v.LinkKey), [16]byte(v.ID), [16]byte(v.Salt), passBytes(v.Passphrase), kdfOrZero(v.KDF))
			if err != nil {
				t.Fatalf("%s: %v", v.Name, err)
			}
			probe := ProbeKey([32]byte(v.LinkKey), [16]byte(v.ID))
			ph, ah := Hash(probe[:]), Hash(k.Auth[:])
			for _, c := range []struct {
				what      string
				got, want []byte
			}{
				{"probe", probe[:], v.Probe}, {"probe_hash", ph[:], v.ProbeHash},
				{"auth", k.Auth[:], v.Auth}, {"auth_hash", ah[:], v.AuthHash},
				{"enc", k.Enc[:], v.Enc}, {"meta", k.Meta[:], v.Meta}, {"commit", k.Commit[:], v.Commit},
			} {
				if !bytes.Equal(c.got, c.want) {
					t.Errorf("%s: %s = %x, want %x", v.Name, c.what, c.got, c.want)
				}
			}
			if err := k.CheckCommit(v.Commit); err != nil {
				t.Errorf("%s: CheckCommit: %v", v.Name, err)
			}
			if v.PassphraseNFC != nil {
				k2, err := DeriveKeys([32]byte(v.LinkKey), [16]byte(v.ID), [16]byte(v.Salt), *v.PassphraseNFC, *v.KDF)
				if err != nil || k2 != k {
					t.Errorf("%s: NFC form derives different keys (%v)", v.Name, err)
				}
			}
		}
		for _, v := range vf.KeysMismatch {
			k, err := DeriveKeys([32]byte(v.LinkKey), [16]byte(v.ID), [16]byte(v.Salt), passBytes(v.Passphrase), kdfOrZero(v.KDF))
			if err != nil {
				t.Fatalf("%s: %v", v.Name, err)
			}
			if err := k.CheckCommit(v.Commit); !errors.Is(err, ErrCommit) {
				t.Errorf("%s: CheckCommit = %v", v.Name, err)
			}
		}
	})

	t.Run("chunks", func(t *testing.T) {
		for _, c := range vf.Chunks {
			ct, err := EncryptChunk([32]byte(c.Key), c.Counter, c.Last, *c.Plaintext)
			if err != nil || !bytes.Equal(ct, c.Ciphertext) {
				t.Errorf("%s: EncryptChunk = %x, %v", c.Name, ct, err)
			}
			pt, err := DecryptChunk([32]byte(c.Key), c.Counter, c.Last, c.Ciphertext)
			if err != nil || !bytes.Equal(pt, *c.Plaintext) {
				t.Errorf("%s: DecryptChunk = %x, %v", c.Name, pt, err)
			}
		}
		for _, c := range vf.ChunksInvalid {
			if _, err := DecryptChunk([32]byte(c.Key), c.Counter, c.Last, c.Ciphertext); !errors.Is(err, ErrDecrypt) {
				t.Errorf("%s: DecryptChunk = %v", c.Name, err)
			}
		}
	})

	cts := map[string][]byte{}
	t.Run("streams", func(t *testing.T) {
		for _, s := range vf.Streams {
			var pt []byte
			if s.PlaintextGen == "mod251" {
				pt = genPlaintext(s.PlaintextLen)
			} else {
				pt = *s.Plaintext
			}
			if !bytes.Equal(sum(pt), s.PlaintextSHA256) {
				t.Fatalf("%s: plaintext hash", s.Name)
			}
			ct := encryptAll(t, [32]byte(s.Key), pt)
			if int64(len(ct)) != s.CiphertextLen || !bytes.Equal(sum(ct), s.CiphertextSHA256) {
				t.Errorf("%s: ciphertext differs", s.Name)
			}
			if s.Ciphertext != nil && !bytes.Equal(ct, *s.Ciphertext) {
				t.Errorf("%s: ciphertext hex differs", s.Name)
			}
			chunks := splitChunks(ct)
			if len(chunks) != len(s.Chunks) {
				t.Fatalf("%s: %d chunks, want %d", s.Name, len(chunks), len(s.Chunks))
			}
			for i, c := range chunks {
				if len(c) != s.Chunks[i].Len || !bytes.Equal(sum(c), s.Chunks[i].SHA256) || !bytes.Equal(c[len(c)-TagSize:], s.Chunks[i].Tag) {
					t.Errorf("%s: chunk %d differs", s.Name, i)
				}
			}
			if CiphertextSize(s.PlaintextLen) != s.CiphertextLen || NumChunks(s.PlaintextLen) != int64(len(s.Chunks)) {
				t.Errorf("%s: size functions disagree", s.Name)
			}
			got, err := decryptAll([32]byte(s.Key), ct)
			if err != nil || !bytes.Equal(got, pt) {
				t.Errorf("%s: decrypt: %v", s.Name, err)
			}
			cts[s.Name] = ct
		}
	})

	t.Run("streams invalid", func(t *testing.T) {
		keys := map[string][]byte{}
		for _, s := range vf.Streams {
			keys[s.Name] = s.Key
		}
		for _, s := range vf.StreamsInvalid {
			ct, err := mutate(cts[s.Stream], s.Mutations)
			if err != nil {
				t.Fatal(err)
			}
			key := keys[s.Stream]
			if s.Key != nil {
				key = *s.Key
			}
			if _, err := decryptAll([32]byte(key), ct); !errors.Is(err, ErrDecrypt) {
				t.Errorf("%s: decrypt = %v", s.Name, err)
			}
		}
	})

	t.Run("text padding", func(t *testing.T) {
		for _, p := range vf.TextPadding {
			if got := len(PadText(make([]byte, p.Len))); got != p.PaddedLen {
				t.Errorf("PadText(%d) = %d bytes, want %d", p.Len, got, p.PaddedLen)
			}
		}
	})

	t.Run("meta", func(t *testing.T) {
		for _, m := range vf.Meta {
			pt, stored, err := encodeMeta(m.Input.meta())
			if err != nil || !bytes.Equal(pt[:], m.Plaintext) || stored != m.Stored.meta() {
				t.Errorf("%s: encodeMeta = %v %+v", m.Name, err, stored)
			}
			sealed, err := sealMetaNonce([32]byte(m.Key), m.Input.meta(), [12]byte(m.Nonce))
			if err != nil || !bytes.Equal(sealed, m.Sealed) {
				t.Errorf("%s: sealMeta differs (%v)", m.Name, err)
			}
			got, err := OpenMeta([32]byte(m.Key), m.Sealed)
			if err != nil || got != m.Stored.meta() {
				t.Errorf("%s: OpenMeta = %+v, %v", m.Name, got, err)
			}
		}
		for _, m := range vf.MetaOpen {
			got, err := OpenMeta([32]byte(m.Key), m.Sealed)
			if err != nil || got != m.Meta.meta() {
				t.Errorf("%s: OpenMeta = %+v, %v", m.Name, got, err)
			}
		}
		for _, m := range vf.MetaInvalid {
			if _, err := OpenMeta([32]byte(m.Key), m.Sealed); !errors.Is(err, ErrMeta) && !errors.Is(err, ErrDecrypt) {
				t.Errorf("%s: OpenMeta = %v", m.Name, err)
			}
		}
	})

	t.Run("envelopes", func(t *testing.T) {
		for _, e := range vf.Envelopes {
			_, id, key, err := ParseLink(e.Link)
			if err != nil {
				t.Fatal(err)
			}
			probe := ProbeKey(key, id)
			if h := Hash(probe[:]); !bytes.Equal(h[:], e.ProbeHash) {
				t.Errorf("%s: probe hash", e.Name)
			}
			k, err := DeriveKeys(key, id, [16]byte(e.Salt), passBytes(e.Passphrase), kdfOrZero(e.KDF))
			if err != nil {
				t.Fatal(err)
			}
			if h := Hash(k.Auth[:]); !bytes.Equal(h[:], e.AuthHash) {
				t.Errorf("%s: auth hash", e.Name)
			}
			if err := k.CheckCommit(e.Commit); err != nil {
				t.Fatalf("%s: %v", e.Name, err)
			}
			m, err := OpenMeta(k.Meta, e.MetaSealed)
			if err != nil || m != e.Meta.meta() {
				t.Fatalf("%s: OpenMeta = %+v, %v", e.Name, m, err)
			}
			content, err := decryptAll(k.Enc, e.Content)
			if err != nil {
				t.Fatal(err)
			}
			if m.Kind == KindText {
				if content, err = UnpadText(content, m.Size); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(content, e.Plaintext) || int64(len(content)) != m.Size {
				t.Errorf("%s: plaintext %q", e.Name, content)
			}
		}
	})
}
