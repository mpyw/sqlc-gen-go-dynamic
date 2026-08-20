package golang

import (
	"strings"
	"testing"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/opts"
)

// This file pins the findings of an adversarial review of the integration. The claim under
// test throughout is the one that makes this a drop-in: a query with no directives must reach
// none of this code, so nothing here can fail it.

func aQuery(text string, comments ...string) *plugin.Query {
	return &plugin.Query{Name: "Q", Cmd: ":many", Text: text, Comments: comments}
}

// options builds the option set with the defaults sqlc always fills in; the zero value has nil
// pointers that buildQueries dereferences.
func options(mut ...func(*opts.Options)) *opts.Options {
	limit := int32(1)
	o := &opts.Options{QueryParameterLimit: &limit}
	for _, f := range mut {
		f(o)
	}
	return o
}

func request(engine string) *plugin.GenerateRequest {
	return &plugin.GenerateRequest{
		Settings: &plugin.Settings{Engine: engine},
		// sqlc always sends a catalog, and the type mapping reads it for enums and composite
		// types; without one an unmapped type name dereferences nil.
		Catalog: &plugin.Catalog{DefaultSchema: "public"},
	}
}

// A directive-free query is not merely handled — it is never touched. Every hard failure this
// code can produce (marker restoration, parsing, the lints) is downstream of the gate, so the
// gate is what makes the byte-for-byte claim structural.
func TestDirectiveFreeQueryIsUntouched(t *testing.T) {
	for _, c := range []struct{ name, engine, text string }{
		{
			// A backslash escape is ordinary MySQL. Restoring markers used to reject it and
			// abort the whole generate, taking any sibling codegen with it.
			name: "a backslash escape in MySQL", engine: "mysql",
			text: `select id from users where name = 'it\'s' and status = ?`,
		},
		{
			name: "a dollar-quoted string", engine: "postgresql",
			text: `select id from users where body = $$it's$$ and status = $1`,
		},
		{
			name: "an escape-string literal", engine: "postgresql",
			text: `select id from users where name = E'it\'s'`,
		},
		{
			// A comment that begins with % is prose, not a directive.
			name: "prose beginning with a percent sign", engine: "postgresql",
			text: "select id from users where id = $1",
		},
		{
			name: "text that merely mentions a directive", engine: "postgresql",
			text: `select id from users where note = '/*%if x*/'`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := aQuery(c.text, " % of users active", " name: Q :many")
			got, err := dynamicQuery(request(c.engine), options(), q)
			if err != nil {
				t.Fatalf("a query with no directive must not fail: %v", err)
			}
			if got != nil {
				t.Errorf("a query with no directive must not become dynamic: %+v", got)
			}
		})
	}
}

// A directive sqlc lifted out of the text leaves no trace in the text, so the gate has to look
// at the comments too or the mistake goes unreported.
func TestColumnZeroDirectiveIsStillCaught(t *testing.T) {
	q := aQuery("select id from users\n", "%if activeOnly*/ and status = $1 /*%end")
	_, err := dynamicQuery(request("postgresql"), options(), q)
	if err == nil || !strings.Contains(err.Error(), "column zero") {
		t.Errorf("err = %v, want the lifted directive reported", err)
	}
}

// sqlc truncates a trailing block comment on some engines, which leaves the directive
// unbalanced. That is loud on its own — the parser refuses it — and is worth a test because the
// message has to come from here rather than from the server.
func TestTruncatedTrailingDirectiveIsRefused(t *testing.T) {
	q := aQuery("select id from users where status = $1 /*%if x*/")
	if _, err := dynamicQuery(request("postgresql"), options(), q); err == nil {
		t.Fatal("want an error for an unbalanced directive")
	}
}

// Only the commands whose templates have a dynamic body may be dynamic. Admitting one without
// that body is worse than refusing it: rendering either crashes or sends the template text to
// the server as SQL.
func TestOnlyCommandsWithADynamicBodyAreSupported(t *testing.T) {
	bodies := map[string]bool{}
	for _, cmd := range []string{":one", ":many", ":exec"} {
		bodies[cmd] = true
	}
	for cmd := range dynamicSupported {
		if !bodies[cmd] {
			t.Errorf("%s is declared supported but has no dynamic template body", cmd)
		}
	}
	for cmd := range bodies {
		if _, ok := dynamicSupported[cmd]; !ok {
			t.Errorf("%s has a dynamic template body but is not declared supported", cmd)
		}
	}
}

