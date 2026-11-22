package secrets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestTextRoundTrip(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "hunter2 ünïcødé", 1, nil)

	st, ir, b := e.info(sec)
	mustStatus(t, "info", st, http.StatusOK, b)
	if ir.State != "ready" || ir.Kind != "text" || ir.Mode != "e2e" || ir.ViewsLeft != 1 || ir.KDF != nil || ir.Size != 1024 {
		t.Fatalf("info: %s", b)
	}

	meta, pt, cr := e.open(sec, nil)
	if string(pt) != "hunter2 ünïcødé" || meta.Kind != "text" || cr.ViewsLeft != 0 {
		t.Fatalf("opened %q %+v %+v", pt, meta, cr)
	}

	// Opened once: the link now shows a tombstone with the time only.
	st, ir, b = e.info(sec)
	mustStatus(t, "info after", st, http.StatusGone, b)
	if ir.State != "burned" || ir.BurnReason != "viewed" || ir.OpenedAt == nil {
		t.Fatalf("tombstone: %s", b)
	}
	keys := map[string]any{}
	_ = json.Unmarshal(b, &keys)
	for k := range keys {
		if !slices.Contains([]string{"error", "state", "opened_at", "burn_reason"}, k) {
			t.Errorf("tombstone leaks %q: %s", k, b)
		}
	}
	// And a second claim gets the same.
	st, _, b = e.claimWith(sec, [32]byte{})
	mustStatus(t, "second claim", st, http.StatusGone, b)
}

func TestMultiChunkFileWithPassphrase(t *testing.T) {
	e := newEnv(t)
	data := gen(2*e2e.ChunkSize + 12345)
	pass := []byte("correct horse battery staple")
	sec := e.createFile("ann", "report.pdf", data, 2, pass)
	if n := len(e.store.Keys()); n != 4 { // 3 chunks + metadata
		t.Fatalf("stored %d blobs, want 4", n)
	}

	st, ir, b := e.info(sec)
	mustStatus(t, "info", st, http.StatusOK, b)
	if ir.KDF == nil || *ir.KDF != testKDF || ir.Size != int64(len(data)) || ir.CTSize != e2e.CiphertextSize(int64(len(data))) {
		t.Fatalf("info: %s", b)
	}

	meta, pt, cr := e.open(sec, pass)
	if !bytes.Equal(pt, data) || meta.Name != "report.pdf" || cr.ViewsLeft != 1 || cr.Ticket == "" {
		t.Fatalf("first open: %d bytes, %+v", len(pt), meta)
	}
	if n := len(e.store.Keys()); n != 4 {
		t.Fatalf("after the first of two views: %d blobs", n)
	}

	// A second view, with a Range request on the way.
	keys := e.keys(sec, pass)
	st, cr, b = e.claimWith(sec, keys.Auth)
	mustStatus(t, "claim 2", st, http.StatusOK, b)
	st, part, h := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil,
		header(secrets.HeaderTicket, cr.Ticket), header("Range", "bytes=1048590-1048600"))
	mustStatus(t, "range", st, http.StatusPartialContent, part)
	if len(part) != 11 || h.Get("Cache-Control") != "no-store" || h.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("range: %d bytes, %v", len(part), h)
	}
	st, full, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, header(secrets.HeaderTicket, cr.Ticket))
	mustStatus(t, "blob", st, http.StatusOK, full)
	if !bytes.Equal(full[1048590:1048601], part) {
		t.Fatal("range bytes differ from the full download")
	}
	// The metadata is gone with the last view; the content waits for done.
	if n := len(e.store.Keys()); n != 3 {
		t.Fatalf("after the last claim: %d blobs, want 3 chunks", n)
	}
	st, b, _ = e.do("POST", "/api/v1/secrets/"+sec.ids+"/done", nil, header(secrets.HeaderTicket, cr.Ticket))
	mustStatus(t, "done", st, http.StatusNoContent, b)
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("after done: %d blobs", n)
	}
	if n := e.col(`SELECT count(*) FROM secret_blobs`); n != 0 {
		t.Fatalf("ledger rows left: %d", n)
	}
}

func TestEmptyFileAndExactChunk(t *testing.T) {
	e := newEnv(t)
	for _, n := range []int{0, e2e.ChunkSize, e2e.ChunkSize + 1} {
		data := gen(n)
		sec := e.createFile("ann", "x.bin", data, 1, nil)
		_, pt, _ := e.open(sec, nil)
		if !bytes.Equal(pt, data) {
			t.Fatalf("%d bytes: round trip differs", n)
		}
	}
}

func TestParallelClaimsExactlyViews(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "only three", 3, nil)
	keys := e.keys(sec, nil)

	var wg sync.WaitGroup
	codes := make([]int, 100)
	for i := range codes {
		wg.Go(func() {
			st, _, _ := e.claimWith(sec, keys.Auth)
			codes[i] = st
		})
	}
	wg.Wait()
	count := map[int]int{}
	for _, c := range codes {
		count[c]++
	}
	if count[http.StatusOK] != 3 || count[http.StatusGone] != 97 {
		t.Fatalf("claims: %v", count)
	}
	if v := e.col(`SELECT views_left FROM secrets WHERE id = ?`, sec.ids); v != 0 {
		t.Fatalf("views_left %d", v)
	}
	eventually(t, "text ciphertext deleted", func() bool { return len(e.store.Keys()) == 0 })
}

