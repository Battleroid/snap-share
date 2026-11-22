package e2e

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func TestNumChunksCiphertextSize(t *testing.T) {
	tests := []struct {
		n      int64
		chunks int64
		size   int64
	}{
		{-1, -1, -1},
		{0, 1, 16},
		{1, 1, 17},
		{ChunkSize - 1, 1, ChunkSize + 15},
		{ChunkSize, 1, ChunkSize + 16},
		{ChunkSize + 1, 2, ChunkSize + 33},
		{2 * ChunkSize, 2, 2*ChunkSize + 32},
		{2*ChunkSize + 1, 3, 2*ChunkSize + 49},
		{math.MaxInt64, math.MaxInt64/ChunkSize + 1, -1},
	}
	for _, tt := range tests {
		if got := NumChunks(tt.n); got != tt.chunks {
			t.Errorf("NumChunks(%d) = %d, want %d", tt.n, got, tt.chunks)
		}
		if got := CiphertextSize(tt.n); got != tt.size {
			t.Errorf("CiphertextSize(%d) = %d, want %d", tt.n, got, tt.size)
		}
	}
}

func TestChunkNonce(t *testing.T) {
	tests := []struct {
		counter uint64
		last    bool
		want    string
	}{
		{0, false, "000000000000000000000000"},
		{0, true, "000000000000000000000001"},
		{1, false, "000000000000000000000100"},
		{0x0102030405060708, true, "000000010203040506070801"},
		{math.MaxUint64, false, "000000ffffffffffffffff00"},
	}
	for _, tt := range tests {
		n := chunkNonce(tt.counter, tt.last)
		if got := hex.EncodeToString(n[:]); got != tt.want {
			t.Errorf("chunkNonce(%#x, %t) = %s, want %s", tt.counter, tt.last, got, tt.want)
		}
	}
}

func TestChunkRoundTrip(t *testing.T) {
	key := tb32("chunk rt")
	full := genPlaintext(ChunkSize)
	tests := []struct {
		name    string
		counter uint64
		last    bool
		pt      []byte
	}{
		{"empty only chunk", 0, true, nil},
		{"short final", 7, true, []byte("x")},
		{"full final", 3, true, full},
		{"full non-final", 0, false, full},
		{"max counter final", math.MaxUint64, true, []byte("end")},
	}
	for _, tt := range tests {
		ct, err := EncryptChunk(key, tt.counter, tt.last, tt.pt)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if len(ct) != len(tt.pt)+TagSize {
			t.Fatalf("%s: len %d", tt.name, len(ct))
		}
		pt, err := DecryptChunk(key, tt.counter, tt.last, ct)
		if err != nil || !bytes.Equal(pt, tt.pt) {
			t.Fatalf("%s: DecryptChunk: %v", tt.name, err)
		}
		if _, err := DecryptChunk(key, tt.counter+1, tt.last, ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: wrong counter accepted: %v", tt.name, err)
		}
		if _, err := DecryptChunk(key, tt.counter, !tt.last, ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: wrong flag accepted: %v", tt.name, err)
		}
		other := key
		other[0] ^= 1
		if _, err := DecryptChunk(other, tt.counter, tt.last, ct); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: wrong key accepted: %v", tt.name, err)
		}
	}
}

func TestChunkSizeRules(t *testing.T) {
	key := tb32("chunk rules")
	enc := []struct {
		name    string
		counter uint64
		last    bool
		n       int
	}{
		{"short non-final", 0, false, ChunkSize - 1},
		{"long non-final", 0, false, ChunkSize + 1},
		{"empty non-final", 0, false, 0},
		{"long final", 0, true, ChunkSize + 1},
		{"empty final after others", 1, true, 0},
	}
	for _, tt := range enc {
		if _, err := EncryptChunk(key, tt.counter, tt.last, make([]byte, tt.n)); !errors.Is(err, ErrChunkSize) {
			t.Errorf("EncryptChunk %s: %v", tt.name, err)
		}
		// The same shapes are rejected on the way in, whatever the tag.
		if _, err := DecryptChunk(key, tt.counter, tt.last, make([]byte, tt.n+TagSize)); !errors.Is(err, ErrDecrypt) {
			t.Errorf("DecryptChunk %s: %v", tt.name, err)
		}
	}
	for _, n := range []int{0, 1, TagSize - 1} {
		if _, err := DecryptChunk(key, 0, true, make([]byte, n)); !errors.Is(err, ErrDecrypt) {
			t.Errorf("DecryptChunk(%d bytes): %v", n, err)
		}
	}
}

