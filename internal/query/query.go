// Package query turns one query from sqlc's request into what codegen needs.
//
// The steps are ordered by what each one can know. Layout is checked first, from the
// comments, because a directive sqlc lifted out of the text leaves no other trace. Markers
// are then restored, which yields the canonical template — the text the generated code
// embeds and the renderer reads. That text is parsed once, and typing walks the same tree
// the renderer will.
package query

import (
	"fmt"
	"sort"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/lint"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/placeholder"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
)

// Param is one entry of sqlc's parameter table, with its Go type already mapped. One type
// rather than one per stage: the number, the name, the nullability and the Go type all come
// from the same Column, and splitting them invites them to disagree.
type Param struct {
	Number   int
	Name     string
	GoType   string
	Import   string // the package GoType needs, empty for a builtin
	Explicit bool   // the type came from an override, so nothing is added to it
	NotNull  bool
	IsSlice  bool
	Nullable bool // written as sqlc.narg; indistinguishable from a nullable column in the request
}

// Column is one result column. Embed names a table when the column stands for the whole of
// it, which is what sqlc.embed reports; sqlc has already expanded the call into the column
// list by then, so the name is all that is left of it.
type Column struct {
	Name     string
	GoType   string
	Import   string // the package GoType needs, empty for a builtin
	Explicit bool   // the type came from an override, so nothing is added to it
	NotNull  bool
	Embed    string
}

// Input is one query as sqlc reports it.
type Input struct {
	Name     string   // Query.name
	Cmd      string   // Query.cmd, e.g. ":many"
	Text     string   // Query.text, with markers already replaced by placeholders
	Comments []string // Query.comments
	Engine   string   // settings.engine
	Params   []Param
	Row      []Column
}

// Query is a prepared query.
type Query struct {
	Name     string
	Cmd      string
	Template string     // canonical: markers restored, ready to embed and to render
	Nodes    []ast.Node // parsed once, for both typing and rendering
	Params   *exprtype.Type
	Row      []Column
	Engine   string

	// Imports are the packages this query's types need, deduplicated.
	Imports []string
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
		return nil, nil, fmt.Errorf("%s: %w", in.Name, err)
	}
	nodes, err := parser.Parse(tmpl, bind.RulesFor(in.Engine))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", in.Name, err)
	}
	params, diags := exprtype.Infer(nodes, typeParams(in.Params))
	exprtype.NameQuery(params, in.Name)
	return &Query{
		Imports:  imports(in),
		Name:     in.Name,
		Cmd:      in.Cmd,
		Template: tmpl,
		Nodes:    nodes,
		Params:   params,
		Row:      in.Row,
		Engine:   in.Engine,
	}, diags, nil
}

// imports collects the packages the query's types need. exprtype carries only type names, so
// the paths have to travel beside them.
func imports(in Input) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range in.Params {
		add(p.Import)
	}
	for _, c := range in.Row {
		add(c.Import)
	}
	sort.Strings(out)
	return out
}

func restoreParams(ps []Param) []placeholder.Param {
	out := make([]placeholder.Param, len(ps))
	for i, p := range ps {
		out[i] = placeholder.Param{Number: p.Number, Name: p.Name, Nullable: p.Nullable, List: p.IsSlice}
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
