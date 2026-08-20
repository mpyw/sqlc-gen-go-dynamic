// Package lint reports the mistakes that sqlc accepts and this cannot.
package lint

import (
	"fmt"
	"strings"
)

// Layout reports directives that sqlc lifted out of the query text.
//
// sqlc treats a block comment starting at column zero as standalone: it moves the comment
// into Query.comments and drops the line from Query.text, greedily enough that a directive
// written flush left takes the SQL beside it along. The parameter table survives, so nothing
// about the generated types looks wrong — a branch has simply vanished. Indenting the
// directive by one space avoids it.
func Layout(comments []string) []error {
	var errs []error
	for _, c := range comments {
		if !strings.HasPrefix(strings.TrimSpace(c), "%") {
			continue
		}
		errs = append(errs, fmt.Errorf("directive /*%s*/ starts at column zero, so sqlc moved it "+
			"out of the query text along with the rest of its line; indent it by at least one space", c))
	}
	return errs
}