func TestWrongProbeIsNeverCounted(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "x", 1, []byte("pw"))
	keys := e.keys(sec, []byte("wrong"))

	wrong := sec
	wrong.key[0] ^= 1 // a different link key: wrong probe
	missing := sec
	missing.id[0] ^= 1
	missing.ids = e2e.Encode(missing.id[:])

	_, _, ref := e.info(missing)
	for name, s := range map[string]secret{"wrong probe": wrong, "missing id": missing} {
		st, _, b := e.info(s)
		mustStatus(t, name+" info", st, http.StatusNotFound, b)
		if !bytes.Equal(b, ref) {
			t.Fatalf("%s: body %s differs from %s", name, b, ref)
		}
		for range 20 {
			st, _, b = e.claimWith(s, keys.Auth)
			mustStatus(t, name+" claim", st, http.StatusNotFound, b)
			if !bytes.Equal(b, ref) {
				t.Fatalf("%s claim: body %s differs", name, b)
			}
		}
	}
	// Malformed ids and probes read the same too.
	for _, path := range []string{"/api/v1/secrets/short/info", "/api/v1/secrets/" + sec.ids + "A/info"} {
		st, b, _ := e.do("POST", path, map[string]string{"probe": sec.probe()})
		if st != http.StatusNotFound || !bytes.Equal(b, ref) {
			t.Fatalf("%s: %d %s", path, st, b)
		}
	}
	for _, body := range []any{map[string]string{}, map[string]string{"probe": "x"}, map[string]string{"probe": sec.probe()[:42] + "B"}} {
		st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/info", body)
		if st != http.StatusNotFound || !bytes.Equal(b, ref) {
			t.Fatalf("probe %v: %d %s", body, st, b)
		}
	}
	if f := e.col(`SELECT failures FROM secrets WHERE id = ?`, sec.ids); f != 0 {
		t.Fatalf("failures = %d after wrong probes", f)
	}
	// Uploading (unfinalized) secrets don't exist either.
	up, _ := e.begin("ann", "text", 1024, 1, nil)
	st, _, b := e.info(up)
	if st != http.StatusNotFound || !bytes.Equal(b, ref) {
		t.Fatalf("unfinalized: %d %s", st, b)
	}
}

