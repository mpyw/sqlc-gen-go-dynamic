package query_test

import (
	"reflect"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprlang"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/query"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/render"
)

// searchUsers is Query.text exactly as sqlc v1.31.1 returned it for the template this design
// was measured against, with its parameter table.
const searchUsersText = `select u.id, u.name
from users u
where 1 = 1
  /*%if activeOnly*/ and u.status = $1 /*%end*/
  /*%if departmentId != null*/ and u.department_id = $2 /*%end*/
  /*%if ageBand == 'adult'*/ and u.age >= $3
  /*%elseif ageBand == 'senior'*/ and u.age >= $4
  /*%else*/ and u.age >= 0 /*%end*/
  /*%if ids != null*/ and u.id in ($5) /*%end*/
  /*%for c in conds*/ and (u.name like $6 or u.status = $7) /*%end*/
  /*%for g in groups*/
    /*%for t in g.tags*/ and u.tags @> array[$8] /*%end*/
  /*%end*/
order by /*%if byName*/ u.name, /*%end*/ u.id`

func searchUsers() query.Input {
	return query.Input{
		Name:   "SearchUsers",
		Cmd:    ":many",
		Text:   searchUsersText,
		Engine: "postgresql",
		Params: []query.Param{
			{Number: 1, Name: "status", GoType: "string", NotNull: true},
			{Number: 2, Name: "department_id", GoType: "int64"},
			{Number: 3, Name: "min_age", GoType: "int32", NotNull: true},
			{Number: 4, Name: "senior_age", GoType: "int32", NotNull: true},
			{Number: 5, Name: "ids", GoType: "int64", NotNull: true, IsSlice: true},
			{Number: 6, Name: "c.name", GoType: "string", NotNull: true},
			{Number: 7, Name: "c.status", GoType: "string", NotNull: true},
			{Number: 8, Name: "t.value", GoType: "string", NotNull: true},
		},
	}
}

// The design rests on one claim: the text sqlc hands back, plus its parameter table, is
// enough. This walks the real text to the Go declarations and then renders it.
func TestPrepare(t *testing.T) {
	q, diags, err := query.Prepare(searchUsers())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	wantTemplate := `select u.id, u.name
from users u
where 1 = 1
  /*%if activeOnly*/ and u.status = sqlc.arg('status') /*%end*/
  /*%if departmentId != null*/ and u.department_id = sqlc.arg('department_id') /*%end*/
  /*%if ageBand == 'adult'*/ and u.age >= sqlc.arg('min_age')
  /*%elseif ageBand == 'senior'*/ and u.age >= sqlc.arg('senior_age')
  /*%else*/ and u.age >= 0 /*%end*/
  /*%if ids != null*/ and u.id in (sqlc.slice('ids')) /*%end*/
  /*%for c in conds*/ and (u.name like sqlc.arg('c.name') or u.status = sqlc.arg('c.status')) /*%end*/
  /*%for g in groups*/
    /*%for t in g.tags*/ and u.tags @> array[sqlc.arg('t.value')] /*%end*/
  /*%end*/
order by /*%if byName*/ u.name, /*%end*/ u.id`
	if q.Template != wantTemplate {
		t.Errorf("Template\n got:\n%s\nwant:\n%s", q.Template, wantTemplate)
	}

	wantDecls := `type SearchUsersParams struct {
	ActiveOnly   bool
	Status       string
	DepartmentID *int64
	AgeBand      string
	MinAge       int32
	SeniorAge    int32
	Ids          []int64
	Conds        []SearchUsersCond
	Groups       []SearchUsersGroup
	ByName       bool
}

type SearchUsersCond struct {
	Name   string
	Status string
}

type SearchUsersGroup struct {
	Tags []SearchUsersGroupTag
}

type SearchUsersGroupTag struct {
	Value string
}`
	if got := exprtype.Declare(q.Params); got != wantDecls {
		t.Errorf("declarations\n got:\n%s\nwant:\n%s", got, wantDecls)
	}
}

// The tree typing walked is the tree the renderer walks, so the parameters it described are
// the ones the query asks for.
func TestPreparedQueryRenders(t *testing.T) {
	q, _, err := query.Prepare(searchUsers())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	params := map[string]any{
		"activeOnly": true, "status": "active",
		"departmentId": 3, "department_id": 3,
		"ageBand": "senior", "senior_age": 65,
		"ids":    []any{1, 2},
		"conds":  []map[string]any{{"name": "%a%", "status": "active"}},
		"groups": []map[string]any{{"tags": []map[string]any{{"value": "vip"}}}},
		"byName": false,
	}
	res, err := render.Render(q.Nodes, params, dialect.PostgreSQL, &exprlang.Evaluator{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wantArgs := []any{"active", 3, 65, 1, 2, "%a%", "active", "vip"}
	if !reflect.DeepEqual(res.Args, wantArgs) {
		t.Errorf("Args\n got: %#v\nwant: %#v", res.Args, wantArgs)
	}
	// Numbering runs off one counter, so the branches that did not render leave no gap.
	for i := range wantArgs {
		if want := "$" + string(rune('1'+i)); !contains(res.SQL, want) {
			t.Errorf("SQL %q is missing %s", res.SQL, want)
		}
	}
	if contains(res.SQL, "$9") {
		t.Errorf("SQL %q numbers past the arguments", res.SQL)
	}
}

func TestPrepareRejectsAColumnZeroDirective(t *testing.T) {
	in := searchUsers()
	in.Comments = []string{"%if activeOnly*/ and u.status = $1 /*%end"}
	if _, _, err := query.Prepare(in); err == nil || !contains(err.Error(), "column zero") {
		t.Errorf("error = %v, want it to report the lifted directive", err)
	}
}

func TestPrepareRejectsAnUnsupportedEngine(t *testing.T) {
	in := searchUsers()
	in.Engine = "oracle"
	if _, _, err := query.Prepare(in); err == nil || !contains(err.Error(), "unsupported engine") {
		t.Errorf("error = %v, want it to reject the engine", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
