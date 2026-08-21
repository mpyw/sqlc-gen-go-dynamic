package exprlang_test

import (
	"testing"
	"time"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprlang"
)

type ready struct{ n int }

func (r ready) Ready() bool { return r.n > 0 }

// Folding makes a template's spelling of a name irrelevant, but a Go method name is not a name
// of that kind: nothing folds it, and folding it here made every method call in a condition
// impossible. A builtin is a different node kind and was never affected.
func TestMethodNamesKeepTheirSpelling(t *testing.T) {
	ev := &exprlang.Evaluator{}
	scope := map[string]any{
		"r":      ready{1},
		"t":      time.Time{},
		"ids":    []int{1},
		"minage": 5,
	}
	for _, c := range []struct {
		src  string
		want any
	}{
		{"r.Ready()", true},
		{"t.IsZero()", true},
		{"len(ids) > 0", true},
		{"minAge > 1", true},  // the scope key is folded, and so is the identifier
		{"min_age > 1", true}, // any spelling of it
		{"ids | len() > 0", true},
	} {
		got, err := ev.Eval(c.src, scope)
		if err != nil {
			t.Errorf("Eval(%q): %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %v, want %v", c.src, got, c.want)
		}
	}
}

// A nil scope is an empty one, so an absent name is false rather than an error. Nothing reaches
// this through the renderer, which always builds a scope, so it is asserted here.
func TestNilScopeIsEmpty(t *testing.T) {
	v, err := (&exprlang.Evaluator{}).Eval("flag", nil)
	if err != nil {
		t.Fatalf("Eval with a nil scope: %v", err)
	}
	if v != nil {
		t.Errorf("Eval = %v, want nil", v)
	}
}

// The cache is keyed by the expression text with the patch already applied at compile time, so
// two goroutines compiling the same expression cannot see a half-rewritten program.
func TestConcurrentCompileOfOneExpression(t *testing.T) {
	ev := &exprlang.Evaluator{}
	done := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				v, err := ev.Eval("minAge > 1 && r.Ready()",
					map[string]any{"minage": 5, "r": ready{1}})
				if err != nil || v != true {
					done <- false
					return
				}
			}
			done <- true
		}()
	}
	for i := 0; i < 16; i++ {
		if !<-done {
			t.Fatal("a concurrent evaluation disagreed")
		}
	}
}
