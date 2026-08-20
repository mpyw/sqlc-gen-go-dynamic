package parser_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/parser"
)

var rules = bind.RulesFor("postgresql")

// An if/elseif/else chain is one node with three arms, the last of which has no condition.
func TestParseArms(t *testing.T) {
	nodes, err := parser.Parse("/*%if a*/A/*%elseif b*/B/*%else*/C/*%end*/", rules)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %#v, want one", nodes)
	}
	got, ok := nodes[0].(ast.If)
	if !ok {
		t.Fatalf("node = %T, want ast.If", nodes[0])
	}
	want := ast.If{Arms: []ast.Arm{
		{Cond: "a", Body: []ast.Node{ast.Text{S: "A"}}},
		{Cond: "b", Body: []ast.Node{ast.Text{S: "B"}}},
		{Body: []ast.Node{ast.Text{S: "C"}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("If\n got: %#v\nwant: %#v", got, want)
	}
}

func TestParseLoop(t *testing.T) {
	nodes, err := parser.Parse("/*%for c in a.conds*/x = sqlc.arg('c.name')/*%end*/", rules)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := nodes[0].(ast.For)
	if !ok {
		t.Fatalf("node = %T, want ast.For", nodes[0])
	}
	if got.Var != "c" || got.Iter != "a.conds" {
		t.Errorf("loop = %q in %q, want c in a.conds", got.Var, got.Iter)
	}
	want := []ast.Node{ast.Text{S: "x = "}, ast.Bind{Name: "c.name"}}
	if !reflect.DeepEqual(got.Body, want) {
		t.Errorf("body\n got: %#v\nwant: %#v", got.Body, want)
	}
}

// Everything after "in" is the iterable verbatim, so a colon does not end it.
func TestParseLoopKeepsIterableVerbatim(t *testing.T) {
	nodes, err := parser.Parse("/*%for x in a ? b : c*/y/*%end*/", rules)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := nodes[0].(ast.For).Iter; got != "a ? b : c" {
		t.Errorf("Iter = %q, want %q", got, "a ? b : c")
	}
}

func TestParseErrors(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"unclosed block", "/*%if x*/a", "unclosed"},
		{"stray end", "a/*%end*/", "without a matching"},
		{"arm with no branch", "a/*%else*/", "without a matching"},
		{"arm after else", "/*%if x*/a/*%else*/b/*%elseif y*/c/*%end*/", "after /*%else*/"},
		{"arm inside a loop", "/*%for x in xs*//*%else*/a/*%end*/", "inside /*%for*/"},
		{"unknown directive", "/*%while x*/a/*%end*/", `unknown directive "while"`},
		{"condition-less if", "/*%if*/a/*%end*/", "has no condition"},
		{"malformed for", "/*%for c*/a/*%end*/", "wants"},
		{"unterminated comment", "a /* b", "unterminated block comment"},
		{"unterminated quote", "a 'b", "unterminated quoted literal"},
		{"malformed marker", "a = @c.name", "dotted name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := parser.Parse(c.src, rules)
			if err == nil {
				t.Fatalf("want an error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}
