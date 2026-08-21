package lint

import (
	"fmt"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/lexer"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/token"
)

// SelectList reports a directive in the select list of a row-returning query.
//
// sqlc reads the query with every directive stripped, so the columns it reports — and the row
// struct generated from them — are the ones the text has with all branches taken at once. A
// directive that changes the select list therefore produces a row struct that does not describe
// what the query returns. Adding a column is loud, because Scan gets the wrong count. Swapping
// one is silent: `select id, /*%if x*/ name /*%else*/ status /*%end*/` reads to sqlc as
// `name AS status`, so the column count and types match and the wrong column is scanned into the
// field named after the other one. Nothing downstream can catch that, which is why it is refused
// here.
//
// This is the one place SQL shape is consulted, and the check is deliberately narrow. It fires
// only on a statement whose first word is `select`, and only for a directive outside every
// parenthesis that appears before the first `from` outside every parenthesis. A subquery's
// directives are inside parentheses; a CTE, a RETURNING clause and anything that does not begin
// with `select` are not examined at all. Failing to notice is the intended failure mode: a false
// positive here aborts a generate.
func SelectList(template, cmd string, rules bind.Rules) error {
	if cmd != ":one" && cmd != ":many" {
		return nil
	}
	if firstWord(template) != "select" {
		return nil
	}
	lx := lexer.New(template, rules)
	depth := 0
	for {
		tok, err := lx.Next()
		if err != nil || tok.Kind == token.EOF {
			// A template that does not lex is not this check's business; the parser reports it.
			return nil
		}
		switch tok.Kind {
		case token.Text:
			d, from := scanText(tok.Text, depth)
			if from {
				return nil // past the select list
			}
			depth = d
		case token.If, token.For:
			if depth == 0 {
				return fmt.Errorf("directive in the select list: sqlc types the row from the "+
					"query with every directive stripped, so a branch that changes the selected "+
					"columns returns something the generated row struct does not describe — and "+
					"a branch that swaps one column for another does it silently. Keep the "+
					"select list fixed and put the directives after `from`. (%s)", cmd)
			}
		}
	}
}

// scanText tracks the parenthesis depth through a run of text and reports whether it holds the
// `from` that ends the select list, at depth zero.
func scanText(s string, depth int) (int, bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case 'f', 'F':
			if depth == 0 && word(s, i) == "from" {
				return depth, true
			}
		}
	}
	return depth, false
}

// word returns the lowercased identifier starting at i, or "" when i is not its first byte.
func word(s string, i int) string {
	if i > 0 && identByte(s[i-1]) {
		return ""
	}
	j := i
	for j < len(s) && identByte(s[j]) {
		j++
	}
	return strings.ToLower(s[i:j])
}

// firstWord returns the first identifier in s, lowercased, skipping leading space. Comments are
// not skipped: a template that opens with one is not examined, which is the safe direction.
func firstWord(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return word(s, i)
}

func identByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	return false
}
