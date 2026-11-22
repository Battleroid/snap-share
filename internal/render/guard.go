package render

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Rendering runs third-party code (chroma, goldmark) that cannot be
// interrupted and, on hostile input, can be slow: some chroma lexers
// tokenise everything before returning a token, some regexes are quadratic
// on unterminated constructs, and goldmark's block and inline parsers have
// quadratic cases. Three things bound it:
//
//   - run executes the work in a goroutine and returns at the context's
//     deadline (ErrTimeout) whether or not the work has finished;
//   - slots caps how many renders run at once, counting abandoned ones,
//     so work that outlives its request cannot pile up;
//   - cheap pre-checks send known-bad input (refused lexers, very long
//     lines, deep Markdown nesting, huge tables) to plain text before any
//     of that code runs.

// MaxConcurrent is how many highlight and Markdown renders run at once in
// the process, including abandoned ones still finishing after their
// deadline.
const MaxConcurrent = 2

// slots is the process-wide render semaphore. It is package state on
// purpose: Highlight and Markdown are package functions called from any
// request, and the bound must hold across all of them. A slot is released
// only when the work actually finishes, not when its caller gives up.
var slots = make(chan struct{}, MaxConcurrent)

// run executes fn in a goroutine holding a slot. It returns fn's result,
// or ErrTimeout (or ctx's error) as soon as ctx ends, while waiting for a
// slot or while fn runs; fn keeps its slot until it returns. fn should
// itself stop early when ctx ends where it can. Panics in fn (chroma panics
// on emitter errors) become errors.
func run(ctx context.Context, fn func() (template.HTML, error)) (template.HTML, error) {
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return "", ctxErr(ctx)
	}
	type result struct {
		out template.HTML
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-slots }()
		var res result
		defer func() {
			if p := recover(); p != nil {
				res = result{"", fmt.Errorf("render: %v", p)}
			}
			done <- res
		}()
		res.out, res.err = fn()
	}()
	select {
	case res := <-done:
		return res.out, res.err
	case <-ctx.Done():
		return "", ctxErr(ctx)
	}
}

// MaxLineLen is the longest line Highlight and Markdown process. Longer
// lines (minified code, data on one line) are where per-match regex costs
// and inline parsing are worst; such input is rendered as plain text.
const MaxLineLen = 20_000

// longLine reports whether src has a line longer than MaxLineLen bytes.
// CR counts as a line break, as normalise turns it into one.
func longLine(src []byte) bool {
	for len(src) > MaxLineLen {
		i := bytes.IndexAny(src[:MaxLineLen+1], "\r\n")
		if i < 0 {
			return true
		}
		src = src[i+1:]
	}
	return false
}

// refused are the chroma lexers Highlight never runs, by Config().Name.
// In snap the language always comes from the user: the lang field, the
// file name, the content, or a Markdown fence, so every one of these is
// reachable by an attacker.
//
// Eager (chroma.DelegatingLexer): these tokenise the whole input inside
// Tokenise, before the first token, so the deadline cannot stop them and
// an abandoned render holds its slot until it is done. PHTML combines that
// with PHP's comment rule /\*.*?\*/ (DotAll), which rescans to the end of
// the input at every unterminated "/*": 40 KB took 9 s, 96 KB 47 s.
// isDelegating also catches delegating lexers added by a chroma upgrade.
//
// Dispatching: these pick a sub-lexer by a name taken from the content (a
// code block's language, a Content-Type header, a :lang pod option, a
// PostgreSQL LANGUAGE clause), so the content can switch to an eager
// lexer. chroma's Markdown lexer does the same for fences; it is allowed
// when no fence names a refused lexer (see fencesRefused).
var refused = map[string]string{
	"PHTML":            "eager (delegating), quadratic PHP comment rule",
	"ERB":              "eager (delegating)",
	"Svelte":           "eager (delegating)",
	"Go HTML Template": "eager (delegating)",
	"YAML+Jinja":       "eager (delegating)",

	"HTTP":                    "sub-lexer from the Content-Type header",
	"reStructuredText":        "sub-lexer from code-block directives",
	"Org Mode":                "sub-lexer from #+BEGIN_SRC",
	"Gemtext":                 "sub-lexer from preformatted block alt text",
	"Markless":                "sub-lexer from code block options",
	"Raku":                    "sub-lexer from :lang pod options",
	"Arturo":                  "sub-lexer from embedded language names",
	"Cassandra CQL":           "sub-lexer from the function LANGUAGE",
	"PostgreSQL SQL dialect":  "sub-lexer from the function LANGUAGE",
	"Materialize SQL dialect": "sub-lexer from the function LANGUAGE",
}

