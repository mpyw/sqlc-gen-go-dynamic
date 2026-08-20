package golang

import (
	"fmt"
	"strings"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/opts"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/query"
)

// RuntimeImport is the package the generated code calls to render a dynamic query.
const RuntimeImport = "github.com/mpyw/sqlc-gen-go-dynamic/dyn"

// dynamic is what a query with directives needs beyond what sqlc reports. A query without
// them has none of it and is emitted exactly as sqlc's own Go codegen would emit it.
type dynamic struct {
	Engine     string // as sqlc's settings name it, for the runtime to read the markers the same way
	Template   string // the canonical template: sqlc's text with its markers restored
	ParamsType string // the params struct the method takes
	Decls      string // that struct, its element structs, and TemplateScope
}

// dynamicQuery prepares a query whose template carries /*%if*/ or /*%for*/, and returns nil
// for one that does not.
//
// The types come from sqlc's own mapping — goType is the same function the static path uses —
// so the parameters of a branching query are typed by the same table as everything else. What
// is added here is the shape: which parameters a branch reaches, and which repeat.
func dynamicQuery(req *plugin.GenerateRequest, options *opts.Options, q *plugin.Query) (*dynamic, error) {
	in := query.Input{
		Name:     q.Name,
		Cmd:      q.Cmd,
		Text:     q.Text,
		Comments: q.Comments,
		Engine:   req.Settings.Engine,
	}
	for _, p := range q.Params {
		t := goType(req, options, p.Column)
		if p.Column.IsSqlcSlice {
			// sqlc has already made a slice parameter's type a slice. The shape does it again
			// from the marker, since that is what says the bind expands, so one layer comes
			// back off here rather than being inferred twice.
			t = strings.TrimPrefix(t, "[]")
		}
		in.Params = append(in.Params, query.Param{
			Number:  int(p.Number),
			Name:    p.Column.GetName(),
			GoType:  t,
			NotNull: p.Column.NotNull,
			IsSlice: p.Column.IsSqlcSlice,
		})
	}

	prepared, diags, err := query.Prepare(in)
	if err != nil {
		return nil, err
	}
	if !prepared.HasDirectives() {
		return nil, nil
	}
	if len(diags) > 0 {
		return nil, fmt.Errorf("%s: %s", q.Name, diags[0])
	}

	var b strings.Builder
	b.WriteString(exprtype.Declare(prepared.Params))
	b.WriteString("\n\n")
	emitScope(&b, prepared.Params)
	return &dynamic{
		Engine:     req.Settings.Engine,
		Template:   prepared.Template,
		ParamsType: prepared.Params.Name,
		Decls:      b.String(),
	}, nil
}

// emitScope writes TemplateScope, keyed by every spelling the template used: a condition may
// write minAge where the marker beside it writes min_age, and both have to resolve. Element
// structs get none — the runtime reflects them, and folding a Go field name reaches every
// spelling a marker can have named it by.
func emitScope(b *strings.Builder, t *exprtype.Type) {
	fmt.Fprintf(b, "// TemplateScope names the fields as the template does.\n")
	fmt.Fprintf(b, "func (p %s) TemplateScope() map[string]any {\n\treturn map[string]any{\n", t.Name)
	for _, m := range t.Fields() {
		for _, s := range m.Spellings {
			fmt.Fprintf(b, "\t\t%q: p.%s,\n", s, exprtype.GoName(m.Name))
		}
	}
	b.WriteString("\t}\n}\n")
}
