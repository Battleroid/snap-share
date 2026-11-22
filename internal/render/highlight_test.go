package render

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with testdata/<name>.golden, rewriting it with -update.
func golden(t *testing.T, path, got string) {
	t.Helper()
	path += ".golden"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestHighlightGolden(t *testing.T) {
	tests := []struct {
		file, lang string
	}{
		{"demo.go", "Go"},
		{"config.yaml", "YAML"},
		{"up.sh", "Bash"},
		{"item.json", "JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			path := filepath.Join("testdata", "hl", tt.file)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, lang, err := Highlight(context.Background(), src, "", tt.file, 0)
			if err != nil {
				t.Fatal(err)
			}
			if lang != tt.lang {
				t.Errorf("lang = %q, want %q", lang, tt.lang)
			}
			checkHighlight(t, string(src), string(got))
			golden(t, path, string(got))
		})
	}
}

var (
	spanRe  = regexp.MustCompile(`<span class="([^"]*)">([^<]*)</span>`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	classes = map[string]bool{"tok-k": true, "tok-s": true, "tok-n": true, "tok-f": true, "tok-t": true, "tok-c": true, "tok-p": true}
)

// checkHighlight asserts the output is only known spans around escaped
// text, that no span crosses a line, and that stripping the markup gives
// back the (escaped) source line for line.
func checkHighlight(t *testing.T, src, out string) {
	t.Helper()
	for _, m := range spanRe.FindAllStringSubmatch(out, -1) {
		if !classes[m[1]] {
			t.Errorf("unexpected class %q", m[1])
		}
		if strings.Contains(m[2], "\n") {
			t.Errorf("span crosses a line: %q", m[0])
		}
	}
	rest := spanRe.ReplaceAllString(out, "$2")
	if strings.ContainsAny(rest, "<>") {
		t.Errorf("unexpected markup left after removing spans: %q", rest)
	}
	want := string(Plain([]byte(src)))
	if rest != want {
		t.Errorf("text mismatch\n got: %q\nwant: %q", rest, want)
	}
}

func TestHighlightEscapes(t *testing.T) {
	src := "<script>alert('x')</script> & \"q\" \x00"
	for _, lang := range []string{"html", "go", "plaintext", "javascript"} {
		got, _, err := Highlight(context.Background(), []byte(src), lang, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		s := tagRe.ReplaceAllStringFunc(string(got), func(tag string) string {
			if strings.HasPrefix(tag, `<span class="tok-`) || tag == "</span>" {
				return ""
			}
			return tag
		})
		if strings.ContainsAny(s, "<>\"'\x00") {
			t.Errorf("%s: unescaped output %q", lang, got)
		}
	}
}

func TestHighlightLexerChoice(t *testing.T) {
	tests := []struct {
		name, lang, file, src, want string
	}{
		{"explicit name", "Python", "x.go", "print(1)", "Python"},
		{"explicit alias", "py", "", "print(1)", "Python"},
		{"explicit extension", "rs", "", "fn main() {}", "Rust"},
		{"unknown lang falls to filename", "nope-lang", "main.go", "package main", "Go"},
		{"filename", "", "Dockerfile", "FROM alpine", "Docker"},
		{"filename with path", "", "/etc/nginx/nginx.conf", "server {}", "Nginx configuration file"},
		{"shebang", "", "", "#!/bin/bash\necho hi\n", "Bash"},
		{"nothing", "", "", "just some words", PlainText},
		{"empty", "", "", "", PlainText},
		{"overlong lang ignored", strings.Repeat("x", 200), "", "words", PlainText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got, err := Highlight(context.Background(), []byte(tt.src), tt.lang, tt.file, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("lang = %q, want %q", got, tt.want)
			}
		})
	}
	if got := DetectLang("notes.yml", []byte("a: 1")); got != "YAML" {
		t.Errorf("DetectLang = %q", got)
	}
}

func TestHighlightLines(t *testing.T) {
	src := "a := `x\ny`\r\n/* one\ntwo */\nb := 1"
	got, _, err := Highlight(context.Background(), []byte(src), "go", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	checkHighlight(t, src, string(got))
	if n := strings.Count(string(got), "\n"); n != 4 {
		t.Errorf("got %d newlines, want 4: %q", n, got)
	}
	if strings.HasSuffix(string(got), "\n") {
		t.Errorf("added a trailing newline: %q", got)
	}
}

func TestHighlightTooLarge(t *testing.T) {
	src := []byte(strings.Repeat("x", 101))
	if _, _, err := Highlight(context.Background(), src, "", "", 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, _, err := Highlight(context.Background(), src[:100], "", "", 100); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	big := make([]byte, DefaultMaxBytes+1)
	if _, _, err := Highlight(context.Background(), big, "", "", 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("default cap: err = %v", err)
	}
}

func TestHighlightContext(t *testing.T) {
	src := []byte(strings.Repeat("func f() { return 1 }\n", 20000))

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	start := time.Now()
	_, lang, err := Highlight(ctx, src, "go", "", 0)
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if lang != "Go" {
		t.Errorf("lang = %q, want it reported with the error", lang)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v after a 1ms deadline", d)
	}

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, _, err := Highlight(cctx, src, "go", "", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: err = %v", err)
	}
}

// TestHighlightPathological checks the CPU bounds with lexers built to
// misbehave: a catastrophically backtracking rule (stopped by regexp2's
// per-match timeout, then by the deadline check between tokens) and a rule
// that matches nothing forever (stopped by the deadline check).
func TestHighlightPathological(t *testing.T) {
	backtrack := chroma.MustNewLexer(&chroma.Config{Name: "backtrack"}, func() chroma.Rules {
		return chroma.Rules{"root": {
			{Pattern: `(a+)+b`, Type: chroma.Keyword},
			{Pattern: `(?s).`, Type: chroma.Text},
		}}
	})
	spin := chroma.MustNewLexer(&chroma.Config{Name: "spin"}, func() chroma.Rules {
		return chroma.Rules{"root": {{Pattern: ``, Type: chroma.Text}}}
	})
	tests := []struct {
		lexer chroma.Lexer
		src   string
	}{
		{backtrack, strings.Repeat("a", 40) + "!" + strings.Repeat("a", 40) + "!"},
		{spin, "anything"},
	}
	const deadline = 100 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.lexer.Config().Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			start := time.Now()
			_, _, err := highlight(ctx, tt.lexer, []byte(tt.src))
			d := time.Since(start)
			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}
			// At most one 250ms match can be in flight past the deadline.
			if d > deadline+time.Second {
				t.Errorf("took %v", d)
			}
		})
	}
}

