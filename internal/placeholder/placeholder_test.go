package placeholder_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/placeholder"
)

func TestRestore(t *testing.T) {
	all := []placeholder.Param{
		{Number: 1, Name: "status"},
		{Number: 2, Name: "note"},
		{Number: 3, Name: "ids", List: true},
		{Number: 4, Name: "c.name"},
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
			name:   "a bare question mark takes the next parameter in order",
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
			params: []placeholder.Param{{Number: 1, Name: "status"}, {Number: 4, Name: "c.name"}},
			style:  placeholder.Dollar,
			in:     "select 1\n  /*%if activeOnly*/ and a = $1 /*%end*/\n  /*%for c in cs*/ and d = $4 /*%end*/",
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
			{Number: 1, Name: "status"},
			{Number: 2, Name: "ids", List: true},
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
		[]placeholder.Param{{Number: 1, Name: "a"}, {Number: 2, Name: "lost"}},
		placeholder.Dollar)
	if err == nil || !strings.Contains(err.Error(), "has no placeholder") {
		t.Errorf("error = %v, want the unused parameter reported", err)
	}
}

// sqlc can report a parameter with no name at all; nothing can bind it, and saying so beats
// emitting sqlc.arg(”) for the lexer to reject.
func TestRestoreReportsANamelessParameter(t *testing.T) {
	_, err := placeholder.Restore("select id from t where 1 = $1",
		[]placeholder.Param{{Number: 1, Name: ""}}, placeholder.Dollar)
	if err == nil || !strings.Contains(err.Error(), "has no name") {
		t.Errorf("error = %v, want the nameless parameter reported", err)
	}
}

// sqlc's own slice marker is a mechanism this renderer replaces, so it must not reach the
// server.
func TestRestoreStripsTheSliceMarker(t *testing.T) {
	got, err := placeholder.Restore("select id from t where id in (/*SLICE:ids*/?)",
		[]placeholder.Param{{Number: 1, Name: "ids", List: true}}, placeholder.Question)
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
		[]placeholder.Param{{Number: 1, Name: "s"}}, placeholder.Question)
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
		[]placeholder.Param{{Number: 1, Name: "a"}}, placeholder.Dollar)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got != "select $$note $2 here$$, a = sqlc.arg('a')" {
		t.Errorf("Restore = %q", got)
	}
}
