package secrets_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/secrets/secretstest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// testKDF is the cheapest accepted Argon2id cost, to keep tests quick.
var testKDF = e2e.KDFParams{MemKiB: e2e.MinKDFMemKiB, Time: e2e.MinKDFTime, Threads: 1}

const fullChunk = e2e.ChunkSize + e2e.TagSize

// syncBuffer collects the server log.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type env struct {
	t     *testing.T
	s     *testutil.Server
	store *secretstest.Store
	logs  *syncBuffer
	svc   *secrets.Service
	c     *http.Client // one client, made once: testutil's Client() is not safe to call concurrently
}

// newEnv starts a server with users ann (id 1) and bob (id 2), both with a
// verified email. Rate limits by IP are lifted for 127.0.0.1 unless the
// caller passes its own RATELIMIT_EXEMPT.
func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	logs := &syncBuffer{}
	opts = append([]testutil.Option{testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"), testutil.LogTo(logs)}, opts...)
	s, st := secretstest.Server(t, opts...)
	e := &env{t: t, s: s, store: st, logs: logs, svc: s.App.Secrets, c: s.Client()}
	e.addUser("ann", 1, true)
	e.addUser("bob", 2, true)
	return e
}

func (e *env) addUser(name string, id int64, verified bool) {
	e.t.Helper()
	var at any
	if verified {
		at = int64(1)
	}
	_, err := e.s.App.DB.W.ExecContext(context.Background(),
		`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, 0, 0)`,
		id, name, name+"@example.test", at)
	if err != nil {
		e.t.Fatal(err)
	}
	e.s.Auth.Add(name, auth.Principal{UserID: id, EmailVerified: verified})
}

func as(name string) func(*http.Request) { return func(r *http.Request) { authtest.Session(r, name) } }

func header(k, v string) func(*http.Request) { return func(r *http.Request) { r.Header.Set(k, v) } }

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15"

// do sends a request; a non-nil body that is not []byte is sent as JSON.
func (e *env) do(method, path string, body any, opts ...func(*http.Request)) (int, []byte, http.Header) {
	e.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ct = "application/octet-stream"
	default:
		j, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(j)
		ct = "application/json"
	}
	req := e.s.Request(e.t, method, path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("User-Agent", browserUA)
	for _, o := range opts {
		o(req)
	}
	res, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.StatusCode, b, res.Header
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %T: %v: %s", v, err, b)
	}
	return v
}

func mustStatus(t *testing.T, what string, got, want int, body []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d: %s", what, got, want, body)
	}
}

// secret is what a creator holds: the link's id and key.
type secret struct {
	id    [16]byte
	ids   string
	key   [32]byte
	token string // upload token
	pass  []byte
}

func (s secret) probe() string {
	p := e2e.ProbeKey(s.key, s.id)
	return e2e.Encode(p[:])
}

type createResp struct {
	ID          string `json:"id"`
	UploadToken string `json:"upload_token"`
	Chunks      int64  `json:"chunks"`
	CTSize      int64  `json:"ct_size"`
}

// chunks splits a STREAM ciphertext into its chunks.
func chunks(ct []byte) [][]byte {
	var out [][]byte
	for len(ct) > fullChunk {
		out = append(out, ct[:fullChunk])
		ct = ct[fullChunk:]
	}
	return append(out, ct)
}

