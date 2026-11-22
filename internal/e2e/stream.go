package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// STREAM framing constants.
const (
	ChunkSize = 1 << 20 // plaintext bytes per chunk; only the last chunk may be shorter
	TagSize   = 16      // AES-GCM tag appended to every chunk
	NonceSize = 12      // 11-byte big-endian counter || 1 flag byte

	fullChunk = ChunkSize + TagSize // ciphertext size of a full chunk
)

var (
	// ErrDecrypt reports a ciphertext that failed authentication: wrong key,
	// modified, reordered, truncated or extended.
	ErrDecrypt = errors.New("e2e: decryption failed")
	// ErrChunkSize reports a plaintext chunk that breaks the framing rules:
	// a non-final chunk must be exactly ChunkSize bytes, a final one at most
	// ChunkSize, and only a lone final chunk may be empty.
	ErrChunkSize = errors.New("e2e: invalid chunk size")
	// ErrTooLarge reports a stream that would overflow the chunk counter.
	ErrTooLarge = errors.New("e2e: stream too large")
	// ErrClosed reports a write after Close.
	ErrClosed = errors.New("e2e: encryptor closed")
)

// NumChunks returns the number of chunks for plainLen bytes: at least one
// (empty input is one empty final chunk). It returns -1 for negative input.
func NumChunks(plainLen int64) int64 {
	if plainLen < 0 {
		return -1
	}
	if plainLen == 0 {
		return 1
	}
	return (plainLen-1)/ChunkSize + 1
}

// CiphertextSize returns the STREAM ciphertext size for plainLen bytes of
// plaintext: plainLen + 16 per chunk. It returns -1 for negative input or
// when the result would overflow int64.
func CiphertextSize(plainLen int64) int64 {
	n := NumChunks(plainLen)
	if n < 0 || plainLen > math.MaxInt64-n*TagSize {
		return -1
	}
	return plainLen + n*TagSize
}

func newAEAD(key [KeySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("e2e: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("e2e: gcm: %w", err)
	}
	return aead, nil
}

// chunkNonce returns the 11-byte big-endian counter followed by the flag
// byte (0x01 for the last chunk, else 0x00). The top three counter bytes are
// always zero because the counter is a uint64.
func chunkNonce(counter uint64, last bool) [NonceSize]byte {
	var n [NonceSize]byte
	binary.BigEndian.PutUint64(n[3:11], counter)
	if last {
		n[11] = 1
	}
	return n
}

// validChunkLen reports whether n plaintext bytes may form chunk counter:
// non-final chunks are full, the final chunk holds 0..ChunkSize bytes, and
// it is empty only when it is the only chunk. So each plaintext has exactly
// one encoding.
func validChunkLen(n int, counter uint64, last bool) bool {
	if !last {
		return n == ChunkSize
	}
	return n <= ChunkSize && (n > 0 || counter == 0)
}

func sealChunk(aead cipher.AEAD, dst []byte, counter uint64, last bool, pt []byte) ([]byte, error) {
	if !validChunkLen(len(pt), counter, last) {
		return nil, fmt.Errorf("%w: %d bytes, last=%t", ErrChunkSize, len(pt), last)
	}
	nonce := chunkNonce(counter, last)
	return aead.Seal(dst, nonce[:], pt, nil), nil
}

func openChunk(aead cipher.AEAD, dst []byte, counter uint64, last bool, ct []byte) ([]byte, error) {
	if len(ct) < TagSize || !validChunkLen(len(ct)-TagSize, counter, last) {
		return nil, fmt.Errorf("%w: chunk %d: bad length", ErrDecrypt, counter)
	}
	nonce := chunkNonce(counter, last)
	pt, err := aead.Open(dst, nonce[:], ct, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: chunk %d", ErrDecrypt, counter)
	}
	return pt, nil
}

// EncryptChunk encrypts one STREAM chunk. A non-final chunk must be exactly
// ChunkSize bytes; the final chunk may be 1..ChunkSize bytes, or empty when
// it is the only chunk (counter 0). Never encrypt different plaintext under
// the same key, counter and flag: a retry must resend the ciphertext
// produced the first time.
func EncryptChunk(key [KeySize]byte, counter uint64, last bool, pt []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return sealChunk(aead, make([]byte, 0, len(pt)+TagSize), counter, last, pt)
}

// DecryptChunk decrypts and authenticates one STREAM chunk, applying the
// same length rules as EncryptChunk to the ciphertext minus its tag. All
// failures wrap ErrDecrypt.
func DecryptChunk(key [KeySize]byte, counter uint64, last bool, ct []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return openChunk(aead, nil, counter, last, ct)
}

// NewEncryptor returns a writer that encrypts everything written to it as a
// STREAM to w. It buffers up to ChunkSize bytes; Close writes the final
// chunk (an empty one if nothing was written) and must be called. Close does
// not close w. Errors are sticky.
func NewEncryptor(w io.Writer, key [KeySize]byte) io.WriteCloser {
	aead, err := newAEAD(key)
	return &encryptor{
		w:    w,
		aead: aead,
		err:  err,
		buf:  make([]byte, 0, ChunkSize),
		out:  make([]byte, 0, fullChunk),
	}
}

type encryptor struct {
	w       io.Writer
	aead    cipher.AEAD
	buf     []byte // pending plaintext, at most ChunkSize
	out     []byte // ciphertext scratch
	counter uint64
	closed  bool
	err     error
}

func (e *encryptor) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.closed {
		return 0, ErrClosed
	}
	n := 0
	for len(p) > 0 {
		// A full buffer is only flushed once more data arrives, because
		// until then it might be the final chunk.
		if len(e.buf) == ChunkSize {
			if err := e.flush(false); err != nil {
				return n, err
			}
		}
		k := min(len(p), ChunkSize-len(e.buf))
		e.buf = append(e.buf, p[:k]...)
		p = p[k:]
		n += k
	}
	return n, nil
}

