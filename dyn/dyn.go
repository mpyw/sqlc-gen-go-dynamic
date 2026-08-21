// Package dyn is the runtime the generated code calls.
//
// A generated query holds its template as a string constant and builds SQL from it per call.
// The template is the text sqlc analyzed, with the markers it replaced restored, so what runs
// is what sqlc checked.
package dyn

import (
	"fmt"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprlang"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/render"
)

// Scoper is implemented by a params type that names its own fields, which spares the reflection
// the other kinds of params need. Generated types implement it.
//
// Returning nil is the same as returning an empty map: every condition against it is false.
type Scoper interface {
	TemplateScope() map[string]any
}

// Statement is a built statement.
type Statement struct {
	SQL  string
	Args []any
}

// Template is a parsed template. Parse does not modify it afterwards and Build is safe to call
// concurrently from many goroutines; it caches compiled expressions internally, which is why the
// type is a pointer.
type Template struct {
	nodes []ast.Node
	d     dialect.Dialect
	ev    *exprlang.Evaluator
}

// Parse parses a template for an engine, as sqlc's settings name it.
func Parse(src, engine string) (*Template, error) {
	d, ok := dialect.For(engine)
	if !ok {
		return nil, fmt.Errorf("dyn: unsupported engine %q", engine)
	}
	nodes, err := parser.Parse(src, bind.RulesFor(engine))
	if err != nil {
		return nil, err
	}
	return &Template{nodes: nodes, d: d, ev: &exprlang.Evaluator{}}, nil
}

// MustParse is Parse for a template known at build time, which a generated one is.
func MustParse(src, engine string) *Template {
	t, err := Parse(src, engine)
	if err != nil {
		panic(err)
	}
	return t
}

// Build renders the template with params, which may be a Scoper, a string-keyed map, a struct,
// or a pointer to one — or nil, for a template that names nothing.
//
// Three things about how a name resolves are worth knowing before writing a template:
//
//   - Case and underscores carry no meaning. UserID, userId and user_id are one name, at every
//     level, and two fields that differ only that way are an error rather than a coin toss.
//   - A name nothing supplies is nil, not an error, so a condition on it is false. That is what
//     makes an absent parameter a skipped branch — and what makes a typo a branch that silently
//     never fires.
//   - An empty list renders as null. `in (null)` matches nothing, which is what an empty IN
//     means; `not in (null)` also matches nothing, which is not what an empty NOT IN means, so
//     an exclusion list needs a guard of its own.
//
// params is read, not copied: the arguments in the returned statement may point into it, so
// mutating it afterwards changes what a later Exec sends.
func (t *Template) Build(params any) (Statement, error) {
	res, err := render.Render(t.nodes, params, t.d, t.ev)
	if err != nil {
		return Statement{}, err
	}
	return Statement{SQL: res.SQL, Args: res.Args}, nil
}
