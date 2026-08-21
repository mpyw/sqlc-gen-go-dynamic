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
	// and for a placeholder the author wrote as a bare ?, so both have to be read. A bare one
	// takes one more than the highest number so far, which is what SQLite itself does.
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

// Param is one entry of sqlc's parameter table, in the order sqlc reports it.
//
// plugin.Parameter.number is deliberately not part of this: it is sqlc's own analysis number,
// not the placeholder's position. For `select … where status = @st … limit ?` sqlc numbers the
// LIMIT placeholder 1 and @st 3, while the text puts @st first — and sqlc's own generated code
// passes the arguments in the order it reports the parameters, not in number order. So position
// in this slice is what a placeholder's number means.
type Param struct {
	Name string // plugin.Parameter.column.name
	List bool   // plugin.Column.is_sqlc_slice
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
//
// The nth argument a driver receives binds the placeholder it numbers n, and sqlc emits the
// arguments in the order it reports the parameters. So the placeholder numbered n is params[n-1]
// — see Param for why its number field is not what decides that.
func Restore(text string, params []Param, style Style) (string, error) {
	var (
		b   strings.Builder
		cur = scan.Cursor{
			Src:          text,
			Backslash:    style == Question,
			Nested:       style == Dollar,
			DollarQuotes: style == Dollar,
		}
		kept = 0 // start of the run not yet copied
		// slice holds the name sqlc's own marker comment carries, for the placeholder that
		// follows it.
		slice = ""
		// next is the highest number assigned so far. A placeholder that carries none takes
		// one more, which is how both SQLite and a bare-? driver read it.
		next     = 0
		restored = make([]bool, len(params))
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
			if name, ok := strings.CutPrefix(body, "SLICE:"); ok {
				slice = name
				flush(at)
				kept = cur.I
			}
			continue
		}

		at := cur.I
		n, numbered, ok := style.read(&cur)
		if !ok {
			cur.I++
			continue
		}
		if name := slice; name != "" && !numbered {
			slice = ""
			// The name sqlc's own marker carries is the only reliable identification when the
			// same slice is bound twice: SQLite gives the second occurrence no number of its
			// own, so counting placeholders took the next parameter instead — a different one,
			// restored into the wrong place, with every consistency check still satisfied.
			// A first occurrence consumes a number; a repeat does not.
			switch i, found := indexOf(params, name); {
			case next < len(params) && params[next].Name == name:
				next++
				restored[next-1] = true
			case found:
				restored[i] = true
			default:
				return "", fmt.Errorf("placeholder: the text marks a slice parameter %q that "+
					"sqlc did not report, so the two disagree about the query", name)
			}
			flush(at)
			b.WriteString(Param{Name: name, List: true}.marker())
			kept = cur.I
			continue
		}
		slice = ""
		if !numbered {
			n = next + 1
		}
		if n > next {
			next = n
		}
		if n < 1 || n > len(params) {
			return "", fmt.Errorf("placeholder: the text has a placeholder numbered %d but sqlc "+
				"reported %d parameter(s), so the two disagree about the query", n, len(params))
		}
		p := params[n-1]
		if p.Name == "" {
			return "", fmt.Errorf("placeholder: parameter %d has no name, so nothing can bind "+
				"it; name it with @name or sqlc.arg(name)", n)
		}
		restored[n-1] = true
		flush(at)
		b.WriteString(p.marker())
		kept = cur.I
	}
	flush(len(text))

	// A parameter sqlc reported but whose placeholder was never found would leave the template
	// with a value nothing binds and the generated API with a field nothing reads. The usual
	// cause is a placeholder the author wrote literally rather than naming.
	for i, p := range params {
		if !restored[i] {
			return "", fmt.Errorf("placeholder: nothing in the text binds parameter %d (%s); "+
				"a template has to name every parameter, so write sqlc.arg('%s') where the "+
				"query has a bare placeholder", i+1, p.Name, p.Name)
		}
	}
	return b.String(), nil
}

// indexOf finds a parameter by name.
func indexOf(params []Param, name string) (int, bool) {
	for i, p := range params {
		if p.Name == name {
			return i, true
		}
	}
	return 0, false
}

// read consumes the placeholder at the cursor. It reports whether there was one, and whether it
// carried a number of its own; without one the caller assigns it.
func (s Style) read(cur *scan.Cursor) (n int, numbered, ok bool) {
	switch s {
	case Dollar:
		if cur.At() != '$' {
			return 0, false, false
		}
		n, numbered = number(cur, 1)
		return n, numbered, numbered
	case QuestionNumbered:
		if cur.At() != '?' {
			return 0, false, false
		}
		if n, ok := number(cur, 1); ok {
			return n, true, true
		}
		cur.I++
		return 0, false, true
	case Question:
		if cur.At() != '?' {
			return 0, false, false
		}
		cur.I++
		return 0, false, true
	}
	return 0, false, false
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
