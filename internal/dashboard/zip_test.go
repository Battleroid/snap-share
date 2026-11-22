package dashboard_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth/authtest"
)

func TestZipDownload(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	big := strings.Repeat("0123456789abcdef", 1<<19) // 8 MiB each: far more than socket buffers hold
	files := []map[string]any{
		e.put("ann", "a.txt", "first"),
		e.put("ann", "A.TXT", "second"),
		e.put("ann", "a.txt", "third"),
		e.put("ann", `..\..\evil.txt`, "evil"),
		e.put("ann", "big1.bin", big),
		e.put("ann", "big2.bin", big+"!"),
		e.put("ann", "big3.bin", big+"!!"),
		e.paste("ann", "main.go", "package main\n"),
		e.paste("ann", "", "unnamed paste"),
		e.link("ann", "https://example.com/"),
		e.album("ann", "Album"),
	}
	res, m := e.bulk("ann", map[string]any{"ids": ids(files...), "action": "download_zip"})
	if res.StatusCode != 200 || num(m, "count") != 9 || num(m, "skipped") != 2 || !strings.Contains(str(m, "url"), "/api/v1/zip/") {
		t.Fatalf("zip: %d %v", res.StatusCode, m)
	}
	u := path(str(m, "url"))

	// Other users and anonymous requests cannot use the URL.
	if res, _ := e.do("GET", u, nil, as("bob")); res.StatusCode != 404 {
		t.Errorf("bob: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", u, nil, header("Accept", "application/json")); res.StatusCode != 401 {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/api/v1/zip/nope", nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("unknown token: %d", res.StatusCode)
	}

	// Stream it, pausing mid-archive: no database connection is in use then
	// (no open rows or transaction), and a write goes through at once.
	req := e.s.Request(t, "GET", u, nil)
	authtest.Session(req, "ann")
	r, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("GET zip: %d %v", r.StatusCode, r.Header)
	}
	head := make([]byte, 64<<10)
	if _, err := io.ReadFull(r.Body, head); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if w, rd := e.s.App.DB.W.Stats().InUse, e.s.App.DB.R.Stats().InUse; w != 0 || rd != 0 {
		t.Errorf("connections in use while streaming: write %d read %d", w, rd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.s.App.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = updated_at + 1 WHERE id = 1`)
		return err
	}); err != nil {
		t.Errorf("write during stream: %v", err)
	}
	rest, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	data := append(head, rest...)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive does not open: %v", err)
	}
	want := map[string]string{
		"a.txt": "first", "A (2).TXT": "second", "a (3).txt": "third", "evil.txt": "evil",
		"big1.bin": big, "big2.bin": big + "!", "big3.bin": big + "!!", "main.go": "package main\n",
	}
	got := map[string]string{}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Method != zip.Store {
			t.Errorf("%s: method %d", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if uint64(len(b)) != f.UncompressedSize64 {
			t.Errorf("%s: size %d header %d", f.Name, len(b), f.UncompressedSize64)
		}
		got[f.Name] = string(b)
	}
	if len(zr.File) != 9 {
		t.Errorf("entries: %v", names)
	}
	for n, c := range want {
		if got[n] != c {
			t.Errorf("%s: %d bytes, want %d (entries %v)", n, len(got[n]), len(c), names)
		}
	}
	var unnamed string
	for n := range got {
		if strings.HasSuffix(n, ".txt") && got[n] == "unnamed paste" {
			unnamed = n
		}
	}
	if unnamed == "" || strings.ContainsAny(unnamed, `/\`) {
		t.Errorf("unnamed paste entry: %v", names)
	}
	for _, n := range names {
		if strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") {
			t.Errorf("unsafe entry name %q", n)
		}
	}

	// A deleted item drops out of an issued URL; expired URLs stop working.
	if res, _ := e.doJSON("DELETE", "/api/v1/items/"+itoa(id(files[0])), nil, as("ann")); res.StatusCode != 204 {
		t.Fatal("delete")
	}
	res2, body := e.do("GET", u, nil, as("ann"))
	zr, err = zip.NewReader(strings.NewReader(body), int64(len(body)))
	if res2.StatusCode != 200 || err != nil || len(zr.File) != 8 {
		t.Errorf("after delete: %d %v", res2.StatusCode, err)
	}
	e.s.App.Dashboard.ExpireZipTickets()
	if res, _ := e.do("GET", u, nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("expired: %d", res.StatusCode)
	}

	// Only links and albums: nothing to zip.
	if res, m := e.bulk("ann", map[string]any{"ids": ids(files[9], files[10]), "action": "download_zip"}); res.StatusCode != 400 {
		t.Errorf("links only: %d %v", res.StatusCode, m)
	}
	// Another user's ids are refused.
	if res, _ := e.bulk("bob", map[string]any{"ids": ids(files[1]), "action": "download_zip"}); res.StatusCode != 404 {
		t.Errorf("bob zip: %d", res.StatusCode)
	}
}
