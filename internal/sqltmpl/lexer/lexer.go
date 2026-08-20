// Package lexer scans a template into tokens.
//
// It accumulates opaque text and stops only at a directive comment or a bind marker. Quotes
// and comments are skipped so that neither can hide one.
package lexer

import (
	"fmt"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/scan"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/token"
)

// Token is one scanned token.
type Token struct {
	Kind token.Kind
	Text string // Text: the run. If/Elseif: the condition. For: the iterable.
	Name string // Bind: the parameter name. For: the loop variable.
	List bool   // Bind: renders as a placeholder list.
}

// Lexer scans a template.
type Lexer struct {
	cur   scan.Cursor
	rules bind.Rules
}

// New creates a Lexer that recognizes the marker spellings the rules allow.
func New(src string, rules bind.Rules) *Lexer {
	return &Lexer{cur: scan.Cursor{Src: src}, rules: rules}
}

// Next returns the next token. The last token is of kind token.EOF.
func (l *Lexer) Next() (Token, error) {
	c := &l.cur
	start := c.I
	for !c.Done() {
		switch {
		case c.AtDollarQuote():
			if err := c.SkipDollarQuote(); err != nil {
				return Token{}, fmt.Errorf("template: %w", err)
			}

		case c.AtQuote():
			if err := c.SkipQuoted(); err != nil {
				return Token{}, fmt.Errorf("template: %w", err)
			}

		case c.AtLineComment(), l.rules.HashComments && c.At() == '#':
			c.SkipLine()

		case c.AtBlockComment():
			at := c.I
			tok, cls, err := l.blockComment()
			if err != nil {
				return Token{}, err
			}
			if cls == plainComment {
				continue // part of the surrounding text run
			}
			// A directive and a dropped comment both end the run. Hand back the text
			// first; the next call re-reads the comment with nothing pending.
			if at > start {
				c.I = at
				return Token{Kind: token.Text, Text: c.Src[start:at]}, nil
			}
			if cls == parserComment {
				start = c.I // the comment is gone; the run resumes after it
				continue
			}
			return tok, nil

		default:
			// A marker cannot follow an operator: `<@ tags` and `@@ x` end in @, and the name
			// after them belongs to the operator, not to a bind.
			if c.I > 0 && bind.OperatorByte(c.Src[c.I-1]) {
				c.I++
				continue
			}
			rest := c.Src[c.I:]
			if reason, bad := l.rules.Malformed(rest); bad {
				return Token{}, fmt.Errorf("template: %s", reason)
			}
			m, ok := l.rules.Recognize(rest)
			if !ok {
				c.I++
				continue
			}
			if c.I > start {
				return Token{Kind: token.Text, Text: c.Src[start:c.I]}, nil
			}
			c.I += m.Len
			return Token{Kind: token.Bind, Name: m.Name, List: m.List}, nil
		}
	}
	if c.I > start {
		return Token{Kind: token.Text, Text: c.Src[start:]}, nil
	}
	return Token{Kind: token.EOF}, nil
}

// commentClass is what a block comment turned out to be.
type commentClass uint8

const (
	plainComment  commentClass = iota // text like any other
	parserComment                     // /*%! ... */, dropped
	directive
)

// blockComment consumes a block comment and classifies it.
func (l *Lexer) blockComment() (Token, commentClass, error) {
	body, err := l.cur.ReadBlockComment()
	if err != nil {
		return Token{}, plainComment, fmt.Errorf("template: %w", err)
	}
	if strings.HasPrefix(body, "%!") {
		return Token{}, parserComment, nil
	}
	if !strings.HasPrefix(body, "%") {
		return Token{}, plainComment, nil
	}

	word, rest := splitWord(strings.TrimSpace(body[1:]))
	switch word {
	case "if", "elseif":
		if rest == "" {
			return Token{}, directive, fmt.Errorf("template: /*%%%s*/ has no condition", word)
		}
		k := token.If
		if word == "elseif" {
			k = token.Elseif
		}
		return Token{Kind: k, Text: rest}, directive, nil
	case "else", "end":
		// Neither takes anything after it, and reading one that does as though it did not is
		// how /*%else if x*/ becomes an unconditional else with the condition dropped.
		if rest != "" {
			return Token{}, directive, fmt.Errorf(
				"template: /*%%%s*/ takes nothing after it, got %q", word, rest)
		}
		if word == "else" {
			return Token{Kind: token.Else}, directive, nil
		}
		return Token{Kind: token.End}, directive, nil
	case "for":
		v, iter, err := forHeader(rest)
		if err != nil {
			return Token{}, directive, err
		}
		return Token{Kind: token.For, Name: v, Text: iter}, directive, nil
	}
	return Token{}, directive, fmt.Errorf("template: unknown directive %q", word)
}

// forHeader splits "x in xs". Everything after "in" is the iterable verbatim, colons
// included, so a ternary or a slice expression survives.
func forHeader(rest string) (string, string, error) {
	v, tail := splitWord(rest)
	kw, iter := splitWord(tail)
	if v == "" || kw != "in" || iter == "" {
		return "", "", fmt.Errorf("template: /*%%for*/ wants \"<var> in <expr>\", got %q", rest)
	}
	return v, iter, nil
}

func splitWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}
