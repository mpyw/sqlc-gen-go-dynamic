// Package lexer scans a template into tokens.
//
// It accumulates opaque text and stops only at a directive comment or a bind marker. Quotes
// and comments are tracked so that neither can hide one; SQL is not otherwise examined.
package lexer

import (
	"fmt"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
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
	src   string
	rules bind.Rules
	i     int
}

// New creates a Lexer that recognizes the marker spellings the rules allow.
func New(src string, rules bind.Rules) *Lexer { return &Lexer{src: src, rules: rules} }

// Next returns the next token. The last token is of kind token.EOF.
func (l *Lexer) Next() (Token, error) {
	start := l.i
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			if err := l.skipQuoted(c); err != nil {
				return Token{}, err
			}

		case c == '-' && l.peek(1) == '-':
			l.skipLine()

		case c == '/' && l.peek(1) == '*':
			at := l.i
			tok, cls, err := l.slashStar()
			if err != nil {
				return Token{}, err
			}
			if cls == plainComment {
				continue // part of the surrounding text run
			}
			// A directive and a dropped comment both end the run. Hand back the text
			// first; the next call re-reads the comment with nothing pending.
			if at > start {
				l.i = at
				return Token{Kind: token.Text, Text: l.src[start:at]}, nil
			}
			if cls == parserComment {
				start = l.i // the comment is gone; the run resumes after it
				continue
			}
			return tok, nil

		default:
			if reason, bad := l.rules.Malformed(l.src[l.i:]); bad {
				return Token{}, fmt.Errorf("template: %s", reason)
			}
			m, ok := l.rules.Recognize(l.src[l.i:])
			if !ok {
				l.i++
				continue
			}
			if l.i > start {
				return Token{Kind: token.Text, Text: l.src[start:l.i]}, nil
			}
			l.i += m.Len
			return Token{Kind: token.Bind, Name: m.Name, List: m.List}, nil
		}
	}
	if l.i > start {
		return Token{Kind: token.Text, Text: l.src[start:]}, nil
	}
	return Token{Kind: token.EOF}, nil
}

func (l *Lexer) peek(n int) byte {
	if l.i+n < len(l.src) {
		return l.src[l.i+n]
	}
	return 0
}

// skipQuoted consumes a quoted string or identifier. A quote is escaped by doubling it;
// backslash escapes are not recognized.
func (l *Lexer) skipQuoted(q byte) error {
	l.i++
	for l.i < len(l.src) {
		if l.src[l.i] != q {
			l.i++
			continue
		}
		if l.peek(1) == q {
			l.i += 2
			continue
		}
		l.i++
		return nil
	}
	return fmt.Errorf("template: unterminated quoted literal")
}

func (l *Lexer) skipLine() {
	for l.i < len(l.src) && l.src[l.i] != '\n' {
		l.i++
	}
}

// commentClass is what a block comment turned out to be.
type commentClass uint8

const (
	plainComment  commentClass = iota // text like any other
	parserComment                     // /*%! ... */, dropped
	directive
)

// slashStar consumes a block comment and classifies it.
func (l *Lexer) slashStar() (Token, commentClass, error) {
	body, ok := l.readBlockComment()
	if !ok {
		return Token{}, plainComment, fmt.Errorf("template: unterminated block comment")
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
	case "else":
		return Token{Kind: token.Else}, directive, nil
	case "end":
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

// readBlockComment consumes a block comment and returns its body. Nesting is honored,
// because PostgreSQL nests block comments.
func (l *Lexer) readBlockComment() (string, bool) {
	l.i += 2
	start := l.i
	depth := 1
	for l.i < len(l.src) {
		switch {
		case l.src[l.i] == '/' && l.peek(1) == '*':
			depth++
			l.i += 2
		case l.src[l.i] == '*' && l.peek(1) == '/':
			depth--
			l.i += 2
			if depth == 0 {
				return l.src[start : l.i-2], true
			}
		default:
			l.i++
		}
	}
	return "", false
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
