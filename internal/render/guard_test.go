package render

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// slowPHP is quadratic in the PHP lexer (its DotAll /\*.*?\*/ rule rescans
// to the end at every "/*") but has short lines.
func slowPHP(n int) []byte {
	return []byte("<?php " + strings.Repeat("/* /* /* /* /* /* /* /* /* /*\n", n/30))
}

// waitIdle waits until no render holds a slot.
func waitIdle(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for len(slots) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d render slots still held after %s", len(slots), within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRefusedLexers(t *testing.T) {
	src := []byte("<?php echo 1; ?>\n{{ x }} <% y %>\n")
	for _, lang := range []string{"erb", "rhtml", "svelte", "go-html-template", "yaml+jinja", "ansible",
		"http", "rst", "org", "gemtext", "raku", "postgres", "cql", "mzsql", "arturo", "mess"} {
		start := time.Now()
		out, used, err := Highlight(context.Background(), src, lang, "", 0)
		if err != nil || used != PlainText || out != Plain(src) {
			t.Errorf("lang %q: used %q, err %v, out %q; want plain", lang, used, err, out)
		}
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Errorf("lang %q: refusing took %s", lang, d)
		}
	}
	for _, file := range []string{"x.erb", "x.svelte", "x.sls", "x.rst", "x.org", "x.raku"} {
		if got := DetectLang(file, nil); got != PlainText {
			t.Errorf("DetectLang(%q) = %q, want plaintext", file, got)
		}
	}
	// PHTML (lang phtml, files named *.php or *.phtml, "<?php" content)
	// becomes the lazy PHP lexer.
	if got := DetectLang("index.php", nil); got != "PHP" {
		t.Errorf("DetectLang(index.php) = %q, want PHP", got)
	}
	for _, c := range []struct{ lang, file string }{{"phtml", ""}, {"php", ""}, {"", "x.phtml"}, {"", "x.php"}} {
		_, used, err := Highlight(context.Background(), []byte("<?php\necho 1;\n"), c.lang, c.file, 0)
		if err != nil || used != "PHP" {
			t.Errorf("%+v: used %q, err %v; want PHP", c, used, err)
		}
	}
}

// emitters lists the emitters of a rule, including those inside ByGroups.
func emitters(e chroma.Emitter) []chroma.Emitter {
	out := []chroma.Emitter{e}
	v := reflect.ValueOf(e)
	if v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Struct {
		if f := v.Elem().FieldByName("Emitters"); f.IsValid() {
			for i := range f.Len() {
				if sub, ok := f.Index(i).Interface().(chroma.Emitter); ok && sub != nil {
					out = append(out, emitters(sub)...)
				}
			}
		}
	}
	return out
}

// TestRegistryEagerLexersRefused walks chroma's registry so that an upgrade
// adding an eager or dispatching lexer, or a fixed Using of a refused one,
// fails here rather than in production.
func TestRegistryEagerLexersRefused(t *testing.T) {
	for name := range refused {
		if lexers.GlobalLexerRegistry.Get(name) == nil {
			t.Errorf("refused lists %q, which chroma no longer has", name)
		}
	}
	for _, l := range lexers.GlobalLexerRegistry.Lexers {
		name := l.Config().Name
		allowed := !lexerRefused(l)
		switch l.(type) {
		case *chroma.RegexLexer:
		default:
			if allowed && name != markdownLexerName && !strings.Contains(fmt.Sprintf("%T", l), "remappingLexer") {
				t.Errorf("%s (%T) has its own Tokenise and is not refused", name, l)
			}
			continue
		}
		rules, err := l.(*chroma.RegexLexer).Rules()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, state := range rules {
			for _, rule := range state {
				if rule.Type == nil {
					continue
				}
				for _, e := range emitters(rule.Type) {
					k, ok := e.(interface{ EmitterKind() string })
					if !ok {
						continue
					}
					switch k.EmitterKind() {
					case "usingbygroup":
						if allowed {
							t.Errorf("%s takes a sub-lexer from the content and is not refused", name)
						}
					case "using":
						target := reflect.ValueOf(e).Elem().FieldByName("Lexer").String()
						if sub := lexers.Get(target); allowed && sub != nil && lexerRefused(sub) {
							t.Errorf("%s uses refused lexer %s", name, target)
						}
					}
				}
			}
		}
	}
}

