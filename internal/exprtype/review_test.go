package exprtype_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
)

// This file pins the findings of an adversarial review. Each test names what was wrong and
// what makes it wrong, because the failure mode they share is the one this package exists to
// prevent: a type that is wrong rather than refused, or a variable that vanishes.
//
// A vanished variable is the worse of the two. The plugin turns any diagnostic into a hard
// error, so a false refusal is an annoyance; a *missing* refusal means the generated params
// struct has no field for a name the template reads, and the condition silently sees nil.

// Every variable a condition mentions has to reach the params struct, however deeply it is
// buried in an expression the walker cannot reduce to a place.
//
// The assertion is structural — the field is looked up in the shape, and a diagnostic is matched
// by its Path — because the first version of this test compared against rendered text and every
// case but one passed for the wrong reason: the wanted names were single letters, and the advice
// appended to a refusal happens to contain every letter of the alphabet worth wanting. Reverting
// all three fixes it was written for left it green.
func TestNoVariableIsSilentlyDropped(t *testing.T) {
	for _, c := range []struct {
		name string
		cond string
		want []string // variables that must be declared, or named by a diagnostic
	}{
		{"a non-literal element of a literal set", "status in ['a', alpha]", []string{"alpha"}},
		{"an operand of a sign", "-(alpha + beta)", []string{"alpha", "beta"}},
		{"a doubled sign", "--alpha", []string{"alpha"}},
		{"an argument of a nested builtin", "len(len(alpha))", []string{"alpha"}},
		{"an operand of a chained nil test", "alpha == null == null", []string{"alpha"}},
		{"a member of a call's result", "map(alpha, #.b)[0] == 'x'", []string{"alpha"}},
		{"a member of a coalesce", "(alpha ?? beta).c", []string{"alpha", "beta"}},
		{"an element of a literal array", "[alpha, beta][0]", []string{"alpha", "beta"}},
		{"an argument of a predicate builtin", "find(alpha, # > 1) == nil", []string{"alpha"}},
		{"both sides of an arithmetic comparison", "alpha * 2 > beta", []string{"alpha", "beta"}},
		{"a nested member", "alpha.one.two == 'x'", []string{"alpha"}},
		{"an index", "alpha[beta] == 'x'", []string{"alpha", "beta"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, diags := infer(t, "/*%if "+c.cond+"*/y/*%end*/")
			for _, w := range c.want {
				// Either the variable is in the shape, or a diagnostic is about it. What must
				// not happen is neither: then the generated struct has no field for a name the
				// condition reads, and the branch silently sees nil.
				if hasField(got, w) || namedByADiagnostic(diags, w) {
					continue
				}
				t.Errorf("%q is neither declared nor diagnosed\nfields: %v\ndiags: %v",
					w, fieldNames(got), diags)
			}
		})
	}
}

// hasField reports whether the shape declares a top-level field of this name.
func hasField(t *exprtype.Type, name string) bool {
	for _, m := range t.Fields() {
		if m.Name == name {
			return true
		}
	}
	return false
}

// namedByADiagnostic reports whether a diagnostic is about this variable, by its path rather
// than by whether the message happens to contain the letters.
func namedByADiagnostic(diags []exprtype.Diagnostic, name string) bool {
	for _, d := range diags {
		head, _, _ := strings.Cut(d.Path, ".")
		if strings.TrimSuffix(head, "[]") == name {
			return true
		}
	}
	return false
}

func fieldNames(t *exprtype.Type) []string {
	var out []string
	for _, m := range t.Fields() {
		out = append(out, m.Name)
	}
	return out
}

// An equality only pins the two sides to each other. Copying a concrete Go type across one
// whose shape disagrees produces a type that compiles and is wrong.
func TestAliasDoesNotStampAnIncompatibleType(t *testing.T) {
	for _, c := range []struct{ name, cond, param, goType string }{
		{"string against an int column", "a == 'x' && a == n", "n", "int64"},
		{"int against a time column", "a > 1 && a == ts", "ts", "time.Time"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, diags := infer(t, "/*%if "+c.cond+"*/y = @"+c.param+"/*%end*/",
				exprtype.SQLParam{Name: c.param, GoType: c.goType, NotNull: true})
			exprtype.NameQuery(got, "Q")
			out := exprtype.Declare(got)
			if len(diags) == 0 {
				t.Errorf("want a conflict, got none:\n%s", out)
			}
			if strings.Contains(out, "A "+c.goType) {
				t.Errorf("a took the column's type despite disagreeing:\n%s", out)
			}
		})
	}
}

