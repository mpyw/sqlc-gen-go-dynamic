// Package query turns one query from sqlc's request into what codegen needs.
//
// The steps are ordered by what each one can know. Layout is checked first, from the
// comments, because a directive sqlc lifted out of the text leaves no other trace. Markers
// are then restored, which yields the canonical template — the text the generated code
// embeds and the renderer reads. That text is parsed once, and typing walks the same tree
// the renderer will.
package query

import (
	"errors"
	"fmt"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/lint"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/placeholder"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/scan"
)

// Param is one entry of sqlc's parameter table, with its Go type already mapped. One type
// rather than one per stage: the number, the name, the nullability and the Go type all come
// from the same Column, and splitting them invites them to disagree.
type Param struct {
	Number   int
	Name     string
	GoType   string
	Explicit bool // the type came from an override, so it is rendered as written
	NotNull  bool
	IsSlice  bool
}

// Input is one query as sqlc reports it.
type Input struct {
	Name     string   // Query.name
	Cmd      string   // Query.cmd, e.g. ":many"
	Text     string   // Query.text, with markers already replaced by placeholders
	Comments []string // Query.comments
	Engine   string   // settings.engine
	Params   []Param
}

// Query is a prepared query.
type Query struct {
	Name     string
	Cmd      string
	Template string     // canonical: markers restored, ready to embed and to render
	Nodes    []ast.Node // parsed once, for both typing and rendering
	Params   *exprtype.Type
	Engine   string
}

// HasDirectives reports whether anything in the template has to be decided per call. A
// template of nothing but text and markers renders the same SQL every time, and is left to be
// emitted as a constant.
func (q *Query) HasDirectives() bool { return hasDirectives(q.Nodes) }

func hasDirectives(ns []ast.Node) bool {
	for _, n := range ns {
		switch n := n.(type) {
		case ast.If:
			return true
		case ast.For:
			return true
		case ast.Bind:
			_ = n
		}
	}
	return false
}

// Prepare validates and prepares a query. Diagnostics from typing are returned alongside the
// result, since each names a variable rather than invalidating the whole query.
func Prepare(in Input) (*Query, []exprtype.Diagnostic, error) {
	if errs := lint.Layout(in.Comments); len(errs) > 0 {
		return nil, nil, errs[0]
	}
	style, err := placeholder.StyleFor(in.Engine)
	if err != nil {
		return nil, nil, err
	}
	tmpl, err := placeholder.Restore(in.Text, restoreParams(in.Params), style)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w%s", in.Name, err, sqliteTail(in.Engine, err))
	}
	nodes, err := parser.Parse(tmpl, bind.RulesFor(in.Engine))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w%s", in.Name, err, sqliteTail(in.Engine, err))
	}
	if err := lint.SelectList(tmpl, in.Cmd, bind.RulesFor(in.Engine)); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", in.Name, err)
	}
	params, diags := exprtype.Infer(nodes, typeParams(in.Params))
	exprtype.NameQuery(params, in.Name)
	return &Query{
		Name:     in.Name,
		Cmd:      in.Cmd,
		Template: tmpl,
		Nodes:    nodes,
		Params:   params,
		Engine:   in.Engine,
	}, diags, nil
}

// sqliteTail explains an unbalanced template on SQLite, where the usual cause is not the
// author's mistake: sqlc's SQLite frontend drops a block comment that ends a statement, so the
// /*%end*/ that closed the last block never arrives — and with several queries in one file the
// dropped tail bleeds into the next query's text. Either half of the comment may survive, so
// both shapes get the explanation.
func sqliteTail(engine string, err error) string {
	if engine != "sqlite" {
		return ""
	}
	if !errors.Is(err, parser.ErrUnclosed) && !errors.Is(err, scan.ErrUnterminatedComment) {
		return ""
	}
	return "; on SQLite a statement cannot end with a directive — sqlc drops a block comment in " +
		"that position, so put the anchor after it (`/*%end*/ and 1 = 1`, an `order by`, or a " +
		"`returning` clause)"
}

func restoreParams(ps []Param) []placeholder.Param {
	out := make([]placeholder.Param, len(ps))
	for i, p := range ps {
		out[i] = placeholder.Param{Name: p.Name, List: p.IsSlice}
	}
	return out
}

func typeParams(ps []Param) []exprtype.SQLParam {
	out := make([]exprtype.SQLParam, len(ps))
	for i, p := range ps {
		out[i] = exprtype.SQLParam{
			Name:     p.Name,
			GoType:   p.GoType,
			Explicit: p.Explicit,
			NotNull:  p.NotNull,
		}
	}
	return out
}
