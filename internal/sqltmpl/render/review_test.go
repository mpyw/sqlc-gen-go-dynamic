package render_test

import (
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprlang"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/render"
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

// An operator ending in @ is not a marker, wherever it appears in a statement. This is the
// end-to-end form of the recognizer's rule: what matters is that the rendered SQL keeps the
// operator and binds nothing extra.
func TestOperatorsEndingInAtSurvive(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"containment against an array", "select id from t where array['a']::text[] <@ tags"},
		{"full-text search", "select id from t where to_tsvector(note) @@ to_tsquery('x')"},
		{"jsonb containment", `select id from t where data @> '{"a":1}'`},
		// No space after the operator: only the preceding byte can tell these apart, which is
		// what the rule reads. Without it each of these invents a parameter.
		{"unspaced containment", "select id from t where tags <@tags2"},
		{"unspaced full-text search", "select id from t where to_tsvector(note) @@query"},
		{"unspaced jsonb containment", "select id from t where data @>path"},
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

// A loop variable is a name like any other, so it is folded on both sides. It was not: the
// element went into the scope under the raw spelling while every lookup folded, so any loop
// variable that was not already lowercase-without-underscores was invisible inside its own
// body — and the compile-time inference folds it, so codegen accepted such templates happily.
func TestLoopVariableIsFoldedOnBothSides(t *testing.T) {
	type cond struct {
		Kind  string
		Value string
	}
	params := map[string]any{"conds": []cond{{Kind: "name", Value: "ada"}}}
	for _, v := range []string{"c", "myCond", "my_cond", "MyCond", "MYCOND"} {
		t.Run(v, func(t *testing.T) {
			src := "select 1 where 1 = 0" +
				"/*%for " + v + " in conds*/" +
				"/*%if " + v + ".kind == 'name'*/ or n = sqlc.arg('" + v + ".value')/*%end*/" +
				"/*%end*/"
			res := build(t, src, params, dialect.PostgreSQL)
			if res.SQL != "select 1 where 1 = 0 or n = $1" {
				t.Errorf("SQL = %q, want the condition and the marker to reach the element", res.SQL)
			}
			if len(res.Args) != 1 || res.Args[0] != "ada" {
				t.Errorf("Args = %#v, want [ada]", res.Args)
			}
		})
	}
}

// A loop variable shadows a parameter of the same name for the length of its body, whatever
// spelling either uses. When the element was keyed unfolded, the condition read the parameter
// while the marker read the element — the two disagreed about one name.
func TestLoopVariableShadowsAParameter(t *testing.T) {
	type item struct{ Name string }
	res := build(t,
		"select 1/*%for userC in items*//*%if userC.name != null*/ a = sqlc.arg('userC.name')/*%end*//*%end*/",
		map[string]any{
			"items":  []item{{Name: "element"}},
			"user_c": item{Name: "parameter"},
		}, dialect.PostgreSQL)
	if len(res.Args) != 1 || res.Args[0] != "element" {
		t.Errorf("Args = %#v, want the element to win inside the loop", res.Args)
	}
}

// A bare bind of a loop element hands over the element, at any spelling. Keying the escape
// hatch exactly while the fallback folded meant another spelling reached the driver as the
// internal map built for the expression language — a wrong value, not a wrong name.
func TestElementBoundWholeAtAnySpelling(t *testing.T) {
	type row struct{ Name string }
	for _, spelling := range []string{"r", "R", "myRow", "my_row"} {
		t.Run(spelling, func(t *testing.T) {
			src := "select 1/*%for myRow in rows*/ x = sqlc.arg('" + spelling + "')/*%end*/"
			if spelling == "r" || spelling == "R" {
				src = "select 1/*%for r in rows*/ x = sqlc.arg('" + spelling + "')/*%end*/"
			}
			res := build(t, src, map[string]any{"rows": []row{{Name: "a"}}}, dialect.PostgreSQL)
			if len(res.Args) != 1 {
				t.Fatalf("Args = %#v, want one", res.Args)
			}
			if got, ok := res.Args[0].(row); !ok {
				t.Errorf("Args[0] = %#v (%T), want the element itself", res.Args[0], res.Args[0])
			} else if got.Name != "a" {
				t.Errorf("Args[0] = %#v", got)
			}
		})
	}
}

// The expression language resolves a member itself, and by exact name, so folding has to reach
// every level of the value. It reached only the top: a condition on a nested map's or nested
// struct's field read nil, which is a branch that quietly disappears.
func TestFoldingReachesNestedValues(t *testing.T) {
	type inner struct {
		MinAge  int
		HTTPURL string
	}
	for _, c := range []struct {
		name   string
		params any
		cond   string
	}{
		{"a nested map, key in Go spelling", map[string]any{"f": map[string]any{"MinAge": 7}}, "f.min_age == 7"},
		{"a nested map, indexed", map[string]any{"f": map[string]any{"is_admin": true}}, "f['isAdmin']"},
		{"a nested map of strings", map[string]any{"f": map[string]string{"Tier": "gold"}}, "f.tier == 'gold'"},
		{"a nested struct", map[string]any{"f": inner{MinAge: 7}}, "f.minAge == 7"},
		{"a nested struct with an initialism", map[string]any{"f": inner{HTTPURL: "u"}}, "f.http_url == 'u'"},
		{"a nested pointer to a struct", map[string]any{"f": &inner{MinAge: 7}}, "f.min_age == 7"},
		{"two levels down", map[string]any{"f": map[string]any{"In": inner{MinAge: 7}}}, "f.in.minAge == 7"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := build(t, "select 1/*%if "+c.cond+"*/ hit/*%end*/", c.params, dialect.PostgreSQL)
			if res.SQL != "select 1 hit" {
				t.Errorf("SQL = %q, want the condition to see the nested value", res.SQL)
			}
		})
	}
}