func encrypt(t *testing.T, key [32]byte, pt []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := e2e.NewEncryptor(&buf, key)
	if _, err := enc.Write(pt); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// begin creates a secret row and returns it with its link key; the caller
// uploads and finalizes.
func (e *env) begin(user, kind string, size int64, views int, extra map[string]any) (secret, createResp) {
	e.t.Helper()
	req := map[string]any{"kind": kind, "size": size, "views": views}
	for k, v := range extra {
		req[k] = v
	}
	var opts []func(*http.Request)
	if user != "" {
		opts = append(opts, as(user))
	}
	st, b, _ := e.do("POST", "/api/v1/secrets", req, opts...)
	mustStatus(e.t, "create", st, http.StatusCreated, b)
	cr := decode[createResp](e.t, b)
	id, err := e2e.Decode16(cr.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	key, _ := e2e.NewLinkKey()
	return secret{id: id, ids: cr.ID, key: key, token: cr.UploadToken}, cr
}

func (e *env) putChunk(sec secret, n int, ct []byte) (int, []byte) {
	e.t.Helper()
	st, b, _ := e.do("PUT", fmt.Sprintf("/api/v1/secrets/%s/chunks/%d", sec.ids, n), ct, header(secrets.HeaderUploadToken, sec.token))
	return st, b
}

// finalizeBody builds a valid finalize request for sec.
func finalizeBody(t *testing.T, sec secret, salt [16]byte, kdf *e2e.KDFParams, keys e2e.Keys, meta e2e.Meta) map[string]any {
	t.Helper()
	probe := e2e.ProbeKey(sec.key, sec.id)
	hp, ha := e2e.Hash(probe[:]), e2e.Hash(keys.Auth[:])
	metaCT, err := e2e.SealMeta(keys.Meta, meta)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"salt": e2e.Encode(salt[:]), "kdf": nil, "h_probe": e2e.Encode(hp[:]), "h_auth": e2e.Encode(ha[:]),
		"commit": e2e.Encode(keys.Commit[:]), "meta_ct": e2e.Encode(metaCT),
	}
	if kdf != nil {
		body["kdf"] = kdf
	}
	return body
}

// create makes a complete secret. content is the plaintext stream (padded
// for text); meta describes it.
func (e *env) create(user string, content []byte, meta e2e.Meta, views int, pass []byte) secret {
	e.t.Helper()
	sec, _ := e.begin(user, meta.Kind, int64(len(content)), views, nil)
	sec.pass = pass
	salt, _ := e2e.NewSalt()
	var kdf *e2e.KDFParams
	if pass != nil {
		kdf = &testKDF
	}
	keys, err := e2e.DeriveKeys(sec.key, sec.id, salt, pass, testKDF)
	if err != nil {
		e.t.Fatal(err)
	}
	for i, c := range chunks(encrypt(e.t, keys.Enc, content)) {
		st, b := e.putChunk(sec, i, c)
		mustStatus(e.t, fmt.Sprintf("chunk %d", i), st, http.StatusOK, b)
	}
	st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/finalize", finalizeBody(e.t, sec, salt, kdf, keys, meta),
		header(secrets.HeaderUploadToken, sec.token))
	mustStatus(e.t, "finalize", st, http.StatusOK, b)
	return sec
}

func (e *env) createText(user, text string, views int, pass []byte) secret {
	e.t.Helper()
	b := []byte(text)
	return e.create(user, e2e.PadText(b), e2e.Meta{Kind: e2e.KindText, Type: "text/plain; charset=utf-8", Size: int64(len(b))}, views, pass)
}

func (e *env) createFile(user, name string, data []byte, views int, pass []byte) secret {
	e.t.Helper()
	return e.create(user, data, e2e.Meta{Kind: e2e.KindFile, Name: name, Type: "application/octet-stream", Size: int64(len(data))}, views, pass)
}

type infoResp struct {
	State      string          `json:"state"`
	Mode       string          `json:"mode"`
	Kind       string          `json:"kind"`
	Salt       string          `json:"salt"`
	KDF        *e2e.KDFParams  `json:"kdf"`
	Size       int64           `json:"size"`
	CTSize     int64           `json:"ct_size"`
	ViewsLeft  int             `json:"views_left"`
	ExpiresAt  string          `json:"expires_at"`
	OpenedAt   *string         `json:"opened_at"`
	BurnReason string          `json:"burn_reason"`
	Raw        json.RawMessage `json:"-"`
}

func (e *env) info(sec secret, opts ...func(*http.Request)) (int, infoResp, []byte) {
	e.t.Helper()
	st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/info", map[string]string{"probe": sec.probe()}, opts...)
	var ir infoResp
	if st == http.StatusOK || st == http.StatusGone {
		ir = decode[infoResp](e.t, b)
	}
	return st, ir, b
}

