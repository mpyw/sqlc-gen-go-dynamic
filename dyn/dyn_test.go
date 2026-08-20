package dyn_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/dyn"
)

// params is shaped like a generated one: it names its own fields, because only the generator
// knows that the template writes activeOnly and min_age where Go writes ActiveOnly and MinAge.
type params struct {
	ActiveOnly bool
	MinAge     *int32
	Ids        []int64
	Conds      []cond
}

type cond struct {
	Name   string
	Status string
}

func (p params) TemplateScope() map[string]any {
	return map[string]any{
		"activeOnly": p.ActiveOnly,
		"minAge":     p.MinAge,
		"min_age":    p.MinAge,
		"ids":        p.Ids,
		"conds":      p.Conds,
	}
}

const src = `select id from users
where 1 = 1
  /*%if activeOnly*/ and status = 'x' /*%end*/
  /*%if minAge != null*/ and age >= sqlc.arg('min_age') /*%end*/
  /*%if ids != null*/ and id in (sqlc.slice('ids')) /*%end*/
  /*%for c in conds*/ and (name like sqlc.arg('c.name') or status = sqlc.arg('c.status')) /*%end*/`

func TestBuild(t *testing.T) {
	tmpl := dyn.MustParse(src, "postgresql")
	minAge := int32(20)
	stmt, err := tmpl.Build(params{
		ActiveOnly: true,
		MinAge:     &minAge,
		Ids:        []int64{1, 2},
		Conds:      []cond{{Name: "%a%", Status: "active"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `select id from users
where 1 = 1
   and status = 'x' 
   and age >= $1 
   and id in ($2, $3) 
   and (name like $4 or status = $5) `
	if stmt.SQL != want {
		t.Errorf("SQL\n got:\n%s\nwant:\n%s", stmt.SQL, want)
	}
	wantArgs := []any{&minAge, int64(1), int64(2), "%a%", "active"}
	if !reflect.DeepEqual(stmt.Args, wantArgs) {
		t.Errorf("Args\n got: %#v\nwant: %#v", stmt.Args, wantArgs)
	}
}

// The zero value takes no branch, and a template that renders nothing dynamic still renders.
func TestBuildNoBranch(t *testing.T) {
	stmt, err := dyn.MustParse(src, "postgresql").Build(params{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(stmt.Args) != 0 {
		t.Errorf("Args = %#v, want none", stmt.Args)
	}
	if strings.Contains(stmt.SQL, "$1") {
		t.Errorf("SQL = %q, want no placeholder", stmt.SQL)
	}
}

// A map is accepted too, which is what makes the runtime usable without the generator. The
// call form is used here because @a is not a bind under MySQL.
func TestBuildFromMap(t *testing.T) {
	stmt, err := dyn.MustParse("select 1 where a = sqlc.arg(a)", "mysql").
		Build(map[string]any{"a": 7})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stmt.SQL != "select 1 where a = ?" || !reflect.DeepEqual(stmt.Args, []any{7}) {
		t.Errorf("SQL = %q, Args = %#v", stmt.SQL, stmt.Args)
	}
}

// The @name shortcut does not exist for MySQL, where @name is a user variable — the same
// reading sqlc makes.
func TestParseFollowsTheDialect(t *testing.T) {
	const v = "select @row_number := @row_number + 1"
	stmt, err := dyn.MustParse(v, "mysql").Build(nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stmt.SQL != v {
		t.Errorf("mysql: SQL = %q, want the user variable left alone", stmt.SQL)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := dyn.Parse("select 1", "oracle"); err == nil ||
		!strings.Contains(err.Error(), "unsupported engine") {
		t.Errorf("error = %v, want it to reject the engine", err)
	}
	if _, err := dyn.Parse("select 1 /*%if x*/", "postgresql"); err == nil ||
		!strings.Contains(err.Error(), "unclosed") {
		t.Errorf("error = %v, want it to report the unclosed block", err)
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want a panic for a template that does not parse")
		}
	}()
	dyn.MustParse("select 1 /*%end*/", "postgresql")
}
