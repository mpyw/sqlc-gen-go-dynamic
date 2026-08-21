package exprtype_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
)

// infer parses a template and types it, which is the shape the plugin uses: typing walks the
// tree the renderer will walk.
func infer(t *testing.T, src string, params ...exprtype.SQLParam) (*exprtype.Type, []exprtype.Diagnostic) {
	t.Helper()
	nodes, err := parser.Parse(src, bind.RulesFor("postgresql"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, diags := exprtype.Infer(nodes, params)
	exprtype.NameQuery(got, "Q")
	return got, diags
}

func declare(t *testing.T, src string, params ...exprtype.SQLParam) string {
	t.Helper()
	got, diags := infer(t, src, params...)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return exprtype.Declare(got)
}

// A loop whose single marker names the loop variable yields a slice of scalars, not a slice
// of one-field structs.
func TestInferScalarLoop(t *testing.T) {
	out := declare(t, "/*%for kw in keywords*/x = @kw/*%end*/",
		exprtype.SQLParam{Name: "kw", GoType: "string", NotNull: true})
	if !strings.Contains(out, "Keywords []string") {
		t.Errorf("want Keywords []string, got:\n%s", out)
	}
}

func TestInferConditionShapes(t *testing.T) {
	for _, c := range []struct{ name, cond, want string }{
		{"bare identifier is a bool", "activeOnly", "ActiveOnly bool"},
		{"negation is a bool", "!disabled", "Disabled bool"},
		{"string comparison", "ageBand == 'adult'", "AgeBand string"},
		{"either operand order", "'adult' == ageBand", "AgeBand string"},
		{"int comparison", "minAge > 18", "MinAge int64"},
		{"float comparison", "ratio >= 0.5", "Ratio float64"},
		{"boolean operators recurse", "activeOnly && !disabled", "ActiveOnly bool"},
		{"disjunction over one variable", "ageBand == 'adult' || ageBand == 'senior'", "AgeBand string"},
		{"string operator", "name matches '^a'", "Name string"},
		{"member access builds a struct", "user.role == 'admin'", "User QUser"},
		{"integer indexing pins a slice", "roles[0] == 'admin'", "Roles []string"},
		{"negative literals are literals", "minAge > -1", "MinAge int64"},
		{"a literal set pins the element", "tier in ['a', 'b']", "Tier string"},
		{"arithmetic carries the literal through", "x * 2 > 10", "X int64"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := declare(t, "/*%if "+c.cond+"*/y/*%end*/")
			if !strings.Contains(out, c.want) {
				t.Errorf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// A nil test in a condition is the one source of optionality this adds. Being inside a branch
// is not one: when the branch does not render, nothing reads the value. Nor is a nullable
// column — sqlc has already chosen a type that expresses absence, and adding a pointer on top
// of it gave the dynamic query a different type from the static one beside it.
func TestInferOptionalitySources(t *testing.T) {
	src := "/*%if activeOnly*/a = @status/*%end*/" + // NOT NULL, no nil test: a value
		"/*%if minAge != null*/b = @min_age/*%end*/" + // nil-tested: unset must differ from zero
		"/*%if withNote*/c = @note/*%end*/" + // nullable, so sqlc's own type says so
		"/*%if withSeen*/d = @seen_at/*%end*/" + // nullable and already a pointer
		"/*%if tier != null*/e = @tier/*%end*/" // nullable and nil-tested: three states
	out := declare(t, src,
		exprtype.SQLParam{Name: "status", GoType: "string", NotNull: true},
		exprtype.SQLParam{Name: "min_age", GoType: "int32", NotNull: true},
		exprtype.SQLParam{Name: "note", GoType: "sql.NullString"},
		exprtype.SQLParam{Name: "seen_at", GoType: "*time.Time"},
		exprtype.SQLParam{Name: "tier", GoType: "sql.NullString"},
	)
	squeezed := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"Status string",
		"MinAge *int32",
		"Note sql.NullString",
		"SeenAt *time.Time",
		"Tier *sql.NullString",
	} {
		if !strings.Contains(squeezed, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// for-if-for-if-for, with conditions reaching the loop variables of enclosing loops.
func TestInferDeepNesting(t *testing.T) {
	src := "/*%for a in xs*/" +
		"/*%if a.enabled*/" +
		"/*%for b in a.ys*/p = sqlc.arg('b.label')" +
		"/*%if b.deep && a.enabled*/" +
		"/*%for c in b.zs*/q = sqlc.arg('c.name')/*%end*/" +
		"/*%end*//*%end*//*%end*//*%end*/"
	out := declare(t, src,
		exprtype.SQLParam{Name: "b.label", GoType: "string", NotNull: true},
		exprtype.SQLParam{Name: "c.name", GoType: "string", NotNull: true},
	)
	for _, want := range []string{
		"type QParams struct", "Xs []QX",
		"type QX struct", "Enabled bool", "Ys      []QXY",
		"type QXY struct", "Label string", "Deep  bool", "Zs    []QXYZ",
		"type QXYZ struct", "Name string",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// An inner loop reusing the name must not capture its own iterable.
func TestInferShadowedLoopVar(t *testing.T) {
	out := declare(t, "/*%for a in xs*//*%for a in a.ys*/p = sqlc.arg('a.name')/*%end*//*%end*/",
		exprtype.SQLParam{Name: "a.name", GoType: "string", NotNull: true})
	for _, want := range []string{"Xs []QX", "Ys []QXY", "Name string"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// A name is derived from the path that reaches a struct, which is lossy, so a collision falls
// back to the un-singularized path. Resolution depends only on first-seen order.
func TestInferNameCollision(t *testing.T) {
	out := declare(t,
		"/*%for g in groups*//*%for t in g.tags*/p = sqlc.arg('t.value')/*%end*//*%end*/"+
			"/*%for gt in groupTags*/q = sqlc.arg('gt.value')/*%end*/",
		exprtype.SQLParam{Name: "t.value", GoType: "string", NotNull: true},
		exprtype.SQLParam{Name: "gt.value", GoType: "string", NotNull: true},
	)
	for _, want := range []string{"Groups    []QGroup", "GroupTags []QGroupTags", "type QGroupTag struct", "type QGroupTags struct"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// A len() guard types itself as soon as the loop it guards binds something: the loop pins the
// slice, the marker pins the element. Refusal is for a collection referenced nowhere else.
func TestInferLenGuardResolvedByItsLoop(t *testing.T) {
	out := declare(t, "/*%if len(keywords) > 0*//*%for kw in keywords*/x = @kw/*%end*//*%end*/",
		exprtype.SQLParam{Name: "kw", GoType: "string", NotNull: true})
	if !strings.Contains(out, "Keywords []string") {
		t.Errorf("want Keywords []string, got:\n%s", out)
	}
}

func TestInferDiagnostics(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			name: "conflicting shapes for one variable",
			src:  "/*%if band == 'adult'*/a/*%end*//*%if band > 1*/b/*%end*/",
			want: "conflicting types",
		},
		{
			// len() accepts strings, slices and maps alike, so on its own it pins nothing.
			name: "a length is a constraint, not a type",
			src:  "/*%if len(keywords) > 0*/a/*%end*/",
			want: "has a length",
		},
		{
			// expr reads a['b'] as a.b, so only a computed subscript is a container lookup.
			name: "a computed subscript is a constraint, not a type",
			src:  "/*%if roles[i] == 'admin'*/a/*%end*/",
			want: "is a container",
		},
		{
			// The "in" operator accepts slices and maps alike.
			name: "membership is a constraint, not a type",
			src:  "/*%if 'admin' in roles*/a/*%end*/",
			want: "is a container",
		},
		{
			name: "a bare sign only proves numeric",
			src:  "/*%if -x*/a/*%end*/",
			want: "is numeric",
		},
		{
			name: "two variables with no literal to anchor them",
			src:  "/*%if a == b*/c/*%end*/",
			want: "cannot infer a type",
		},
		{
			name: "condition disagrees with sqlc",
			src:  "/*%if status > 5*/a = @status/*%end*/",
			want: "conflicting types",
		},
		{
			name: "a marker with no parameter",
			src:  "/*%for c in conds*/a = sqlc.arg('d.name')/*%end*/",
			want: "no sqlc parameter",
		},
		{
			name: "a function call in a condition",
			src:  "/*%if normalize(name) == 'x'*/a/*%end*/",
			want: "boolean gate",
		},
		{
			name: "a loop that binds nothing",
			src:  "/*%for c in conds*/a/*%end*/",
			want: "nothing inside the loop",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, diags := infer(t, c.src, exprtype.SQLParam{Name: "status", GoType: "string", NotNull: true})
			var joined []string
			for _, d := range diags {
				joined = append(joined, d.String())
			}
			if !strings.Contains(strings.Join(joined, "\n"), c.want) {
				t.Errorf("want a diagnostic containing %q, got %v", c.want, diags)
			}
		})
	}
}

func TestGoName(t *testing.T) {
	for in, want := range map[string]string{
		"status":        "Status",
		"department_id": "DepartmentID",
		"departmentId":  "DepartmentID",
		"min_age":       "MinAge",
		"api_url":       "APIURL",
		"id":            "ID",
	} {
		if got := exprtype.GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A condition and a marker may spell one name differently, and both have to reach one field —
// named after sqlc's own spelling, since that is the one the column has.
func TestInferJoinsTheSpellingsOfOneName(t *testing.T) {
	got, diags := infer(t, "/*%if minAge != null*/a = @min_age/*%end*/",
		exprtype.SQLParam{Name: "min_age", GoType: "int32", NotNull: true})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	members := got.Fields()
	if len(members) != 1 {
		t.Fatalf("fields = %v, want one", members)
	}
	if members[0].Name != "min_age" {
		t.Errorf("Name = %q, want the snake spelling for Go naming", members[0].Name)
	}
}

// The same rule for a parameter: an overridden type is rendered as written, even where
// nullability would otherwise add a pointer.
func TestInferLeavesAnOverriddenTypeAlone(t *testing.T) {
	out := declare(t, "/*%if minAge != null*/a = @min_age/*%end*/",
		exprtype.SQLParam{Name: "min_age", GoType: "pgtype.Timestamptz", Explicit: true})
	if !strings.Contains(out, "MinAge pgtype.Timestamptz") {
		t.Errorf("want the type as written, got:\n%s", out)
	}
}