// A bind resolves from the value the caller passed, never from the folded view. Otherwise a
// marker naming a nested struct hands the driver a map of its fields.
func TestNestedBindKeepsTheRawValue(t *testing.T) {
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	type inner struct {
		SeenAt time.Time
		Note   sql.NullString
	}
	res := build(t, "select 1 where a = sqlc.arg('f.seen_at') and b = sqlc.arg('f.note')",
		map[string]any{"f": inner{SeenAt: when, Note: sql.NullString{String: "n", Valid: true}}},
		dialect.PostgreSQL)
	want := []any{when, sql.NullString{String: "n", Valid: true}}
	if !reflect.DeepEqual(res.Args, want) {
		t.Errorf("Args = %#v, want %#v", res.Args, want)
	}
}

// A struct with no exported fields is a value, not a shape: converting time.Time into a map of
// nothing would make a condition on it meaningless and a bind on it wrong.
func TestAValueLikeStructIsNotConverted(t *testing.T) {
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	res := build(t, "select 1/*%if seenAt != null*/ a = sqlc.arg('seen_at')/*%end*/",
		map[string]any{"seen_at": when}, dialect.PostgreSQL)
	if len(res.Args) != 1 || res.Args[0] != when {
		t.Errorf("Args = %#v, want the time itself", res.Args)
	}
}

// Go rejects an ambiguous promoted selector; picking one silently is worse than saying so.
func TestAmbiguousPromotionIsReported(t *testing.T) {
	type a struct{ Val int }
	type b struct{ Val int }
	type params struct {
		a
		b
	}
	if _, err := buildErr(t, "select sqlc.arg('val')", params{a{1}, b{2}}, dialect.PostgreSQL); err == nil {
		t.Error("want the ambiguity reported rather than resolved by declaration order")
	}
	// A field of the outer struct still shadows an embedded one, as Go's promotion does.
	type outer struct {
		a
		Val int
	}
	res := build(t, "select sqlc.arg('val')", outer{a{1}, 9}, dialect.PostgreSQL)
	if len(res.Args) != 1 || res.Args[0] != 9 {
		t.Errorf("Args = %#v, want the shallower field", res.Args)
	}
}

// A typed nil params value resolves no name at all, which would silently be a different query.
func TestNilParamsValueIsReported(t *testing.T) {
	type params struct{ A int }
	if _, err := buildErr(t, "select 1", (*params)(nil), dialect.PostgreSQL); err == nil {
		t.Error("want a nil params value reported")
	}
	// nil itself remains legal: a template that names nothing needs no params.
	if _, err := buildErr(t, "select 1", nil, dialect.PostgreSQL); err != nil {
		t.Errorf("nil params: %v", err)
	}
}

