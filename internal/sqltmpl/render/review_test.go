package render_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
)

// This file pins the findings of an adversarial review of the engine.

// A loop element bound as a whole is the value, not the scope built from it. Converting is for
// the expression language's benefit — it resolves a member by exact name — and a bind that
// takes the element itself must not see the conversion. It did, and sent a map to the driver.
func TestElementBoundWholeKeepsItsValue(t *testing.T) {
	type row struct {
		Name string
	}
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		params any
		want   any
	}{
		{"a struct the driver understands", map[string]any{"xs": []time.Time{when}}, when},
		{"a null wrapper", map[string]any{"xs": []sql.NullString{{String: "a", Valid: true}}}, sql.NullString{String: "a", Valid: true}},
		{"a pointer to a struct", map[string]any{"xs": []*row{{Name: "a"}}}, &row{Name: "a"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, "select 1 where 1 = 0/*%for x in xs*/ or a = @x/*%end*/", c.params, dialect.PostgreSQL)
			if len(res.Args) != 1 {
				t.Fatalf("Args = %#v, want one", res.Args)
			}
			if !reflect.DeepEqual(res.Args[0], c.want) {
				t.Errorf("Args[0] = %#v (%T), want %#v", res.Args[0], res.Args[0], c.want)
			}
		})
	}
}

// Both readings of an element have to keep working: the whole value for a bind, and the fields
// for a condition and a dotted marker.
func TestElementReadBothWays(t *testing.T) {
	type cond struct {
		Enabled bool
		Name    string
	}
	res := build(t,
		"select 1/*%for c in conds*//*%if c.enabled*/ and n = sqlc.arg('c.name') and whole = @c/*%end*//*%end*/",
		map[string]any{"conds": []cond{{Enabled: true, Name: "ada"}}},
		dialect.PostgreSQL)
	if len(res.Args) != 2 {
		t.Fatalf("Args = %#v, want two", res.Args)
	}
	if res.Args[0] != "ada" {
		t.Errorf("Args[0] = %#v, want the field", res.Args[0])
	}
	if _, ok := res.Args[1].(cond); !ok {
		t.Errorf("Args[1] = %T, want the element itself", res.Args[1])
	}
}

// A nested loop reusing the name restores the outer element on the way out.
func TestShadowedElementIsRestored(t *testing.T) {
	res := build(t,
		"select 1/*%for x in outer*//*%for x in inner*/ i = @x/*%end*/ o = @x/*%end*/",
		map[string]any{"outer": []string{"O"}, "inner": []string{"I"}},
		dialect.PostgreSQL)
	want := []any{"I", "O"}
	if !reflect.DeepEqual(res.Args, want) {
		t.Errorf("Args = %#v, want %#v", res.Args, want)
	}
}

// A dollar-quoted string is opaque: it is valid PostgreSQL, and what looks like a marker
// inside one is text.
func TestDollarQuotedStringIsOpaque(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"an apostrophe needs no escape", "select $$it's fine$$"},
		{"a marker inside is text", "select $$ @notabind $$"},
		{"a call form inside is text", "select $tag$ sqlc.slice(ids) $tag$"},
		{"a dotted name inside is text", "select $$ @a.b $$"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, c.src, nil, dialect.PostgreSQL)
			if res.SQL != c.src {
				t.Errorf("SQL = %q, want it verbatim", res.SQL)
			}
			if len(res.Args) != 0 {
				t.Errorf("Args = %#v, want none", res.Args)
			}
		})
	}
	// A placeholder still works beside one.
	res := build(t, "select $$x$$, a = @a", map[string]any{"a": 1}, dialect.PostgreSQL)
	if res.SQL != "select $$x$$, a = $1" {
		t.Errorf("SQL = %q", res.SQL)
	}
}

// A backslash escapes a quote in MySQL's default mode, so the literal is one span rather than
// an unterminated one.
func TestBackslashEscapeInAQuotedString(t *testing.T) {
	res := build(t, `select 1 where name = 'O\'Brien' and x = sqlc.arg(x)`,
		map[string]any{"x": 1}, dialect.MySQL)
	if !strings.HasSuffix(res.SQL, "and x = ?") {
		t.Errorf("SQL = %q, want the literal to close and the marker to bind", res.SQL)
	}
	if len(res.Args) != 1 {
		t.Errorf("Args = %#v, want one", res.Args)
	}
}

// A near-miss spelling of a directive is refused, not read as something else. `/*%else if b*/`
// silently became an unconditional else, dropping the condition.
func TestNearMissDirectivesAreRefused(t *testing.T) {
	for _, src := range []string{
		"select 1 /*%if a*/A/*%else if b*/B/*%end*/",
		"select 1 /*%if a*/A/*%else whatever*/B/*%end*/",
		"select 1 /*%if a*/A/*%end of if*/",
	} {
		t.Run(src, func(t *testing.T) {
			if _, err := parse(t, src); err == nil {
				t.Error("want an error for a directive with trailing text")
			}
		})
	}
}

