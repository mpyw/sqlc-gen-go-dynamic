package directive_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-bisql/internal/directive"
	"github.com/mpyw/sqlc-gen-bisql/internal/exprtype"
)

// searchUsersText is Query.text exactly as sqlc v1.31.1 returned it for the
// template this design was measured against: directives preserved verbatim,
// every bind rewritten to the placeholder sqlc assigned.
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

func searchUsersParams() []directive.Param {
	return []directive.Param{
		{Number: 1, Name: "status"},
		{Number: 2, Name: "department_id"},
		{Number: 3, Name: "min_age"},
		{Number: 4, Name: "senior_age"},
		{Number: 5, Name: "ids"},
		{Number: 6, Name: "c.name"},
		{Number: 7, Name: "c.status"},
		{Number: 8, Name: "t.value"},
	}
}

// The whole point of the design is that Query.text plus the parameter table is
// enough, so this walks the real text all the way to the generated Go types.
func TestParseToTypes(t *testing.T) {
	root, err := directive.Parse(searchUsersText, searchUsersParams(), directive.Dollar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got, diags := exprtype.Infer(root, []exprtype.SQLParam{
		{Name: "status", GoType: "string", NotNull: true},
		{Name: "department_id", GoType: "int64"},
		{Name: "min_age", GoType: "int32", NotNull: true},
		{Name: "senior_age", GoType: "int32", NotNull: true},
		{Name: "ids", GoType: "int64", NotNull: true, Slice: true},
		{Name: "c.name", GoType: "string", NotNull: true},
		{Name: "c.status", GoType: "string", NotNull: true},
		{Name: "t.value", GoType: "string", NotNull: true},
	})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	exprtype.NameQuery(got, "SearchUsers")
	out := exprtype.Declare(got)
	t.Logf("\n%s", out)

	want := `type SearchUsersParams struct {
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
	if out != want {
		t.Errorf("declarations mismatch\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

func TestParseStructure(t *testing.T) {
	root, err := directive.Parse(searchUsersText, searchUsersParams(), directive.Dollar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The arms of a chain are siblings, and a loop's body nests.
	kinds := make([]exprtype.DirKind, 0, len(root.Children))
	for _, c := range root.Children {
		kinds = append(kinds, c.Kind)
	}
	want := []exprtype.DirKind{
		exprtype.If, exprtype.If, exprtype.If, exprtype.ElseIf, exprtype.Else,
		exprtype.If, exprtype.For, exprtype.For, exprtype.If,
	}
	if len(kinds) != len(want) {
		t.Fatalf("top-level kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("child %d kind = %v, want %v", i, kinds[i], want[i])
		}
	}

	nested := root.Children[7] // /*%for g in groups*/
	if nested.Var != "g" || nested.Iter != "groups" {
		t.Errorf("outer loop = %q in %q, want g in groups", nested.Var, nested.Iter)
	}
	if len(nested.Children) != 1 || nested.Children[0].Iter != "g.tags" {
		t.Fatalf("outer loop children = %+v, want one loop over g.tags", nested.Children)
	}
	if binds := nested.Children[0].Binds; len(binds) != 1 || binds[0] != "t.value" {
		t.Errorf("inner loop binds = %v, want [t.value]", binds)
	}
}

// A bare ? is how sqlc emits MySQL, and it carries no number of its own.
func TestParseQuestion(t *testing.T) {
	text := `select id from users
where 1 = 1
  /*%if activeOnly*/ and status = ? /*%end*/
  /*%for kw in keywords*/ and name like ? /*%end*/`
	root, err := directive.Parse(text, []directive.Param{
		{Number: 1, Name: "status"},
		{Number: 2, Name: "kw"},
	}, directive.Question)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if b := root.Children[0].Binds; len(b) != 1 || b[0] != "status" {
		t.Errorf("first branch binds = %v, want [status]", b)
	}
	if b := root.Children[1].Binds; len(b) != 1 || b[0] != "kw" {
		t.Errorf("loop binds = %v, want [kw]", b)
	}
}

// Under PostgreSQL a bare "?" is an operator, not a parameter: it tests for a jsonb key.
func TestParseQuestionMarkIsNotAPlaceholderUnderDollar(t *testing.T) {
	text := `select id from users where data ? 'k' and status = $1`
	root, err := directive.Parse(text, []directive.Param{{Number: 1, Name: "status"}}, directive.Dollar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if b := root.Binds; len(b) != 1 || b[0] != "status" {
		t.Errorf("binds = %v, want [status]", b)
	}
}

// Quotes and non-directive comments must not be mistaken for directives or
// placeholders.
func TestParseSkipsQuotesAndComments(t *testing.T) {
	text := `select '/*%if x*/ $9 ''$8''' as lit, "a $7 col", ` + "`b $6`" + ` as bt
-- a line comment with $5 and /*%if y*/
/** a plain comment with $4 */
from t where id = $1`
	root, err := directive.Parse(text, []directive.Param{{Number: 1, Name: "id"}}, directive.Dollar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(root.Children) != 0 {
		t.Errorf("children = %+v, want none", root.Children)
	}
	if b := root.Binds; len(b) != 1 || b[0] != "id" {
		t.Errorf("binds = %v, want [id]", b)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"unclosed block", `select 1 /*%if x*/ and 2`, "unclosed"},
		{"stray end", `select 1 /*%end*/`, "without a matching"},
		{"arm without a branch", `select 1 /*%else*/`, "without a matching"},
		{"unknown directive", `select 1 /*%while x*/ /*%end*/`, `unknown directive "while"`},
		{"condition-less if", `select 1 /*%if*/ /*%end*/`, "no condition"},
		{"malformed for", `select 1 /*%for c*/ /*%end*/`, "wants"},
		{"placeholder with no parameter", `select $3`, "no parameter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := directive.Parse(c.text, nil, directive.Dollar)
			if err == nil {
				t.Fatalf("want an error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

// A directive flush against column zero never reaches Query.text at all: sqlc
// moves it into Query.comments and drops its line. That is the one layout mistake
// which is invisible in the types, so it is reported from the comments instead.
func TestCheckComments(t *testing.T) {
	errs := directive.CheckComments([]string{
		"* a plain comment ",
		"%if activeOnly*/ and u.status = $1 /*%end",
		"%end",
	})
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want two", errs)
	}
	if !strings.Contains(errs[0].Error(), "column zero") {
		t.Errorf("error = %q, want it to mention column zero", errs[0])
	}
}

// sqlc emits ?n for SQLite, which sits between the other two styles: the "?" makes it look
// positional, and the number makes position wrong. A parameter used twice appears twice
// with its own number, which is exactly what position cannot express.
func TestParseQuestionNumbered(t *testing.T) {
	text := `select id from t
where 1 = 1
  /*%if a*/ and x = ?1 /*%end*/
  /*%if b*/ and y = ?2 and z = ?1 /*%end*/`
	root, err := directive.Parse(text, []directive.Param{
		{Number: 1, Name: "one"},
		{Number: 2, Name: "two"},
	}, directive.QuestionNumbered)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if b := root.Children[0].Binds; len(b) != 1 || b[0] != "one" {
		t.Errorf("first branch binds = %v, want [one]", b)
	}
	if b := root.Children[1].Binds; len(b) != 2 || b[0] != "two" || b[1] != "one" {
		t.Errorf("second branch binds = %v, want [two one]", b)
	}
}

// ":3" is ordinary PostgreSQL syntax inside an array slice, so a style that reads it as a
// placeholder breaks a legitimate query. Only $n is a placeholder under Dollar.
func TestParseDollarLeavesColonsAlone(t *testing.T) {
	root, err := directive.Parse("select a[1:3] from t where id = $1",
		[]directive.Param{{Number: 1, Name: "id"}}, directive.Dollar)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if b := root.Binds; len(b) != 1 || b[0] != "id" {
		t.Errorf("binds = %v, want [id]", b)
	}
}

func TestStyleFor(t *testing.T) {
	for engine, want := range map[string]directive.Style{
		"postgresql": directive.Dollar,
		"mysql":      directive.Question,
		"sqlite":     directive.QuestionNumbered,
	} {
		got, err := directive.StyleFor(engine)
		if err != nil {
			t.Errorf("StyleFor(%q): %v", engine, err)
			continue
		}
		if got != want {
			t.Errorf("StyleFor(%q) = %v, want %v", engine, got, want)
		}
	}
	if _, err := directive.StyleFor("oracle"); err == nil {
		t.Error("StyleFor(oracle): want an error, sqlc has no such engine")
	}
}
