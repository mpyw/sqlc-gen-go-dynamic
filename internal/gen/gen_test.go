package gen_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gen"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/query"
)

func prepare(t *testing.T, in query.Input) *query.Query {
	t.Helper()
	q, diags, err := query.Prepare(in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return q
}

func searchUsers() query.Input {
	return query.Input{
		Name:   "SearchUsers",
		Cmd:    ":many",
		Engine: "postgresql",
		Text: "select u.id, u.name from users u where 1 = 1\n" +
			"  /*%if activeOnly*/ and u.status = $1 /*%end*/\n" +
			"  /*%if minAge != null*/ and u.age >= $2 /*%end*/\n" +
			"  /*%for c in conds*/ and u.name like $3 /*%end*/",
		Params: []query.Param{
			{Number: 1, Name: "status", GoType: "string", NotNull: true},
			{Number: 2, Name: "min_age", GoType: "int32", NotNull: true},
			{Number: 3, Name: "c.name", GoType: "string", NotNull: true},
		},
		Row: []query.Column{
			{Name: "id", GoType: "int64", NotNull: true},
			{Name: "name", GoType: "string", NotNull: true},
		},
	}
}

// The emitted file has to compile, so it is parsed and formatted before being returned; a
// golden comparison then only has to say what it should look like.
func TestFile(t *testing.T) {
	out, err := gen.File(gen.Options{Package: "db"}, []*query.Query{prepare(t, searchUsers())})
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	got := string(out)
	t.Logf("\n%s", got)

	for _, want := range []string{
		`package db`,
		`"github.com/mpyw/sqlc-gen-go-dynamic/dyn"`,
		"const searchUsersSQL = `",
		"and u.status = sqlc.arg('status')", // the marker is restored
		`var searchUsersTemplate = dyn.MustParse(searchUsersSQL, "postgresql")`,
		"type SearchUsersParams struct",
		"ActiveOnly bool",
		"Status     string",
		"MinAge     *int32", // nil-tested by the condition, so unset must differ from zero
		"Conds      []SearchUsersCond",
		"type SearchUsersCond struct",
		"Name string",
		"func (p SearchUsersParams) TemplateScope() map[string]any",
		`"minAge":     p.MinAge,`, // both spellings the template used
		`"min_age":    p.MinAge,`,
		"type SearchUsersRow struct",
		"ID   int64",
		"Name string",
		"func (q *Queries) SearchUsers(ctx context.Context, arg SearchUsersParams) ([]SearchUsersRow, error)",
		"stmt, err := searchUsersTemplate.Build(arg)",
		"q.db.QueryContext(ctx, stmt.SQL, stmt.Args...)",
		"rows.Scan(&i.ID, &i.Name)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestFileShapes(t *testing.T) {
	for _, c := range []struct{ cmd, want string }{
		{":many", "([]QRow, error)"},
		{":one", "(QRow, error)"},
		{":exec", ") error {"},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			in := query.Input{
				Name: "Q", Cmd: c.cmd, Engine: "postgresql",
				Text:   "select id from t where a = $1 /*%if x*/ and b = 1 /*%end*/",
				Params: []query.Param{{Number: 1, Name: "a", GoType: "int64", NotNull: true}},
				Row:    []query.Column{{Name: "id", GoType: "int64", NotNull: true}},
			}
			out, err := gen.File(gen.Options{Package: "db"}, []*query.Query{prepare(t, in)})
			if err != nil {
				t.Fatalf("gen: %v", err)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("missing %q in:\n%s", c.want, out)
			}
		})
	}
}

func TestFileRejects(t *testing.T) {
	base := func() query.Input {
		return query.Input{
			Name: "Q", Cmd: ":many", Engine: "postgresql",
			Text:   "select id from t where a = $1 /*%if x*/ and b = 1 /*%end*/",
			Params: []query.Param{{Number: 1, Name: "a", GoType: "int64", NotNull: true}},
			Row:    []query.Column{{Name: "id", GoType: "int64", NotNull: true}},
		}
	}
	t.Run("an unsupported command", func(t *testing.T) {
		in := base()
		in.Cmd = ":copyfrom"
		_, err := gen.File(gen.Options{Package: "db"}, []*query.Query{prepare(t, in)})
		if err == nil || !strings.Contains(err.Error(), "unsupported command") {
			t.Errorf("error = %v, want it to reject the command", err)
		}
	})
	t.Run("an embedded table", func(t *testing.T) {
		in := base()
		in.Row = append(in.Row, query.Column{Embed: "authors"})
		_, err := gen.File(gen.Options{Package: "db"}, []*query.Query{prepare(t, in)})
		if err == nil || !strings.Contains(err.Error(), "sqlc.embed") {
			t.Errorf("error = %v, want it to reject the embed", err)
		}
	})
	t.Run("no package name", func(t *testing.T) {
		if _, err := gen.File(gen.Options{}, nil); err == nil {
			t.Error("want an error with no package name")
		}
	})
}