func TestWrongPassphraseBurnsAtMaxFailures(t *testing.T) {
	e := newEnv(t)
	// Notifications are opt-in (internal/notify) and sent by the job queue.
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO notify_prefs (user_id, email_enabled, updated_at) VALUES (1, 1, 0)`); err != nil {
		t.Fatal(err)
	}
	jobsCtx, stopJobs := context.WithCancel(context.Background())
	jobsDone := make(chan struct{})
	go func() { defer close(jobsDone); e.s.App.Jobs.Run(jobsCtx) }()
	t.Cleanup(func() { stopJobs(); <-jobsDone })
	sec := e.createFile("ann", "keys.txt", gen(5000), 2, []byte("right"))
	e.createText("ann", "other", 1, nil)
	bad := e.keys(sec, []byte("wrong"))

	for i := 1; i <= 9; i++ {
		st, cr, b := e.claimWith(sec, bad.Auth)
		mustStatus(t, "wrong", st, http.StatusForbidden, b)
		if cr.AttemptsLeft != 10-i || cr.State != "ready" {
			t.Fatalf("attempt %d: %s", i, b)
		}
		_, ir, _ := e.info(sec)
		if ir.ViewsLeft != 2 {
			t.Fatalf("a wrong passphrase used a view: %+v", ir)
		}
	}
	st, cr, b := e.claimWith(sec, bad.Auth)
	mustStatus(t, "tenth", st, http.StatusForbidden, b)
	if cr.AttemptsLeft != 0 || cr.State != "burned" {
		t.Fatalf("tenth: %s", b)
	}
	st, ir, b := e.info(sec)
	mustStatus(t, "info", st, http.StatusGone, b)
	if ir.BurnReason != "failed attempts" || ir.OpenedAt != nil {
		t.Fatalf("tombstone: %s", b)
	}
	// Any claim is too late now.
	st, _, b = e.claimWith(sec, bad.Auth)
	mustStatus(t, "after burn", st, http.StatusGone, b)
	if v := e.col(`SELECT views_left FROM secrets WHERE id = ?`, sec.ids); v != 2 {
		t.Fatalf("views_left %d", v)
	}
	// Its files are gone; the other secret's stay.
	if n := e.col(`SELECT count(*) FROM secret_blobs WHERE secret_id = ?`, sec.ids); n != 0 {
		t.Fatalf("%d ledger rows left", n)
	}
	if n := len(e.store.Keys()); n != 2 {
		t.Fatalf("store has %d blobs, want the other secret's 2", n)
	}
	// The owner is told.
	eventually(t, "burn notification", func() bool { return len(e.s.Mail.Sent()) == 1 })
	m := e.s.Mail.Sent()[0]
	if m.To[0] != "ann@example.test" || !strings.Contains(m.Subject, "failed passphrase") || strings.Contains(m.Text, sec.ids) {
		t.Fatalf("mail: %+v", m)
	}
}

func TestTicketsAreBoundAndExpire(t *testing.T) {
	e := newEnv(t)
	a := e.createFile("ann", "a.bin", gen(3000), 3, nil)
	b := e.createFile("ann", "b.bin", gen(3000), 3, nil)
	ka := e.keys(a, nil)
	st, cr, body := e.claimWith(a, ka.Auth)
	mustStatus(t, "claim", st, http.StatusOK, body)

	get := func(sec secret, opts ...func(*http.Request)) int {
		st, _, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, opts...)
		return st
	}
	if st := get(a, header(secrets.HeaderTicket, cr.Ticket)); st != http.StatusOK {
		t.Fatalf("own ticket: %d", st)
	}
	if st := get(b, header(secrets.HeaderTicket, cr.Ticket)); st != http.StatusNotFound {
		t.Fatalf("ticket on another secret: %d", st)
	}
	if st := get(a); st != http.StatusNotFound {
		t.Fatalf("no ticket: %d", st)
	}
	st, _, _ = e.do("GET", "/api/v1/secrets/"+a.ids+"/blob?ticket="+cr.Ticket, nil)
	if st != http.StatusNotFound {
		t.Fatalf("ticket in the URL: %d", st)
	}
	if st := get(a, header("Authorization", "Bearer "+cr.Ticket)); st != http.StatusUnauthorized {
		t.Fatalf("ticket as Authorization: %d", st)
	}
	st, _, _ = e.do("POST", "/api/v1/secrets/"+b.ids+"/done", nil, header(secrets.HeaderTicket, cr.Ticket))
	if st != http.StatusNotFound {
		t.Fatalf("done on another secret: %d", st)
	}
	// TTL is at least 15 minutes.
	exp, err := time.Parse(time.RFC3339, cr.TicketExpiresAt)
	if err != nil || time.Until(exp) < 14*time.Minute {
		t.Fatalf("ticket expiry %q", cr.TicketExpiresAt)
	}
	e.exec(`UPDATE secret_tickets SET expires_at = 1`)
	if st := get(a, header(secrets.HeaderTicket, cr.Ticket)); st != http.StatusNotFound {
		t.Fatalf("expired ticket: %d", st)
	}
	st, _, _ = e.do("POST", "/api/v1/secrets/"+a.ids+"/done", nil, header(secrets.HeaderTicket, cr.Ticket))
	if st != http.StatusNotFound {
		t.Fatalf("done with an expired ticket: %d", st)
	}
}

func TestTicketOutlivesSweep(t *testing.T) {
	e := newEnv(t)
	data := gen(e2e.ChunkSize + 7)
	sec := e.createFile("ann", "f.bin", data, 1, nil)
	keys := e.keys(sec, nil)
	st, cr, b := e.claimWith(sec, keys.Auth)
	mustStatus(t, "claim", st, http.StatusOK, b)
	if cr.ViewsLeft != 0 {
		t.Fatal("expected the last view")
	}
	ctx := context.Background()
	// Sweeps while the ticket is live, even past the secret's expiry, keep
	// the content.
	for _, at := range []time.Time{time.Now(), time.Now().Add(10 * time.Minute)} {
		if err := e.svc.SweepAt(ctx, at); err != nil {
			t.Fatal(err)
		}
		st, got, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, header(secrets.HeaderTicket, cr.Ticket))
		mustStatus(t, "blob after sweep", st, http.StatusOK, got)
		pt, err := readAllDecrypt(got, keys.Enc)
		if err != nil || !bytes.Equal(pt, data) {
			t.Fatalf("after sweep: %v", err)
		}
	}
	// Once the ticket is past its expiry, the sweeper deletes the content.
	if err := e.svc.SweepAt(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs left", n)
	}
	if n := e.col(`SELECT count(*) FROM secret_tickets`); n != 0 {
		t.Fatalf("%d tickets left", n)
	}
}

func readAllDecrypt(ct []byte, key [32]byte) ([]byte, error) {
	var out bytes.Buffer
	_, err := out.ReadFrom(e2e.NewDecryptor(bytes.NewReader(ct), key))
	return out.Bytes(), err
}

func TestTextCiphertextGoneAfterLastView(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "bye", 2, nil)
	e.open(sec, nil)
	if n := len(e.store.Keys()); n != 2 {
		t.Fatalf("after one of two views: %d blobs", n)
	}
	e.open(sec, nil)
	eventually(t, "ciphertext deleted", func() bool { return len(e.store.Keys()) == 0 })
	eventually(t, "ledger cleared", func() bool { return e.col(`SELECT count(*) FROM secret_blobs`) == 0 })
}

func TestUploadAndFinalizeValidation(t *testing.T) {
	e := newEnv(t)
	sec, cr := e.begin("ann", "file", int64(e2e.ChunkSize+10), 1, nil)
	if cr.Chunks != 2 || cr.CTSize != int64(e2e.ChunkSize+10+32) {
		t.Fatalf("create: %+v", cr)
	}
	salt, _ := e2e.NewSalt()
	keys, _ := e2e.DeriveKeys(sec.key, sec.id, salt, nil, e2e.KDFParams{})
	ct := chunks(encrypt(t, keys.Enc, gen(e2e.ChunkSize+10)))
	meta := e2e.Meta{Kind: "file", Name: "a", Type: "x/y", Size: int64(e2e.ChunkSize + 10)}
	fin := func(body map[string]any, token string) (int, []byte) {
		st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/finalize", body, header(secrets.HeaderUploadToken, token))
		return st, b
	}

	// Chunks: wrong token, out of order, wrong size, bad numbers.
	bad := sec
	bad.token = e2e.Encode(make([]byte, 32))
	if st, b := e.putChunk(bad, 0, ct[0]); st != http.StatusNotFound {
		t.Fatalf("wrong token: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 1, ct[1]); st != http.StatusConflict {
		t.Fatalf("out of order: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 0, ct[0][:100]); st != http.StatusBadRequest {
		t.Fatalf("short chunk: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 0, append(slices.Clone(ct[0]), 1)); st != http.StatusBadRequest {
		t.Fatalf("long chunk: %d %s", st, b)
	}
	for _, n := range []string{"2", "-1", "01", "x"} {
		st, b, _ := e.do("PUT", "/api/v1/secrets/"+sec.ids+"/chunks/"+n, ct[1], header(secrets.HeaderUploadToken, sec.token))
		if st != http.StatusBadRequest {
			t.Fatalf("chunk %q: %d %s", n, st, b)
		}
	}
	// Finalize before every chunk is in.
	good := finalizeBody(t, sec, salt, nil, keys, meta)
	if st, b := fin(good, sec.token); st != http.StatusConflict {
		t.Fatalf("early finalize: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 0, ct[0]); st != http.StatusOK {
		t.Fatalf("chunk 0: %d %s", st, b)
	}
	// A resend of the same bytes is fine; different bytes are not.
	if st, b := e.putChunk(sec, 0, ct[0]); st != http.StatusOK {
		t.Fatalf("resend: %d %s", st, b)
	}
	other := slices.Clone(ct[0])
	other[5] ^= 1
	if st, b := e.putChunk(sec, 0, other); st != http.StatusConflict {
		t.Fatalf("changed resend: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 1, ct[1]); st != http.StatusOK {
		t.Fatalf("chunk 1: %d %s", st, b)
	}

	for name, mutate := range map[string]func(map[string]any){
		"short meta":   func(m map[string]any) { m["meta_ct"] = m["meta_ct"].(string)[:100] },
		"long meta":    func(m map[string]any) { m["meta_ct"] = m["meta_ct"].(string) + "AAAA" },
		"bad salt":     func(m map[string]any) { m["salt"] = "AAAA" },
		"bad commit":   func(m map[string]any) { m["commit"] = e2e.Encode(make([]byte, 31)) },
		"low m":        func(m map[string]any) { m["kdf"] = e2e.KDFParams{MemKiB: 1024, Time: 3, Threads: 1} },
		"high m":       func(m map[string]any) { m["kdf"] = e2e.KDFParams{MemKiB: 1 << 22, Time: 3, Threads: 1} },
		"zero t":       func(m map[string]any) { m["kdf"] = e2e.KDFParams{MemKiB: 65536, Time: 0, Threads: 1} },
		"two lanes":    func(m map[string]any) { m["kdf"] = e2e.KDFParams{MemKiB: 65536, Time: 3, Threads: 2} },
		"empty kdf":    func(m map[string]any) { m["kdf"] = map[string]any{} },
		"unknown kdf":  func(m map[string]any) { m["kdf"] = map[string]any{"m": 65536, "t": 3, "p": 1, "x": 1} },
		"unknown key":  func(m map[string]any) { m["extra"] = true },
		"padded salt":  func(m map[string]any) { m["salt"] = m["salt"].(string) + "==" },
		"std base64":   func(m map[string]any) { m["meta_ct"] = strings.ReplaceAll(m["meta_ct"].(string), "-", "+") + "+" },
		"missing meta": func(m map[string]any) { delete(m, "meta_ct") },
	} {
		body := finalizeBody(t, sec, salt, nil, keys, meta)
		mutate(body)
		if st, b := fin(body, sec.token); st != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, st, b)
		}
	}
	if st, b := fin(good, bad.token); st != http.StatusNotFound {
		t.Fatalf("finalize with a wrong token: %d %s", st, b)
	}
	if st, b := fin(good, sec.token); st != http.StatusOK {
		t.Fatalf("finalize: %d %s", st, b)
	}
	// The token is spent.
	if st, b := fin(good, sec.token); st != http.StatusNotFound {
		t.Fatalf("second finalize: %d %s", st, b)
	}
	if st, b := e.putChunk(sec, 1, ct[1]); st != http.StatusNotFound {
		t.Fatalf("chunk after finalize: %d %s", st, b)
	}
	if _, pt, _ := e.open(sec, nil); !bytes.Equal(pt, gen(e2e.ChunkSize+10)) {
		t.Fatal("round trip after validation")
	}

	// Create-time limits.
	for name, req := range map[string]map[string]any{
		"unpadded text": {"kind": "text", "size": 1000},
		"text too big":  {"kind": "text", "size": 2 << 20},
		"file too big":  {"kind": "file", "size": 257 << 20},
		"bad kind":      {"kind": "link", "size": 10},
		"zero views":    {"kind": "text", "size": 1024, "views": -1},
		"many views":    {"kind": "text", "size": 1024, "views": 101},
		"short expiry":  {"kind": "text", "size": 1024, "expires_in": 60},
		"long expiry":   {"kind": "text", "size": 1024, "expires_in": 31 * 86400},
		"long note":     {"kind": "text", "size": 1024, "note": strings.Repeat("n", 201)},
		"control note":  {"kind": "text", "size": 1024, "note": "a\u202eb"},
	} {
		st, b, _ := e.do("POST", "/api/v1/secrets", req, as("ann"))
		if st != http.StatusBadRequest && st != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: %d %s", name, st, b)
		}
	}
}

func TestCreateNeedsVerifiedEmailAndScope(t *testing.T) {
	e := newEnv(t)
	e.addUser("carl", 3, false)
	req := map[string]any{"kind": "text", "size": 1024}
	if st, b, _ := e.do("POST", "/api/v1/secrets", req, as("carl")); st != http.StatusForbidden {
		t.Fatalf("unverified: %d %s", st, b)
	}
	e.s.Auth.Add("ann-read", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 9, Scopes: []auth.Scope{auth.ScopeSecretsRead}})
	e.s.Auth.Add("ann-write", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 10, Scopes: []auth.Scope{auth.ScopeSecretsWrite}})
	if st, b, _ := e.do("POST", "/api/v1/secrets", req, func(r *http.Request) { authtest.Bearer(r, "ann-read") }); st != http.StatusForbidden {
		t.Fatalf("read-only key: %d %s", st, b)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets", req, func(r *http.Request) { authtest.Bearer(r, "ann-write") }); st != http.StatusCreated {
		t.Fatalf("write key: %d %s", st, b)
	}
}

func TestOpenUploadsAreCapped(t *testing.T) {
	e := newEnv(t)
	for range 5 {
		e.begin("ann", "text", 1024, 1, nil)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 1024}, as("ann")); st != http.StatusTooManyRequests {
		t.Fatalf("sixth open upload: %d %s", st, b)
	}
	// Bob is not affected (the IP cap is lifted for 127.0.0.1 here).
	e.begin("bob", "text", 1024, 1, nil)
	// Abandoned uploads are swept after an hour, with their chunks.
	sec, _ := e.begin("bob", "file", 100, 1, nil)
	e.putChunk(sec, 0, gen(116))
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := e.col(`SELECT count(*) FROM secrets`); n != 0 {
		t.Fatalf("%d rows left", n)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs left", n)
	}
}

func TestAnonymous(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", ""))
	req := map[string]any{"kind": "text", "size": 1024}
	if st, b, _ := e.do("POST", "/api/v1/secrets", req); st != http.StatusUnauthorized {
		t.Fatalf("off by default: %d %s", st, b)
	}
	if err := secrets.Anonymous.Set(context.Background(), e.s.App.Settings, true); err != nil {
		t.Fatal(err)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": 10}); st != http.StatusForbidden {
		t.Fatalf("anonymous file: %d %s", st, b)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 128 << 10}); st != http.StatusBadRequest {
		t.Fatalf("anonymous 128 KiB: %d %s", st, b)
	}
	sec := e.createText("", "anon", 1, nil)
	if n := e.col(`SELECT count(*) FROM secrets WHERE id = ? AND owner_id IS NULL`, sec.ids); n != 1 {
		t.Fatal("anonymous secret has an owner")
	}
	_, pt, _ := e.open(sec, nil)
	if string(pt) != "anon" {
		t.Fatalf("got %q", pt)
	}
	e.flushEvents()
	if n := e.col(`SELECT count(*) FROM secret_events`); n != 0 {
		t.Fatalf("anonymous secret recorded %d events", n)
	}
	// The per-IP limit is strict: the burst of three is used up above.
	if st, b, _ := e.do("POST", "/api/v1/secrets", req); st != http.StatusTooManyRequests {
		t.Fatalf("fourth: %d %s", st, b)
	}
}

func TestInfoAndClaimRateLimited(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", ""))
	sec := e.createText("ann", "x", 1, nil)
	limited := false
	for range 40 {
		if st, _, _ := e.info(sec); st == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("info was never rate limited")
	}
}

type sealedResp struct {
	URL       string `json:"url"`
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
	Views     int    `json:"views"`
}

func sealedUpload(t *testing.T, e *env, query string, build func(*multipart.Writer)) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	build(mw)
	_ = mw.Close()
	st, b, _ := e.do("POST", "/api/v1/secrets/sealed"+query, body.Bytes(), as("ann"), header("Content-Type", mw.FormDataContentType()))
	return st, b
}

func TestSealedMode(t *testing.T) {
	e := newEnv(t)
	data := gen(e2e.ChunkSize*2 + 99)
	st, b := sealedUpload(t, e, "", func(mw *multipart.Writer) {
		_ = mw.WriteField("views", "2")
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="file"; filename="shot.png"`}
		h["Content-Type"] = []string{"image/png"}
		w, _ := mw.CreatePart(h)
		_, _ = w.Write(data)
		_ = mw.WriteField("expires", "1h")
	})
	mustStatus(t, "sealed", st, http.StatusCreated, b)
	sr := decode[sealedResp](t, b)
	base, id, key, err := e2e.ParseLink(sr.URL)
	if err != nil || base != e.s.URL || e2e.Encode(id[:]) != sr.ID || sr.Views != 2 {
		t.Fatalf("link %q: %v %+v", sr.URL, err, sr)
	}
	exp, _ := time.Parse(time.RFC3339, sr.ExpiresAt)
	if d := time.Until(exp); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("expires %s", sr.ExpiresAt)
	}
	sec := secret{id: id, ids: sr.ID, key: key}
	st, ir, ib := e.info(sec)
	mustStatus(t, "info", st, http.StatusOK, ib)
	if ir.Mode != "sealed" || ir.KDF != nil || ir.ViewsLeft != 2 {
		t.Fatalf("info %s", ib)
	}
	meta, pt, _ := e.open(sec, nil)
	if !bytes.Equal(pt, data) || meta.Name != "shot.png" || meta.Type != "image/png" {
		t.Fatalf("sealed file: %+v", meta)
	}

	// Raw text body, bare URL back.
	st, tb, _ := e.do("POST", "/api/v1/secrets/sealed?views=1&expires=5m", []byte("sealed text"), as("ann"),
		header("Content-Type", "text/plain"), header("Accept", "text/plain"))
	mustStatus(t, "sealed text", st, http.StatusCreated, tb)
	link := strings.TrimSpace(string(tb))
	_, id2, key2, err := e2e.ParseLink(link)
	if err != nil {
		t.Fatalf("bare url %q", tb)
	}
	_, pt, _ = e.open(secret{id: id2, ids: e2e.Encode(id2[:]), key: key2}, nil)
	if string(pt) != "sealed text" {
		t.Fatalf("sealed text %q", pt)
	}

	// Limits and errors.
	if st, b := sealedUpload(t, e, "?views=0", func(mw *multipart.Writer) { _ = mw.WriteField("text", "x") }); st != http.StatusBadRequest {
		t.Fatalf("views=0: %d %s", st, b)
	}
	if st, b := sealedUpload(t, e, "", func(mw *multipart.Writer) { _ = mw.WriteField("other", "x") }); st != http.StatusBadRequest {
		t.Fatalf("no content: %d %s", st, b)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets/sealed", []byte{0xff, 0xfe}, as("ann"), header("Content-Type", "text/plain")); st != http.StatusBadRequest {
		t.Fatalf("binary text: %d %s", st, b)
	}
	if st, b, _ := e.do("POST", "/api/v1/secrets/sealed", []byte("x")); st != http.StatusUnauthorized {
		t.Fatalf("anonymous sealed: %d %s", st, b)
	}

	// The keys were never logged or stored.
	e.flushEvents()
	for _, k := range [][32]byte{key, key2} {
		enc := e2e.Encode(k[:])
		if strings.Contains(e.logs.String(), enc) {
			t.Fatal("link key in the log")
		}
		for _, f := range []string{"snap.db", "snap.db-wal"} {
			raw, err := os.ReadFile(filepath.Join(e.s.DataDir(), f))
			if err != nil {
				continue
			}
			if bytes.Contains(raw, k[:]) || bytes.Contains(raw, []byte(enc)) {
				t.Fatalf("link key in %s", f)
			}
		}
	}
	if n := e.col(`SELECT count(*) FROM secrets WHERE mode = 'sealed' AND kdf IS NULL`); n != 2 {
		t.Fatalf("%d sealed secrets", n)
	}
}

