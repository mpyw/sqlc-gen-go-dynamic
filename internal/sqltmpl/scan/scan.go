// Package scan walks SQL text past the spans that can hide something.
//
// Both the lexer and the placeholder restorer look for a different alphabet in the same
// text, and both have to skip quoted spans and comments so that neither is fooled by what
// they contain. That skipping is all they share, and it lives here.
//
// SQL is not otherwise examined: no keyword is recognized and no grammar is applied.
package scan

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnterminatedComment reports a block comment with no end. It is worth telling apart because
// one cause is not the author's mistake: an engine frontend that drops a block comment ending a
// statement can leave half of one behind.
var ErrUnterminatedComment = errors.New("unterminated block comment")

// Cursor is a position in SQL text. The two flags are the whole of what a span means
// differently from one engine to the next.
type Cursor struct {
	Src string
	I   int
	// Backslash marks a dialect where a backslash escapes a quote inside a string literal.
	// MySQL does by default; PostgreSQL under standard_conforming_strings and SQLite do not,
	// and reading one there runs past the real closing quote, which makes the next quote open
	// a span and turns the text between two literals into code.
	Backslash bool
	// Nested marks a dialect where block comments nest, which PostgreSQL does and MySQL and
	// SQLite do not: there `/* a /* b */` is a complete comment.
	Nested bool
	// DollarQuotes marks a dialect with $tag$…$tag$ strings, which is PostgreSQL alone. A
	// MySQL identifier may contain dollars, so `a$x$b` there is a name and not the opening of
	// a span that would swallow whatever followed.
	DollarQuotes bool
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
	if !c.DollarQuotes {
		return false
	}
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
// in some dialects by a backslash as well — see Cursor.Backslash, and PostgreSQL's E'…', where
// a backslash escapes whatever the setting says elsewhere.
func (c *Cursor) SkipQuoted() error {
	q := c.At()
	esc := c.Backslash || c.atEscapeString()
	c.I++
	for !c.Done() {
		if esc && c.At() == '\\' && c.I+1 < len(c.Src) {
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

// atEscapeString reports whether the quote at the cursor opens a PostgreSQL E'…' literal, in
// which a backslash escapes regardless of standard_conforming_strings. The E has to be a word
// of its own, so that a column named `note` before a quote is not read as one.
func (c *Cursor) atEscapeString() bool {
	if c.At() != '\'' || c.I == 0 {
		return false
	}
	if b := c.Src[c.I-1]; b != 'E' && b != 'e' {
		return false
	}
	if c.I == 1 {
		return true
	}
	return !isTagByte(c.Src[c.I-2], false)
}

// SkipLine consumes the rest of the line, leaving the newline.
func (c *Cursor) SkipLine() {
	for !c.Done() && c.At() != '\n' {
		c.I++
	}
}

// ReadBlockComment consumes the block comment at the cursor and returns its body. Nesting is
// honored where the engine does it — see Cursor.Nested; where it does not, `/** a /* b */` is a
// whole comment and treating it as an open one made a legal query fail to build.
func (c *Cursor) ReadBlockComment() (string, error) {
	c.I += 2
	start := c.I
	depth := 1
	for !c.Done() {
		switch {
		case c.Nested && c.At() == '/' && c.Peek(1) == '*':
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
	return "", ErrUnterminatedComment
}
