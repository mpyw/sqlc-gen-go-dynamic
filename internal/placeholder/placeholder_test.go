package placeholder_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/placeholder"
)

func TestRestore(t *testing.T) {
	params := []placeholder.Param{
		{Number: 1, Name: "status"},
		{Number: 2, Name: "note", Nullable: true},
		{Number: 3, Name: "ids", List: true},
		{Number: 4, Name: "c.name"},
	}
	cases := []struct {
		name  string
		style placeholder.Style
		in    string
		want  string
	}{
		{
			name:  "each form restores to the call that produced it",
			style: placeholder.Dollar,
			in:    "select 1 where a = $1 and b = $2 and c in ($3) and d = $4",
			want: "select 1 where a = sqlc.arg('status') and b = sqlc.narg('note') " +
				"and c in (sqlc.slice('ids')) and d = sqlc.arg('c.name')",
		},
		{
			name:  "a bare question mark takes the next parameter in order",
			style: placeholder.Question,
			in:    "select 1 where a = ? and b = ?",
			want:  "select 1 where a = sqlc.arg('status') and b = sqlc.narg('note')",
		},
		{
			name:  "a numbered question mark takes the parameter it names",
			style: placeholder.QuestionNumbered,
			in:    "select 1 where a = ?1 and b = ?2 and c = ?1",
			want: "select 1 where a = sqlc.arg('status') and b = sqlc.narg('note') " +
				"and c = sqlc.arg('status')",
		},
		{
			name:  "directives and text are untouched",
			style: placeholder.Dollar,
			in:    "select 1\n  /*%if activeOnly*/ and a = $1 /*%end*/\n  /*%for c in cs*/ and d = $4 /*%end*/",
			want: "select 1\n  /*%if activeOnly*/ and a = sqlc.arg('status') /*%end*/\n" +
				"  /*%for c in cs*/ and d = sqlc.arg('c.name') /*%end*/",
		},
		{
			name:  "a placeholder inside a quoted span or a comment is text",
			style: placeholder.Dollar,
			in:    "select '$2', \"$2\" /** $2 */ -- $2\nwhere a = $1",
			want:  "select '$2', \"$2\" /** $2 */ -- $2\nwhere a = sqlc.arg('status')",
		},
		{
			// :3 is an array slice, and ? is the jsonb key operator. Neither is a
			// placeholder under Dollar.
			name:  "only the engine's own spelling is a placeholder",
			style: placeholder.Dollar,
			in:    "select a[1:3] from t where data ? 'k' and id = $1",
			want:  "select a[1:3] from t where data ? 'k' and id = sqlc.arg('status')",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := placeholder.Restore(c.in, params, c.style)
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
		!strings.Contains(err.Error(), "has no parameter") {
		t.Errorf("error = %v, want it to report a placeholder with no parameter", err)
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