// Both templates must carry a dynamic branch for every supported command, or the query falls
// through to a static body that sends the template as SQL.
func TestEveryTemplateHasABranchForEverySupportedCommand(t *testing.T) {
	for _, driver := range []string{"pgx", "stdlib"} {
		src := readTemplate(t, driver)
		for cmd := range dynamicSupported {
			block := commandBlock(t, src, cmd)
			if !strings.Contains(block, "{{if .Dynamic}}") {
				t.Errorf("%s: %s has no {{if .Dynamic}} branch", driver, cmd)
			}
			if !strings.Contains(block, ".TemplateVar}}.Build(arg)") {
				t.Errorf("%s: %s does not build the template", driver, cmd)
			}
		}
	}
}

// The identifier minted for the parsed template cannot be one sqlc already owns.
func TestTemplateVarAvoidsATakenName(t *testing.T) {
	taken := map[string]struct{}{"searchUsersTemplate": {}}
	if got := uniqueName("searchUsersTemplate", taken); got == "searchUsersTemplate" {
		t.Error("want a name that does not collide")
	}
	if got := uniqueName("other", taken); got != "other" {
		t.Errorf("uniqueName(other) = %q, want it unchanged", got)
	}
}

// The types a dynamic params struct mentions have to be visible to import resolution and to
// unused-struct filtering, or the generated file names a type it never imports.
func TestDynamicTypesAreCollected(t *testing.T) {
	q := aQuery("select id from users where 1 = 1\n" +
		"  /*%if cutoff != null*/ and seen_at > $1 /*%end*/")
	// A nullable timestamptz is pgtype.Timestamptz under pgx, which needs an import.
	sqlPkg := "pgx/v5"
	got, err := dynamicQuery(request("postgresql"),
		options(func(o *opts.Options) { o.SqlPackage = sqlPkg }),
		withParam(q, "cutoff", "timestamptz"))
	if err != nil {
		t.Fatalf("dynamicQuery: %v", err)
	}
	if got == nil {
		t.Fatal("want a dynamic query")
	}
	var found bool
	for _, ty := range got.Types {
		if ty == "pgtype.Timestamptz" {
			found = true
		}
	}
	if !found {
		t.Errorf("Types = %q, want it to name the parameter's type", got.Types)
	}
}

// A template is not fixed SQL, so it cannot be prepared. The combination is refused at codegen
// rather than failing against a server.
func TestPreparedQueriesAreRefused(t *testing.T) {
	req := request("postgresql")
	req.Queries = []*plugin.Query{withParam(aQuery(
		"select id from users where 1 = 1 /*%if a*/ and status = $1 /*%end*/"), "status", "string")}
	_, err := buildQueries(req, options(func(o *opts.Options) { o.EmitPreparedQueries = true }), nil)
	if err == nil || !strings.Contains(err.Error(), "emit_prepared_queries") {
		t.Errorf("err = %v, want the combination refused", err)
	}
}

func withParam(q *plugin.Query, name, goType string) *plugin.Query {
	q.Params = append(q.Params, &plugin.Parameter{
		Number: int32(len(q.Params) + 1),
		Column: &plugin.Column{
			Name:    name,
			NotNull: false,
			Type:    &plugin.Identifier{Name: goType},
		},
	})
	return q
}

// readTemplate and commandBlock let a test assert on the generated-code templates themselves,
// which is where a missing dynamic branch hides.
func readTemplate(t *testing.T, driver string) string {
	t.Helper()
	b, err := templates.ReadFile("templates/" + driver + "/queryCode.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	return string(b)
}

// commandBlock returns the {{if eq .Cmd "<cmd>"}} block's body.
func commandBlock(t *testing.T, src, cmd string) string {
	t.Helper()
	open := `{{if eq .Cmd "` + cmd + `"}}`
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("no block for %s", cmd)
	}
	rest := src[i+len(open):]
	if j := strings.Index(rest, `{{if eq .Cmd "`); j >= 0 {
		return rest[:j]
	}
	return rest
}