func TestMarkdownLexerFences(t *testing.T) {
	ok := []byte("# t\n\n```go\nvar x = 1\n```\n")
	if out, used, err := Highlight(context.Background(), ok, "markdown", "", 0); err != nil || used != "markdown" || !strings.Contains(string(out), "tok-") {
		t.Errorf("go fence: used %q, err %v, out %q", used, err, out)
	}
	for _, lang := range []string{"phtml", "php", "http", "erb"} {
		src := []byte("# t\n\n```" + lang + "\n<?php /* x */\n```\n")
		_, used, err := Highlight(context.Background(), src, "md", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{true: PlainText, false: "markdown"}[lang != "php"]; used != want {
			t.Errorf("%s fence: used %q, want %q", lang, used, want)
		}
	}
	// lexers.Get on an unknown name glob-matches every lexer's file
	// patterns; the fence check must not, for each of many fence names.
	var many strings.Builder
	for i := 0; many.Len() < MaxHighlightBytes; i++ {
		fmt.Fprintf(&many, "```x%d\n```\n", i)
	}
	start := time.Now()
	if !fencesRefused([]byte(many.String())) {
		if d := time.Since(start); d > 2*time.Second { // lexers.Get would take over a minute
			t.Errorf("checking %d fence names took %s", strings.Count(many.String(), "\n")/2, d)
		}
	} else {
		t.Error("unknown fence names refused")
	}
}

// TestFenceNamesMatchRegistry: the precomputed names agree with
// lexers.Get for every name, alias and file extension chroma knows.
func TestFenceNamesMatchRegistry(t *testing.T) {
	// Names and aliases of every lexer (Get finds them in a map), and the
	// file patterns of refused ones (Get globs; slow, so only these).
	var words []string
	for _, l := range lexers.GlobalLexerRegistry.Lexers {
		c := l.Config()
		words = append(words, c.Name, strings.ToUpper(c.Name))
		words = append(words, c.Aliases...)
		if lexerRefused(l) {
			for _, g := range append(append([]string{}, c.Filenames...), c.AliasFilenames...) {
				words = append(words, strings.TrimPrefix(g, "*."), "a."+strings.TrimPrefix(g, "*."))
			}
		}
	}
	words = append(words, "php3", "php5", "inc", "md", "x", "")
	for _, w := range words {
		l := lexers.Get(w)
		if want := l != nil && lexerRefused(l); want && !names.refused(w) {
			t.Errorf("Get(%q) = %s (refused), names.refused says no", w, l.Config().Name)
		}
		if l != nil && !lexerRefused(l) && l.Config().Name == strings.TrimSpace(w) && names.refused(w) {
			t.Errorf("%q is the allowed lexer %s, names.refused says yes", w, l.Config().Name)
		}
	}
	if names.refused("php") || !names.refused("phtml") || !names.refused("erb") || names.refused("go") {
		t.Error("php/phtml/erb/go")
	}
}

// TestRunSlots: abandoned work keeps its slot until it finishes, and a
// caller waiting for a slot gives up at its deadline.
func TestRunSlots(t *testing.T) {
	waitIdle(t, 10*time.Second)
	release := make(chan struct{})
	for i := range MaxConcurrent {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		start := time.Now()
		_, err := run(ctx, func() (template.HTML, error) { <-release; return "late", nil })
		cancel()
		if !errors.Is(err, ErrTimeout) || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("run %d: %v after %s", i, err, time.Since(start))
		}
		if len(slots) != i+1 {
			t.Fatalf("after %d abandoned runs, %d slots held", i+1, len(slots))
		}
	}
	// All slots are held by abandoned work: the next caller times out
	// waiting, and its function never runs.
	ran := false
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	start := time.Now()
	_, err := run(ctx, func() (template.HTML, error) { ran = true; return "", nil })
	cancel()
	if !errors.Is(err, ErrTimeout) || ran || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("full: err %v, ran %v, after %s", err, ran, time.Since(start))
	}
	close(release)
	waitIdle(t, time.Second)

	// Panics become errors and release the slot.
	if _, err := run(context.Background(), func() (template.HTML, error) { panic("boom") }); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("panic: %v", err)
	}
	waitIdle(t, time.Second)
}