// A named byte-slice type is still a scalar; only a real list is expanded.
func TestNamedByteSliceIsAScalar(t *testing.T) {
	type raw []byte
	res := build(t, "select 1 where id in (sqlc.slice(b))",
		map[string]any{"b": raw("ab")}, dialect.PostgreSQL)
	if res.SQL != "select 1 where id in ($1)" {
		t.Errorf("SQL = %q, want one placeholder", res.SQL)
	}
	if len(res.Args) != 1 {
		t.Errorf("Args = %#v, want the slice bound whole", res.Args)
	}
}

// Two fields that fold onto one key cannot both be reached, so the ambiguity is reported
// rather than resolved by declaration order.
func TestFoldedFieldNamesCollide(t *testing.T) {
	params := struct {
		UserID int64
		UserId string
	}{1, "second"}
	if _, err := buildErr(t, "select sqlc.arg('user_id')", params, dialect.PostgreSQL); err == nil {
		t.Error("want an error naming the ambiguity between UserID and UserId")
	}
}

// An embedded struct's fields are reachable, and by the same spellings as a direct field's —
// a condition and a marker must not disagree about a name.
func TestEmbeddedFieldsAreReachable(t *testing.T) {
	type common struct{ TenantID int64 }
	type params struct {
		common
		Name string
	}
	res := build(t, "select 1 where 1 = 1/*%if tenantId != null*/ and t = sqlc.arg('tenant_id')/*%end*/",
		params{common{7}, "x"}, dialect.PostgreSQL)
	if !strings.Contains(res.SQL, "and t = $1") {
		t.Errorf("SQL = %q, want the embedded field to satisfy both the condition and the marker", res.SQL)
	}
	if len(res.Args) != 1 || res.Args[0] != int64(7) {
		t.Errorf("Args = %#v, want [7]", res.Args)
	}
}

// A trailing run of capitals is one word, so the snake spelling a condition might use exists.
func TestSnakeSplitsATrailingInitialism(t *testing.T) {
	params := struct {
		HTTPURL string
		UserID  int64
	}{"x", 1}
	for _, c := range []struct{ name, cond string }{
		{"snake", "http_url != null"},
		{"camel", "httpURL != null"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, "select 1 where 1 = 1/*%if "+c.cond+"*/ and u = sqlc.arg('http_url')/*%end*/",
				params, dialect.PostgreSQL)
			if !strings.Contains(res.SQL, "and u = $1") {
				t.Errorf("SQL = %q, want the condition to see the field", res.SQL)
			}
		})
	}
}

// A nil scope is an empty scope, as a nil params already is; a condition against it is false,
// not an error.
func TestNilScopeBehavesLikeAnEmptyOne(t *testing.T) {
	const src = "select 1 where 1 = 1/*%if flag*/ and a = sqlc.arg(a)/*%end*/"
	for _, c := range []struct {
		name   string
		params any
	}{
		{"nil", nil},
		{"a nil map", map[string]any(nil)},
		{"an empty map", map[string]any{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, src, c.params, dialect.PostgreSQL)
			if res.SQL != "select 1 where 1 = 1" {
				t.Errorf("SQL = %q, want the branch skipped", res.SQL)
			}
		})
	}
}

// A self-referential pointer type must not spin forever unwrapping.
func TestSelfReferentialPointerTerminates(t *testing.T) {
	type selfPtr *int
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = buildErr(t, "select 1", new(selfPtr), dialect.PostgreSQL)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Build did not return; the pointer unwrapping is uncapped")
	}
}

// An operator ending in @ is not a marker, wherever it appears in a statement. This is the
// end-to-end form of the recognizer's rule: what matters is that the rendered SQL keeps the
// operator and binds nothing extra.
func TestOperatorsEndingInAtSurvive(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"containment against an array", "select id from t where array['a']::text[] <@ tags"},
		{"full-text search", "select id from t where to_tsvector(note) @@ to_tsquery('x')"},
		{"jsonb containment", `select id from t where data @> '{"a":1}'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, c.src, nil, dialect.PostgreSQL)
			if res.SQL != c.src {
				t.Errorf("SQL\n got: %q\nwant: %q", res.SQL, c.src)
			}
			if len(res.Args) != 0 {
				t.Errorf("Args = %#v, want none", res.Args)
			}
		})
	}
	// A real marker beside one still binds.
	res := build(t, "select id from t where tags <@ @all and s = @s",
		map[string]any{"all": "{a}", "s": "x"}, dialect.PostgreSQL)
	if res.SQL != "select id from t where tags <@ $1 and s = $2" {
		t.Errorf("SQL = %q", res.SQL)
	}
}

// MySQL's # comment runs to the end of the line, so a marker inside one is text.
func TestHashCommentIsAComment(t *testing.T) {
	res := build(t, "select id from t # sqlc.arg(zzz)\nwhere s = sqlc.arg(s)",
		map[string]any{"s": 1}, dialect.MySQL)
	if res.SQL != "select id from t # sqlc.arg(zzz)\nwhere s = ?" {
		t.Errorf("SQL = %q, want the comment left alone", res.SQL)
	}
	if len(res.Args) != 1 {
		t.Errorf("Args = %#v, want one", res.Args)
	}
	// In PostgreSQL # is an operator, so the same text is not a comment there.
	if got := build(t, "select 1 # 2", nil, dialect.PostgreSQL).SQL; got != "select 1 # 2" {
		t.Errorf("SQL = %q", got)
	}
}