func TestSealedTooLarge(t *testing.T) {
	e := newEnv(t)
	if err := secrets.MaxBytes.Set(context.Background(), e.s.App.Settings, 2000); err != nil {
		t.Fatal(err)
	}
	st, b := sealedUpload(t, e, "", func(mw *multipart.Writer) {
		w, _ := mw.CreateFormFile("file", "big.bin")
		_, _ = w.Write(gen(2001))
	})
	mustStatus(t, "too large", st, http.StatusRequestEntityTooLarge, b)
	if n := e.col(`SELECT count(*) FROM secrets`); n != 0 {
		t.Fatalf("%d rows after a refused upload", n)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs after a refused upload", n)
	}
}

func TestOwnerOnly(t *testing.T) {
	e := newEnv(t)
	a := e.createText("ann", "ann's", 2, nil)
	e.info(a, header("User-Agent", browserUA))

	// Bob can't list, burn or read the timeline of Ann's secret.
	st, b, _ := e.do("GET", "/api/v1/secrets", nil, as("bob"))
	mustStatus(t, "bob list", st, http.StatusOK, b)
	if strings.Contains(string(b), a.ids) {
		t.Fatal("bob's list shows ann's secret")
	}
	for _, r := range []struct{ method, path string }{
		{"DELETE", "/api/v1/secrets/" + a.ids},
		{"GET", "/api/v1/secrets/" + a.ids + "/events"},
		{"POST", "/dashboard/secrets/" + a.ids + "/burn"},
	} {
		st, b, _ := e.do(r.method, r.path, nil, as("bob"))
		if st != http.StatusNotFound {
			t.Fatalf("bob %s %s: %d %s", r.method, r.path, st, b)
		}
	}
	st, b, _ = e.do("GET", "/dashboard/secrets", nil, as("bob"))
	mustStatus(t, "bob dashboard", st, http.StatusOK, b)
	if strings.Contains(string(b), a.ids) {
		t.Fatal("bob's dashboard shows ann's secret")
	}
	// Anonymous callers are sent to sign in.
	if st, _, _ := e.do("GET", "/api/v1/secrets", nil); st != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", st)
	}
	// A read-only key can list but not burn.
	e.s.Auth.Add("ann-read", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 9, Scopes: []auth.Scope{auth.ScopeSecretsRead}})
	bearer := func(r *http.Request) { authtest.Bearer(r, "ann-read") }
	if st, b, _ := e.do("GET", "/api/v1/secrets", nil, bearer); st != http.StatusOK || !strings.Contains(string(b), a.ids) {
		t.Fatalf("read key list: %d", st)
	}
	if st, _, _ := e.do("DELETE", "/api/v1/secrets/"+a.ids, nil, bearer); st != http.StatusForbidden {
		t.Fatalf("read key burn: %d", st)
	}

	// Ann can.
	st, b, _ = e.do("GET", "/api/v1/secrets", nil, as("ann"))
	list := decode[struct {
		Secrets []secrets.Summary
		Total   int
	}](t, b)
	if st != http.StatusOK || list.Total != 1 || list.Secrets[0].ID != a.ids || list.Secrets[0].State != "ready" {
		t.Fatalf("ann list: %s", b)
	}
	if strings.Contains(string(b), "h_auth") || strings.Contains(string(b), "salt") {
		t.Fatalf("list leaks envelope values: %s", b)
	}
	e.flushEvents()
	st, b, _ = e.do("GET", "/api/v1/secrets/"+a.ids+"/events", nil, as("ann"))
	mustStatus(t, "events", st, http.StatusOK, b)
	if !strings.Contains(string(b), "link_opened") {
		t.Fatalf("events: %s", b)
	}
	st, b, _ = e.do("DELETE", "/api/v1/secrets/"+a.ids, nil, as("ann"))
	mustStatus(t, "burn", st, http.StatusOK, b)
	if st, ir, _ := e.info(a); st != http.StatusGone || ir.State != "revoked" {
		t.Fatalf("after burn: %d %+v", st, ir)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs after burn", n)
	}
}