// blockingLexer stands in for an eager lexer: Tokenise does all its work
// (here: waits for release) before returning.
type blockingLexer struct {
	chroma.Lexer
	release chan struct{}
}

func (b blockingLexer) Tokenise(o *chroma.TokeniseOptions, text string) (chroma.Iterator, error) {
	<-b.release
	return b.Lexer.Tokenise(o, text)
}

// TestHighlightAbandonsEagerLexer: Highlight returns at the deadline even
// when the lexer cannot be interrupted; the slot is freed when it finishes.
func TestHighlightAbandonsEagerLexer(t *testing.T) {
	waitIdle(t, 10*time.Second)
	l := blockingLexer{lexers.Get("go"), make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, used, err := highlight(ctx, l, []byte("package main\n"))
	if !errors.Is(err, ErrTimeout) || used != "Go" {
		t.Fatalf("err %v, used %q", err, used)
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("returned after %s", d)
	}
	if len(slots) != 1 {
		t.Errorf("%d slots held while the lexer runs, want 1", len(slots))
	}
	close(l.release)
	waitIdle(t, time.Second)
}

// TestHighlightTimeoutUnderLoad: many concurrent requests with slow input
// each return close to their deadline, whether they got a slot or not,
// and the slots drain afterwards.
func TestHighlightTimeoutUnderLoad(t *testing.T) {
	waitIdle(t, 10*time.Second)
	src := slowPHP(200 << 10)
	const deadline = 200 * time.Millisecond
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()
			start := time.Now()
			_, used, err := Highlight(ctx, src, "php", "", 0)
			if d := time.Since(start); d > deadline+400*time.Millisecond {
				t.Errorf("returned after %s", d)
			}
			if !errors.Is(err, ErrTimeout) || used != "PHP" {
				t.Errorf("err %v, used %q", err, used)
			}
		})
	}
	wg.Wait()
	waitIdle(t, 2*time.Second) // the PHP lexer is lazy: abandoned work stops within a token
}

func TestLongLinePlain(t *testing.T) {
	ok := []byte("x := \"" + strings.Repeat("a", MaxLineLen-7) + "\"\n")
	if out, used, err := Highlight(context.Background(), ok, "go", "", 0); err != nil || used != "Go" || !strings.Contains(string(out), "tok-s") {
		t.Errorf("line of %d: used %q, err %v", len(ok)-1, used, err)
	}
	long := []byte("package main\n" + strings.Repeat("a", MaxLineLen+1) + "\nfunc f() {}\n")
	out, used, err := Highlight(context.Background(), long, "go", "", 0)
	if err != nil || used != "Go" || out != Plain(long) {
		t.Errorf("long line: used %q, err %v", used, err)
	}
	// Over MaxHighlightBytes (but under the input cap): plain too.
	big := []byte(strings.Repeat("func f() { return 1 }\n", MaxHighlightBytes/22+1))
	start := time.Now()
	out, used, err = Highlight(context.Background(), big, "go", "", 0)
	if err != nil || used != "Go" || out != Plain(big) || time.Since(start) > time.Second {
		t.Errorf("over MaxHighlightBytes: used %q, err %v, %s", used, err, time.Since(start))
	}
	if longLine([]byte(strings.Repeat("a\n", MaxLineLen))) || longLine([]byte(strings.Repeat("a\r", MaxLineLen))) {
		t.Error("short lines reported long")
	}
	// A long line in a fence: the document is refused as a whole.
	if _, err := Markdown([]byte("```go\n"+string(long)+"```\n"), 0); !errors.Is(err, ErrTooComplex) {
		t.Errorf("markdown with a long line: %v", err)
	}
}

