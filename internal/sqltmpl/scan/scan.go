// Package scan walks SQL text past the spans that can hide something.
//
// Both the lexer and the placeholder restorer look for a different alphabet in the same
// text, and both have to skip quoted spans and comments so that neither is fooled by what
// they contain. That skipping is all they share, and it lives here.
//
// SQL is not otherwise examined: no keyword is recognized and no grammar is applied.
package scan

import (
	"fmt"
	"strings"
)

// Cursor is a position in SQL text.
type Cursor struct {
	Src string
	I   int
}

// Done reports whether the cursor is at the end.
func (c *Cursor) Done() bool { return c.I >= len(c.Src) }

// At returns the byte at the cursor, or zero at the end.
func (c *Cursor) At() byte { return c.Peek(0) }

// Peek returns the byte n ahead of the cursor, or zero past the end.
func (c *Cursor) Peek(n int) byte {
	if c.I+n < len(c.Src) {
		return c.Src[c.I+n]
	}
	return 0
}

// AtQuote reports whether a quoted string or identifier starts at the cursor.
func (c *Cursor) AtQuote() bool {
	switch c.At() {
	case '\'', '"', '`':
		return true
	}
	return false
}

// AtDollarQuote reports whether a PostgreSQL dollar-quoted string starts at the cursor. The
// tag between the dollars is an identifier or empty, which is what tells $$…$$ and $tag$…$tag$
// apart from a $1 placeholder or a lone $.
func (c *Cursor) AtDollarQuote() bool {
	_, ok := c.dollarTag()
	return ok
}

// dollarTag returns the opening delimiter at the cursor, "$$" or "$tag$".
func (c *Cursor) dollarTag() (string, bool) {
	if c.At() != '$' {
		return "", false
	}
	i := c.I + 1
	for i < len(c.Src) && isTagByte(c.Src[i], i == c.I+1) {
		i++
	}
	if i >= len(c.Src) || c.Src[i] != '$' {
		return "", false
	}
	return c.Src[c.I : i+1], true
}

func isTagByte(b byte, first bool) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b == '_':
		return true
	case b >= '0' && b <= '9':
		return !first
	}
	return false
}

// SkipDollarQuote consumes the dollar-quoted string at the cursor. Nothing inside one is
// escaped, so it ends at the first repeat of its own delimiter.
func (c *Cursor) SkipDollarQuote() error {
	tag, ok := c.dollarTag()
	if !ok {
		return fmt.Errorf("not a dollar quote")
	}
	c.I += len(tag)
	if j := strings.Index(c.Src[c.I:], tag); j >= 0 {
		c.I += j + len(tag)
		return nil
	}
	c.I = len(c.Src)
	return fmt.Errorf("unterminated dollar-quoted string")
}

// AtLineComment reports whether a -- comment starts at the cursor.
func (c *Cursor) AtLineComment() bool { return c.At() == '-' && c.Peek(1) == '-' }

// AtBlockComment reports whether a /* comment starts at the cursor.
func (c *Cursor) AtBlockComment() bool { return c.At() == '/' && c.Peek(1) == '*' }

// SkipQuoted consumes the quoted span at the cursor. A quote is escaped by doubling it, and
// also by a backslash: MySQL does that by default, and rejecting it would turn a legal query
// into a build failure. In a dialect where a backslash is literal this reads one character
// further than the server would, which can only extend the span, never end it early.
func (c *Cursor) SkipQuoted() error {
	q := c.At()
	c.I++
	for !c.Done() {
		if c.At() == '\\' && c.I+1 < len(c.Src) {
			c.I += 2
			continue
		}
		if c.At() != q {
			c.I++
			continue
		}
		if c.Peek(1) == q {
			c.I += 2
			continue
		}
		c.I++
		return nil
	}
	return fmt.Errorf("unterminated quoted literal")
}

// SkipLine consumes the rest of the line, leaving the newline.
func (c *Cursor) SkipLine() {
	for !c.Done() && c.At() != '\n' {
		c.I++
	}
}

// ReadBlockComment consumes the block comment at the cursor and returns its body. Nesting is
// honored, because PostgreSQL nests block comments.
func (c *Cursor) ReadBlockComment() (string, error) {
	c.I += 2
	start := c.I
	depth := 1
	for !c.Done() {
		switch {
		case c.At() == '/' && c.Peek(1) == '*':
			depth++
			c.I += 2
		case c.At() == '*' && c.Peek(1) == '/':
			depth--
			c.I += 2
			if depth == 0 {
				return c.Src[start : c.I-2], nil
			}
		default:
			c.I++
		}
	}
	return "", fmt.Errorf("unterminated block comment")
}
