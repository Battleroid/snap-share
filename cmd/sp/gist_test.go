package main

import (
	"strconv"
	"strings"
	"testing"
)

// sp paste with several files makes one gist.
func TestPasteGist(t *testing.T) {
	e := newCLIEnv(t)
	dir := t.TempDir()
	a := writeFile(t, dir, "main.go", "package main\n")
	b := writeFile(t, dir, "notes.md", "# notes\n")
	out := e.mustSP("log line\n", "paste", a, b, "-", "--name", "run.log", "--title", "Debug", "--expire", "1d")
	u := strings.TrimSpace(out)
	if it := e.item(u); it.kind != "gist" || it.title != "Debug" || it.filename != "main.go" || !it.expires.Valid {
		t.Fatalf("gist: %+v", it)
	}
	for i, want := range []string{"package main\n", "# notes\n", "log line\n"} {
		if code, body := e.get(u + "/raw?f=" + strconv.Itoa(i)); code != 200 || body != want {
			t.Errorf("file %d: %d %q", i, code, body)
		}
	}
	if code, page := e.get(u); code != 200 || !strings.Contains(page, "run.log") {
		t.Errorf("gist page: %d", code)
	}
	if ls := e.mustSP("", "ls"); !strings.Contains(ls, " gist ") || !strings.Contains(ls, "Debug") {
		t.Errorf("ls:\n%s", ls)
	}
	// Stdin without a name gets one from the server.
	out = e.mustSP("from stdin\n", "paste", "-", a)
	if code, body := e.get(strings.TrimSpace(out) + "/raw?f=0"); code != 200 || body != "from stdin\n" {
		t.Errorf("unnamed stdin: %d %q", code, body)
	}
	// One file is still a paste.
	if it := e.item(strings.TrimSpace(e.mustSP("", "paste", a, "--title", "One"))); it.kind != "paste" || it.title != "One" {
		t.Errorf("single paste: %+v", it)
	}

	bin := writeFile(t, dir, "x.bin", "\xff\xfe")
	many := make([]string, 51)
	for i := range many {
		many[i] = a
	}
	big := strings.Repeat("x", 9<<20)
	b1, b2 := writeFile(t, dir, "b1.txt", big), writeFile(t, dir, "b2.txt", big)
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"paste", "-", "-"}, "at most once"},
		{[]string{"paste", a, b, "--name", "x.txt"}, "for stdin"},
		{[]string{"paste", a, bin}, "x.bin is not UTF-8"},
		{append([]string{"paste"}, many...), "at most 50 files"},
		{[]string{"paste", b1, b2}, "limited to"},
		{[]string{"paste", a, a}, "Two files are named main.go"},
	} {
		if _, stderr, err := e.sp("", c.args...); err == nil || !strings.Contains(err.Error()+stderr, c.msg) {
			t.Errorf("%v: %v %s", c.args[1:min(len(c.args), 4)], err, stderr)
		}
	}
}