func TestRevokeKillsTickets(t *testing.T) {
	e := newEnv(t)
	sec := e.createFile("ann", "f", gen(100), 3, nil)
	keys := e.keys(sec, nil)
	_, cr, _ := e.claimWith(sec, keys.Auth)
	st, b, _ := e.do("POST", "/dashboard/secrets/"+sec.ids+"/burn", nil, as("ann"))
	if st != http.StatusSeeOther {
		t.Fatalf("dashboard burn: %d %s", st, b)
	}
	if st, _, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, header(secrets.HeaderTicket, cr.Ticket)); st != http.StatusNotFound {
		t.Fatalf("ticket after revoke: %d", st)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs", n)
	}
}

func TestEventsTimeline(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "x", 1, []byte("pw"))
	// The owner's own requests are not recorded.
	e.info(sec, as("ann"))
	bad := e.keys(sec, []byte("nope"))
	e.claimWith(sec, bad.Auth, header("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"))
	// A headless browser's link_opened is dropped, but its reveal is not.
	headless := header("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/140.0.0.0 Safari/537.36")
	e.open(sec, []byte("pw"), headless)
	e.flushEvents()

	type ev struct{ Kind, IP, UASummary string }
	st, b, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/events", nil, as("ann"))
	mustStatus(t, "events", st, http.StatusOK, b)
	evs := decode[struct{ Events []ev }](t, b).Events
	var kinds []string
	for _, x := range evs {
		kinds = append(kinds, x.Kind)
		if x.IP == "" {
			t.Errorf("event without IP: %+v", x)
		}
	}
	// Newest first: burned, revealed (headless), link_opened (from keys()
	// in e.open, with browserUA), failed (Firefox), link_opened (keys() for
	// the bad attempt).
	want := []string{"burned", "revealed", "link_opened", "failed", "link_opened"}
	slices.Sort(kinds)
	slices.Sort(want)
	if !slices.Equal(kinds, want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}

	// The dashboard shows the row and its timeline.
	st, b, _ = e.do("GET", "/dashboard/secrets", nil, as("ann"))
	mustStatus(t, "dashboard", st, http.StatusOK, b)
	for _, want := range []string{"wrong passphrase", "revealed", "Firefox · Linux", "burned", "opened"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
}

