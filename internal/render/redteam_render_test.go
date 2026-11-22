package render

// Red-team proof of concept (not a product test). See the report in the
// redteam commit message. These tests FAIL while the bug exists.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// redteamSize lets the input be scaled up to the real cap
// (REDTEAM_RENDER_BYTES=524288) to measure the full cost.
func redteamSize(def int) int {
	if v, err := strconv.Atoi(os.Getenv("REDTEAM_RENDER_BYTES")); err == nil && v > 0 {
		return v
	}
	return def
}

// TestRedteamDelegatingLexerIgnoresDeadline: Highlight promises to stop at
// the context deadline (DefaultTimeout at paste creation). Delegating
// lexers (PHP/PHTML, ERB, Svelte, Go templates, YAML+Jinja) tokenise the
// whole input eagerly inside lexer.Tokenise, before highlight() ever checks
// the context, and the PHP comment rule `/\*.*?\*/` (DotAll) rescans to the
// end of the input at every unterminated "/*", which is quadratic.
func TestRedteamDelegatingLexerIgnoresDeadline(t *testing.T) {
	n := redteamSize(40 << 10)
	src := []byte("<?php " + strings.Repeat("/* ", n/3))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, used, err := Highlight(ctx, src, "phtml", "", 0)
	d := time.Since(start)
	t.Logf("input %d bytes, lexer %s, err %v, took %s (deadline 500ms)", len(src), used, err, d)
	if d > 2*time.Second {
		t.Errorf("Highlight ran %s past a 500ms deadline: the deadline is not enforced for delegating lexers", d)
	}
}

// TestRedteamMarkdownFenceIgnoresDeadline: the same through a Markdown
// fenced block, which shares one DefaultTimeout for the document.
func TestRedteamMarkdownFenceIgnoresDeadline(t *testing.T) {
	n := redteamSize(40 << 10)
	src := []byte("```phtml\n<?php " + strings.Repeat("/* ", n/3) + "\n```\n")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := markdown(ctx, src, 0)
	d := time.Since(start)
	t.Logf("input %d bytes, err %v, took %s (deadline 500ms)", len(src), err, d)
	if d > 2*time.Second {
		t.Errorf("Markdown ran %s past a 500ms deadline", d)
	}
}
