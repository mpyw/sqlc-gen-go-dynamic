package exprtype

import (
	"strings"
	"unicode"
)

// fold collapses the spellings one name arrives in: camelCase from a template, snake_case from
// sqlc, and the exported Go form. It matches render's and the evaluator's folding, and the three
// have to stay identical — a name that folds differently at build time and at run time is a
// parameter that types one way and resolves another.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
