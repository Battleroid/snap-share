package render

import (
	"bytes"
)

// Limits checkMarkdown enforces. goldmark is quadratic in each of these
// (measured at 128 KiB: a line of 64k ">" took 8 s; a 2000-column table
// with one-cell rows, which goldmark pads to full width, 111 s).
const (
	// maxMarkdownDepth is the deepest blockquote/list nesting a line may
	// open.
	maxMarkdownDepth = 32
	// maxTableCells bounds the cells of all tables: columns of the
	// delimiter row times the rows up to the next blank line.
	maxTableCells = 50_000
)

// checkMarkdown is a linear pre-scan that refuses (ErrTooComplex) Markdown
// that is known to be slow to render: lines over MaxLineLen, deep container
// nesting on a line, and very large tables.
func checkMarkdown(src []byte) error {
	if longLine(src) {
		return ErrTooComplex
	}
	cells := 0
	tableCols := 0 // columns of the table being counted, 0 outside one
	var prev []byte
	for rest := src; len(rest) > 0; {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		depth, body := containers(line)
		if depth > maxMarkdownDepth {
			return ErrTooComplex
		}
		switch {
		case len(bytes.TrimSpace(body)) == 0:
			tableCols = 0
		case tableCols > 0:
			cells += tableCols
		default:
			// GFM tables need a pipe in the header or delimiter row.
			if n := delimiterRow(body); n > 0 && (bytes.IndexByte(body, '|') >= 0 || bytes.IndexByte(prev, '|') >= 0) {
				tableCols = n
				cells += 2 * n // header and delimiter rows
			}
		}
		prev = body
		if cells > maxTableCells {
			return ErrTooComplex
		}
	}
	return nil
}

// containers counts the blockquote markers and list item markers that
// open a line and returns the rest.
func containers(line []byte) (int, []byte) {
	depth := 0
	i := 0
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			return depth, line[i:]
		}
		switch c := line[i]; {
		case c == '>':
			i++
		case c == '-' || c == '+' || c == '*':
			if i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t' {
				return depth, line[i:]
			}
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(line) && j-i < 10 && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j >= len(line) || line[j] != '.' && line[j] != ')' ||
				j+1 < len(line) && line[j+1] != ' ' && line[j+1] != '\t' {
				return depth, line[i:]
			}
			i = j + 1
		default:
			return depth, line[i:]
		}
		depth++
	}
}

// delimiterRow returns the number of columns if line is a GFM table
// delimiter row (| :--- | ---: |), else 0.
func delimiterRow(line []byte) int {
	line = bytes.TrimSpace(line)
	line = bytes.TrimPrefix(line, []byte("|"))
	line = bytes.TrimSuffix(line, []byte("|"))
	if len(line) == 0 {
		return 0
	}
	cols := 0
	for cell := range bytes.SplitSeq(line, []byte("|")) {
		cell = bytes.TrimSpace(cell)
		cell = bytes.TrimPrefix(cell, []byte(":"))
		cell = bytes.TrimSuffix(cell, []byte(":"))
		if len(cell) == 0 || len(bytes.Trim(cell, "-")) != 0 {
			return 0
		}
		cols++
	}
	return cols
}
