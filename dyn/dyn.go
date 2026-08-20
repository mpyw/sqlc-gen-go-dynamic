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

// Scoper is implemented by a params type that names its own fields. Generated types do,
// because only the generator knows both spellings: a template writes activeOnly and c.name
// where Go writes ActiveOnly and Name. A type that does not is reflected instead.
type Scoper = render.Scoper

// Statement is a built statement.
type Statement struct {
	SQL  string
	Args []any
}

// Template is a parsed template. It is immutable and safe for concurrent use.
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

// Build renders the template with params, which may be a Scoper, a map, or a struct.
func (t *Template) Build(params any) (Statement, error) {
	res, err := render.Render(t.nodes, params, t.d, t.ev)
	if err != nil {
		return Statement{}, err
	}
	return Statement{SQL: res.SQL, Args: res.Args}, nil
}
