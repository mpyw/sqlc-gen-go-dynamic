package bind_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
)

var (
	pg = bind.RulesFor("postgresql")
	my = bind.RulesFor("mysql")
)

func TestRecognize(t *testing.T) {
	cases := []struct {
		in   string
		name string
		list bool
		n    int
	}{
		{"@status", "status", false, 7},
		{"@status and x = 1", "status", false, 7},
		{"@_leading", "_leading", false, 9},
		{"sqlc.arg(status)", "status", false, 16},
		{"sqlc.arg('status')", "status", false, 18},
		{"sqlc.narg(note)", "note", false, 15},
		{"sqlc.slice(ids)", "ids", true, 15},
		{"sqlc.slice('ids')", "ids", true, 17},
		{"sqlc.arg('c.name')", "c.name", false, 18},
		{"sqlc.arg( 'x' )", "x", false, 15},
		{"sqlc.arg('x')::text", "x", false, 13}, // the cast follows the marker
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			m, ok := pg.Recognize(c.in)
			if !ok {
				t.Fatalf("Recognize(%q) = not recognized", c.in)
			}
			if m.Name != c.name || m.List != c.list || m.Len != c.n {
				t.Errorf("Recognize(%q) = %+v, want {Name:%q List:%v Len:%d}", c.in, m, c.name, c.list, c.n)
			}
		})
	}
}

func TestRecognizeRejects(t *testing.T) {
	for _, in := range []string{
		"", "@", "@1abc", "@ ", "status", "'@status'", "@@x", "@>",
		"sqlc.arg('x'", "sqlc.args('x')", "sqlc_arg(x)",
		// sqlc.embed selects a table into a nested struct: a result column, not a bind.
		"sqlc.embed(authors)",
	} {
		t.Run(in, func(t *testing.T) {
			if m, ok := pg.Recognize(in); ok {
				t.Errorf("Recognize(%q) = %+v, want not recognized", in, m)
			}
		})
	}
}

// @a.b yields @a, the reading sqlc makes too, which is why Recognize alone cannot tell a
// bind from a mistake.
func TestRecognizeStopsAtDot(t *testing.T) {
	m, ok := pg.Recognize("@a.b")
	if !ok || m.Name != "a" || m.Len != 2 {
		t.Errorf(`Recognize("@a.b") = %+v, %v; want name a spanning 2 bytes`, m, ok)
	}
}

func TestMalformed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"@c.name", "dotted name"},
		{"@c.name = 1", "dotted name"},
		{"sqlc.arg(c.name)", "has to be quoted"},
		{"sqlc.narg(c.note)", "has to be quoted"},
		{`sqlc.arg("x")`, "takes a name"},
		{"sqlc.arg()", "takes a name"},
		{"sqlc.arg('')", "takes a name"},
		{"sqlc.arg('x'", "takes a name"},
	} {
		t.Run(c.in, func(t *testing.T) {
			reason, bad := pg.Malformed(c.in)
			if !bad {
				t.Fatalf("Malformed(%q) = not malformed", c.in)
			}
			if !strings.Contains(reason, c.want) {
				t.Errorf("reason = %q, want it to contain %q", reason, c.want)
			}
		})
	}
}

func TestMalformedLeavesEverythingElseAlone(t *testing.T) {
	for _, in := range []string{
		"@status", "sqlc.arg(x)", "sqlc.arg('x')", "sqlc.narg(note)", "sqlc.slice('ids')",
		"", "@", "@>", "@@version", "tags @> '{a}'", "status",
		"sqlc.args('x')", "sqlc.embed(authors)",
	} {
		t.Run(in, func(t *testing.T) {
			if reason, bad := pg.Malformed(in); bad {
				t.Errorf("Malformed(%q) = %q, want not malformed", in, reason)
			}
		})
	}
}

// sqlc does not support the @name shortcut for MySQL, where @name is a user variable.
func TestRulesForMySQL(t *testing.T) {
	if my.AtForm || !pg.AtForm {
		t.Fatalf("AtForm: mysql=%v postgresql=%v", my.AtForm, pg.AtForm)
	}
	for _, in := range []string{"@status", "@row_number", "@c.name"} {
		if m, ok := my.Recognize(in); ok {
			t.Errorf("my.Recognize(%q) = %+v, want not recognized", in, m)
		}
		if reason, bad := my.Malformed(in); bad {
			t.Errorf("my.Malformed(%q) = %q, want not malformed", in, reason)
		}
	}
	if m, ok := my.Recognize("sqlc.arg(status)"); !ok || m.Name != "status" {
		t.Errorf("my.Recognize(call form) = %+v, %v", m, ok)
	}
}