// TestMarkdownPathological: the shapes from the pentest (runs of ">",
// "[" and "*"; measured at 400 KB: 74 s, 5.4 s, 3.1 s) and other quadratic
// cases either are refused up front or finish within the deadline.
func TestMarkdownPathological(t *testing.T) {
	waitIdle(t, 10*time.Second)
	lines := func(unit string, width, n int) []byte {
		var b strings.Builder
		for b.Len() < n {
			b.WriteString(strings.Repeat(unit, width/len(unit)))
			b.WriteByte('\n')
		}
		return []byte(b.String()[:n])
	}
	var table strings.Builder
	table.WriteString(strings.Repeat("|a", 400) + "|\n" + strings.Repeat("|-", 400) + "|\n")
	for table.Len() < 100<<10 {
		table.WriteString("|x\n")
	}
	cases := []struct {
		name string
		src  []byte
		want error // nil: renders (or times out) within the deadline
	}{
		{"> run 200 KB", []byte(strings.Repeat(">", 200<<10)), ErrTooLarge},
		{"> run at the cap", []byte(strings.Repeat(">", MarkdownMaxBytes)), ErrTooComplex},
		{"> nested 40 deep", lines(">", 40, MarkdownMaxBytes), ErrTooComplex},
		{"> - nested", lines("> - ", 4*40, 64<<10), ErrTooComplex},
		{"1. nested", lines("1. ", 3*40, 64<<10), ErrTooComplex},
		{"[ run", []byte(strings.Repeat("[", MarkdownMaxBytes)), ErrTooComplex},
		{"* run", []byte(strings.Repeat("*", MarkdownMaxBytes)), ErrTooComplex},
		{"[ lines", lines("[", MaxLineLen-1, MarkdownMaxBytes), nil},
		{"* lines", lines("*", MaxLineLen-1, MarkdownMaxBytes), nil},
		{"[]( lines", lines("[](", MaxLineLen-1, MarkdownMaxBytes), nil},
		{"> 32 deep", lines(">", 32, MarkdownMaxBytes), nil},
		{"wide table", []byte(table.String()), ErrTooComplex},
	}
	const deadline = 2 * time.Second
	for _, c := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		start := time.Now()
		_, err := markdown(ctx, c.src, 0)
		d := time.Since(start)
		cancel()
		t.Logf("%-16s %6d bytes: %v in %s", c.name, len(c.src), err, d.Round(time.Millisecond))
		switch {
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		case c.want == nil && err != nil && !errors.Is(err, ErrTimeout):
			t.Errorf("%s: %v", c.name, err)
		}
		if d > deadline+200*time.Millisecond {
			t.Errorf("%s: took %s", c.name, d)
		}
		if c.want != nil && d > 50*time.Millisecond {
			t.Errorf("%s: refusing took %s", c.name, d)
		}
	}
}

// TestMarkdownDeadline: a document slow to parse returns ErrTimeout at the
// deadline; the abandoned parse keeps its slot until it is done.
func TestMarkdownDeadline(t *testing.T) {
	// Parses abandoned by the previous test may still be running; on a slow
	// CI runner under -race that can take well past 10 s.
	waitIdle(t, 30*time.Second)
	var b strings.Builder
	for b.Len() < MarkdownMaxBytes-MaxLineLen {
		b.WriteString(strings.Repeat("[](", (MaxLineLen-1)/3) + "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := markdown(ctx, []byte(b.String()), 0)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err %v after %s", err, time.Since(start))
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("returned after %s", d)
	}
	if len(slots) != 1 {
		t.Errorf("%d slots held right after the deadline, want 1 (the parse is still running)", len(slots))
	}
	waitIdle(t, 30*time.Second)
}

func TestMarkdownTables(t *testing.T) {
	small := "| a | b |\n|:--|--:|\n| 1 | 2 |\n\nnot a table\n---\nx\n"
	if err := checkMarkdown([]byte(small)); err != nil {
		t.Errorf("small table: %v", err)
	}
	// "---" under a paragraph is a heading, not a table: no cells counted.
	if err := checkMarkdown([]byte("t\n---\n" + strings.Repeat("x\n", 60_000))); err != nil {
		t.Errorf("setext heading: %v", err)
	}
	var b strings.Builder
	b.WriteString("|" + strings.Repeat(" a |", 50) + "\n|" + strings.Repeat("---|", 50) + "\n")
	for range maxTableCells / 50 {
		b.WriteString("| x |\n")
	}
	if err := checkMarkdown([]byte(b.String())); !errors.Is(err, ErrTooComplex) {
		t.Errorf("table over the cell limit: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	limit := b.String()[:strings.LastIndex(b.String()[:len(b.String())-6*10], "\n")+1]
	if _, err := markdown(ctx, []byte(limit), 1<<20); err != nil {
		t.Errorf("table at the cell limit: %v", err)
	}
	t.Logf("table at the cell limit (%d bytes) rendered in %s", len(limit), time.Since(start))
}
