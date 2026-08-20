// Package dialect generates placeholders for the engines sqlc supports.
package dialect

import "strconv"

// Dialect is an engine's placeholder spelling.
type Dialect struct {
	name        string
	placeholder func(index int) string
}

// Name returns the engine name, as sqlc's settings spell it.
func (d Dialect) Name() string { return d.name }

// Placeholder returns the placeholder for a 1-based argument index.
func (d Dialect) Placeholder(index int) string { return d.placeholder(index) }

// The three engines sqlc supports. SQLite accepts both ? and ?n; ? is what its drivers
// expect, and sqlc's own choice of ?n is a separate convention that never reaches here.
var (
	PostgreSQL = Dialect{"postgresql", func(i int) string { return "$" + strconv.Itoa(i) }}
	MySQL      = Dialect{"mysql", func(int) string { return "?" }}
	SQLite     = Dialect{"sqlite", func(int) string { return "?" }}
)

// For returns the dialect for an engine name.
func For(engine string) (Dialect, bool) {
	switch engine {
	case "postgresql":
		return PostgreSQL, true
	case "mysql":
		return MySQL, true
	case "sqlite":
		return SQLite, true
	}
	return Dialect{}, false
}
