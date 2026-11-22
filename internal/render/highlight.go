// Package render turns user text into safe HTML: syntax-highlighted code
// using the Patchbay token classes, and sanitized Markdown.
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

const (
	// DefaultMaxBytes is the input cap used when a caller passes
	// maxBytes <= 0. Text up to it is previewed.
	DefaultMaxBytes = 512 << 10

	// MaxHighlightBytes is the most Highlight tokenises; larger input (up
	// to maxBytes) comes back plain.
	MaxHighlightBytes = 256 << 10

	// DefaultTimeout bounds highlighting when the context has no deadline.
	DefaultTimeout = 5 * time.Second

	// PlainText is the language name reported when no lexer applies.
	PlainText = "plaintext"

	// analyseBytes is how much of the input content analysis looks at.
	// Chroma's analysers are regexes without a match timeout; a prefix
	// is enough to spot shebangs, doctypes and similar markers.
	analyseBytes = 8 << 10

	// maxNameLen caps the language and file names used for lexer lookup.
	maxNameLen = 128
)

var (
	// ErrTooLarge is returned when the input is above the byte cap.
	ErrTooLarge = errors.New("render: input too large")

	// ErrTimeout is returned when rendering runs past its deadline.
	// The error also matches context.DeadlineExceeded.
	ErrTimeout = errors.New("render: rendering timed out")
)

// Highlight tokenises src and returns HTML for the inside of a
// <pre><code> element, with tokens wrapped in <span class="tok-x"> using the
// Patchbay classes. Line breaks are preserved and no span crosses a line.
//
// The lexer is chosen by the explicit lang (a name, alias or extension),
// then by filename, then by content analysis, and falls back to plain text.
// langUsed is the chosen lexer's name ("Go", "YAML", "plaintext"…); it is
// also returned alongside ErrTimeout so a caller can fall back to Plain.
//
// Lexers that cannot be stopped or that take their sub-language from the
// content are never used (see refused; PHTML becomes PHP): that input
// comes back plain with langUsed PlainText. Input over MaxHighlightBytes
// or with a line longer than MaxLineLen is not tokenised either: it comes
// back plain with the lexer's name as langUsed.
//
// Input above maxBytes (DefaultMaxBytes when maxBytes <= 0) returns
// ErrTooLarge. Highlight returns ErrTimeout at the context deadline, or
// after DefaultTimeout when the context has none, even if the lexer is
// still running; at most MaxConcurrent renders run at once in the process.
func Highlight(ctx context.Context, src []byte, lang, filename string, maxBytes int) (html template.HTML, langUsed string, err error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if len(src) > maxBytes {
		return "", "", ErrTooLarge
	}
	lexer := safeLexer(pickLexer(lang, filename, src, true), src)
	if lexer == nil {
		return Plain(src), PlainText, nil
	}
	if len(src) > MaxHighlightBytes || longLine(src) {
		return Plain(src), lexerName(lexer), nil
	}
	return highlight(ctx, lexer, src)
}

// Plain returns src HTML-escaped with no token spans: the fallback for
// input that is too large or timed out. CRLF is normalised to LF.
func Plain(src []byte) template.HTML {
	var b strings.Builder
	b.Grow(len(src) + len(src)/8)
	template.HTMLEscape(&b, normalise(src))
	return template.HTML(b.String()) //nolint:gosec // escaped above
}

// Languages returns the names of the lexers Highlight uses, sorted
// case-insensitively, for a language picker. Each name is accepted as
// Highlight's lang.
func Languages() []string {
	var names []string
	for _, n := range lexers.Names(false) {
		if l := lexers.Get(n); l == nil || !lexerRefused(l) {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
		if a == b {
			return names[i] < names[j]
		}
		return a < b
	})
	return names
}

// DetectLang returns the language Highlight would use for a file with this
// name and content when no explicit language is given.
func DetectLang(filename string, src []byte) string {
	l := safeLexer(pickLexer("", filename, src, true), src)
	if l == nil {
		return PlainText
	}
	return lexerName(l)
}

// Ext returns the usual file extension (".go") for a language name, alias
// or extension as Highlight's lang takes it, or "" when the language is
// unknown or has no simple extension.
func Ext(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" || len(lang) > maxNameLen {
		return ""
	}
	l := lexers.Get(lang)
	if l == nil {
		return ""
	}
	for _, g := range l.Config().Filenames {
		e, ok := strings.CutPrefix(g, "*.")
		if ok && e != "" && len(e) <= 10 && strings.IndexFunc(e, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		}) < 0 {
			return "." + e
		}
	}
	return ""
}

// pickLexer resolves the lexer: explicit lang, then filename, then (if
// analyse) content analysis, then plain text.
func pickLexer(lang, filename string, src []byte, analyse bool) chroma.Lexer {
	if lang = strings.TrimSpace(lang); lang != "" && len(lang) <= maxNameLen {
		if l := lexers.Get(lang); l != nil {
			return l
		}
	}
	if filename != "" {
		if base := filepath.Base(filename); len(base) <= maxNameLen {
			if l := lexers.Match(base); l != nil {
				return l
			}
		}
	}
	if analyse && len(src) > 0 {
		head := src
		if len(head) > analyseBytes {
			head = head[:analyseBytes]
		}
		if l := lexers.Analyse(string(head)); l != nil {
			return l
		}
	}
	return plainLexer()
}