// keys derives the envelope keys for sec from the server's info.
func (e *env) keys(sec secret, pass []byte) e2e.Keys {
	e.t.Helper()
	st, ir, b := e.info(sec)
	mustStatus(e.t, "info", st, http.StatusOK, b)
	salt, err := e2e.Decode16(ir.Salt)
	if err != nil {
		e.t.Fatal(err)
	}
	var p e2e.KDFParams
	if ir.KDF != nil {
		p = *ir.KDF
	}
	k, err := e2e.DeriveKeys(sec.key, sec.id, salt, pass, p)
	if err != nil {
		e.t.Fatal(err)
	}
	return k
}

type claimResp struct {
	Kind            string `json:"kind"`
	Commit          string `json:"commit"`
	MetaCT          string `json:"meta_ct"`
	ViewsLeft       int    `json:"views_left"`
	CTSize          int64  `json:"ct_size"`
	Content         string `json:"content"`
	Ticket          string `json:"ticket"`
	TicketExpiresAt string `json:"ticket_expires_at"`
	Error           string `json:"error"`
	AttemptsLeft    int    `json:"attempts_left"`
	State           string `json:"state"`
}

func (e *env) claimWith(sec secret, auth [32]byte, opts ...func(*http.Request)) (int, claimResp, []byte) {
	e.t.Helper()
	st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/claim",
		map[string]string{"probe": sec.probe(), "auth": e2e.Encode(auth[:])}, opts...)
	var cr claimResp
	if len(b) > 0 && b[0] == '{' {
		cr = decode[claimResp](e.t, b)
	}
	return st, cr, b
}

// open reveals sec the way a client does, and returns what it decrypted.
func (e *env) open(sec secret, pass []byte, opts ...func(*http.Request)) (e2e.Meta, []byte, claimResp) {
	e.t.Helper()
	keys := e.keys(sec, pass)
	st, cr, b := e.claimWith(sec, keys.Auth, opts...)
	mustStatus(e.t, "claim", st, http.StatusOK, b)
	commit, err := e2e.Decode32(cr.Commit)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := keys.CheckCommit(commit[:]); err != nil {
		e.t.Fatal(err)
	}
	metaCT, err := base64.RawURLEncoding.DecodeString(cr.MetaCT)
	if err != nil {
		e.t.Fatal(err)
	}
	meta, err := e2e.OpenMeta(keys.Meta, metaCT)
	if err != nil {
		e.t.Fatal(err)
	}
	var ct []byte
	if meta.Kind == e2e.KindText {
		if ct, err = base64.RawURLEncoding.DecodeString(cr.Content); err != nil {
			e.t.Fatal(err)
		}
	} else {
		var b []byte
		st, b, _ = e.do("GET", "/api/v1/secrets/"+sec.ids+"/blob", nil, header(secrets.HeaderTicket, cr.Ticket))
		mustStatus(e.t, "blob", st, http.StatusOK, b)
		ct = b
	}
	pt, err := io.ReadAll(e2e.NewDecryptor(bytes.NewReader(ct), keys.Enc))
	if err != nil {
		e.t.Fatal(err)
	}
	if meta.Kind == e2e.KindText {
		if pt, err = e2e.UnpadText(pt, meta.Size); err != nil {
			e.t.Fatal(err)
		}
	} else {
		st, b, _ := e.do("POST", "/api/v1/secrets/"+sec.ids+"/done", nil, header(secrets.HeaderTicket, cr.Ticket))
		mustStatus(e.t, "done", st, http.StatusNoContent, b)
	}
	return meta, pt, cr
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *env) col(q string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.s.App.DB.R.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.ExecContext(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) flushEvents() {
	e.t.Helper()
	if err := e.s.App.Stats.Flush(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func gen(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func contains(haystack string, needles ...string) string {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, n) {
			return n
		}
	}
	return ""
}
