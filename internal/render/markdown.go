package render

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// IDPrefix is prepended to every generated heading id, and to in-page
// fragment links, so user content can't clobber DOM globals or app ids.
const IDPrefix = "user-content-"

// LinkRel is set on every link in rendered Markdown.
const LinkRel = "nofollow noopener noreferrer"

// policy is built once and only read afterwards; bluemonday policies are
// safe for concurrent use.
var policy = newPolicy()

// MarkdownMaxBytes is the Markdown input cap used when a caller passes
// maxBytes <= 0. Larger documents are shown as source.
const MarkdownMaxBytes = 128 << 10

// ErrTooComplex is returned for Markdown whose structure is known to make
// rendering slow (see checkMarkdown); show the source instead.
var ErrTooComplex = errors.New("render: markdown too deeply nested or too wide")

// Markdown renders GitHub-flavoured Markdown (tables, strikethrough,
// autolinks, task lists) to sanitized HTML for a pb-prose container.
//
// Raw HTML is dropped. Heading ids get IDPrefix and "#frag" links are
// rewritten to match. Links get rel=LinkRel and only http, https and mailto
// URLs survive (images: http and https). Fenced code blocks are highlighted
// with the Patchbay token classes inside <pre><code>, with the same lexer
// rules as Highlight; blocks still being highlighted when 60% of the time
// is used up are plain.
//
// Input above maxBytes (MarkdownMaxBytes when <= 0) returns ErrTooLarge;
// input that fails checkMarkdown returns ErrTooComplex. The whole render
// gets DefaultTimeout, after which ErrTimeout is returned, and shares the
// MaxConcurrent render slots with Highlight.
func Markdown(src []byte, maxBytes int) (template.HTML, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return markdown(ctx, src, maxBytes)
}

func markdown(ctx context.Context, src []byte, maxBytes int) (template.HTML, error) {
	if maxBytes <= 0 {
		maxBytes = MarkdownMaxBytes
	}
	if len(src) > maxBytes {
		return "", ErrTooLarge
	}
	src = normalise(src)
	if err := checkMarkdown(src); err != nil {
		return "", err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	return run(ctx, func() (template.HTML, error) {
		// Fenced code gets the first 60% of the time left, so that a slow
		// block becomes plain text and the document still renders.
		fctx := ctx
		if dl, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			fctx, cancel = context.WithDeadline(ctx, time.Now().Add(time.Until(dl)*3/5))
			defer cancel()
		}
		md := goldmark.New(
			goldmark.WithExtensions(
				extension.Linkify,
				extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
				extension.Strikethrough,
				extension.TaskList,
			),
			goldmark.WithParserOptions(
				parser.WithAutoHeadingID(),
				parser.WithASTTransformers(util.Prioritized(safeTransformer{}, 1000)),
			),
			goldmark.WithRendererOptions(
				// Raw HTML is omitted by default (no html.WithUnsafe).
				renderer.WithNodeRenderers(util.Prioritized(&codeRenderer{ctx: fctx}, 100)),
			),
		)
		var buf bytes.Buffer
		if err := md.Convert(src, &buf); err != nil {
			return "", err
		}
		if err := ctxErr(ctx); err != nil {
			return "", err // nobody is waiting; skip sanitizing
		}
		return template.HTML(policy.SanitizeBytes(buf.Bytes())), nil //nolint:gosec // sanitized by bluemonday
	})
}

// safeTransformer prefixes heading ids, sets rel on links, rewrites
// fragment links and removes links and images whose URL is not allowed.
type safeTransformer struct{}

func (safeTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var unwrap, drop []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if id, ok := n.AttributeString("id"); ok {
				if b, ok := id.([]byte); ok {
					n.SetAttributeString("id", append([]byte(IDPrefix), b...))
				}
			}
		case *ast.Link:
			dest, ok := safeURL(string(n.Destination), true)
			if !ok {
				unwrap = append(unwrap, n)
				break
			}
			n.Destination = []byte(dest)
			n.SetAttributeString("rel", []byte(LinkRel))
		case *ast.AutoLink:
			u := string(n.URL(source))
			if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(u), "mailto:") {
				u = "mailto:" + u
			}
			if _, ok := safeURL(u, true); !ok {
				drop = append(drop, n)
				break
			}
			n.SetAttributeString("rel", []byte(LinkRel))
		case *ast.Image:
			dest, ok := safeURL(string(n.Destination), false)
			if !ok {
				drop = append(drop, n)
				break
			}
			n.Destination = []byte(dest)
		}
		return ast.WalkContinue, nil
	})
	// Disallowed links keep their text; disallowed images and autolinks
	// become their text.
	for _, n := range unwrap {
		parent := n.Parent()
		for c := n.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, n, c)
			c = next
		}
		parent.RemoveChild(parent, n)
	}
	for _, n := range drop {
		var label []byte
		switch n := n.(type) {
		case *ast.AutoLink:
			label = n.Label(source)
		case *ast.Image:
			label = nodeText(n, source)
		}
		t := ast.NewString(label)
		n.Parent().ReplaceChild(n.Parent(), n, t)
	}
}