func TestExpiry(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "x", 1, nil)
	e.exec(`UPDATE secrets SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).UnixMilli(), sec.ids)
	st, ir, b := e.info(sec)
	if st != http.StatusGone || ir.State != "expired" {
		t.Fatalf("info: %d %s", st, b)
	}
	if st, _, b := e.claimWith(sec, [32]byte{}); st != http.StatusGone {
		t.Fatalf("claim: %d %s", st, b)
	}
	if err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e.col(`SELECT count(*) FROM secrets WHERE id = ? AND state = 'expired'`, sec.ids); s != 1 {
		t.Fatal("not marked expired")
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs after expiry", n)
	}
	// Tombstones go after 30 days, with their events.
	e.flushEvents()
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := e.col(`SELECT count(*) FROM secrets`); n != 0 {
		t.Fatalf("%d rows after 31 days", n)
	}
	if n := e.col(`SELECT count(*) FROM secret_events`); n != 0 {
		t.Fatalf("%d events after 31 days", n)
	}
}

func TestOrphansAreSwept(t *testing.T) {
	e := newEnv(t)
	e.createFile("bob", "f", gen(10), 1, nil)
	e.info(e.createText("bob", "y", 1, nil))
	e.flushEvents()
	// Deleting the user cascades to the rows; files and events are swept.
	e.exec(`DELETE FROM users WHERE id = 2`)
	if err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.store.Keys()); n != 0 {
		t.Fatalf("%d blobs left", n)
	}
	if n := e.col(`SELECT count(*) FROM secret_blobs`) + e.col(`SELECT count(*) FROM secret_events`); n != 0 {
		t.Fatalf("%d ledger rows or events left", n)
	}
}

func TestRevealPage(t *testing.T) {
	e := newEnv(t)
	var bodies []string
	for _, id := range []string{e2e.Encode(gen(16)), "AAAAAAAAAAAAAAAAAAAAAA", "not-even-an-id"} {
		st, b, h := e.do("GET", "/s/"+id, nil)
		mustStatus(t, "page", st, http.StatusOK, b)
		if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Robots-Tag") != "noindex" {
			t.Fatalf("headers: %v", h)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "'wasm-unsafe-eval'") {
			t.Fatalf("csp: %q", h.Get("Content-Security-Policy"))
		}
		for _, want := range []string{`<meta name="google" content="notranslate">`, `<meta name="robots" content="noindex">`, "secret-open.js", `translate="no"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("page lacks %s", want)
			}
		}
		if strings.Contains(string(b), id) {
			t.Fatal("the page contains the id")
		}
		bodies = append(bodies, string(b))
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatal("the reveal page differs between ids")
	}
	// The composer page.
	st, b, h := e.do("GET", "/new/secret", nil, as("ann"))
	mustStatus(t, "composer", st, http.StatusOK, b)
	if !strings.Contains(h.Get("Content-Security-Policy"), "'wasm-unsafe-eval'") || !strings.Contains(string(b), "data-secret-composer") {
		t.Fatal("composer page")
	}
	if !strings.Contains(string(b), "Visible to the server, not the recipient.") {
		t.Fatal("composer note hint")
	}
	if st, _, _ := e.do("GET", "/new/secret", nil); st != http.StatusSeeOther {
		t.Fatalf("anonymous composer: %d", st)
	}
	// Every route that carries a secret id keeps its path out of the log.
	for _, rt := range e.s.App.Router.Routes() {
		if strings.Contains(rt.Pattern, "{id}") && strings.Contains(rt.Pattern, "secret") && !rt.Redact {
			t.Errorf("%s is not redacted", rt.Pattern)
		}
		if strings.HasPrefix(rt.Pattern, "GET /s/") && !rt.Redact {
			t.Errorf("%s is not redacted", rt.Pattern)
		}
	}
}