// substitutes replace a refused lexer with a safe one that highlights the
// same code. The PHP lexer is lazy (the deadline stops it) and leaves the
// HTML around <?php ... ?> plain.
var substitutes = map[string]string{
	"PHTML": "PHP",
}

// delegatingType is chroma's unexported delegating lexer type.
var delegatingType = reflect.TypeOf(chroma.DelegatingLexer(nil, nil))

func isDelegating(l chroma.Lexer) bool { return reflect.TypeOf(l) == delegatingType }

// safeLexer returns l, a substitute for it, or nil if l may not run on src.
func safeLexer(l chroma.Lexer, src []byte) chroma.Lexer {
	name := l.Config().Name
	if _, bad := refused[name]; bad || isDelegating(l) {
		if sub := lexers.Get(substitutes[name]); sub != nil && substitutes[name] != "" {
			return safeLexer(sub, src)
		}
		return nil
	}
	if name == markdownLexerName && fencesRefused(src) {
		return nil
	}
	return l
}

func lexerRefused(l chroma.Lexer) bool {
	_, bad := refused[l.Config().Name]
	return bad || isDelegating(l)
}

// markdownLexerName is chroma's Markdown lexer.
const markdownLexerName = "markdown"

// fenceLang matches what chroma's Markdown lexer takes as a fence's
// language (```name), anywhere, which is a superset of where its rule
// applies.
var fenceLang = regexp.MustCompile("```(\\w+)")

// fencesRefused reports whether a fence in src names a refused lexer (the
// Markdown lexer would tokenise that block with it).
func fencesRefused(src []byte) bool {
	seen := map[string]bool{}
	for _, m := range fenceLang.FindAllSubmatch(src, -1) {
		name := string(m[1])
		if seen[name] {
			continue
		}
		seen[name] = true
		if names.refused(name) {
			return true
		}
	}
	return false
}

// names answers "would lexers.Get(name) return a refused lexer?" without
// calling it: for a name that is not a lexer name or alias, Get glob-matches
// it against every lexer's file patterns, which takes milliseconds, and a
// fence name is attacker-chosen. Built once, read-only afterwards.
var names = newLexerNames()

type lexerNames struct {
	byName, byAlias map[string]bool // name or alias -> refused
	exts            map[string]bool // refused lexers' "*.ext" patterns
	globs           []string        // their other file patterns
}

func newLexerNames() *lexerNames {
	n := &lexerNames{byName: map[string]bool{}, byAlias: map[string]bool{}, exts: map[string]bool{}}
	for _, l := range lexers.GlobalLexerRegistry.Lexers {
		c, bad := l.Config(), lexerRefused(l)
		for _, k := range []string{c.Name, strings.ToLower(c.Name)} {
			n.byName[k] = n.byName[k] || bad
		}
		for _, a := range c.Aliases {
			for _, k := range []string{a, strings.ToLower(a)} {
				n.byAlias[k] = n.byAlias[k] || bad
			}
		}
		if !bad {
			continue
		}
		for _, g := range append(append([]string{}, c.Filenames...), c.AliasFilenames...) {
			if ext, ok := strings.CutPrefix(g, "*."); ok && !strings.ContainsAny(ext, "*?[\\") {
				n.exts[ext] = true
			} else {
				n.globs = append(n.globs, g)
			}
		}
	}
	return n
}

// refused follows chroma's LexerRegistry.Get: exact name, exact alias,
// lower-case name, lower-case alias, then file patterns ("filename."+name
// and name). For the patterns it is conservative: any refused lexer's
// pattern matching counts, whatever its priority.
func (n *lexerNames) refused(name string) bool {
	if len(name) > maxNameLen {
		return false // pickLexer and Get ignore it as well
	}
	lower := strings.ToLower(name)
	for _, m := range []struct {
		set map[string]bool
		key string
	}{{n.byName, name}, {n.byAlias, name}, {n.byName, lower}, {n.byAlias, lower}} {
		if bad, ok := m.set[m.key]; ok {
			return bad
		}
	}
	// "*.ext" matches "filename."+name when name ends in ".ext" (or is
	// ext), and name itself when it ends in ".ext".
	if n.exts[name] {
		return true
	}
	for rest := name; ; {
		i := strings.IndexByte(rest, '.')
		if i < 0 {
			break
		}
		if rest = rest[i+1:]; n.exts[rest] {
			return true
		}
	}
	for _, g := range n.globs {
		if ok, _ := filepath.Match(g, "filename."+name); ok {
			return true
		}
		if ok, _ := filepath.Match(g, name); ok {
			return true
		}
	}
	return false
}
