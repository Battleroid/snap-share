package render

// Red-team check (expected to PASS: the sanitizer held): hostile Markdown
// never yields script, event handlers, styles, forms or non-http(s) URLs.

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRedteamMarkdownPayloads(t *testing.T) {
	payloads := []string{
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](&#106;avascript:alert(1))",
		"[x](java&#x09;script:alert(1))",
		"[x]( javascript:alert(1))",
		"[x](<javascript:alert(1)>)",
		"[x](data:text/html,<script>alert(1)</script>)",
		"[x](vbscript:msgbox(1))",
		"[x](//evil.example/x)",
		"[x](/\\evil.example)",
		"[x](https://a.example \"t\\\" onmouseover=alert(1) x=\\\"\")",
		"[x](https://a.example/\"onmouseover=\"alert(1))",
		"![x](https://a.example/\"onerror=\"alert(1))",
		"![x](javascript:alert(1))",
		"![x](data:image/svg+xml,<svg onload=alert(1)>)",
		"<https://a.example/\"onclick=\"alert(1)>",
		"<javascript:alert(1)>",
		"<mailto:a@b.example?subject=\"><script>alert(1)</script>>",
		"[r]: javascript:alert(1)\n\n[click][r]",
		"[r]: https://a.example \"x\\\" onmouseover=\\\"y\"\n\n[click][r]",
		"<script>alert(1)</script>",
		"<img src=x onerror=alert(1)>",
		"<a href=\"javascript:alert(1)\">x</a>",
		"<iframe src=https://evil.example></iframe>",
		"<form action=/logout method=post><button>x</button></form>",
		"<svg><script>alert(1)</script></svg>",
		"<style>body{display:none}</style>",
		"<div style=\"position:fixed\">x</div>",
		"<input autofocus onfocus=alert(1)>",
		"- [ ] task <input onfocus=alert(1) autofocus>",
		"| a | b |\n|---|---|\n| <script>x</script> | <img src=x onerror=y> |",
		"```html\n<script>alert(1)</script>\n```",
		"```\"><script>alert(1)</script>\n```",
		"```go onload=alert(1)\nx\n```",
		"# heading {#x onclick=alert(1)}",
		"# <img src=x onerror=alert(1)>",
		"[x](#\" onmouseover=\"alert(1))",
		"[x](#%22onmouseover=alert(1))",
		"www.evil.example/<script>alert(1)</script>",
		"https://evil.example/\"><script>alert(1)</script>",
		"<!-- --><script>alert(1)</script><!-- -->",
		"<![CDATA[<script>alert(1)</script>]]>",
		"<a href=\"https://a.example\" hx-get=\"/logout\" hx-trigger=\"load\">x</a>",
		"<div data-hx-post=\"/settings/security/signout-all\" data-hx-trigger=\"load\"></div>",
		"<base href=\"https://evil.example/\">",
		"<meta http-equiv=\"refresh\" content=\"0;url=https://evil.example\">",
		"<object data=x></object><embed src=x>",
		"<math><mi xlink:href=\"javascript:alert(1)\">x</mi></math>",
	}
	for _, p := range payloads {
		out, err := markdown(context.Background(), []byte(p), 0)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		checkHTML(t, p, string(out))
	}
}

// checkHTML tokenizes s and fails on any element, attribute or URL outside
// what a pb-prose Markdown render may contain.
func checkHTML(t *testing.T, input, s string) {
	t.Helper()
	okTags := map[string]bool{"p": true, "br": true, "hr": true, "blockquote": true, "pre": true, "code": true, "em": true,
		"strong": true, "del": true, "ul": true, "ol": true, "li": true, "table": true, "thead": true, "tbody": true, "tr": true,
		"th": true, "td": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "a": true, "img": true,
		"span": true, "input": true}
	okAttrs := map[string]bool{"href": true, "src": true, "alt": true, "title": true, "rel": true, "id": true, "class": true,
		"align": true, "start": true, "type": true, "checked": true, "disabled": true}
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			continue
		}
		tok := z.Token()
		if !okTags[tok.Data] {
			t.Errorf("payload %q: element <%s> in %q", input, tok.Data, s)
		}
		for _, a := range tok.Attr {
			if !okAttrs[a.Key] {
				t.Errorf("payload %q: attribute %s on <%s> in %q", input, a.Key, tok.Data, s)
			}
			v := strings.ToLower(strings.TrimSpace(a.Val))
			switch a.Key {
			case "href", "src":
				if !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "mailto:") && !strings.HasPrefix(v, "#user-content-") {
					t.Errorf("payload %q: %s=%q", input, a.Key, a.Val)
				}
			case "id":
				if !strings.HasPrefix(v, "user-content-") {
					t.Errorf("payload %q: id=%q", input, a.Val)
				}
			case "type":
				if v != "checkbox" {
					t.Errorf("payload %q: type=%q", input, a.Val)
				}
			}
		}
		if tok.Data == "input" {
			dis := false
			for _, a := range tok.Attr {
				dis = dis || a.Key == "disabled"
			}
			if !dis {
				t.Errorf("payload %q: enabled <input> in %q", input, s)
			}
		}
	}
}

func TestRedteamHighlightEscapes(t *testing.T) {
	src := []byte("<script>alert(1)</script>\n\"'><img src=x onerror=alert(1)>\n</span><span class=\"x\" onclick=y>")
	for _, lang := range []string{"html", "go", "javascript", "markdown", "xml", "plaintext", "diff", "php", "bash"} {
		out, _, err := Highlight(context.Background(), src, lang, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		z := html.NewTokenizer(strings.NewReader(string(out)))
		for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
			if tt == html.TextToken || tt == html.EndTagToken && z.Token().Data == "span" {
				continue
			}
			tok := z.Token()
			if tok.Data != "span" || len(tok.Attr) != 1 || tok.Attr[0].Key != "class" || !strings.HasPrefix(tok.Attr[0].Val, "tok-") {
				t.Errorf("%s: unexpected token %q", lang, tok.String())
			}
		}
	}
}