func TestLogsCarryNoSecrets(t *testing.T) {
	e := newEnv(t)
	text := e.createText("ann", "log me not", 2, []byte("pw"))
	file := e.createFile("ann", "f.bin", gen(e2e.ChunkSize+1), 2, nil)
	e.open(text, []byte("pw"))
	_, _, cr := e.open(file, nil)
	bad := e.keys(text, []byte("wrong"))
	e.claimWith(text, bad.Auth)
	e.do("GET", "/s/"+text.ids, nil)
	e.do("GET", "/api/v1/secrets/"+text.ids+"/events", nil, as("ann"))
	e.do("DELETE", "/api/v1/secrets/"+file.ids, nil, as("ann"))
	_, sb := sealedUpload(t, e, "", func(mw *multipart.Writer) { _ = mw.WriteField("text", "sealed") })
	sealedURL := decode[sealedResp](t, sb)
	// Something for the error paths, too.
	e.do("POST", "/api/v1/secrets/"+text.ids+"/info", map[string]string{"probe": "bad"})
	e.putChunk(text, 0, []byte("x"))
	e.flushEvents()

	logs := e.logs.String()
	if os.Getenv("SECRETS_TEST_LOG") != "" {
		t.Log(logs)
	}
	if !strings.Contains(logs, "route=") {
		t.Fatalf("no request log captured: %q", logs)
	}
	needles := []string{text.ids, file.ids, text.probe(), file.probe(), text.token, file.token, cr.Ticket,
		e2e.Encode(text.key[:]), e2e.Encode(file.key[:]), sealedURL.ID, sealedURL.URL}
	if n := contains(logs, needles...); n != "" {
		t.Fatalf("the log contains %q:\n%s", n, logs)
	}
}

func TestBlobPathEscapes(t *testing.T) {
	// The ledger only ever holds keys the store issued; ids in paths are
	// validated before any lookup. A path like ../ can't reach the store.
	e := newEnv(t)
	for _, p := range []string{"/api/v1/secrets/..%2F..%2Fsnap.db/blob", "/api/v1/secrets/%2e%2e/blob"} {
		st, b, _ := e.do("GET", p, nil, header(secrets.HeaderTicket, e2e.Encode(make([]byte, 32))))
		if st == http.StatusOK {
			t.Fatalf("%s: %d %s", p, st, b)
		}
	}
}

func TestSummaryJSONShape(t *testing.T) {
	sm := secrets.Summary{ID: "x"}
	b, _ := json.Marshal(sm)
	if strings.Contains(string(b), "Opened") || strings.Contains(string(b), "Created\"") {
		t.Fatalf("template fields leak into JSON: %s", b)
	}
}
