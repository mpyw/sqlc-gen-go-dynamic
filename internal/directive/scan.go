package directive

import (
	"fmt"
	"strconv"
	"strings"
)

// Style is how sqlc spelled the placeholders it substituted. There is one per engine sqlc
// supports, and they have to be told apart rather than accepted together: a bare "?" is an
// operator in PostgreSQL — it tests for a jsonb key — and ":3" is ordinary syntax there too,
// inside an array slice like a[1:3].
type Style uint8

const (
	// Dollar is $n, which sqlc emits for PostgreSQL.
	Dollar Style = iota
	// Question is a bare ?, which sqlc emits for MySQL. Position decides which parameter
	// it is, since it carries no number.
	Question
	// QuestionNumbered is ?n, which sqlc emits for SQLite. The number matters: a parameter
	// used twice appears twice with the same number, which position alone cannot express.
	QuestionNumbered
)

// StyleFor returns the style sqlc uses for an engine, as plugin.Request.settings.engine
// names it.
func StyleFor(engine string) (Style, error) {
	switch engine {
	case "postgresql":
		return Dollar, nil
	case "mysql":
		return Question, nil
	case "sqlite":
		return QuestionNumbered, nil
	}
	return 0, fmt.Errorf("unsupported engine %q", engine)
}

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

		case s.style == Dollar && c == '$':
			if n, ok := s.readNumberAfter(1); ok {
				return token{kind: tokPlaceholder, number: n, pos: s.i - 1}, true
			}
			s.i++

		case s.style == QuestionNumbered && c == '?':
			if n, ok := s.readNumberAfter(1); ok {
				return token{kind: tokPlaceholder, number: n, pos: s.i - 1}, true
			}
			s.i++

		case s.style == Question && c == '?':
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