func (e *encryptor) flush(last bool) error {
	if !last && e.counter == math.MaxUint64 {
		e.err = ErrTooLarge
		return e.err
	}
	ct, err := sealChunk(e.aead, e.out[:0], e.counter, last, e.buf)
	if err != nil {
		e.err = err
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		e.err = err
		return err
	}
	clear(e.buf)
	e.buf = e.buf[:0]
	e.counter++
	return nil
}

// Close writes the final chunk. Calling it again returns the first result.
func (e *encryptor) Close() error {
	if e.err != nil || e.closed {
		return e.err
	}
	e.closed = true
	return e.flush(true)
}

// NewDecryptor returns a reader that decrypts and authenticates a STREAM
// read from r. Each chunk is returned only after its tag verifies, but the
// stream as a whole is only known to be complete when Read returns io.EOF:
// truncation is detected at the end. Callers must treat everything read as
// untrusted until they see io.EOF with no error before it, and discard it
// otherwise. Integrity failures wrap ErrDecrypt; errors from r are returned
// as they are. Errors are sticky.
func NewDecryptor(r io.Reader, key [KeySize]byte) io.Reader {
	aead, err := newAEAD(key)
	return &decryptor{
		r:     r,
		aead:  aead,
		err:   err,
		buf:   make([]byte, fullChunk+1),
		plain: make([]byte, 0, ChunkSize),
	}
}

type decryptor struct {
	r       io.Reader
	aead    cipher.AEAD
	buf     []byte // ciphertext; one byte beyond a full chunk to detect the end
	n       int    // bytes held in buf
	plain   []byte // decrypted chunk
	out     []byte // unread part of plain
	counter uint64
	done    bool // final chunk verified
	err     error
}

func (d *decryptor) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			d.err = io.EOF
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			d.err = err
		}
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	return n, nil
}

// next reads and verifies one chunk. A chunk is final exactly when the
// input ends after it.
func (d *decryptor) next() error {
	m, err := io.ReadFull(d.r, d.buf[d.n:])
	d.n += m
	switch {
	case err == nil:
		// More input follows a full chunk, so it is not the final one.
		if d.counter == math.MaxUint64 {
			return ErrTooLarge
		}
		pt, err := openChunk(d.aead, d.plain[:0], d.counter, false, d.buf[:fullChunk])
		if err != nil {
			return err
		}
		d.buf[0] = d.buf[fullChunk]
		d.n = 1
		d.counter++
		d.out = pt
		return nil
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		if d.n < TagSize {
			return fmt.Errorf("%w: chunk %d: truncated", ErrDecrypt, d.counter)
		}
		pt, err := openChunk(d.aead, d.plain[:0], d.counter, true, d.buf[:d.n])
		if err != nil {
			return err
		}
		d.n = 0
		d.done = true
		d.out = pt
		return nil
	default:
		return err
	}
}