// nodeText concatenates the text of n's descendants (an image's alt).
func nodeText(n ast.Node, source []byte) []byte {
	var b []byte
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b = append(b, c.Value(source)...)
		case *ast.String:
			b = append(b, c.Value...)
		}
		return ast.WalkContinue, nil
	})
	return b
}

// safeURL allows absolute http and https URLs with a host, mailto (when
// mailto is true) and in-page fragments, which are rewritten to IDPrefix.
// Relative paths, protocol-relative URLs and every other scheme are refused.
// The URL is checked in the escaped form goldmark writes, which is what
// the sanitizer sees, so the two agree on what survives.
func safeURL(raw string, mailto bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if frag, ok := strings.CutPrefix(raw, "#"); ok {
		if frag == "" || strings.ContainsAny(frag, " \t\n\"'<>\\`") {
			return "", false
		}
		if !strings.HasPrefix(frag, IDPrefix) {
			frag = IDPrefix + frag
		}
		return "#" + frag, true
	}
	u, err := url.Parse(string(util.URLEscape([]byte(raw), true)))
	if err != nil {
		return "", false
	}
	switch u.Scheme {
	case "http", "https":
		return raw, u.Host != ""
	case "mailto":
		return raw, mailto && u.Opaque != ""
	}
	return "", false
}

// codeRenderer highlights fenced code blocks. Blocks without a known or
// allowed language, with a line over MaxLineLen, or after the shared
// deadline, are escaped plain text.
type codeRenderer struct {
	ctx context.Context
}

func (r *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
}

func (r *codeRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	var code bytes.Buffer
	lines := n.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		code.Write(seg.Value(source))
	}
	lang := strings.TrimSpace(string(n.Language(source)))
	out := Plain(code.Bytes())
	if lang != "" && r.ctx.Err() == nil && !longLine(code.Bytes()) {
		// Already in a render slot: format on this goroutine.
		if lexer := safeLexer(pickLexer(lang, "", nil, false), code.Bytes()); lexer != nil && lexerName(lexer) != PlainText {
			if h, err := format(r.ctx, lexer, code.Bytes()); err == nil {
				out = h
			}
		}
	}
	_, _ = w.WriteString("<pre><code>")
	_, _ = w.WriteString(string(out))
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// newPolicy allows exactly what the Markdown renderer emits. It starts
// from an empty policy rather than bluemonday.UGCPolicy because UGC allows
// id, lang, dir and title on every element and allowances cannot be
// removed; the element set is the subset of UGC that goldmark GFM produces.
func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements("p", "br", "hr", "blockquote", "pre", "code",
		"em", "strong", "del", "ul", "li", "table", "thead", "tbody", "tr")
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^user-content-[a-z0-9-]+$`)).
		OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("start").Matching(regexp.MustCompile(`^[0-9]{1,9}$`)).OnElements("ol")
	p.AllowElements("ol")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right)$`)).OnElements("th", "td")
	p.AllowElements("th", "td")

	// Links and images. Relative URLs are allowed here only because the
	// AST pass has already reduced them to "#user-content-…" fragments.
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^` + LinkRel + `$`)).OnElements("a")
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AllowAttrs("title").OnElements("a", "img") // values are re-escaped
	p.AllowAttrs("src").OnElements("img")
	p.AllowAttrs("alt").OnElements("img")

	// Highlighted code.
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^tok-[ksnftcp]$`)).OnElements("span")
	p.AllowElements("span")

	// Task-list checkboxes. Raw HTML is off, so the only inputs are the
	// task list's, which goldmark always renders disabled.
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")

	return p
}
