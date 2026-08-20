// Package lint reports the mistakes that sqlc accepts and this cannot.
//
// Every check here has to be exact about what it fires on. These run against every query sqlc
// reports, including ones with no directives at all, and a false positive does not degrade
// output — it aborts the whole generate, taking any sibling codegen with it.
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
		if !IsDirective(c) {
			continue
		}
		errs = append(errs, fmt.Errorf("directive /*%s*/ starts at column zero, so sqlc moved it "+
			"out of the query text along with the rest of its line; indent it by at least one space", c))
	}
	return errs
}

// directiveWords are the only things a /*% ... */ comment can open with. Matching on a bare
// "%" would catch prose: a line comment reading "% of users active" is not a directive, and
// treating it as one aborts a generate over a sentence.
var directiveWords = []string{"if", "elseif", "else", "end", "for"}

// IsDirective reports whether a comment body, as sqlc reports it with the delimiters stripped,
// is one of this plugin's directives.
func IsDirective(body string) bool {
	rest, ok := cutPrefix(strings.TrimSpace(body), "%")
	if !ok || strings.HasPrefix(rest, "!") {
		return false
	}
	word := rest
	if i := strings.IndexAny(rest, " \t\r\n*"); i >= 0 {
		word = rest[:i]
	}
	for _, w := range directiveWords {
		if word == w {
			return true
		}
	}
	return false
}

func cutPrefix(s, prefix string) (string, bool) {
	if !strings.HasPrefix(s, prefix) {
		return s, false
	}
	return s[len(prefix):], true
}
