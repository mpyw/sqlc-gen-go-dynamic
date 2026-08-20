package directive

import (
	"fmt"
	"strconv"
	"strings"
)

// Style says how sqlc spelled the placeholders it substituted, which decides
// whether a "?" in the text is a parameter or just an operator: PostgreSQL's
// jsonb "?" would otherwise be mistaken for one.
type Style uint8

const (
	Numbered   Style = iota // $n, :n, @pn — PostgreSQL, Oracle, SQL Server
	Positional              // ? — MySQL, SQLite
)

type tokenKind uint8

const (
	tokDirective tokenKind = iota
	tokPlaceholder
)

type token struct {
	kind   tokenKind
	text   string // directive body, with the leading "%" stripped
	number int    // placeholder number; zero means "take the next positional slot"
	pos    int
}

// scanner walks the query text looking only for directive comments and
// placeholders. Everything else — including every keyword — stays opaque, the
// same discipline bisql's own lexer follows: this is not a SQL parser, and it
// only needs to know enough about quotes and comments not to be fooled by them.
type scanner struct {
	src   string
	style Style
	i     int
}

func (s *scanner) next() (token, bool) {
	for s.i < len(s.src) {
		c := s.src[s.i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			s.skipQuoted(c)

		case c == '-' && s.peek(1) == '-':
			s.skipLine()

		case c == '/' && s.peek(1) == '*':
			start := s.i
			body := s.readBlockComment()
			// A directive is /*% ... */. A parser comment, /*%! ... */, is bisql's own
			// and carries nothing typing needs.
			if strings.HasPrefix(body, "%") && !strings.HasPrefix(body, "%!") {
				return token{kind: tokDirective, text: body[1:], pos: start}, true
			}

		case s.style == Numbered && c == '$':
			if n, ok := s.readNumberAfter(1); ok {
				return token{kind: tokPlaceholder, number: n, pos: s.i - 1}, true
			}
			s.i++

		case s.style == Numbered && c == ':':
			if n, ok := s.readNumberAfter(1); ok {
				return token{kind: tokPlaceholder, number: n, pos: s.i - 1}, true
			}
			s.i++

		case s.style == Numbered && c == '@' && s.peek(1) == 'p':
			if n, ok := s.readNumberAfter(2); ok {
				return token{kind: tokPlaceholder, number: n, pos: s.i - 1}, true
			}
			s.i++

		case s.style == Positional && c == '?':
			s.i++
			return token{kind: tokPlaceholder, number: 0, pos: s.i - 1}, true

		default:
			s.i++
		}
	}
	return token{}, false
}

func (s *scanner) peek(n int) byte {
	if s.i+n < len(s.src) {
		return s.src[s.i+n]
	}
	return 0
}

// skipQuoted consumes a quoted string or identifier. Quotes are escaped by
// doubling; backslash escapes are not recognized, matching bisql.
func (s *scanner) skipQuoted(q byte) {
	s.i++ // opening quote
	for s.i < len(s.src) {
		if s.src[s.i] != q {
			s.i++
			continue
		}
		if s.peek(1) == q {
			s.i += 2 // an escaped quote
			continue
		}
		s.i++ // closing quote
		return
	}
}

func (s *scanner) skipLine() {
	for s.i < len(s.src) && s.src[s.i] != '\n' {
		s.i++
	}
}

// readBlockComment consumes a block comment and returns its body. Nesting is
// honored because PostgreSQL nests block comments; the other engines do not, but
// a nested opener is vanishingly rare there and treating it as nesting is the
// safer reading.
func (s *scanner) readBlockComment() string {
	s.i += 2 // "/*"
	start := s.i
	depth := 1
	for s.i < len(s.src) {
		switch {
		case s.src[s.i] == '/' && s.peek(1) == '*':
			depth++
			s.i += 2
		case s.src[s.i] == '*' && s.peek(1) == '/':
			depth--
			s.i += 2
			if depth == 0 {
				return s.src[start : s.i-2]
			}
		default:
			s.i++
		}
	}
	return s.src[start:] // unterminated; the body is whatever is left
}

// readNumberAfter reads the digits at offset from the current position and, on
// success, advances past them.
func (s *scanner) readNumberAfter(offset int) (int, bool) {
	j := s.i + offset
	k := j
	for k < len(s.src) && s.src[k] >= '0' && s.src[k] <= '9' {
		k++
	}
	if k == j {
		return 0, false
	}
	n, err := strconv.Atoi(s.src[j:k])
	if err != nil {
		return 0, false
	}
	s.i = k
	return n, true
}

// CheckComments reports directives that sqlc lifted out of the query text.
//
// sqlc treats a block comment starting at column zero as a standalone comment: it
// moves the comment into Query.comments and drops the line from Query.text,
// greedily enough that a directive written flush left takes the SQL beside it
// along. The parameter table survives, so the damage is invisible in the types
// and shows up only as a directive that never arrives — which is exactly the kind
// of silent hole worth failing on. Indenting the directive by a single space
// avoids it entirely.
func CheckComments(comments []string) []error {
	var errs []error
	for _, c := range comments {
		if !strings.HasPrefix(strings.TrimSpace(c), "%") {
			continue
		}
		errs = append(errs, fmt.Errorf(
			"directive /*%s*/ starts at column zero, so sqlc moved it out of the query "+
				"text along with the rest of its line; indent it by at least one space", c))
	}
	return errs
}
