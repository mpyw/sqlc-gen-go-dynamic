package placeholder_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/placeholder"
)

func TestRestore(t *testing.T) {
	all := []placeholder.Param{
		{Name: "status"},
		{Name: "note"},
		{Name: "ids", List: true},
		{Name: "c.name"},
	}
	one := all[:1]
	two := all[:2]
	cases := []struct {
		name   string
		style  placeholder.Style
		params []placeholder.Param
		in     string
		want   string
	}{
		{
			name:   "each form restores to the call that produced it",
			params: all,
			style:  placeholder.Dollar,
			in:     "select 1 where a = $1 and b = $2 and c in ($3) and d = $4",
			want: "select 1 where a = sqlc.arg('status') and b = sqlc.arg('note') " +
				"and c in (sqlc.slice('ids')) and d = sqlc.arg('c.name')",
		},
		{
			name:   "a bare question mark takes the next parameter in order of appearance",
			params: two,
			style:  placeholder.Question,
			in:     "select 1 where a = ? and b = ?",
			want:   "select 1 where a = sqlc.arg('status') and b = sqlc.arg('note')",
		},
		{
			name:   "a numbered question mark takes the parameter it names",
			params: two,
			style:  placeholder.QuestionNumbered,
			in:     "select 1 where a = ?1 and b = ?2 and c = ?1",
			want: "select 1 where a = sqlc.arg('status') and b = sqlc.arg('note') " +
				"and c = sqlc.arg('status')",
		},
		{
			name:   "directives and text are untouched",
			params: []placeholder.Param{{Name: "status"}, {Name: "c.name"}},
			style:  placeholder.Dollar,
			in:     "select 1\n  /*%if activeOnly*/ and a = $1 /*%end*/\n  /*%for c in cs*/ and d = $2 /*%end*/",
			want: "select 1\n  /*%if activeOnly*/ and a = sqlc.arg('status') /*%end*/\n" +
				"  /*%for c in cs*/ and d = sqlc.arg('c.name') /*%end*/",
		},
		{
			name:   "a placeholder inside a quoted span or a comment is text",
			params: one,
			style:  placeholder.Dollar,
			in:     "select '$2', \"$2\" /** $2 */ -- $2\nwhere a = $1",
			want:   "select '$2', \"$2\" /** $2 */ -- $2\nwhere a = sqlc.arg('status')",
		},
		{
			// :3 is an array slice, and ? is the jsonb key operator. Neither is a
			// placeholder under Dollar.
			name:   "only the engine's own spelling is a placeholder",
			params: one,
			style:  placeholder.Dollar,
			in:     "select a[1:3] from t where data ? 'k' and id = $1",
			want:   "select a[1:3] from t where data ? 'k' and id = sqlc.arg('status')",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := placeholder.Restore(c.in, c.params, c.style)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if got != c.want {
				t.Errorf("Restore\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

func TestRestoreErrors(t *testing.T) {
	if _, err := placeholder.Restore("select $9", nil, placeholder.Dollar); err == nil ||
		!strings.Contains(err.Error(), "disagree about the query") {
		t.Errorf("error = %v, want it to report the mismatch", err)
	}
	if _, err := placeholder.Restore("select 'a", nil, placeholder.Dollar); err == nil ||
		!strings.Contains(err.Error(), "unterminated") {
		t.Errorf("error = %v, want it to report the unterminated literal", err)
	}
}

func TestStyleFor(t *testing.T) {
	for engine, want := range map[string]placeholder.Style{
		"postgresql": placeholder.Dollar,
		"mysql":      placeholder.Question,
		"sqlite":     placeholder.QuestionNumbered,
	} {
		got, err := placeholder.StyleFor(engine)
		if err != nil || got != want {
			t.Errorf("StyleFor(%q) = %v, %v; want %v", engine, got, err, want)
		}
	}
	if _, err := placeholder.StyleFor("oracle"); err == nil {
		t.Error("StyleFor(oracle): want an error, sqlc has no such engine")
	}
}

// sqlc emits a bare ? alongside ?n for SQLite — for a slice, and for a parameter it did not
// number. Reading only the numbered form dropped those parameters from the template and from
// the generated API, silently.
func TestRestoreSQLiteMixesNumberedAndBare(t *testing.T) {
	got, err := placeholder.Restore(
		"select id from t where 1 = 1\n  /*%if s != null*/ and s = ?1 /*%end*/\n  and id in (/*SLICE:ids*/?)",
		[]placeholder.Param{
			{Name: "status"},
			{Name: "ids", List: true},
		}, placeholder.QuestionNumbered)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want := "select id from t where 1 = 1\n  /*%if s != null*/ and s = sqlc.arg('status') /*%end*/\n" +
		"  and id in (sqlc.slice('ids'))"
	if got != want {
		t.Errorf("Restore\n got: %q\nwant: %q", got, want)
	}
}

// A parameter sqlc reported whose placeholder is nowhere in the text would leave the generated
// API with a field nothing binds, so the disagreement is reported.
func TestRestoreReportsAnUnusedParameter(t *testing.T) {
	_, err := placeholder.Restore("select id from t where a = $1",
		[]placeholder.Param{{Name: "a"}, {Name: "lost"}},
		placeholder.Dollar)
	if err == nil || !strings.Contains(err.Error(), "nothing in the text binds") {
		t.Errorf("error = %v, want the unused parameter reported", err)
	}
}

// sqlc can report a parameter with no name at all; nothing can bind it, and saying so beats
// emitting sqlc.arg(”) for the lexer to reject.
func TestRestoreReportsANamelessParameter(t *testing.T) {
	_, err := placeholder.Restore("select id from t where 1 = $1",
		[]placeholder.Param{{Name: ""}}, placeholder.Dollar)
	if err == nil || !strings.Contains(err.Error(), "has no name") {
		t.Errorf("error = %v, want the nameless parameter reported", err)
	}
}

// sqlc's own slice marker is a mechanism this renderer replaces, so it must not reach the
// server.
func TestRestoreStripsTheSliceMarker(t *testing.T) {
	got, err := placeholder.Restore("select id from t where id in (/*SLICE:ids*/?)",
		[]placeholder.Param{{Name: "ids", List: true}}, placeholder.Question)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if strings.Contains(got, "SLICE") {
		t.Errorf("Restore = %q, want the marker comment gone", got)
	}
}

// MySQL's # comment hides a placeholder from restoration, as it does from sqlc.
func TestRestoreSkipsAHashComment(t *testing.T) {
	got, err := placeholder.Restore("select id from t where s = ? # really?\n",
		[]placeholder.Param{{Name: "s"}}, placeholder.Question)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got != "select id from t where s = sqlc.arg('s') # really?\n" {
		t.Errorf("Restore = %q", got)
	}
}

// A dollar-quoted string is opaque to restoration too, or a placeholder inside one is rewritten.
func TestRestoreSkipsADollarQuotedString(t *testing.T) {
	got, err := placeholder.Restore("select $$note $2 here$$, a = $1",
		[]placeholder.Param{{Name: "a"}}, placeholder.Dollar)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got != "select $$note $2 here$$, a = sqlc.arg('a')" {
		t.Errorf("Restore = %q", got)
	}
}

// These pin what real sqlc actually reports, because the obvious reading of the request is
// wrong: plugin.Parameter.number is sqlc's own analysis number, not the placeholder's position.
// Reading it put every marker in the wrong place while every consistency check still passed.
//
// The observations, from sqlc v1.31.1:
//
//	mysql   select … where status = ? and age > ? order by id limit ? offset ?
//	        numbers 3, 4, 1, 2 — and sqlc's own code passes arg.St, arg.Lo, arg.Limit, arg.Offset
//	sqlite  select … where status = ?1 and age > ?2 order by id limit ?4 offset ?3
//	        numbers 1, 2, 3, 4 for st, lo, off, lim
//
// In both, the order sqlc reports the parameters in is the order it passes the arguments in,
// and the nth argument binds the placeholder the driver numbers n. So position decides.
func TestRestoreFollowsSqlcsArgumentOrder(t *testing.T) {
	for _, c := range []struct {
		name   string
		style  placeholder.Style
		params []placeholder.Param
		in     string
		want   string
	}{
		{
			// A literal ? the author wrote is a parameter like any other, and naming it is
			// what lets the renderer emit it in the same place.
			name:   "a literal limit placeholder, which sqlc numbers first and reports last",
			style:  placeholder.Question,
			params: []placeholder.Param{{Name: "st"}, {Name: "lo"}, {Name: "limit"}, {Name: "offset"}},
			in:     "select id from users where status = ? and age > ? order by id limit ? offset ?",
			want: "select id from users where status = sqlc.arg('st') and age > sqlc.arg('lo') " +
				"order by id limit sqlc.arg('limit') offset sqlc.arg('offset')",
		},
		{
			name:   "numbered placeholders out of textual order",
			style:  placeholder.QuestionNumbered,
			params: []placeholder.Param{{Name: "st"}, {Name: "lo"}, {Name: "off"}, {Name: "lim"}},
			in:     "select id from users where status = ?1 and age > ?2 order by id limit ?4 offset ?3",
			want: "select id from users where status = sqlc.arg('st') and age > sqlc.arg('lo') " +
				"order by id limit sqlc.arg('lim') offset sqlc.arg('off')",
		},
		{
			// The bare one is the slice, and it takes one more than the highest number so
			// far — SQLite's own rule. Counting occurrences instead made the reused ?1
			// inflate the count and refused the query outright.
			name:   "a reused number beside a bare placeholder",
			style:  placeholder.QuestionNumbered,
			params: []placeholder.Param{{Name: "a"}, {Name: "ids", List: true}},
			in:     "select id from users where (name = ?1 or status = ?1) and id in (/*SLICE:ids*/?)",
			want: "select id from users where (name = sqlc.arg('a') or status = sqlc.arg('a')) " +
				"and id in (sqlc.slice('ids'))",
		},
		{
			// MySQL reports a reused marker twice, once per occurrence.
			name:   "a marker reused, which MySQL reports once per occurrence",
			style:  placeholder.Question,
			params: []placeholder.Param{{Name: "a"}, {Name: "a"}, {Name: "b"}},
			in:     "select id from users where (name = ? or status = ?) and age > ?",
			want: "select id from users where (name = sqlc.arg('a') or status = sqlc.arg('a')) " +
				"and age > sqlc.arg('b')",
		},
		{
			name:   "a slice ahead of numbered placeholders",
			style:  placeholder.QuestionNumbered,
			params: []placeholder.Param{{Name: "ids", List: true}, {Name: "st"}, {Name: "lim"}},
			in:     "select id from users where id in (/*SLICE:ids*/?) and status = ?2 order by id limit ?3",
			want: "select id from users where id in (sqlc.slice('ids')) and status = sqlc.arg('st') " +
				"order by id limit sqlc.arg('lim')",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := placeholder.Restore(c.in, c.params, c.style)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if got != c.want {
				t.Errorf("Restore\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// A backslash escapes a quote in MySQL and does not in PostgreSQL, where a lone backslash ends
// nothing: reading one there ran past the closing quote, so the next quote opened a span and the
// SQL between two literals was read as code.
func TestRestoreReadsEscapesPerDialect(t *testing.T) {
	// MySQL: the literal contains a quote, so it closes at the second ', not the first.
	got, err := placeholder.Restore(`select id from t where name = 'O\'Brien' and s = ?`,
		[]placeholder.Param{{Name: "s"}}, placeholder.Question)
	if err != nil {
		t.Fatalf("mysql: %v", err)
	}
	if want := `select id from t where name = 'O\'Brien' and s = sqlc.arg('s')`; got != want {
		t.Errorf("mysql\n got: %q\nwant: %q", got, want)
	}
	// PostgreSQL: 'a\' is a complete literal holding a backslash, so what follows is code and
	// the placeholder in it is one.
	got, err = placeholder.Restore(`select id from t where a = 'a\' and b = $1`,
		[]placeholder.Param{{Name: "b"}}, placeholder.Dollar)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	if want := `select id from t where a = 'a\' and b = sqlc.arg('b')`; got != want {
		t.Errorf("postgres\n got: %q\nwant: %q", got, want)
	}
	// PostgreSQL's E'…' does honor the backslash, so the span runs to the second quote.
	got, err = placeholder.Restore(`select id from t where a = E'it\'s' and b = $1`,
		[]placeholder.Param{{Name: "b"}}, placeholder.Dollar)
	if err != nil {
		t.Fatalf("escape string: %v", err)
	}
	if want := `select id from t where a = E'it\'s' and b = sqlc.arg('b')`; got != want {
		t.Errorf("escape string\n got: %q\nwant: %q", got, want)
	}
}

// MySQL and SQLite do not nest block comments, so `/** a /* b */` is a whole comment there.
// Treating it as an unterminated one aborted the generate for a query that was fine.
func TestRestoreNestsCommentsOnlyWherePostgresDoes(t *testing.T) {
	const src = "select id from t /** legacy /* note */ where s = ?"
	got, err := placeholder.Restore(src, []placeholder.Param{{Name: "s"}}, placeholder.Question)
	if err != nil {
		t.Fatalf("mysql: %v", err)
	}
	if want := "select id from t /** legacy /* note */ where s = sqlc.arg('s')"; got != want {
		t.Errorf("mysql\n got: %q\nwant: %q", got, want)
	}
	// PostgreSQL does nest, so the same shape is one comment that swallows the rest.
	if _, err := placeholder.Restore("select id from t /** a /* b */ where s = $1",
		[]placeholder.Param{{Name: "s"}}, placeholder.Dollar); err == nil {
		t.Error("want an error: PostgreSQL nests, so that comment is still open")
	}
}

// SQLite gives a second occurrence of the same slice no number of its own — sqlc's own
// generated code replaces only the first — so counting placeholders took the next parameter
// instead. The marker comment names the parameter, which is the only sound identification.
func TestRestoreReadsARepeatedSliceByName(t *testing.T) {
	for _, c := range []struct {
		name   string
		style  placeholder.Style
		params []placeholder.Param
		in     string
		want   string
	}{
		{
			// sqlite: one parameter for both occurrences, and a scalar numbered after it.
			name:   "sqlite numbers the slice once",
			style:  placeholder.QuestionNumbered,
			params: []placeholder.Param{{Name: "ids", List: true}, {Name: "st"}},
			in:     "select id from t where id in (/*SLICE:ids*/?) and id2 in (/*SLICE:ids*/?) and s = ?2",
			want: "select id from t where id in (sqlc.slice('ids')) and id2 in (sqlc.slice('ids')) " +
				"and s = sqlc.arg('st')",
		},
		{
			// mysql: one parameter per occurrence, so both consume a number.
			name:   "mysql reports it once per occurrence",
			style:  placeholder.Question,
			params: []placeholder.Param{{Name: "ids", List: true}, {Name: "ids", List: true}, {Name: "lo"}},
			in:     "select id from t where id in (/*SLICE:ids*/?) and s in (/*SLICE:ids*/?) and a > ?",
			want: "select id from t where id in (sqlc.slice('ids')) and s in (sqlc.slice('ids')) " +
				"and a > sqlc.arg('lo')",
		},
		{
			// postgresql: numbers are authoritative and there is no marker comment at all.
			name:   "postgresql repeats the number",
			style:  placeholder.Dollar,
			params: []placeholder.Param{{Name: "ids", List: true}, {Name: "lo"}},
			in:     "select id from t where id in ($1) and id2 in ($1) and a > $2",
			want: "select id from t where id in (sqlc.slice('ids')) and id2 in (sqlc.slice('ids')) " +
				"and a > sqlc.arg('lo')",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := placeholder.Restore(c.in, c.params, c.style)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if got != c.want {
				t.Errorf("Restore\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// A slice marker naming something sqlc did not report means the text and the table disagree,
// which is worth saying rather than restoring some other parameter there.
func TestRestoreReportsAnUnknownSliceName(t *testing.T) {
	_, err := placeholder.Restore("select id from t where id in (/*SLICE:other*/?)",
		[]placeholder.Param{{Name: "ids", List: true}}, placeholder.QuestionNumbered)
	if err == nil || !strings.Contains(err.Error(), "disagree about the query") {
		t.Errorf("error = %v, want the disagreement reported", err)
	}
}

// Only PostgreSQL has dollar-quoted strings. A MySQL identifier may contain dollars, and
// reading `a$x$b` as the opening of a span swallowed whatever came after it.
func TestRestoreReadsDollarQuotesOnlyInPostgres(t *testing.T) {
	got, err := placeholder.Restore("select a$x$b from t where s = ?",
		[]placeholder.Param{{Name: "s"}}, placeholder.Question)
	if err != nil {
		t.Fatalf("mysql: %v", err)
	}
	if want := "select a$x$b from t where s = sqlc.arg('s')"; got != want {
		t.Errorf("mysql\n got: %q\nwant: %q", got, want)
	}
}
