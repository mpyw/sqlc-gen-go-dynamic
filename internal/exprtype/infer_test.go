package exprtype

import (
	"strings"
	"testing"
)

// searchUsers is the directive tree of the template that was actually run through
// sqlc v1.31.1, with the parameter table sqlc returned for it.
func searchUsers() (*Node, []SQLParam) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: If, Cond: "activeOnly", Binds: []string{"status"}},
		{Kind: If, Cond: "departmentId != null", Binds: []string{"department_id"}},
		{Kind: If, Cond: "ageBand == 'adult'", Binds: []string{"min_age"}},
		{Kind: ElseIf, Cond: "ageBand == 'senior'", Binds: []string{"senior_age"}},
		{Kind: Else},
		{Kind: If, Cond: "ids != null", Binds: []string{"ids"}},
		{Kind: For, Var: "c", Iter: "conds", Binds: []string{"c.name", "c.status"}},
		{Kind: For, Var: "g", Iter: "groups", Children: []*Node{
			{Kind: For, Var: "t", Iter: "g.tags", Binds: []string{"t.value"}},
		}},
		{Kind: If, Cond: "byName"},
	}}
	params := []SQLParam{
		{Name: "status", GoType: "string", NotNull: true},
		{Name: "department_id", GoType: "int64"},
		{Name: "min_age", GoType: "int32", NotNull: true},
		{Name: "senior_age", GoType: "int32", NotNull: true},
		{Name: "ids", GoType: "int64", NotNull: true, Slice: true},
		{Name: "c.name", GoType: "string", NotNull: true},
		{Name: "c.status", GoType: "string", NotNull: true},
		{Name: "t.value", GoType: "string", NotNull: true},
	}
	return root, params
}

func TestInferSearchUsers(t *testing.T) {
	root, params := searchUsers()
	got, diags := Infer(root, params)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "SearchUsers")
	out := Declare(got)
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

// A loop whose single bind is the loop variable itself yields a slice of scalars,
// not a slice of one-field structs.
func TestInferScalarLoop(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: For, Var: "kw", Iter: "keywords", Binds: []string{"kw"}},
	}}
	got, diags := Infer(root, []SQLParam{{Name: "kw", GoType: "string", NotNull: true}})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	if out := Declare(got); !strings.Contains(out, "Keywords []string") {
		t.Errorf("want Keywords []string, got:\n%s", out)
	}
}

