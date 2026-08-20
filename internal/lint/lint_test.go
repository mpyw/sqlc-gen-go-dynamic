package lint_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/lint"
)

// A directive at column zero never reaches Query.text, so the only trace of it is the
// comment sqlc collected.
func TestLayout(t *testing.T) {
	errs := lint.Layout([]string{
		"* a plain comment ",
		"%if activeOnly*/ and u.status = $1 /*%end",
		"%end",
	})
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want two", errs)
	}
	if !strings.Contains(errs[0].Error(), "column zero") {
		t.Errorf("error = %q, want it to mention column zero", errs[0])
	}
}

func TestLayoutAcceptsOrdinaryComments(t *testing.T) {
	if errs := lint.Layout([]string{"* note ", " name: X :many"}); len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}
}
