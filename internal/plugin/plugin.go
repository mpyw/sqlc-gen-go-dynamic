// Package plugin implements sqlc's process-plugin protocol over JSON.
//
// sqlc marshals the request with protojson when a codegen entry sets format: json, and reads
// the response the same way. protojson uses the proto field names and emits unpopulated
// fields, so plain structs with matching json tags are enough — which is why there is no
// protobuf dependency here. Bytes are base64 in that encoding, and encoding/json does that
// for a []byte on its own.
//
// The WASM protocol is protobuf only, so a WASM build needs the generated types. That is a
// separate step; a process plugin is what a local project uses anyway.
package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gen"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gotype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/query"
)

// Request is the part of sqlc's GenerateRequest this reads.
type Request struct {
	Settings      Settings `json:"settings"`
	Queries       []Query  `json:"queries"`
	PluginOptions []byte   `json:"plugin_options"`
}

type Settings struct {
	Engine  string  `json:"engine"`
	Codegen Codegen `json:"codegen"`
}

type Codegen struct {
	Out string `json:"out"`
	// Options is a bytes field carrying the codegen entry's options as JSON text, which
	// protojson renders as base64; encoding/json decodes that for a []byte.
	Options []byte `json:"options"`
}

type Query struct {
	Text     string      `json:"text"`
	Name     string      `json:"name"`
	Cmd      string      `json:"cmd"`
	Columns  []Column    `json:"columns"`
	Params   []Parameter `json:"params"`
	Comments []string    `json:"comments"`
}

type Parameter struct {
	Number int    `json:"number"`
	Column Column `json:"column"`
}

type Column struct {
	Name        string      `json:"name"`
	NotNull     bool        `json:"not_null"`
	IsArray     bool        `json:"is_array"`
	ArrayDims   int         `json:"array_dims"`
	IsSqlcSlice bool        `json:"is_sqlc_slice"`
	Type        Identifier  `json:"type"`
	EmbedTable  *Identifier `json:"embed_table"`
}

type Identifier struct {
	Name string `json:"name"`
}

// Options are this plugin's own settings, from the codegen entry. The names match sqlc's own
// Go codegen where they mean the same thing, so a project switching over does not rewrite
// them.
type Options struct {
	Package    string           `json:"package"`
	SQLPackage string           `json:"sql_package"`
	Filename   string           `json:"filename"`
	Runtime    string           `json:"runtime"`
	Overrides  gotype.Overrides `json:"overrides"`
}

// Response is sqlc's GenerateResponse.
type Response struct {
	Files []File `json:"files"`
}

type File struct {
	Name     string `json:"name"`
	Contents []byte `json:"contents"`
}

// Run reads a request, generates, and writes the response.
func Run(r io.Reader, w io.Writer) error {
	var req Request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return fmt.Errorf("plugin: reading request: %w", err)
	}
	resp, err := Generate(req)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(resp)
}

// Generate turns a request into a response.
func Generate(req Request) (Response, error) {
	opts := Options{Filename: "queries.gen.go"}
	if len(req.Settings.Codegen.Options) > 0 {
		if err := json.Unmarshal(req.Settings.Codegen.Options, &opts); err != nil {
			return Response{}, fmt.Errorf("plugin: reading options: %w", err)
		}
	}
	if opts.Package == "" {
		return Response{}, fmt.Errorf("plugin: options.package is required")
	}

	queries := make([]*query.Query, 0, len(req.Queries))
	for _, q := range sortedQueries(req.Queries) {
		in, err := input(req.Settings.Engine, opts, q)
		if err != nil {
			return Response{}, err
		}
		prepared, diags, err := query.Prepare(in)
		if err != nil {
			return Response{}, err
		}
		if len(diags) > 0 {
			return Response{}, fmt.Errorf("%s: %s", q.Name, diags[0])
		}
		queries = append(queries, prepared)
	}

	src, err := gen.File(gen.Options{
		Package:    opts.Package,
		SQLPackage: opts.SQLPackage,
		Runtime:    opts.Runtime,
	}, queries)
	if err != nil {
		return Response{}, err
	}
	return Response{Files: []File{{Name: opts.Filename, Contents: src}}}, nil
}

// input converts one query, resolving every Go type up front so that an unmapped one is
// reported against the query that needs it.
func input(engine string, opts Options, q Query) (query.Input, error) {
	in := query.Input{
		Name:     q.Name,
		Cmd:      q.Cmd,
		Text:     q.Text,
		Comments: q.Comments,
		Engine:   engine,
	}
	for _, p := range q.Params {
		t, err := gotype.For(gotype.Request{
			Engine:     engine,
			SQLPackage: opts.SQLPackage,
			Name:       p.Column.Type.Name,
			NotNull:    p.Column.NotNull,
			IsArray:    p.Column.IsArray,
			ArrayDims:  p.Column.ArrayDims,
			Overrides:  opts.Overrides,
		})
		if err != nil {
			return query.Input{}, fmt.Errorf("%s: parameter %s: %w", q.Name, p.Column.Name, err)
		}
		in.Params = append(in.Params, query.Param{
			Number:   p.Number,
			Name:     p.Column.Name,
			GoType:   t.Name,
			Import:   t.Import,
			Explicit: t.Explicit,
			NotNull:  p.Column.NotNull,
			IsSlice:  p.Column.IsSqlcSlice,
			// sqlc reports the same not_null for sqlc.narg and for a nullable column, so the
			// marker restored is the plain one; the nullable Go type comes from NotNull either
			// way.
		})
	}
	for _, c := range q.Columns {
		col := query.Column{Name: c.Name, NotNull: c.NotNull}
		if c.EmbedTable != nil && c.EmbedTable.Name != "" {
			col.Embed = c.EmbedTable.Name
			in.Row = append(in.Row, col)
			continue
		}
		t, err := gotype.For(gotype.Request{
			Engine:     engine,
			SQLPackage: opts.SQLPackage,
			Name:       c.Type.Name,
			NotNull:    c.NotNull,
			IsArray:    c.IsArray,
			ArrayDims:  c.ArrayDims,
			Overrides:  opts.Overrides,
		})
		if err != nil {
			return query.Input{}, fmt.Errorf("%s: column %s: %w", q.Name, c.Name, err)
		}
		col.GoType, col.Import, col.Explicit = t.Name, t.Import, t.Explicit
		in.Row = append(in.Row, col)
	}
	return in, nil
}

// sortedQueries orders queries by name so the emitted file does not depend on map iteration
// or on the order sqlc happened to walk the files in.
func sortedQueries(qs []Query) []Query {
	out := append([]Query(nil), qs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