// Two incompatible pins joined by an equality is a conflict, not something to fill in around.
func TestAliasReportsADisagreement(t *testing.T) {
	_, diags := infer(t, "/*%if a == 'x'*/y/*%end*//*%if b > 1*/z/*%end*//*%if a == b*/w/*%end*/")
	if len(diags) == 0 {
		t.Fatal("want a diagnostic: a is a string and b is an int, so a == b cannot hold")
	}
	if !strings.Contains(diags[0].String(), "compared with each other") {
		t.Errorf("diagnostic = %q, want it to name the comparison", diags[0])
	}
}

// Ordering and arithmetic accept a mixed int/float pair in expr, so they prove nothing about
// the operands sharing a type.
func TestOrderingDoesNotAliasTypes(t *testing.T) {
	got, diags := infer(t, "/*%if a > 1 && a < b*/y/*%end*/")
	exprtype.NameQuery(got, "Q")
	out := exprtype.Declare(got)
	if strings.Contains(out, "B int64") {
		t.Errorf("b took a's type from an ordering comparison:\n%s", out)
	}
	if len(diags) == 0 {
		t.Errorf("want b refused, got none:\n%s", out)
	}
}

// A scalar sqlc named cannot replace a shape something else proved.
func TestSqlcTypeDoesNotFlattenAShape(t *testing.T) {
	t.Run("a slice a loop iterates", func(t *testing.T) {
		_, diags := infer(t, "/*%for x in xs*/p = @x_name/*%end*/ q = @xs",
			exprtype.SQLParam{Name: "x_name", GoType: "string", NotNull: true},
			exprtype.SQLParam{Name: "xs", GoType: "time.Time", NotNull: true})
		if len(diags) == 0 {
			t.Error("want a conflict: xs is iterated and also bound as a scalar")
		}
	})
	t.Run("a struct a member access reached", func(t *testing.T) {
		_, diags := infer(t, "/*%if a.b*/p/*%end*/ q = @a",
			exprtype.SQLParam{Name: "a", GoType: "time.Time", NotNull: true})
		if len(diags) == 0 {
			t.Error("want a conflict: a has a member and is also bound as a scalar")
		}
	})
}

// Reading a member proves the base is a struct, so doing it to a slice or a scalar is a
// conflict rather than a value filed under a shape nothing reads back.
func TestMemberAccessOnANonStruct(t *testing.T) {
	for _, c := range []struct {
		name, src string
		params    []exprtype.SQLParam
	}{
		{
			name:   "on a slice",
			src:    "/*%for x in xs*/p = @x/*%end*//*%if xs.name == 'a'*/y/*%end*/",
			params: []exprtype.SQLParam{{Name: "x", GoType: "string", NotNull: true}},
		},
		{
			name: "on a bool",
			src:  "/*%if flag*/p/*%end*//*%if flag.x == 'a'*/y/*%end*/",
		},
		{
			name:   "on a column's type",
			src:    "p = @status /*%if status.x == 'a'*/y/*%end*/",
			params: []exprtype.SQLParam{{Name: "status", GoType: "string", NotNull: true}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, diags := infer(t, c.src, c.params...)
			if len(diags) == 0 {
				t.Error("want a conflict for member access on a non-struct")
			}
		})
	}
}

// A struct with no fields says nothing about what it holds, so it is not a decided type.
func TestEmptyStructIsRefused(t *testing.T) {
	for _, src := range []string{
		"/*%if a.x*/p/*%end*//*%if a == b*/q/*%end*/",
		"/*%if a == a.b*/y/*%end*/",
	} {
		t.Run(src, func(t *testing.T) {
			// The refusal is the contract: a diagnostic aborts codegen, so what the printer
			// would have written never reaches a file.
			if _, diags := infer(t, src); len(diags) == 0 {
				t.Error("want a refusal for a struct nothing described")
			}
		})
	}
}

// A Go struct value is never nil, so a nil test on one has to be able to be nil.
func TestNilTestedStructIsAPointer(t *testing.T) {
	got, diags := infer(t, "/*%if a != null && a.b*/y/*%end*/")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	exprtype.NameQuery(got, "Q")
	if out := exprtype.Declare(got); !strings.Contains(out, "A *QA") {
		t.Errorf("want a pointer so the nil test can be false:\n%s", out)
	}
}

// Two parameters that fold onto one key cannot both be reached by a marker.
func TestFoldedParameterNamesCollide(t *testing.T) {
	_, diags := infer(t, "p = @min_age q = @minAge",
		exprtype.SQLParam{Name: "min_age", GoType: "int32", NotNull: true},
		exprtype.SQLParam{Name: "minAge", GoType: "string"})
	if len(diags) == 0 {
		t.Fatal("want a diagnostic: min_age and minAge fold together")
	}
	if !strings.Contains(diags[0].String(), "collides") {
		t.Errorf("diagnostic = %q, want it to name the collision", diags[0])
	}
}

