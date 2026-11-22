package secrets_test

// Regression tests for the security review of one-time secrets. The first
// group are the review's proofs of concept, inverted: each fails if its
// weakness comes back. The rest test the fixes that came with them.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// xff sets X-Forwarded-For (the test server's peer, 127.0.0.1, is configured
// as a trusted proxy, as Caddy would be).
func xff(ip string) func(*http.Request) { return header("X-Forwarded-For", ip) }

// usage returns a user's used and reserved bytes.
func (e *env) usage(uid int64) (used, reserved int64) {
	e.t.Helper()
	if err := e.s.App.DB.R.QueryRow(`SELECT used_bytes, reserved_bytes FROM users WHERE id = ?`, uid).Scan(&used, &reserved); err != nil {
		e.t.Fatal(err)
	}
	return used, reserved
}

func (e *env) wantUsage(what string, uid, used, reserved int64) {
	e.t.Helper()
	if u, r := e.usage(uid); u != used || r != reserved {
		e.t.Fatalf("%s: used %d reserved %d, want %d and %d", what, u, r, used, reserved)
	}
}

// ---- the review's proofs of concept, inverted ----

// RT-QUOTA: secrets count against the owner's quota and per-user upload
// limit, like items.
func TestSecretsRespectUserQuota(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE users SET quota_bytes = 1024, max_upload_bytes = 1024 WHERE id = 1`)
	st, b, _ := e.do("POST", "/api/v1/uploads", map[string]any{"name": "x.bin", "size": 3 << 20}, as("ann"))
	if st < 400 {
		t.Fatalf("items accepted a 3 MiB upload for a 1 KiB quota: %d %s", st, b)
	}
	st, b, _ = e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": 3 << 20}, as("ann"))
	mustStatus(t, "3 MiB secret over the upload limit", st, http.StatusRequestEntityTooLarge, b)
	// Under the upload limit but over the quota.
	e.exec(`UPDATE users SET quota_bytes = 4096, max_upload_bytes = NULL WHERE id = 1`)
	st, b, _ = e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": 3 << 20}, as("ann"))
	mustStatus(t, "3 MiB secret over the quota", st, http.StatusRequestEntityTooLarge, b)
	if !strings.Contains(string(b), "quota") {
		t.Fatalf("message: %s", b)
	}
	if n := e.col(`SELECT count(*) FROM secrets`); n != 0 {
		t.Fatalf("%d rows left by refused creates", n)
	}
	e.wantUsage("after refusals", 1, 0, 0)
}

// RT-IPV6: per-IP limits group IPv6 clients by /64.
func TestIPv6ClientsShareLimitsPerPrefix(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", ""), testutil.Env("TRUSTED_PROXIES", "127.0.0.1/32"))
	if err := secrets.Anonymous.Set(context.Background(), e.s.App.Settings, true); err != nil {
		t.Fatal(err)
	}
	req := map[string]any{"kind": "text", "size": 64 << 10}
	var single []int
	for range 4 {
		st, _, _ := e.do("POST", "/api/v1/secrets", req, xff("2001:db8:aaaa:bbbb::1"))
		single = append(single, st)
	}
	if single[0] != http.StatusCreated || single[3] != http.StatusTooManyRequests {
		t.Fatalf("single address: %v", single)
	}
	// A new interface id in the same /64 gets the same bucket.
	for i := range 20 {
		ip := fmt.Sprintf("2001:db8:aaaa:bbbb:%x:%x:%x:%x", i+2, i*7, i*13, i*31)
		if st, b, _ := e.do("POST", "/api/v1/secrets", req, xff(ip)); st != http.StatusTooManyRequests {
			t.Fatalf("create %d from %s: %d %s", i, ip, st, b)
		}
	}
	// Another /64 is another client.
	if st, b, _ := e.do("POST", "/api/v1/secrets", req, xff("2001:db8:aaaa:cccc::1")); st != http.StatusCreated {
		t.Fatalf("other /64: %d %s", st, b)
	}
	// The open-upload cap is keyed the same way.
	ips := e.col(`SELECT count(*) FROM secrets WHERE upload_ip = 'ip6:2001:db8:aaaa:bbbb::/64'`)
	if ips != 3 {
		t.Fatalf("upload_ip by /64: %d rows", ips)
	}
	if n := e.col(`SELECT count(*) FROM secrets WHERE upload_ip LIKE '%2001:db8:aaaa:bbbb::1%'`); n != 0 {
		t.Fatal("upload_ip holds a full address")
	}

	// The info and claim limiter (30 burst per client) too.
	sec := e.createText("ann", "x", 1, nil)
	limited := false
	for i := range 40 {
		st, _, _ := e.info(sec, xff(fmt.Sprintf("2001:db8:1:2:%x::9", i)))
		if st == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("info from one /64 was never limited")
	}
}

// Items and accounts limiters use the same keys: the deletion-token lookup
// (20 a minute) from one /64.
func TestIPv6PrefixLimitsItemTokens(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", ""), testutil.Env("TRUSTED_PROXIES", "127.0.0.1/32"))
	limited := false
	for i := range 30 {
		st, _, _ := e.do("POST", "/api/v1/delete/lookup", map[string]string{"token": "nope"}, xff(fmt.Sprintf("2001:db8:5:6:%x::1", i)))
		if st == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("deletion lookups from one /64 were never limited")
	}
}

// RT-LOGID: near-miss requests (wrong method, trailing slash or dot) never
// log the secret id.
func TestNearMissPathsDoNotLogSecretID(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "x", 1, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/secrets/" + sec.ids + "/info"},
		{"GET", "/s/" + sec.ids + "/"},
		{"POST", "/api/v1/secrets/" + sec.ids + "/claim/"},
		{"GET", "/s/" + sec.ids + "."},
		{"DELETE", "/s/" + sec.ids},
		{"GET", "/api/v1/secrets/" + sec.ids + "/blob/x"},
		{"GET", "/x/" + sec.ids},
		{"GET", "/verify/" + sec.ids},
	} {
		e.do(c.method, c.path, nil)
	}
	logs := e.logs.String()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, sec.ids) {
			t.Errorf("log line carries the id: %s", line)
		}
	}
	if !strings.Contains(logs, "/api") || !strings.Contains(logs, "/s") {
		t.Fatalf("near misses are not logged at all: %s", logs)
	}
}

// RT-TOMB: a burned secret's tombstone keeps no passphrase verifier, so a
// copy of the database plus the link can't test passphrases offline.
func TestTombstoneKeepsNoPassphraseVerifier(t *testing.T) {
	e := newEnv(t)
	pass := []byte("tr0ub4dor&3")
	sec := e.createText("ann", "the vault code", 1, pass)
	e.open(sec, pass) // the last view
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(21*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.wantNoVerifier(sec, "burned", true)
	// The owner's page still knows it had a passphrase.
	st, b, _ := e.do("GET", "/api/v1/secrets", nil, as("ann"))
	mustStatus(t, "list", st, http.StatusOK, b)
	if !strings.Contains(string(b), `"passphrase":true`) {
		t.Fatalf("list lost the passphrase flag: %s", b)
	}
}

// wantNoVerifier checks that sec's row is a tombstone in state without salt,
// kdf, h_auth or commit_tag, but with h_probe.
func (e *env) wantNoVerifier(sec secret, state string, hasPass bool) {
	e.t.Helper()
	var st string
	var salt, kdf, hAuth, commit, hProbe any
	var hp int
	err := e.s.App.DB.R.QueryRow(`SELECT state, salt, kdf, h_auth, commit_tag, h_probe, has_passphrase FROM secrets WHERE id = ?`, sec.ids).
		Scan(&st, &salt, &kdf, &hAuth, &commit, &hProbe, &hp)
	if err != nil {
		e.t.Fatal(err)
	}
	if st != state || salt != nil || kdf != nil || hAuth != nil || commit != nil {
		e.t.Fatalf("tombstone %s: salt %v kdf %v h_auth %v commit %v", st, salt, kdf, hAuth, commit)
	}
	if hProbe == nil || (hp == 1) != hasPass {
		e.t.Fatalf("tombstone %s: h_probe %v has_passphrase %d", st, hProbe, hp)
	}
	// Link holders still get the tombstone.
	if s, _, b := e.info(sec); s != http.StatusGone {
		e.t.Fatalf("info on the tombstone: %d %s", s, b)
	}
}

// RT-WAL: rows purged by the sweeper leave nothing in snap.db or snap.db-wal.
func TestPurgedRowsLeaveNoWALResidue(t *testing.T) {
	e := newEnv(t)
	var r [6]byte
	_, _ = rand.Read(r[:])
	marker := "RTNOTE" + hex.EncodeToString(r[:])
	sec := e.createText("ann", "x", 1, nil)
	e.exec(`UPDATE secrets SET note = ? WHERE id = ?`, marker, sec.ids)
	e.open(sec, nil, xff("198.51.100.77"))
	e.flushEvents()
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := e.col(`SELECT count(*) FROM secrets WHERE id = ?`, sec.ids); n != 0 {
		t.Fatalf("tombstone still there")
	}
	if _, err := e.s.App.DB.W.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatal(err)
	}
	if where := e.dbFilesContain([]byte(marker), []byte(sec.ids)); where != "" {
		t.Fatalf("purged row found in %s", where)
	}
}

// dbFilesContain reports which of snap.db and snap.db-wal holds any needle.
func (e *env) dbFilesContain(needles ...[]byte) string {
	e.t.Helper()
	file := filepath.Join(e.s.DataDir(), "snap.db")
	var where []string
	for _, f := range []string{file, file + "-wal"} {
		b, err := os.ReadFile(f) // #nosec G304 -- the test's own database
		if err != nil && !os.IsNotExist(err) {
			e.t.Fatal(err)
		}
		for _, n := range needles {
			if bytes.Contains(b, n) {
				where = append(where, fmt.Sprintf("%s (%x…)", filepath.Base(f), n[:4]))
			}
		}
	}
	return strings.Join(where, ", ")
}

// ---- the fixes ----

// Verifier fields are dropped on every way out of ready: last view,
// failures, revoke and expiry. A secret with views left keeps them.
func TestVerifierClearedOnEveryTransition(t *testing.T) {
	e := newEnv(t)
	pass := []byte("pw")

	multi := e.createText("ann", "two", 2, pass)
	e.open(multi, pass)
	if n := e.col(`SELECT count(*) FROM secrets WHERE id = ? AND h_auth IS NOT NULL AND salt IS NOT NULL AND kdf IS NOT NULL AND commit_tag IS NOT NULL`, multi.ids); n != 1 {
		t.Fatal("a secret with views left lost its verifier")
	}
	e.open(multi, pass)
	e.wantNoVerifier(multi, "burned", true)

	failed := e.createText("ann", "f", 1, pass)
	var wrong [32]byte
	for range 10 {
		e.claimWith(failed, wrong)
	}
	e.wantNoVerifier(failed, "burned", true)

	revoked := e.createFile("ann", "r.bin", gen(100), 1, pass)
	if st, b, _ := e.do("DELETE", "/api/v1/secrets/"+revoked.ids, nil, as("ann")); st != http.StatusOK {
		t.Fatalf("revoke: %d %s", st, b)
	}
	e.wantNoVerifier(revoked, "revoked", true)

	expired := e.createText("ann", "e", 1, nil)
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.wantNoVerifier(expired, "expired", false)
}

// After a burn the verifier and commitment are gone from both database
// files, not only from the row.
func TestBurnTruncatesWAL(t *testing.T) {
	e := newEnv(t)
	pass := []byte("pw")
	sec := e.createText("ann", "x", 1, pass)
	keys := e.keys(sec, pass)
	hAuth := e2e.Hash(keys.Auth[:])
	if where := e.dbFilesContain(keys.Commit[:], hAuth[:]); where == "" {
		t.Fatal("setup: the commitment should be in the database before the burn")
	}
	e.open(sec, pass)
	// The purge and the checkpoint run once the claim response is out.
	eventually(t, "verifier gone from snap.db and snap.db-wal", func() bool {
		return e.dbFilesContain(keys.Commit[:], hAuth[:]) == ""
	})
}

// A wrong auth on a secret without a passphrase comes from someone with the
// probe but not the link: it gets the same 403 and is not counted, so it
// can't burn the secret.
func TestNoPassphraseWrongAuthNotCounted(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "no pass", 1, nil)
	var wrong [32]byte
	for i := range 15 {
		st, cr, b := e.claimWith(sec, wrong)
		mustStatus(t, fmt.Sprintf("wrong auth %d", i), st, http.StatusForbidden, b)
		if cr.AttemptsLeft != 10 || cr.State != "ready" || cr.Error != "Wrong passphrase." {
			t.Fatalf("answer %d: %s", i, b)
		}
	}
	if n := e.col(`SELECT failures FROM secrets WHERE id = ?`, sec.ids); n != 0 {
		t.Fatalf("failures = %d", n)
	}
	e.flushEvents()
	if n := e.col(`SELECT count(*) FROM secret_events WHERE kind = 'failed'`); n != 0 {
		t.Fatalf("%d failed events", n)
	}
	if _, pt, _ := e.open(sec, nil); string(pt) != "no pass" {
		t.Fatalf("got %q", pt)
	}
	// With a passphrase, wrong ones still count.
	withPass := e.createText("ann", "p", 1, []byte("pw"))
	st, cr, b := e.claimWith(withPass, wrong)
	if st != http.StatusForbidden || cr.AttemptsLeft != 9 {
		t.Fatalf("with passphrase: %d %s", st, b)
	}
}

// A ticket serves at most 2 × chunks + 16 requests and 3 × ct_size bytes.
func TestTicketDownloadBudget(t *testing.T) {
	e := newEnv(t)
	claim := func(sec secret) string {
		t.Helper()
		keys := e.keys(sec, nil)
		st, cr, b := e.claimWith(sec, keys.Auth)
		mustStatus(t, "claim", st, http.StatusOK, b)
		return cr.Ticket
	}
	get := func(sec secret, ticket string, rng string) int {
		t.Helper()
		opts := []func(*http.Request){header(secrets.HeaderTicket, ticket)}
		if rng != "" {
			opts = append(opts, header("Range", rng))
		}
		st, _, _ := e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, opts...)
		return st
	}

	// Requests: one chunk, so 18 small ranges, then 410.
	small := e.createFile("ann", "s.bin", gen(4000), 1, nil)
	ticket := claim(small)
	for i := range 18 {
		if st := get(small, ticket, "bytes=0-9"); st != http.StatusPartialContent {
			t.Fatalf("range %d: %d", i, st)
		}
	}
	if st := get(small, ticket, "bytes=0-9"); st != http.StatusGone {
		t.Fatalf("request over the budget: %d", st)
	}

	// Bytes: three whole downloads, then 410.
	big := e.createFile("ann", "b.bin", gen(3*e2e.ChunkSize+5), 1, nil)
	ticket = claim(big)
	ct := e2e.CiphertextSize(3*e2e.ChunkSize + 5)
	for i := range 3 {
		if st := get(big, ticket, ""); st != http.StatusOK {
			t.Fatalf("download %d: %d", i, st)
		}
		// Bytes are counted after the response is written.
		eventually(t, "bytes counted", func() bool {
			return e.col(`SELECT bytes_served FROM secret_tickets WHERE secret_id = ?`, big.ids) >= int64(i+1)*ct
		})
	}
	if st := get(big, ticket, "bytes=0-0"); st != http.StatusGone {
		t.Fatalf("bytes over the budget: %d", st)
	}
	// done still works, and the file goes.
	st, b, _ := e.do("POST", "/api/v1/secrets/"+big.ids+"/done", nil, header(secrets.HeaderTicket, ticket))
	mustStatus(t, "done", st, http.StatusNoContent, b)
	if n := e.col(`SELECT count(*) FROM secret_blobs WHERE secret_id = ?`, big.ids); n != 0 {
		t.Fatalf("%d blobs left", n)
	}
}

// Quota accounting across the whole lifecycle: create reserves, finalize
// turns the reservation into usage, and every way the content goes releases
// it. The items reconcile job keeps it.
func TestSecretQuotaLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`UPDATE users SET quota_bytes = ? WHERE id = 1`, 10<<20)
	size := int64(3 << 20)
	ct := e2e.CiphertextSize(size)

	// Reserved at create; revoking an unfinished upload releases it.
	sec, _ := e.begin("ann", "file", size, 1, nil)
	e.wantUsage("created", 1, 0, ct)
	if st, b, _ := e.do("DELETE", "/api/v1/secrets/"+sec.ids, nil, as("ann")); st != http.StatusOK {
		t.Fatalf("revoke upload: %d %s", st, b)
	}
	e.wantUsage("upload revoked", 1, 0, 0)

	// Used once finalized.
	viewed := e.createFile("ann", "a.bin", gen(int(size)), 1, nil)
	e.wantUsage("finalized", 1, ct, 0)
	revoked := e.createFile("ann", "b.bin", gen(int(size)), 1, nil)
	expiring := e.createFile("ann", "c.bin", gen(int(size)), 1, nil)
	e.wantUsage("three ready", 1, 3*ct, 0)

	// The fourth would go over 10 MiB.
	st, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": size}, as("ann"))
	mustStatus(t, "over quota", st, http.StatusRequestEntityTooLarge, b)

	// Reconcile recomputes usage from items and live secrets.
	if err := e.s.App.Items.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.wantUsage("reconciled", 1, 3*ct, 0)

	// Last view and done: released.
	e.open(viewed, nil)
	e.wantUsage("viewed", 1, 2*ct, 0)
	// Revoke: released.
	if st, b, _ := e.do("DELETE", "/api/v1/secrets/"+revoked.ids, nil, as("ann")); st != http.StatusOK {
		t.Fatalf("revoke: %d %s", st, b)
	}
	e.wantUsage("revoked", 1, ct, 0)
	// Expiry and sweep: released.
	if err := e.svc.SweepAt(ctx, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.wantUsage("expired", 1, 0, 0)
	if n := e.col(`SELECT count(*) FROM secrets WHERE id = ? AND quota_used = 0 AND quota_reserved = 0`, expiring.ids); n != 1 {
		t.Fatal("expired secret still holds quota")
	}

	// Abandoned upload: the reservation goes with it.
	abandoned, _ := e.begin("ann", "file", size, 1, nil)
	e.wantUsage("uploading", 1, 0, ct)
	if err := e.s.App.Items.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	e.wantUsage("uploading, reconciled", 1, 0, ct)
	if err := e.svc.SweepAt(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.wantUsage("abandoned", 1, 0, 0)
	if n := e.col(`SELECT count(*) FROM secrets WHERE id = ?`, abandoned.ids); n != 0 {
		t.Fatal("abandoned upload not swept")
	}

	// Burned after failed passphrases: released.
	burned := e.createText("ann", "t", 1, []byte("pw"))
	tct := e2e.CiphertextSize(1024)
	e.wantUsage("text", 1, tct, 0)
	var wrong [32]byte
	for range 10 {
		e.claimWith(burned, wrong)
	}
	e.wantUsage("burned by failures", 1, 0, 0)

	// A file whose last view was claimed stays charged while its ticket may
	// still download it, and is released once it is gone.
	held := e.createFile("ann", "h.bin", gen(5000), 1, nil)
	keys := e.keys(held, nil)
	st, cr, b := e.claimWith(held, keys.Auth)
	mustStatus(t, "claim", st, http.StatusOK, b)
	hct := e2e.CiphertextSize(5000)
	e.wantUsage("claimed, ticket live", 1, hct, 0)
	if err := e.svc.SweepAt(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.wantUsage("ticket expired, swept", 1, 0, 0)
	_ = cr

	// A text secret's last view: released once the response is out.
	text := e.createText("ann", "last", 1, nil)
	e.wantUsage("text ready", 1, tct, 0)
	e.open(text, nil)
	eventually(t, "text released", func() bool { u, r := e.usage(1); return u == 0 && r == 0 })
}

// Sealed uploads are charged chunk by chunk and stop at the quota.
func TestSealedQuota(t *testing.T) {
	e := newEnv(t)
	e.exec(`UPDATE users SET quota_bytes = ? WHERE id = 1`, 3<<20)
	file := func(n int) (int, []byte) {
		return sealedUpload(t, e, "", func(mw *multipart.Writer) {
			w, _ := mw.CreateFormFile("file", "f.bin")
			_, _ = w.Write(gen(n))
		})
	}
	st, b := file(e2e.ChunkSize + 10)
	mustStatus(t, "sealed under quota", st, http.StatusCreated, b)
	ct := e2e.CiphertextSize(e2e.ChunkSize + 10)
	e.wantUsage("sealed", 1, ct, 0)

	st, b = file(2*e2e.ChunkSize + 10)
	mustStatus(t, "sealed over quota", st, http.StatusRequestEntityTooLarge, b)
	if !strings.Contains(string(b), "quota") {
		t.Fatalf("message: %s", b)
	}
	e.wantUsage("refused", 1, ct, 0)
	if n := e.col(`SELECT count(*) FROM secrets WHERE state = 'uploading'`); n != 0 {
		t.Fatalf("%d uploading rows left", n)
	}
	if n := e.col(`SELECT count(*) FROM secret_blobs`); n != 3 { // the first secret: two chunks and its metadata
		t.Fatalf("%d blobs", n)
	}

	// The per-user upload limit lowers the sealed size cap.
	e.exec(`UPDATE users SET quota_bytes = NULL, max_upload_bytes = 1000 WHERE id = 1`)
	st, b = file(2000)
	mustStatus(t, "sealed over the upload limit", st, http.StatusRequestEntityTooLarge, b)
}

// An API key's constraints apply to secrets: the expiry cap and the upload
// cap, in E2E and sealed mode.
func TestKeyConstraintsApply(t *testing.T) {
	e := newEnv(t)
	e.s.Auth.Add("ann-key", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 7,
		Scopes: []auth.Scope{auth.ScopeSecretsWrite, auth.ScopeSecretsRead},
		Key:    &auth.KeyConstraints{MaxExpiry: 10 * time.Minute, MaxUploadBytes: 4096}})
	key := func(r *http.Request) { authtest.Bearer(r, "ann-key") }

	st, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": 5000}, key)
	mustStatus(t, "over the key's upload cap", st, http.StatusRequestEntityTooLarge, b)

	st, b, _ = e.do("POST", "/api/v1/secrets", map[string]any{"kind": "file", "size": 4000, "expires_in": 7 * 86400}, key)
	mustStatus(t, "create", st, http.StatusCreated, b)
	cr := decode[struct {
		ExpiresAt string `json:"expires_at"`
	}](t, b)
	exp, _ := time.Parse(time.RFC3339, cr.ExpiresAt)
	if d := time.Until(exp); d > 11*time.Minute || d < 9*time.Minute {
		t.Fatalf("expiry not clamped: %s", cr.ExpiresAt)
	}

	sealed := func(body []byte, q string) (int, []byte) {
		st, b, _ := e.do("POST", "/api/v1/secrets/sealed"+q, body, key, header("Content-Type", "text/plain"))
		return st, b
	}
	st, b = sealed(bytes.Repeat([]byte("x"), 5000), "")
	mustStatus(t, "sealed over the key's cap", st, http.StatusRequestEntityTooLarge, b)
	st, b = sealed([]byte("hello"), "?expires=7d")
	mustStatus(t, "sealed", st, http.StatusCreated, b)
	sr := decode[sealedResp](t, b)
	exp, _ = time.Parse(time.RFC3339, sr.ExpiresAt)
	if d := time.Until(exp); d > 11*time.Minute || d < 9*time.Minute {
		t.Fatalf("sealed expiry not clamped: %s", sr.ExpiresAt)
	}
}

// Anonymous secrets: a site-wide hourly cap and a cap on the live total.
func TestAnonymousSiteCaps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	st := e.s.App.Settings
	if err := secrets.Anonymous.Set(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	if err := secrets.AnonymousPerHour.Set(ctx, st, 3); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		e.createText("", fmt.Sprint(i), 1, nil)
	}
	code, b, _ := e.do("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 1024})
	mustStatus(t, "fourth anonymous create this hour", code, http.StatusTooManyRequests, b)
	// Signed-in users are not affected.
	e.createText("ann", "mine", 1, nil)

	// The live total: 128 KiB holds one 64 KiB text, not two.
	e.exec(`DELETE FROM secrets WHERE owner_id IS NULL`)
	if err := secrets.AnonymousPerHour.Set(ctx, st, 100); err != nil {
		t.Fatal(err)
	}
	if err := secrets.AnonymousMaxTotalBytes.Set(ctx, st, 128<<10); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 40<<10) // pads to 64 KiB
	first := e.createText("", big, 1, nil)
	code, b, _ = e.do("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 64 << 10})
	mustStatus(t, "over the anonymous total", code, http.StatusServiceUnavailable, b)
	e.open(first, nil) // burned: its bytes are released once the response is out
	eventually(t, "anonymous bytes released", func() bool {
		return e.col(`SELECT quota_used + quota_reserved FROM secrets WHERE id = ?`, first.ids) == 0
	})
	e.createText("", big, 1, nil)
}

// /s is the paste box: the same page as /s/{id}, with the same headers.
// Secret pages load fragment.js before any module, and their forms can't
// submit natively.
func TestSecretPagesHardening(t *testing.T) {
	e := newEnv(t)
	st, paste, h := e.do("GET", "/s", nil)
	mustStatus(t, "paste page", st, http.StatusOK, paste)
	if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Robots-Tag") != "noindex" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "'wasm-unsafe-eval'") {
		t.Fatalf("headers: %v", h)
	}
	_, reveal, _ := e.do("GET", "/s/AAAAAAAAAAAAAAAAAAAAAA", nil)
	if !bytes.Equal(paste, reveal) {
		t.Fatal("/s and /s/{id} differ")
	}
	page := string(reveal)
	frag := regexp.MustCompile(`<script src="/static/js/fragment\.js\?v=[0-9a-f]+"></script>`).FindStringIndex(page)
	mod := strings.Index(page, `<script type="module"`)
	if frag == nil || mod < 0 || frag[0] > mod {
		t.Fatal("fragment.js must be a classic script before the modules")
	}
	for _, want := range []string{`data-paste-form method="dialog"`, `data-reveal-form method="dialog"`, `data-reveal-btn disabled`,
		`data-paste-btn disabled`, "<noscript>", `autocomplete="one-time-code"`, "data-1p-ignore", `data-lpignore="true"`,
		"data-bwignore", `data-form-type="other"`} {
		if !strings.Contains(page, want) {
			t.Errorf("reveal page lacks %s", want)
		}
	}

	for _, path := range []string{"/new/secret", "/"} {
		st, b, _ := e.do("GET", path, nil, as("ann"))
		mustStatus(t, path, st, http.StatusOK, b)
		page := string(b)
		for _, want := range []string{`data-secret-composer`, `method="dialog"`, `data-submit disabled`, "<noscript>",
			`autocomplete="one-time-code"`, "data-1p-ignore", e.hostOf() + "/s"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks %s", path, want)
			}
		}
		// No form on the page can submit natively with a GET.
		for _, form := range regexp.MustCompile(`<form[^>]*>`).FindAllString(page, -1) {
			if !strings.Contains(form, `method="dialog"`) && !strings.Contains(form, `method="post"`) {
				t.Errorf("%s: %s submits natively", path, form)
			}
		}
	}

	// Token pages strip their fragment before any module runs.
	for _, path := range []string{"/verify", "/reset", "/invite", "/x"} {
		st, b, _ := e.do("GET", path, nil)
		mustStatus(t, path, st, http.StatusOK, b)
		page := string(b)
		frag := strings.Index(page, "/static/js/fragment.js")
		mod := strings.Index(page, `<script type="module"`)
		if frag < 0 || frag > mod {
			t.Errorf("%s: fragment.js missing or after the modules", path)
		}
	}
}

func (e *env) hostOf() string {
	return strings.TrimPrefix(strings.TrimPrefix(e.s.URL, "https://"), "http://")
}