// TestHighlightHostileInput runs real lexers on input shaped to make them
// work hard and checks each call ends close to its deadline.
func TestHighlightHostileInput(t *testing.T) {
	inputs := map[string]string{
		"quotes":   strings.Repeat(`"\`, 50000),
		"brackets": strings.Repeat("([{<", 25000),
		"heredoc":  "<<EOF\n" + strings.Repeat("$((", 30000),
	}
	const deadline = 150 * time.Millisecond
	for name, in := range inputs {
		for _, lang := range []string{"go", "bash", "php", "markdown"} {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			start := time.Now()
			_, _, err := Highlight(ctx, []byte(in), lang, "", 0)
			d := time.Since(start)
			cancel()
			if err != nil && !errors.Is(err, ErrTimeout) {
				t.Errorf("%s/%s: %v", name, lang, err)
			}
			if d > deadline+time.Second {
				t.Errorf("%s/%s took %v", name, lang, d)
			}
		}
	}
}

// TestHighlightAllLexers runs every lexer on a small mixed sample: no
// panics escape, and output uses only the Patchbay classes.
func TestHighlightAllLexers(t *testing.T) {
	src := "#!/x\n<a b=\"c\">{{ d }}</a> 'e' 1.5 // f\n/* g */ h(i) [j] $k @l %m\n"
	for _, lang := range Languages() {
		// Some lexers loop on odd input (Jungle never finishes this
		// sample); the deadline stops them.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		got, used, err := Highlight(ctx, []byte(src), lang, "", 0)
		cancel()
		if err != nil {
			t.Logf("%s: %v", lang, err) // a lexer error is reported, not panicked
			continue
		}
		if used == "" {
			t.Errorf("%s: empty langUsed", lang)
		}
		checkHighlight(t, src, string(got))
	}
}

func TestPlain(t *testing.T) {
	got := Plain([]byte("a<b>&\r\nc\xff"))
	if want := "a&lt;b&gt;&amp;\nc�"; string(got) != want {
		t.Errorf("Plain = %q, want %q", got, want)
	}
}

func TestLanguages(t *testing.T) {
	names := Languages()
	if len(names) < 100 {
		t.Fatalf("only %d languages", len(names))
	}
	seen := map[string]bool{}
	for i, n := range names {
		if i > 0 && strings.ToLower(names[i-1]) > strings.ToLower(n) {
			t.Errorf("not sorted: %q before %q", names[i-1], n)
		}
		seen[n] = true
	}
	for _, want := range []string{"Go", "YAML", "Bash", "JSON", PlainText} {
		if !seen[want] {
			t.Errorf("missing %q", want)
		}
	}
}