// A name that cannot be an exported Go identifier is reported against the template's own
// spelling, not left to fail as a syntax error inside generated code.
func TestInvalidGoNamesAreReported(t *testing.T) {
	// An empty name is refused earlier still, by the marker recognizer, so it is bind's to test.
	for _, name := range []string{"a-b", "2fa", "_"} {
		t.Run("name="+name, func(t *testing.T) {
			_, diags := infer(t, "p = sqlc.arg('"+name+"')",
				exprtype.SQLParam{Name: name, GoType: "string", NotNull: true})
			var joined string
			for _, d := range diags {
				joined += d.String() + "\n"
			}
			if !strings.Contains(joined, "exported Go identifier") {
				t.Errorf("want the name refused, got: %v", diags)
			}
		})
	}
}

// Titling reads a rune, not a byte, so a name starting with a multi-byte character survives.
func TestGoNameDoesNotSplitARune(t *testing.T) {
	for in, want := range map[string]string{
		"état":   "État",
		"日本語":    "日本語",
		"status": "Status",
	} {
		if got := exprtype.GoName(in); got != want {
			t.Errorf("GoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidName(t *testing.T) {
	for in, want := range map[string]bool{
		"status": true, "min_age": true, "a1": true, "_x": true,
		"a-b": false, "2fa": false, "_": false, "": false, "$env": false,
	} {
		if got := exprtype.ValidName(in); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", in, got, want)
		}
	}
}

// The fallback of a coalesce is what says what the expression is.
func TestCoalesceReadsItsFallback(t *testing.T) {
	for _, c := range []struct{ cond, want string }{
		{"x ?? false", "X *bool"},
		{"(x ?? 'a') == 'b'", "X *string"},
	} {
		t.Run(c.cond, func(t *testing.T) {
			got, diags := infer(t, "/*%if "+c.cond+"*/y/*%end*/")
			exprtype.NameQuery(got, "Q")
			out := exprtype.Declare(got)
			if len(diags) != 0 {
				t.Fatalf("unexpected diagnostics: %v\n%s", diags, out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// A marker spelling the loop variable differently still reaches the element.
func TestLoopVariableFoldsLikeEverythingElse(t *testing.T) {
	out := declare(t, "/*%for minAge in xs*/p = @min_age/*%end*/",
		exprtype.SQLParam{Name: "min_age", GoType: "int32", NotNull: true})
	if !strings.Contains(out, "Xs []int32") {
		t.Errorf("want Xs []int32, got:\n%s", out)
	}
}

// null is the nil literal wherever it appears, not a variable to invent a field for.
func TestNullIsNeverAField(t *testing.T) {
	got, _ := infer(t, "/*%if null == null*/y/*%end*/")
	for _, m := range got.Fields() {
		if strings.EqualFold(m.Name, "null") {
			t.Errorf("a field named %q was invented", m.Name)
		}
	}
}

// Only a slice's element is de-pluralized; a struct field is named after itself.
func TestOnlySliceElementsAreSingularized(t *testing.T) {
	out := declare(t, "/*%if status.code == 'x' && settings.on*/y/*%end*//*%for c in conds*/p = @c/*%end*/",
		exprtype.SQLParam{Name: "c", GoType: "string", NotNull: true})
	for _, bad := range []string{"type QStatu struct", "type QSetting struct"} {
		if strings.Contains(out, bad) {
			t.Errorf("a struct field was de-pluralized (%q):\n%s", bad, out)
		}
	}
	for _, want := range []string{"type QStatus struct", "type QSettings struct", "Conds    []string"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// An override decides the type outright, and that has to survive an alias.
func TestOverrideSurvivesAnAlias(t *testing.T) {
	out := declare(t, "/*%if a != null && a == ov*/y = @ov/*%end*/",
		exprtype.SQLParam{Name: "ov", GoType: "pgtype.Timestamptz", Explicit: true, NotNull: true})
	if strings.Contains(out, "*pgtype.Timestamptz") {
		t.Errorf("the override was decorated with a pointer:\n%s", out)
	}
}

// sqlc's own type wins over what a condition assumed only when the two can agree. A named type
// — an enum, a nullable wrapper, an override — does not compare equal to a literal at run time,
// so a condition that compares one is a branch that never fires.
//
// This used to depend on the order the two facts arrived in, and a condition is always walked
// before the bind it guards, so the silent direction was the usual one.
func TestSqlcsTypeAndAConditionMustAgree(t *testing.T) {
	for _, c := range []struct{ name, src, goType string }{
		{"a condition before the bind", "/*%if a == 'x'*/ b = @a /*%end*/", "sql.NullString"},
		{"a bind before the condition", "b = @a/*%if a == 'x'*/ c /*%end*/", "sql.NullString"},
		{"an enum", "/*%if a == 'happy'*/ b = @a /*%end*/", "Mood"},
		{"a numeric wrapper", "/*%if a > 10*/ b = @a /*%end*/", "pgtype.Numeric"},
		{"a boolean gate on a wrapper", "/*%if a*/ b = @a /*%end*/", "sql.NullBool"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, diags := infer(t, c.src, exprtype.SQLParam{Name: "a", GoType: c.goType})
			if len(diags) == 0 {
				t.Fatalf("want a diagnostic: %s cannot be compared the way the condition does", c.goType)
			}
			if !strings.Contains(diags[0].Msg, c.goType) {
				t.Errorf("diagnostic = %v, want it to name the type sqlc chose", diags[0])
			}
		})
	}
}

// The nil test is the case that must keep working: it says nothing about the shape, so a
// nullable type is free to be whatever sqlc chose.
func TestANilTestAgreesWithAnyType(t *testing.T) {
	for _, goType := range []string{"sql.NullString", "pgtype.Timestamptz", "Mood", "int32"} {
		_, diags := infer(t, "/*%if a != null*/ b = @a /*%end*/",
			exprtype.SQLParam{Name: "a", GoType: goType})
		if len(diags) != 0 {
			t.Errorf("%s: unexpected diagnostics: %v", goType, diags)
		}
	}
}

// A template can have directives and no variables at all. Refusing an empty params struct
// refused that, with a message whose path was empty.
func TestATemplateWithNoVariablesIsFine(t *testing.T) {
	got, diags := infer(t, "/*%if true*/ and status = 'active' /*%end*/")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(got.Fields()) != 0 {
		t.Errorf("fields = %v, want none", fieldNames(got))
	}
	// A variable that nothing decides is still refused; the root's emptiness is the only case
	// that became legal.
	// Two unknowns compared with each other decide nothing.
	if _, diags := infer(t, "/*%if a == b*/x/*%end*/"); len(diags) == 0 {
		t.Error("want an undecided variable still refused")
	}
}

// A name that folds onto one of the expression language's own functions resolves to the
// function, not to the scope: the generated code would compile and then fail on every call.
func TestABuiltinNameIsRefused(t *testing.T) {
	for _, name := range []string{"filter", "len", "map", "sortBy", "sort_by", "Filter"} {
		_, diags := infer(t, "/*%if "+name+"*/x/*%end*/")
		if len(diags) == 0 {
			t.Errorf("%s: want it refused as a name the expression language has taken", name)
		}
	}
	// A name that merely contains one is fine.
	if _, diags := infer(t, "/*%if lengthy*/x/*%end*/"); len(diags) != 0 {
		t.Errorf("lengthy: unexpected diagnostics: %v", diags)
	}
	// And so is a member of that name: only the top level is resolved against the builtins.
	if _, diags := infer(t, "/*%if a.filter*/x/*%end*/"); len(diags) != 0 {
		t.Errorf("a.filter: unexpected diagnostics: %v", diags)
	}
}

// sqlc has already chosen a type that expresses absence for a nullable column, so a pointer on
// top of it made the dynamic query's params differ from the static ones beside them — up to
// **string. A pointer is added only where absence has nowhere else to live.
func TestAPointerIsAddedOnlyWhereItIsNeeded(t *testing.T) {
	for _, c := range []struct{ name, src, goType, want string }{
		{"nullable, no nil test", "/*%if g*/a = @a/*%end*/", "sql.NullString", "A sql.NullString"},
		{"nullable and already a pointer", "/*%if g*/a = @a/*%end*/", "*string", "A *string"},
		{"not null, nil-tested", "/*%if a != null*/a = @a/*%end*/", "int32", "A *int32"},
		{"nullable, nil-tested", "/*%if a != null*/a = @a/*%end*/", "sql.NullString", "A *sql.NullString"},
		{"a pointer, nil-tested", "/*%if a != null*/a = @a/*%end*/", "*string", "A *string"},
		// sqlc's type for a slice parameter arrives with one [] already taken off, because the
		// marker is what says the bind expands.
		{"a slice needs no pointer", "a in (sqlc.slice(a))", "int64", "A []int64"},
	} {
		t.Run(c.name, func(t *testing.T) {
			notNull := !strings.Contains(c.name, "nullable") && !strings.Contains(c.name, "a pointer,")
			out := declare(t, c.src, exprtype.SQLParam{Name: "a", GoType: c.goType, NotNull: notNull})
			if !strings.Contains(strings.Join(strings.Fields(out), " "), c.want) {
				t.Errorf("want %q in:\n%s", c.want, out)
			}
		})
	}
}
