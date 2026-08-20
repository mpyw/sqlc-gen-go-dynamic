// Package bind recognizes sqlc's parameter markers.
//
// Recognition mirrors sqlc's exactly. A spelling one side binds and the other does not is
// the failure this arrangement exists to prevent: sqlc would type a parameter that never
// gets bound, or reject a template that renders fine.
package bind

import (
	"fmt"
	"strings"
)

// Marker is a recognized parameter marker.
type Marker struct {
	Name string
	List bool // sqlc.slice: renders as a placeholder list
	Len  int  // bytes spanned
}

// Rules are the spellings a template may use, and the comment syntax the engine has. Both are
// dialect-dependent because sqlc's reading of them is: sqlc does not support the @name shortcut
// for MySQL, where @name is a user variable, and MySQL alone treats # as a line comment.
type Rules struct {
	AtForm       bool
	HashComments bool
}

// RulesFor resolves the rules for an engine name. An engine it does not know gets the narrower
// reading: recognizing a marker that sqlc did not is the failure worth avoiding.
func RulesFor(engine string) Rules {
	switch engine {
	case "postgresql", "sqlite":
		return Rules{AtForm: true}
	case "mysql":
		return Rules{HashComments: true}
	}
	return Rules{}
}

// OperatorByte reports whether b can be part of a SQL operator. A marker is only a marker when
// what precedes it is not one: `<@`, `@@` and their relatives end in @, and reading the name
// after them as a bind invents a parameter.
func OperatorByte(b byte) bool {
	switch b {
	case '+', '-', '*', '/', '<', '>', '=', '~', '!', '@', '#', '%', '^', '&', '|', '`', '?':
		return true
	}
	return false
}

var callForms = []struct {
	prefix string
	list   bool
}{
	{"sqlc.arg(", false},
	{"sqlc.narg(", false},
	{"sqlc.slice(", true},
}

// Recognize reports the marker at the start of s. It reads only s's prefix and never scans
// ahead, so a caller that tracks quotes and comments keeps that tracking.
//
// A bare @name ends at the first character that cannot continue an identifier, so @a.b
// yields @a — the reading sqlc makes too. Malformed is what distinguishes such a prefix from
// a bind.
func (r Rules) Recognize(s string) (Marker, bool) {
	if r.AtForm {
		if name, n, ok := atName(s); ok {
			return Marker{Name: name, Len: n}, true
		}
	}
	for _, f := range callForms {
		if !strings.HasPrefix(s, f.prefix) {
			continue
		}
		name, n, ok := argument(s[len(f.prefix):])
		if !ok {
			return Marker{}, false
		}
		return Marker{Name: name, List: f.list, Len: len(f.prefix) + n}, true
	}
	return Marker{}, false
}

// Malformed reports a reason when s begins with something that can only have been meant as
// a marker but cannot be one, so a caller can fail instead of passing it through.
//
// Both cases would otherwise become plausible SQL that is wrong. A dotted @a.b binds only
// "a" and leaves ".b", rendering as "$1.b"; sqlc reads it the same way and then rejects the
// edited query, a second step a renderer does not have. A call whose argument is neither a
// bare nor a quoted name matches nothing and is emitted verbatim, as a call to a function
// that does not exist.
//
// Consult it before Recognize, which accepts the leading @a of a dotted name.
func (r Rules) Malformed(s string) (string, bool) {
	if name, n, ok := atName(s); ok && r.AtForm {
		if n < len(s) && s[n] == '.' {
			return fmt.Sprintf("@%s is followed by a period: a dotted name has to be written as "+
				"sqlc.arg('%s.…'), since @%s.… reads as the parameter @%s and then trailing text",
				name, name, name, name), true
		}
		return "", false
	}
	for _, f := range callForms {
		if !strings.HasPrefix(s, f.prefix) {
			continue
		}
		rest := s[len(f.prefix):]
		if _, _, ok := argument(rest); ok {
			return "", false
		}
		if name, n, ok := leadingIdent(rest); ok && n < len(rest) && rest[n] == '.' {
			return fmt.Sprintf("%s%s.…) has a dotted name, which has to be quoted: %s'%s.…')",
				f.prefix, name, f.prefix, name), true
		}
		return fmt.Sprintf("%s…) takes a name, either bare as %sname) or quoted as %s'a.name')",
			f.prefix, f.prefix, f.prefix), true
	}
	return "", false
}

// atName reads @name. The name is a bare identifier that follows the @ directly: PostgreSQL
// has operators ending in @, and skipping space here turned `array[…] <@ tags` into a bind on
// `tags` that sqlc never made. A dotted name has only the quoted call spelling, since sqlc
// rejects @a.b too.
func atName(s string) (string, int, bool) {
	if !strings.HasPrefix(s, "@") {
		return "", 0, false
	}
	i := 1
	for i < len(s) && isIdent(s[i], i == 1) {
		i++
	}
	if i == 1 {
		return "", 0, false
	}
	return s[1:i], i, true
}

// argument reads a call argument, `name)` or `'name')`. Only the quoted spelling may carry a
// dot: an unquoted a.b parses as a column reference, which sqlc rejects.
func argument(s string) (string, int, bool) {
	if name, i, ok := leadingIdent(s); ok {
		i = skipSpace(s, i)
		if i < len(s) && s[i] == ')' {
			return name, i + 1, true
		}
	}
	i := skipSpace(s, 0)
	if i >= len(s) || s[i] != '\'' {
		return "", 0, false
	}
	i++
	start := i
	for i < len(s) && s[i] != '\'' {
		i++
	}
	if i >= len(s) || i == start {
		return "", 0, false
	}
	name := s[start:i]
	i = skipSpace(s, i+1)
	if i >= len(s) || s[i] != ')' {
		return "", 0, false
	}
	return name, i + 1, true
}

// leadingIdent reads the identifier at the start of s, after any space, and reports how far
// it read. It does not care what follows, which lets a caller tell `name)` from `name.other)`.
func leadingIdent(s string) (string, int, bool) {
	i := skipSpace(s, 0)
	start := i
	for i < len(s) && isIdent(s[i], i == start) {
		i++
	}
	if i == start {
		return "", 0, false
	}
	return s[start:i], i, true
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	return i
}

func isIdent(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}