// Any string-keyed map is params, not just the unnamed map[string]any. A named one was refused
// with a message that said maps were accepted.
func TestEveryStringKeyedMapIsParams(t *testing.T) {
	type scope map[string]any
	for _, params := range []any{
		scope{"a": 1},
		map[string]int{"a": 1},
		map[string]any{"a": 1},
		&map[string]any{"a": 1},
	} {
		res := build(t, "select sqlc.arg('a')", params, dialect.PostgreSQL)
		if len(res.Args) != 1 {
			t.Errorf("%T: Args = %#v, want one", params, res.Args)
		}
	}
}

// Two keys of a caller's own map that fold together are an ambiguity in the caller's own names,
// so it is reported rather than resolved by iteration order. The struct path had a test; the map
// path did not, and deleting its check left the suite green.
func TestFoldedMapKeysCollide(t *testing.T) {
	_, err := buildErr(t, "select sqlc.arg('user_id')",
		map[string]any{"userId": 1, "user_id": 2}, dialect.PostgreSQL)
	if err == nil {
		t.Error("want the ambiguity between userId and user_id reported")
	}
	// Two spellings of one value are not an ambiguity: nothing is lost by folding them.
	if _, err := buildErr(t, "select sqlc.arg('user_id')",
		map[string]any{"userId": 1, "user_id": 1}, dialect.PostgreSQL); err != nil {
		t.Errorf("same value under two spellings: %v", err)
	}
}

// A Scoper is what generated params implement, and the same folding applies to the map it hands
// back. Returning nil is an empty scope rather than a failure.
func TestScoperIsFoldedToo(t *testing.T) {
	res := build(t, "select 1/*%if activeOnly*/ a = sqlc.arg('min_age')/*%end*/",
		scoper{"activeOnly": true, "minAge": 7}, dialect.PostgreSQL)
	if len(res.Args) != 1 || res.Args[0] != 7 {
		t.Errorf("Args = %#v, want [7]", res.Args)
	}
	if got := build(t, "select 1/*%if a*/x/*%end*/", scoper(nil), dialect.PostgreSQL); got.SQL != "select 1" {
		t.Errorf("SQL = %q, want the branch skipped", got.SQL)
	}
}

type scoper map[string]any

func (s scoper) TemplateScope() map[string]any { return s }

// A method call in a condition names a Go method, and Go does not fold method names. Folding
// them made every method call impossible.
func TestMethodCallsKeepTheirSpelling(t *testing.T) {
	res := build(t, "select 1/*%if seenAt.IsZero()*/ zero/*%end*/",
		map[string]any{"seen_at": time.Time{}}, dialect.PostgreSQL)
	if res.SQL != "select 1 zero" {
		t.Errorf("SQL = %q, want the method call to work", res.SQL)
	}
}