func plainLexer() chroma.Lexer {
	if l := lexers.Get(PlainText); l != nil {
		return l
	}
	return lexers.Fallback
}

func lexerName(l chroma.Lexer) string {
	if l == lexers.Fallback {
		return PlainText
	}
	return l.Config().Name
}

// highlight formats src with lexer in a render slot, returning at the
// deadline (DefaultTimeout if ctx has none).
func highlight(ctx context.Context, lexer chroma.Lexer, src []byte) (template.HTML, string, error) {
	name := lexerName(lexer)
	if name == PlainText {
		return Plain(src), name, nil
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	out, err := run(ctx, func() (template.HTML, error) { return format(ctx, lexer, src) })
	return out, name, err
}

// format runs the lexer and formats the tokens on the calling goroutine.
// It checks the context between tokens, so a lazy lexer stops within one
// token of the deadline, and turns lexer panics (chroma panics on emitter
// errors) into errors.
func format(ctx context.Context, lexer chroma.Lexer, src []byte) (out template.HTML, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("render: %s lexer failed: %v", lexerName(lexer), r)
		}
	}()
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	text := string(normalise(src))
	it, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root"}, text)
	if err != nil {
		return "", fmt.Errorf("render: tokenise %s: %w", lexerName(lexer), err)
	}
	f := formatter{trailingNL: strings.HasSuffix(text, "\n")}
	f.b.Grow(len(text) * 2)
	for tok := it(); tok != chroma.EOF; tok = it() {
		if err := ctxErr(ctx); err != nil {
			return "", err
		}
		f.add(tokenClass(tok.Type), tok.Value)
	}
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	return template.HTML(f.finish()), nil //nolint:gosec // formatter escapes all text
}

func ctxErr(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return err
}

// normalise converts CRLF and lone CR to LF and replaces invalid UTF-8.
func normalise(src []byte) []byte {
	if bytes.IndexByte(src, '\r') >= 0 {
		src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
		src = bytes.ReplaceAll(src, []byte("\r"), []byte("\n"))
	}
	if !utf8.Valid(src) {
		src = bytes.ToValidUTF8(src, []byte("�"))
	}
	return src
}

// formatter merges adjacent tokens of the same class and writes escaped
// text, closing and reopening spans at line breaks.
type formatter struct {
	b          strings.Builder
	class      string
	pending    strings.Builder
	trailingNL bool
}

func (f *formatter) add(class, value string) {
	if value == "" {
		return
	}
	if class != f.class {
		f.flush()
		f.class = class
	}
	f.pending.WriteString(value)
}

func (f *formatter) flush() {
	text := f.pending.String()
	f.pending.Reset()
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			f.b.WriteByte('\n')
		}
		if line == "" {
			continue
		}
		if f.class != "" {
			f.b.WriteString(`<span class="`)
			f.b.WriteString(f.class)
			f.b.WriteString(`">`)
		}
		template.HTMLEscape(&f.b, []byte(line))
		if f.class != "" {
			f.b.WriteString(`</span>`)
		}
	}
}

// finish writes the last segment. Lexers with EnsureNL append a newline
// the source did not have; it is dropped so output and input line up.
func (f *formatter) finish() string {
	if !f.trailingNL {
		s := f.pending.String()
		if strings.HasSuffix(s, "\n") {
			f.pending.Reset()
			f.pending.WriteString(strings.TrimSuffix(s, "\n"))
		}
	}
	f.flush()
	return f.b.String()
}

// tokenClass maps a chroma token type onto the seven Patchbay classes.
// Types not listed are left unwrapped (plain text, names, variables,
// whitespace, errors).
func tokenClass(t chroma.TokenType) string {
	switch t {
	case chroma.KeywordConstant, chroma.NameConstant, chroma.LiteralStringBoolean, chroma.LiteralDate:
		return "tok-n" // true, false, nil, constants
	case chroma.KeywordType:
		return "tok-t" // int, string…
	case chroma.NameBuiltin, chroma.NameBuiltinPseudo, chroma.OperatorWord, chroma.CommentPreproc:
		return "tok-k" // builtins, `and`/`or`, #include
	case chroma.CommentPreprocFile:
		return "tok-s" // <stdio.h>
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameDecorator:
		return "tok-f"
	case chroma.NameClass, chroma.NameTag, chroma.NameAttribute, chroma.NameNamespace,
		chroma.NameException, chroma.NameEntity, chroma.NameProperty:
		return "tok-t"
	case chroma.GenericHeading, chroma.GenericSubheading:
		return "tok-t" // diff headers
	case chroma.GenericInserted:
		return "tok-s" // diff +
	case chroma.GenericDeleted:
		return "tok-n" // diff -
	case chroma.GenericPrompt:
		return "tok-p"
	}
	switch {
	case t.InCategory(chroma.Keyword):
		return "tok-k"
	case t.InSubCategory(chroma.LiteralString):
		return "tok-s"
	case t.InSubCategory(chroma.LiteralNumber):
		return "tok-n"
	case t.InCategory(chroma.Comment):
		return "tok-c"
	case t.InCategory(chroma.Punctuation), t.InCategory(chroma.Operator):
		return "tok-p"
	}
	return ""
}