func TestInferConditionShapes(t *testing.T) {
	cases := []struct {
		name string
		cond string
		want string // "<field> <gotype>"
	}{
		{"bare identifier is a bool", "activeOnly", "ActiveOnly bool"},
		{"negation is a bool", "!disabled", "Disabled bool"},
		{"string comparison", "ageBand == 'adult'", "AgeBand string"},
		{"either operand order", "'adult' == ageBand", "AgeBand string"},
		{"int comparison", "minAge > 18", "MinAge int64"},
		{"float comparison", "ratio >= 0.5", "Ratio float64"},
		{"nil check only marks optional", "note != null", ""},
		{"boolean operators recurse", "activeOnly && !disabled", "ActiveOnly bool"},
		{"disjunction over one variable", "ageBand == 'adult' || ageBand == 'senior'", "AgeBand string"},
		{"string builtin", "name matches '^a'", "Name string"},
		{"member access builds a struct", "user.role == 'admin'", "User QUser"},
		{"integer indexing pins a slice", "roles[0] == 'admin'", "Roles []string"},
		{"negative literals are literals", "minAge > -1", "MinAge int64"},
		{"a literal set pins the element", "tier in ['a', 'b']", "Tier string"},
		{"arithmetic carries the literal through", "x * 2 > 10", "X int64"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, diags := Infer(&Node{Kind: Root, Children: []*Node{{Kind: If, Cond: c.cond}}}, nil)
			NameQuery(got, "Q")
			out := Declare(got)
			t.Logf("%s\n%s\ndiags=%v", c.cond, out, diags)
			if c.want == "" {
				return // shape is deliberately undecidable; see TestInferDiagnostics
			}
			if len(diags) != 0 {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

func TestInferOptionalFromNilCheck(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		// The nil check makes it optional; the bind gives it a type. Note the bind
		// sits at top level, so optionality comes only from the expression.
		{Kind: If, Cond: "note != null"},
	}}
	root.Binds = []string{"note"}
	got, diags := Infer(root, []SQLParam{{Name: "note", GoType: "string", NotNull: true}})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	if out := Declare(got); !strings.Contains(out, "Note *string") {
		t.Errorf("want Note *string, got:\n%s", out)
	}
}

// Optionality has exactly two sources, and being inside a branch is not one of
// them: when the branch does not render, nothing reads the value.
func TestInferOptionalitySources(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		// Inside a conditional, but the condition tests a different variable and sqlc
		// says NOT NULL: a plain value.
		{Kind: If, Cond: "activeOnly", Binds: []string{"status"}},
		// Nil-tested, so unset must be distinguishable from zero even though sqlc
		// reports the column as NOT NULL.
		{Kind: If, Cond: "minAge != null", Binds: []string{"min_age"}},
		// Nullable per sqlc — whether from sqlc.narg or from a nullable column, which
		// the request does not distinguish — so a nullable Go type, as sqlc's own Go
		// codegen would emit.
		{Kind: If, Cond: "withNote", Binds: []string{"note"}},
	}}
	got, diags := Infer(root, []SQLParam{
		{Name: "status", GoType: "string", NotNull: true},
		{Name: "min_age", GoType: "int32", NotNull: true},
		{Name: "note", GoType: "string"},
	})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	out := Declare(got)
	squeezed := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"Status string", "MinAge *int32", "Note *string"} {
		if !strings.Contains(squeezed, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

func TestInferDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		root *Node
		want string
	}{
		{
			name: "conflicting shapes for one variable",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "band == 'adult'"},
				{Kind: If, Cond: "band > 1"},
			}},
			want: "conflicting types",
		},
		{
			// len() accepts strings, slices and maps alike, so on its own it pins
			// nothing at all.
			name: "a length is a constraint, not a type",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "len(keywords) > 0"},
			}},
			want: "has a length",
		},
		{
			// expr represents a['b'] exactly as a.b, so a string subscript is field
			// access and only a computed one is genuinely a container lookup.
			name: "a computed subscript is a constraint, not a type",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "roles[i] == 'admin'"},
			}},
			want: "is a container",
		},
		{
			// The "in" operator accepts slices and maps alike.
			name: "membership is a constraint, not a type",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "'admin' in roles"},
			}},
			want: "is a container",
		},
		{
			name: "a bare sign only proves numeric",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "-x"},
			}},
			want: "is numeric",
		},
		{
			name: "two variables with no literal to anchor them",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "a == b"},
			}},
			want: "cannot infer a type",
		},
		{
			name: "condition disagrees with sqlc",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "status > 5", Binds: []string{"status"}},
			}},
			want: "conflicting types",
		},
		{
			name: "bind under an unknown loop variable",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: For, Var: "c", Iter: "conds", Binds: []string{"d.name"}},
			}},
			want: "no sqlc parameter",
		},
		{
			name: "function call in a condition",
			root: &Node{Kind: Root, Children: []*Node{
				{Kind: If, Cond: "normalize(name) == 'x'"},
			}},
			want: "boolean gate",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params := []SQLParam{{Name: "status", GoType: "string", NotNull: true}}
			_, diags := Infer(c.root, params)
			t.Logf("diags=%v", diags)
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
		if got := GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// for-if-for-if-for, with conditions that reach loop variables of enclosing loops.
func TestInferDeepNesting(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: For, Var: "a", Iter: "xs", Children: []*Node{
			{Kind: If, Cond: "a.enabled", Children: []*Node{
				{Kind: For, Var: "b", Iter: "a.ys", Binds: []string{"b.label"}, Children: []*Node{
					{Kind: If, Cond: "b.deep && a.enabled", Children: []*Node{
						{Kind: For, Var: "c", Iter: "b.zs", Binds: []string{"c.name"}},
					}},
				}},
			}},
		}},
	}}
	got, diags := Infer(root, []SQLParam{
		{Name: "b.label", GoType: "string", NotNull: true},
		{Name: "c.name", GoType: "string", NotNull: true},
	})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	t.Logf("\n%s", Declare(got))
}

// The same loop variable name reused by an inner loop must not capture the
// iterable expression of that very loop.
func TestInferShadowedLoopVar(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: For, Var: "a", Iter: "xs", Children: []*Node{
			{Kind: For, Var: "a", Iter: "a.ys", Binds: []string{"a.name"}},
		}},
	}}
	got, diags := Infer(root, []SQLParam{{Name: "a.name", GoType: "string", NotNull: true}})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	t.Logf("\n%s", Declare(got))
}

// Two distinct paths whose generated names coincide.
func TestInferNameCollision(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: For, Var: "g", Iter: "groups", Children: []*Node{
			{Kind: For, Var: "t", Iter: "g.tags", Binds: []string{"t.value"}},
		}},
		{Kind: For, Var: "gt", Iter: "groupTags", Binds: []string{"gt.value"}},
	}}
	got, diags := Infer(root, []SQLParam{
		{Name: "t.value", GoType: "string", NotNull: true},
		{Name: "gt.value", GoType: "string", NotNull: true},
	})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	t.Logf("\n%s", Declare(got))
}

// A len() guard is the common shape, and it types itself as soon as the loop it
// guards binds something: the loop pins the slice, the bind pins the element.
// Refusal is reserved for a collection that really is referenced nowhere else.
func TestInferLenGuardResolvedByItsLoop(t *testing.T) {
	root := &Node{Kind: Root, Children: []*Node{
		{Kind: If, Cond: "len(keywords) > 0", Children: []*Node{
			{Kind: For, Var: "kw", Iter: "keywords", Binds: []string{"kw"}},
		}},
	}}
	got, diags := Infer(root, []SQLParam{{Name: "kw", GoType: "string", NotNull: true}})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	NameQuery(got, "Q")
	if out := Declare(got); !strings.Contains(out, "Keywords []string") {
		t.Errorf("want Keywords []string, got:\n%s", out)
	}
}
