// Package placeholder restores the parameter markers in the SQL sqlc hands back.
//
// sqlc returns the query body with each marker replaced by the placeholder it assigned.
// Restoring the names undoes that, and the result is the canonical template: the text the
// generated code embeds, the text the renderer reads, and the text typing walks. One text
// and one parser, so build time and run time cannot disagree about what a template means.
package placeholder

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/scan"
)

// Style is how sqlc spelled the placeholders. There is one per engine, and they cannot be
// accepted together: a bare ? is a jsonb operator in PostgreSQL, and :3 is ordinary syntax
// there inside an array slice like a[1:3].
type Style uint8

const (
	// Dollar is $n, for PostgreSQL.
	Dollar Style = iota
	// Question is a bare ?, for MySQL. It carries no number, so position decides.
	Question
	// QuestionNumbered is ?n, for SQLite — but sqlc emits a bare ? there too, for a slice
	// and for a parameter it did not number, so both have to be read. A bare one takes the
	// next parameter in order of appearance, which is the order sqlc numbers them in.
	QuestionNumbered
)

// StyleFor returns the style sqlc uses for an engine.
func StyleFor(engine string) (Style, error) {
	switch engine {
	case "postgresql":
		return Dollar, nil
	case "mysql":
		return Question, nil
	case "sqlite":
		return QuestionNumbered, nil
	}
	return 0, fmt.Errorf("placeholder: unsupported engine %q", engine)
}

// Param is one entry of sqlc's parameter table.
type Param struct {
	Number int    // 1-based, as plugin.Parameter.number
	Name   string // plugin.Parameter.column.name
	List   bool   // plugin.Column.is_sqlc_slice
}

// marker is the spelling Param restores to. The name is always quoted, which is valid for every
// name and required for a dotted one.
//
// A nullable parameter comes back as sqlc.arg rather than sqlc.narg. The request cannot tell the
// two apart — not_null is false for both — and the distinction changes nothing here: nullability
// reaches the generated types through the parameter table, not through the marker.
func (p Param) marker() string {
	if p.List {
		return "sqlc.slice('" + p.Name + "')"
	}
	return "sqlc.arg('" + p.Name + "')"
}

// Restore rewrites every placeholder in text back into the marker that produced it.
func Restore(text string, params []Param, style Style) (string, error) {
	byNumber := make(map[int]Param, len(params))
	for _, p := range params {
		byNumber[p.Number] = p
	}

	var (
		b        strings.Builder
		cur      = scan.Cursor{Src: text}
		kept     = 0 // start of the run not yet copied
		nth      = 0 // placeholders passed, in order; what an occurrence with no number takes
		restored = map[int]bool{}
		flush    = func(to int) { b.WriteString(text[kept:to]) }
	)
	for !cur.Done() {
		switch {
		case cur.AtDollarQuote():
			if err := cur.SkipDollarQuote(); err != nil {
				return "", fmt.Errorf("placeholder: %w", err)
			}
			continue
		case cur.AtQuote():
			if err := cur.SkipQuoted(); err != nil {
				return "", fmt.Errorf("placeholder: %w", err)
			}
			continue
		case cur.AtLineComment():
			cur.SkipLine()
			continue
		case style == Question && cur.At() == '#':
			// MySQL's line comment. sqlc treats the rest of the line as a comment, so a
			// placeholder inside one is not one.
			cur.SkipLine()
			continue
		case cur.AtBlockComment():
			at := cur.I
			body, err := cur.ReadBlockComment()
			if err != nil {
				return "", fmt.Errorf("placeholder: %w", err)
			}
			// sqlc marks a slice parameter with a comment its own renderer consumes. This
			// renderer expands the marker instead, so the comment is an artifact of a
			// mechanism that no longer applies and would otherwise reach the server.
			if strings.HasPrefix(body, "SLICE:") {
				flush(at)
				kept = cur.I
			}
			continue
		}

		at := cur.I
		n, ok := style.read(&cur)
		if !ok {
			cur.I++
			continue
		}
		nth++
		if n == 0 {
			// sqlc numbers parameters in order of appearance, so an occurrence that carries no
			// number is the nth parameter.
			n = nth
		}
		p, ok := byNumber[n]
		if !ok {
			return "", fmt.Errorf("placeholder: the text has a placeholder numbered %d but sqlc "+
				"reported %d parameter(s), so the two disagree about the query", n, len(params))
		}
		if p.Name == "" {
			return "", fmt.Errorf("placeholder: parameter %d has no name, so nothing can bind "+
				"it; name it with @name or sqlc.arg(name)", n)
		}
		restored[n] = true
		flush(at)
		b.WriteString(p.marker())
		kept = cur.I
	}
	flush(len(text))

	// A parameter sqlc reported but whose placeholder was never found would leave the template
	// with a value nothing binds and the generated API with a field nothing reads.
	for _, p := range params {
		if !restored[p.Number] {
			return "", fmt.Errorf("placeholder: parameter %d (%s) has no placeholder in the "+
				"text, so it cannot be restored", p.Number, p.Name)
		}
	}
	return b.String(), nil
}

// read consumes the placeholder at the cursor and returns its number, or zero when the style
// carries none. It reports false when there is no placeholder there.
func (s Style) read(cur *scan.Cursor) (int, bool) {
	switch s {
	case Dollar:
		if cur.At() != '$' {
			return 0, false
		}
		return number(cur, 1)
	case QuestionNumbered:
		if cur.At() != '?' {
			return 0, false
		}
		if n, ok := number(cur, 1); ok {
			return n, true
		}
		cur.I++
		return 0, true // a bare one: the caller assigns it the next in order
	case Question:
		if cur.At() != '?' {
			return 0, false
		}
		cur.I++
		return 0, true
	}
	return 0, false
}

// number reads the digits at offset from the cursor and, on success, advances past them.
func number(cur *scan.Cursor, offset int) (int, bool) {
	j := cur.I + offset
	k := j
	for k < len(cur.Src) && cur.Src[k] >= '0' && cur.Src[k] <= '9' {
		k++
	}
	if k == j {
		return 0, false
	}
	n, err := strconv.Atoi(cur.Src[j:k])
	if err != nil {
		return 0, false
	}
	cur.I = k
	return n, true
}
