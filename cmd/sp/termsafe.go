package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Terminal safety. A secret's content and a server's error sentences are
// chosen by someone else; written raw to a terminal, control sequences in
// them can set the clipboard (OSC 52), forge links (OSC 8), rewrite what was
// printed above or the window title, or fake sp's own messages.

// termEscape makes b safe to write to a terminal: C0 control characters
// other than \n and \t, DEL and C1 control characters become caret
// notation (ESC is ^[, DEL ^?, U+009B is ^[[, its 7-bit equivalent), and
// bytes that are not valid UTF-8 become \xNN. It reports whether anything
// changed. Printable text, including non-ASCII, is unchanged.
func termEscape(b []byte) ([]byte, bool) {
	var out []byte
	changed := false
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		var esc string
		switch {
		case r == utf8.RuneError && size <= 1:
			esc = fmt.Sprintf(`\x%02x`, b[i])
		case r == '\n' || r == '\t':
		case r < 0x20:
			esc = "^" + string(r+0x40)
		case r == 0x7f:
			esc = "^?"
		case r >= 0x80 && r <= 0x9f:
			esc = "^[" + string(r-0x40)
		}
		if esc != "" {
			if !changed {
				out = append(make([]byte, 0, len(b)+16), b[:i]...)
				changed = true
			}
			out = append(out, esc...)
		} else if changed {
			out = append(out, b[i:i+size]...)
		}
		i += size
	}
	if !changed {
		return b, false
	}
	return out, true
}

// termString is termEscape for strings (error messages printed by main).
func termString(s string) string {
	b, changed := termEscape([]byte(s))
	if !changed {
		return s
	}
	return string(b)
}

// maxServerText caps sentences a server sends (error messages).
const maxServerText = 300

// serverText cleans a sentence from a server for printing: control
// characters (C0, DEL, C1, \n and \t included) and invalid UTF-8 are
// dropped and it is cut to maxServerText characters.
func serverText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		if n == maxServerText {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
