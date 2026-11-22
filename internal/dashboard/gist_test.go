package dashboard_test

import (
	"archive/zip"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func (e *env) gist(user string, body map[string]any) map[string]any {
	e.t.Helper()
	res, m := e.doJSON("POST", "/api/v1/gists", body, as(user))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("gist: %d %v", res.StatusCode, m)
	}
	return m
}

// blobs counts the blobs in the files store (not its temp directory).
func (e *env) blobs() int {
	e.t.Helper()
	n := 0
	err := filepath.WalkDir(e.s.App.Cfg.FilesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".tmp" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A gist's files go into the zip in a folder of their own; the type pill
// counts it.
func TestGistZipAndPill(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	files := []map[string]any{{"name": "main.go", "content": "package main\n"}, {"name": "README.md", "content": "# hi\n"}}
	g1 := e.gist("ann", map[string]any{"title": "notes", "files": files})
	g2 := e.gist("ann", map[string]any{"title": "Notes", "files": files})
	g3 := e.gist("ann", map[string]any{"title": "../..", "files": files[:1]})
	plain := e.put("ann", "notes", "a file named like the first gist")
	res, m := e.bulk("ann", map[string]any{"ids": ids(g1, g2, g3, plain), "action": "download_zip"})
	if res.StatusCode != 200 || num(m, "count") != 4 {
		t.Fatalf("zip: %d %v", res.StatusCode, m)
	}
	res2, body := e.do("GET", path(str(m, "url")), nil, as("ann"))
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if res2.StatusCode != 200 || err != nil {
		t.Fatalf("GET zip: %d %v", res2.StatusCode, err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
		if strings.Contains(f.Name, `\`) || strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") || strings.Count(f.Name, "/") > 1 {
			t.Errorf("unsafe entry %q", f.Name)
		}
	}
	// Folders are named after the gist's title, numbered on repeats; a title
	// with nothing safe in it ("../..") falls back to the slug.
	want := map[string]string{
		"notes/main.go": "package main\n", "notes/README.md": "# hi\n",
		"Notes (2)/main.go": "package main\n", "Notes (2)/README.md": "# hi\n",
		str(g3, "slug") + "/main.go": "package main\n",
		"notes (3)":                  "a file named like the first gist",
	}
	if len(got) != len(want) {
		t.Errorf("entries: %v", got)
	}
	for n, c := range want {
		if got[n] != c {
			t.Errorf("%s: %q (entries %v)", n, got[n], got)
		}
	}

	if _, page := e.do("GET", "/dashboard", nil, as("ann")); !strings.Contains(page, `data-type="gist">gist <span class="pb-pill__count">3</span>`) {
		t.Error("dashboard has no gist pill")
	}
	if names := e.names("ann", "type=gist"); len(names) != 3 {
		t.Errorf("type=gist: %v", names)
	}
}

// Bulk delete removes a gist's file blobs and renders.
func TestGistBulkDelete(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	g := e.gist("ann", map[string]any{"files": []map[string]any{{"name": "a.go", "content": "package a\n"}, {"name": "b.md", "content": "# b\n"}}})
	if n := e.blobs(); n != 4 {
		t.Fatalf("%d blobs, want 2 files and 2 renders", n)
	}
	if res, m := e.bulk("ann", map[string]any{"ids": ids(g), "action": "delete"}); res.StatusCode != 200 || num(m, "count") != 1 {
		t.Fatalf("bulk delete: %d %v", res.StatusCode, m)
	}
	if n := e.blobs(); n != 0 {
		t.Errorf("%d blobs left after bulk delete", n)
	}
}