// One Template is shared by every call of a generated method, so Build has to be safe to call
// concurrently — including the map of loop elements a render keeps.
func TestBuildIsSafeForConcurrentUse(t *testing.T) {
	type cond struct{ Value string }
	nodes, err := parse(t, "select 1 where 1 = 0"+
		"/*%for c in conds*//*%if c.value != null*/ or a = sqlc.arg('c.value') and b = @c/*%end*/"+
		"/*%for c in conds*/ or inner = sqlc.arg('c.value')/*%end*//*%end*/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ev := &exprlang.Evaluator{}
	params := map[string]any{"conds": []cond{{Value: "a"}, {Value: "b"}}}
	first, err := render.Render(nodes, params, dialect.PostgreSQL, ev)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, err := render.Render(nodes, params, dialect.PostgreSQL, ev)
				if err != nil {
					t.Errorf("render: %v", err)
					return
				}
				if got.SQL != first.SQL || !reflect.DeepEqual(got.Args, first.Args) {
					t.Errorf("render differed:\n got %q %#v\nwant %q %#v",
						got.SQL, got.Args, first.SQL, first.Args)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Only PostgreSQL nests block comments and has dollar-quoted strings; only MySQL escapes a
// quote with a backslash. Reading any of those in the wrong dialect turns a legal query into a
// build failure, or lets a span swallow a directive.
func TestSpansAreReadPerDialect(t *testing.T) {
	// MySQL: /** a /* b */ is a whole comment, and the directive after it is a directive.
	res := build(t, "select 1 /** legacy /* note */ /*%if a*/x/*%end*/",
		map[string]any{"a": true}, dialect.MySQL)
	if res.SQL != "select 1 /** legacy /* note */ x" {
		t.Errorf("mysql comment: SQL = %q", res.SQL)
	}
	// PostgreSQL nests, so the same shape is still open and swallows the directive.
	if _, err := buildErr(t, "select 1 /** a /* b */ /*%if a*/x/*%end*/", nil, dialect.PostgreSQL); err == nil {
		t.Error("postgres comment: want the unterminated comment reported")
	}
	// PostgreSQL: 'a\' is a complete literal, so the directive after it is one.
	res = build(t, `select 'a\' /*%if a*/x/*%end*/`, map[string]any{"a": true}, dialect.PostgreSQL)
	if res.SQL != `select 'a\' x` {
		t.Errorf("postgres backslash: SQL = %q", res.SQL)
	}
	// MySQL identifiers may hold dollars, so a$x$b is a name and not the start of a span.
	res = build(t, "select a$x$b /*%if a*/x/*%end*/", map[string]any{"a": true}, dialect.MySQL)
	if res.SQL != "select a$x$b x" {
		t.Errorf("mysql dollars: SQL = %q", res.SQL)
	}
}

// A call form after an operator is a marker. Applying the at-form's operator rule to the call
// forms too dropped the parameter from `id=sqlc.arg('id')` — which is exactly what restoring a
// placeholder produces, so it hit the most ordinary SQL there is.
func TestCallFormAfterAnOperatorIsAMarker(t *testing.T) {
	for _, src := range []string{
		"select 1 where id=sqlc.arg('a')",
		"select 1 where age>=sqlc.arg('a')",
		"select 1 where age-sqlc.arg('a')>0",
		"select 1 where s!=sqlc.arg('a')",
		"select 1 where id in (sqlc.slice('a'))",
	} {
		t.Run(src, func(t *testing.T) {
			res := build(t, src, map[string]any{"a": 1}, dialect.PostgreSQL)
			if len(res.Args) != 1 {
				t.Errorf("Args = %#v for %q, want the marker bound", res.Args, src)
			}
			if strings.Contains(res.SQL, "sqlc.") {
				t.Errorf("SQL = %q, want the marker replaced", res.SQL)
			}
		})
	}
}

// Every supported engine renders, and each spells placeholders its own way. SQLite had no test
// of any kind: changing its placeholder to $n, or dropping it from the dialect table so that
// dyn.Parse refused the engine outright, left the suite green.
func TestEveryEngineRenders(t *testing.T) {
	const src = "select id from users where 1 = 1" +
		"/*%if activeOnly*/ and status = sqlc.arg('status')/*%end*/" +
		"/*%if len(ids) > 0*/ and id in (sqlc.slice('ids'))/*%end*/"
	params := map[string]any{"activeOnly": true, "status": "ok", "ids": []int64{1, 2}}
	for _, c := range []struct{ engine, want string }{
		{"postgresql", "select id from users where 1 = 1 and status = $1 and id in ($2, $3)"},
		{"mysql", "select id from users where 1 = 1 and status = ? and id in (?, ?)"},
		{"sqlite", "select id from users where 1 = 1 and status = ? and id in (?, ?)"},
	} {
		t.Run(c.engine, func(t *testing.T) {
			d, ok := dialect.For(c.engine)
			if !ok {
				t.Fatalf("dialect.For(%q) reports the engine unsupported", c.engine)
			}
			res := build(t, src, params, d)
			if res.SQL != c.want {
				t.Errorf("SQL\n got: %q\nwant: %q", res.SQL, c.want)
			}
			if len(res.Args) != 3 {
				t.Errorf("Args = %#v, want three in placeholder order", res.Args)
			}
		})
	}
}
