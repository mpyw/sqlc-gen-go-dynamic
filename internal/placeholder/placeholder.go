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
	// QuestionNumbered is ?n, for SQLite. The number matters: a parameter used twice
	// appears twice with its own number, which position cannot express.
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
	Number   int    // 1-based, as plugin.Parameter.number
	Name     string // plugin.Parameter.column.name
	Nullable bool   // false for a NOT NULL column bound with sqlc.arg
	List     bool   // plugin.Column.is_sqlc_slice
}

// marker is the spelling Param restores to. The name is always quoted, which is valid for
// every name and required for a dotted one.
func (p Param) marker() string {
	fn := "sqlc.arg"
	switch {
	case p.List:
		fn = "sqlc.slice"
	case p.Nullable:
		fn = "sqlc.narg"
	}
	return fn + "('" + p.Name + "')"
}

// Restore rewrites every placeholder in text back into the marker that produced it.
func Restore(text string, params []Param, style Style) (string, error) {
	byNumber := make(map[int]Param, len(params))
	for _, p := range params {
		byNumber[p.Number] = p
	}

	var (
		b     strings.Builder
		cur   = scan.Cursor{Src: text}
		kept  = 0 // start of the run not yet copied
		seen  = 0 // placeholders passed, for the style that carries no number
		flush = func(to int) { b.WriteString(text[kept:to]) }
	)
	for !cur.Done() {
		switch {
		case cur.AtQuote():
			if err := cur.SkipQuoted(); err != nil {
				return "", fmt.Errorf("placeholder: %w", err)
			}
			continue
		case cur.AtLineComment():
			cur.SkipLine()
			continue
		case cur.AtBlockComment():
			if _, err := cur.ReadBlockComment(); err != nil {
				return "", fmt.Errorf("placeholder: %w", err)
			}
			continue
		}

		at := cur.I
		n, ok := style.read(&cur)
		if !ok {
			cur.I++
			continue
		}
		if n == 0 {
			seen++
			n = seen
		}
		p, ok := byNumber[n]
		if !ok {
			return "", fmt.Errorf("placeholder: %d at offset %d has no parameter", n, at)
		}
		flush(at)
		b.WriteString(p.marker())
		kept = cur.I
	}
	flush(len(text))
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
		return number(cur, 1)
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
