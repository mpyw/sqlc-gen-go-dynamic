package render_test

import (
	"reflect"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprlang"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/render"
)

func build(t *testing.T, src string, params any, d dialect.Dialect) render.Result {
	t.Helper()
	nodes, err := parser.Parse(src, bind.RulesFor(d.Name()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := render.Render(nodes, params, d, &exprlang.Evaluator{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return res
}

func TestRender(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		params any
		sql    string
		args   []any
	}{
		{
			name:   "at form",
			src:    "select id from users where status = @status",
			params: map[string]any{"status": "active"},
			sql:    "select id from users where status = $1",
			args:   []any{"active"},
		},
		{
			name:   "call forms",
			src:    "select id from users where a = sqlc.arg(a) and b = sqlc.narg('b')",
			params: map[string]any{"a": 1, "b": nil},
			sql:    "select id from users where a = $1 and b = $2",
			args:   []any{1, nil},
		},
		{
			name:   "slice expands without parentheses of its own",
			src:    "select id from users where id in (sqlc.slice(ids))",
			params: map[string]any{"ids": []int{1, 2, 3}},
			sql:    "select id from users where id in ($1, $2, $3)",
			args:   []any{1, 2, 3},
		},
		{
			name:   "an empty slice renders as null",
			src:    "select id from users where id in (sqlc.slice(ids))",
			params: map[string]any{"ids": []int{}},
			sql:    "select id from users where id in (null)",
			args:   nil,
		},
		{
			name:   "a plain marker binds a slice whole",
			src:    "select id from users where id = any(@ids)",
			params: map[string]any{"ids": []int{1, 2}},
			sql:    "select id from users where id = any($1)",
			args:   []any{[]int{1, 2}},
		},
		{
			// Numbering runs off one counter, so the skipped branch leaves no gap.
			name: "branches that do not render are not counted",
			src: "select id from users where 1 = 1" +
				"/*%if activeOnly*/ and status = @status/*%end*/" +
				"/*%if minAge != null*/ and age >= @min_age/*%end*/",
			params: map[string]any{"activeOnly": false, "minAge": 20, "min_age": 20},
			sql:    "select id from users where 1 = 1 and age >= $1",
			args:   []any{20},
		},
		{
			name:   "arms are tried in order",
			src:    "select 1 /*%if band == 'a'*/A/*%elseif band == 'b'*/B/*%else*/C/*%end*/",
			params: map[string]any{"band": "b"},
			sql:    "select 1 B",
			args:   nil,
		},
		{
			name:   "an absent condition is false, not an error",
			src:    "select 1 /*%if missing*/X/*%end*/",
			params: map[string]any{},
			sql:    "select 1 ",
			args:   nil,
		},
		{
			// Nothing is inserted between iterations; the template carries the connector.
			name:   "a loop inserts nothing of its own",
			src:    "select 1 where 1 = 1/*%for kw in keywords*/ and name like @kw/*%end*/",
			params: map[string]any{"keywords": []string{"%a%", "%b%"}},
			sql:    "select 1 where 1 = 1 and name like $1 and name like $2",
			args:   []any{"%a%", "%b%"},
		},
		{
			name:   "an empty iterable yields no iterations",
			src:    "select 1 where 1 = 1/*%for kw in keywords*/ and name like @kw/*%end*/",
			params: map[string]any{"keywords": []string{}},
			sql:    "select 1 where 1 = 1",
			args:   nil,
		},
		{
			name:   "a nil iterable yields no iterations",
			src:    "select 1 where 1 = 1/*%for kw in keywords*/ and name like @kw/*%end*/",
			params: map[string]any{},
			sql:    "select 1 where 1 = 1",
			args:   nil,
		},
		{
			name: "a loop element is reached by a dotted name",
			src:  "select 1 where 1 = 0/*%for c in conds*/ or (a = sqlc.arg('c.name') and b = sqlc.arg('c.status'))/*%end*/",
			params: map[string]any{"conds": []map[string]any{
				{"name": "ada", "status": "active"},
			}},
			sql:  "select 1 where 1 = 0 or (a = $1 and b = $2)",
			args: []any{"ada", "active"},
		},
		{
			name: "loops nest",
			src:  "select 1/*%for g in groups*//*%for t in g.tags*/ and tag = sqlc.arg('t.value')/*%end*//*%end*/",
			params: map[string]any{"groups": []map[string]any{
				{"tags": []map[string]any{{"value": "x"}, {"value": "y"}}},
			}},
			sql:  "select 1 and tag = $1 and tag = $2",
			args: []any{"x", "y"},
		},
		{
			name:   "text is emitted verbatim, comments included",
			src:    "select 1 /** a comment */ -- a line\nfrom t where x = @x",
			params: map[string]any{"x": 1},
			sql:    "select 1 /** a comment */ -- a line\nfrom t where x = $1",
			args:   []any{1},
		},
		{
			name:   "a parser comment is dropped",
			src:    "select 1 /*%! a note */from t where x = @x",
			params: map[string]any{"x": 1},
			sql:    "select 1 from t where x = $1",
			args:   []any{1},
		},
		{
			name:   "a marker inside a quoted span is text",
			src:    `select '@a', "@b" from t where x = @x`,
			params: map[string]any{"x": 1},
			sql:    `select '@a', "@b" from t where x = $1`,
			args:   []any{1},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, c.src, c.params, dialect.PostgreSQL)
			if res.SQL != c.sql {
				t.Errorf("SQL\n got: %q\nwant: %q", res.SQL, c.sql)
			}
			if !reflect.DeepEqual(res.Args, c.args) {
				t.Errorf("Args\n got: %#v\nwant: %#v", res.Args, c.args)
			}
		})
	}
}

// sqlc does not support the @name shortcut for MySQL, where @name is a user variable, so
// neither does this.
func TestRenderAtFormFollowsTheDialect(t *testing.T) {
	const src = "select @row_number := @row_number + 1"
	if got := build(t, src, nil, dialect.MySQL).SQL; got != src {
		t.Errorf("mysql: SQL = %q, want the user variable left alone", got)
	}
	got := build(t, src, map[string]any{"row_number": 1}, dialect.PostgreSQL)
	if got.SQL != "select $1 := $2 + 1" {
		t.Errorf("postgres: SQL = %q, want the marker read as a bind", got.SQL)
	}
}

// A generated params struct names its own fields, because only the generator knows both
// spellings. A struct that does not is reflected, with names folded.
type namedParams struct {
	ActiveOnly bool
	MinAge     int32
}

func (p namedParams) TemplateScope() map[string]any {
	return map[string]any{"activeOnly": p.ActiveOnly, "min_age": p.MinAge}
}

func TestRenderParamsSources(t *testing.T) {
	const src = "select 1 where 1 = 1/*%if activeOnly*/ and age >= @min_age/*%end*/"
	want := "select 1 where 1 = 1 and age >= $1"

	if got := build(t, src, namedParams{ActiveOnly: true, MinAge: 20}, dialect.PostgreSQL); got.SQL != want {
		t.Errorf("Scoper: SQL = %q, want %q", got.SQL, want)
	}

	// The same struct without TemplateScope: activeOnly folds to ActiveOnly, min_age to
	// MinAge.
	plain := struct {
		ActiveOnly bool
		MinAge     int32
	}{true, 20}
	if got := build(t, src, plain, dialect.PostgreSQL); got.SQL != want {
		t.Errorf("struct: SQL = %q, want %q", got.SQL, want)
	}
}

func TestRenderErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"missing parameter", "select @absent", "no value for parameter"},
		{"condition is not a bool", "select 1 /*%if 'x'*/y/*%end*/", "want bool"},
		{"iterable is not a slice", "select 1 /*%for x in n*/y/*%end*/", "needs a slice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes, err := parser.Parse(c.src, bind.RulesFor("postgresql"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = render.Render(nodes, map[string]any{"n": 1}, dialect.PostgreSQL, &exprlang.Evaluator{})
			if err == nil {
				t.Fatalf("want an error containing %q, got nil", c.want)
			}
			if !contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A condition reaching into a loop element needs the element in the scope by the template's
// spelling: the expression language resolves a member by its exact Go field name, so
// c.enabled would not find Enabled.
func TestRenderConditionOnStructElement(t *testing.T) {
	type cond struct {
		Enabled bool
		Name    string
	}
	res := build(t,
		"select 1/*%for c in conds*//*%if c.enabled*/ and n = sqlc.arg('c.name')/*%end*//*%end*/",
		map[string]any{"conds": []cond{
			{Enabled: true, Name: "ada"},
			{Enabled: false, Name: "skipped"},
			{Enabled: true, Name: "bob"},
		}},
		dialect.PostgreSQL)
	if want := "select 1 and n = $1 and n = $2"; res.SQL != want {
		t.Errorf("SQL\n got: %q\nwant: %q", res.SQL, want)
	}
	if want := []any{"ada", "bob"}; !reflect.DeepEqual(res.Args, want) {
		t.Errorf("Args = %#v, want %#v", res.Args, want)
	}
}

// A nested slice inside a struct element still iterates.
func TestRenderNestedStructElements(t *testing.T) {
	type tag struct{ Value string }
	type group struct{ Tags []tag }
	res := build(t,
		"select 1/*%for g in groups*//*%for t in g.tags*/ and v = sqlc.arg('t.value')/*%end*//*%end*/",
		map[string]any{"groups": []group{{Tags: []tag{{"x"}, {"y"}}}}},
		dialect.PostgreSQL)
	if want := []any{"x", "y"}; !reflect.DeepEqual(res.Args, want) {
		t.Errorf("Args = %#v, want %#v", res.Args, want)
	}
}
