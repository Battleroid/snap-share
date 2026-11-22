package secrets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/stats"
)

// memStore is an in-memory blob.Store for unit tests.
type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
	n  int
}

func (s *memStore) Create(context.Context) (blob.Writer, error) { return &memWriter{s: s}, nil }

func (s *memStore) Open(_ context.Context, key string) (blob.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return memFile{bytes.NewReader(b)}, nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *memStore) Path(string) (string, error) { return "", errors.New("no paths") }

type memWriter struct {
	s   *memStore
	buf bytes.Buffer
}

func (w *memWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *memWriter) Abort() error                { return nil }
func (w *memWriter) Commit() (string, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	if w.s.m == nil {
		w.s.m = map[string][]byte{}
	}
	w.s.n++
	k := fmt.Sprintf("%032x", w.s.n)
	w.s.m[k] = w.buf.Bytes()
	return k, nil
}

type memFile struct{ *bytes.Reader }

func (memFile) Close() error { return nil }

func TestPartsReader(t *testing.T) {
	st := &memStore{}
	var all []byte
	var ps []part
	for i, size := range []int{5, 1, 7, 3} {
		b := bytes.Repeat([]byte{byte('a' + i)}, size)
		w, _ := st.Create(context.Background())
		_, _ = w.Write(b)
		k, _ := w.Commit()
		ps = append(ps, part{Part: int64(i), Key: k, Size: int64(size)})
		all = append(all, b...)
	}
	rd := newPartsReader(context.Background(), st, ps)
	got, err := io.ReadAll(rd)
	if err != nil || !bytes.Equal(got, all) {
		t.Fatalf("read all: %q %v", got, err)
	}
	// Every Range ServeContent might ask for.
	for off := 0; off <= len(all); off++ {
		for n := 0; off+n <= len(all); n++ {
			if _, err := rd.Seek(int64(off), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(rd, buf); err != nil || !bytes.Equal(buf, all[off:off+n]) {
				t.Fatalf("[%d:+%d] = %q, %v", off, n, buf, err)
			}
		}
	}
	if end, _ := rd.Seek(0, io.SeekEnd); end != int64(len(all)) {
		t.Fatalf("size %d", end)
	}
	if _, err := rd.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek")
	}
	// Through http.ServeContent with a Range header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=4-6")
	http.ServeContent(rec, req, "", time.Time{}, newPartsReader(context.Background(), st, ps))
	if rec.Code != http.StatusPartialContent || rec.Body.String() != string(all[4:7]) {
		t.Fatalf("range: %d %q", rec.Code, rec.Body)
	}
	// A blob shorter than its ledger entry is an error, not silent truncation.
	short := append([]part(nil), ps...)
	short[1].Size = 2
	if _, err := io.ReadAll(newPartsReader(context.Background(), st, short)); err == nil {
		t.Fatal("size mismatch not detected")
	}
	_ = rd.Close()
}

func TestTicketTTL(t *testing.T) {
	for size, want := range map[int64]time.Duration{
		0:         MinTicketTTL,
		100 << 20: MinTicketTTL,
		256 << 20: 2148 * time.Second, // 268435456 bytes at 125000 B/s
	} {
		if got := ticketTTL(size); got != want {
			t.Errorf("ticketTTL(%d) = %v, want %v", size, got, want)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": 0, "5m": 5 * time.Minute, "1h": time.Hour, "1d": 24 * time.Hour, "30d": 30 * 24 * time.Hour,
		"300": 300 * time.Second, "1h30m": 90 * time.Minute,
	} {
		if got, err := parseExpiry(in); err != nil || got != want {
			t.Errorf("parseExpiry(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"0", "-1", "x", "0d", "-5m", "1.5d", "99999d"} {
		if _, err := parseExpiry(in); err == nil {
			t.Errorf("parseExpiry(%q) accepted", in)
		}
	}
}

func TestCleanNote(t *testing.T) {
	if got, err := cleanNote("  for Sam  "); err != nil || got != "for Sam" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := cleanNote(strings.Repeat("é", 200)); err != nil || got == "" {
		t.Fatalf("200 runes: %v", err)
	}
	for _, bad := range []string{strings.Repeat("x", 201), "a\nb", "a\x00b", "a\u202eb", "a\u2066b", "\xff"} {
		if _, err := cleanNote(bad); err == nil {
			t.Errorf("cleanNote(%q) accepted", bad)
		}
	}
}

func TestHashEqual(t *testing.T) {
	secret := []byte("probe")
	h := sha256.Sum256(secret)
	if !hashEqual(h[:], secret) || hashEqual(h[:], []byte("other")) || hashEqual(nil, secret) || hashEqual(h[:31], secret) {
		t.Fatal("hashEqual")
	}
}

func TestNotificationMessage(t *testing.T) {
	m := notificationMessage(Notification{
		SecretID: "AAAAAAAAAAAAAAAAAAAAAA", Note: "prod db", Kind: stats.KindRevealed, ViewsLeft: 1, MaxViews: 2,
		At: time.Date(2026, 11, 22, 14, 3, 0, 0, time.UTC), IP: "203.0.113.4", UASummary: "Evil\r\nBcc: x@y\u202e",
	}, "https://snap.test/")
	if strings.ContainsAny(m.Subject, "\r\n") || strings.Contains(m.Subject, "\u202e") {
		t.Fatalf("subject %q", m.Subject)
	}
	if strings.Contains(m.Text, "AAAAAAAAAAAAAAAAAAAAAA") || !strings.Contains(m.Text, "https://snap.test/dashboard/secrets") {
		t.Fatalf("text %q", m.Text)
	}
	b := notificationMessage(Notification{Kind: stats.KindBurned, At: time.Now()}, "https://snap.test")
	if !strings.Contains(b.Subject, "failed passphrase") || !strings.Contains(b.Text, "—") {
		t.Fatalf("burned %+v", b)
	}
}

func TestPlainSizeAndChunks(t *testing.T) {
	r := &row{CTSize: 2*fullChunk + 26, ChunkCount: 3}
	if r.plainSize() != 2*(fullChunk-16)+10 || r.expectedChunkSize(0) != fullChunk || r.expectedChunkSize(2) != 26 {
		t.Fatalf("sizes: %d %d", r.plainSize(), r.expectedChunkSize(2))
	}
}