// manualStream builds a STREAM from EncryptChunk calls.
func manualStream(t *testing.T, key [32]byte, pt []byte) []byte {
	t.Helper()
	n := NumChunks(int64(len(pt)))
	var out []byte
	for i := range n {
		end := min(int64(len(pt)), (i+1)*ChunkSize)
		ct, err := EncryptChunk(key, uint64(i), i == n-1, pt[i*ChunkSize:end])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ct...)
	}
	return out
}

func writeIn(t *testing.T, w io.WriteCloser, pt []byte, piece int) {
	t.Helper()
	for len(pt) > 0 {
		k := min(piece, len(pt))
		n, err := w.Write(pt[:k])
		if err != nil || n != k {
			t.Fatalf("Write = %d, %v", n, err)
		}
		pt = pt[k:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamRoundTrip(t *testing.T) {
	key := tb32("stream rt")
	sizes := []int{0, 1, 1000, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2 * ChunkSize, 2*ChunkSize + 5}
	pieces := []int{math.MaxInt, 4096, 65537, ChunkSize}
	for _, size := range sizes {
		pt := genPlaintext(int64(size))
		want := manualStream(t, key, pt)
		if int64(len(want)) != CiphertextSize(int64(size)) {
			t.Fatalf("size %d: CiphertextSize disagrees", size)
		}
		for _, piece := range pieces {
			var buf bytes.Buffer
			writeIn(t, NewEncryptor(&buf, key), pt, piece)
			if !bytes.Equal(buf.Bytes(), want) {
				t.Fatalf("size %d, piece %d: encryptor output differs from EncryptChunk", size, piece)
			}
		}
		readers := map[string]func(io.Reader) io.Reader{
			"plain":    func(r io.Reader) io.Reader { return r },
			"half":     iotest.HalfReader,
			"data+eof": iotest.DataErrReader,
		}
		if size <= ChunkSize+1 {
			readers["one byte"] = iotest.OneByteReader
		}
		for name, wrap := range readers {
			got, err := io.ReadAll(NewDecryptor(wrap(bytes.NewReader(want)), key))
			if err != nil || !bytes.Equal(got, pt) {
				t.Fatalf("size %d, %s reader: %v", size, name, err)
			}
		}
	}
}

func TestDecryptorReader(t *testing.T) {
	key := tb32("iotest")
	pt := genPlaintext(ChunkSize + 3000)
	ct := manualStream(t, key, pt)
	if err := iotest.TestReader(NewDecryptor(bytes.NewReader(ct), key), pt); err != nil {
		t.Fatal(err)
	}
}

func TestStreamTamper(t *testing.T) {
	key := tb32("tamper")
	pt := genPlaintext(2*ChunkSize + 100) // chunks: full, full, 100 bytes
	ct := manualStream(t, key, pt)
	wrongKey := key
	wrongKey[5] ^= 1
	emptyFinal1 := rawChunk(t, key, 1, true, nil)
	reencodedLast, err := EncryptChunk(key, 2, false, append(pt[2*ChunkSize:], make([]byte, ChunkSize-100)...))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		key  [32]byte
		muts []vMutation
		ct   []byte // used instead of mutating ct when set
	}{
		{name: "wrong key", key: wrongKey},
		{name: "empty input", muts: []vMutation{{Op: "truncate", Length: 0}}},
		{name: "drop final chunk", muts: []vMutation{{Op: "truncate", Length: 2 * fullChunk}}},
		{name: "drop last two chunks", muts: []vMutation{{Op: "truncate", Length: fullChunk}}},
		{name: "truncate inside final tag", muts: []vMutation{{Op: "truncate", Length: int64(len(ct) - 1)}}},
		{name: "truncate to final tag only", muts: []vMutation{{Op: "truncate", Length: 2*fullChunk + TagSize}}},
		{name: "truncate mid chunk", muts: []vMutation{{Op: "truncate", Length: fullChunk + 12345}}},
		{name: "truncate leaving less than a tag", muts: []vMutation{{Op: "truncate", Length: 2*fullChunk + 5}}},
		{name: "extend one byte", muts: []vMutation{{Op: "append", Hex: []byte{0x42}}}},
		{name: "extend with empty final", muts: []vMutation{{Op: "append", Hex: rawChunk(t, key, 3, true, nil)}}},
		{name: "repeat final chunk", muts: []vMutation{{Op: "repeat", Index: 2}}},
		{name: "repeat first chunk", muts: []vMutation{{Op: "repeat", Index: 0}}},
		{name: "swap chunks", muts: []vMutation{{Op: "swap", A: 0, B: 1}}},
		{name: "drop first chunk", muts: []vMutation{{Op: "drop", Index: 0}}},
		{name: "drop middle chunk", muts: []vMutation{{Op: "drop", Index: 1}}},
		{name: "flip first byte", muts: []vMutation{{Op: "flip", Offset: 0, Mask: 1}}},
		{name: "flip chunk 0 tag", muts: []vMutation{{Op: "flip", Offset: fullChunk - 1, Mask: 0x80}}},
		{name: "flip chunk 1 first byte", muts: []vMutation{{Op: "flip", Offset: fullChunk, Mask: 2}}},
		{name: "flip final byte", muts: []vMutation{{Op: "flip", Offset: int64(len(ct) - 1), Mask: 1}}},
		{name: "full chunk then empty final", ct: append(bytes.Clone(ct[:fullChunk]), emptyFinal1...)},
		{name: "final chunk marked non-final", ct: append(bytes.Clone(ct[:2*fullChunk]), reencodedLast...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := key
			if tt.key != ([32]byte{}) {
				k = tt.key
			}
			in := tt.ct
			if in == nil {
				var err error
				if in, err = mutate(ct, tt.muts); err != nil {
					t.Fatal(err)
				}
			}
			r := NewDecryptor(bytes.NewReader(in), k)
			got, err := io.ReadAll(r)
			if !errors.Is(err, ErrDecrypt) {
				t.Fatalf("ReadAll = %d bytes, %v; want ErrDecrypt", len(got), err)
			}
			// Errors are sticky and never turn into EOF.
			if n, err2 := r.Read(make([]byte, 10)); n != 0 || !errors.Is(err2, ErrDecrypt) {
				t.Fatalf("Read after failure = %d, %v", n, err2)
			}
			// Whatever was returned before the failure is a prefix of
			// authenticated plaintext, never forged data.
			if !bytes.HasPrefix(pt, got) && k == key {
				t.Fatal("returned data is not a plaintext prefix")
			}
		})
	}
}

func TestDecryptorReaderError(t *testing.T) {
	key := tb32("reader error")
	boom := errors.New("boom")
	ct := manualStream(t, key, genPlaintext(ChunkSize+10))
	r := NewDecryptor(io.MultiReader(bytes.NewReader(ct[:fullChunk+3]), iotest.ErrReader(boom)), key)
	got, err := io.ReadAll(r)
	if !errors.Is(err, boom) {
		t.Fatalf("ReadAll error = %v, want boom", err)
	}
	if len(got) != ChunkSize {
		t.Fatalf("got %d bytes before the error", len(got))
	}
}

func TestDecryptorCounterOverflow(t *testing.T) {
	key := tb32("overflow")
	ct := manualStream(t, key, genPlaintext(ChunkSize+1))
	d := NewDecryptor(bytes.NewReader(ct), key).(*decryptor)
	d.counter = math.MaxUint64
	if _, err := io.ReadAll(d); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadAll = %v, want ErrTooLarge", err)
	}
}

func TestEncryptorCounterOverflow(t *testing.T) {
	key := tb32("overflow")
	var buf bytes.Buffer
	e := NewEncryptor(&buf, key).(*encryptor)
	e.counter = math.MaxUint64
	if _, err := e.Write(make([]byte, ChunkSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Write = %v, want ErrTooLarge", err)
	}
	if err := e.Close(); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Close = %v, want sticky ErrTooLarge", err)
	}

	// The final chunk may use the last counter value.
	buf.Reset()
	e = NewEncryptor(&buf, key).(*encryptor)
	e.counter = math.MaxUint64
	writeIn(t, e, make([]byte, ChunkSize), ChunkSize)
	if _, err := DecryptChunk(key, math.MaxUint64, true, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestEncryptorErrors(t *testing.T) {
	key := tb32("enc errors")
	var buf bytes.Buffer
	w := NewEncryptor(&buf, key)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if buf.Len() != TagSize {
		t.Fatalf("empty stream is %d bytes", buf.Len())
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close = %v", err)
	}

	boom := errors.New("boom")
	w = NewEncryptor(failWriter{boom}, key)
	if _, err := w.Write(make([]byte, ChunkSize)); err != nil {
		t.Fatalf("buffered Write = %v", err) // nothing is written until more data arrives
	}
	if n, err := w.Write([]byte("more")); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("Write = %d, %v; want boom", n, err)
	}
	if err := w.Close(); !errors.Is(err, boom) {
		t.Fatalf("Close = %v, want sticky boom", err)
	}
}
