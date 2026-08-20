package render

import (
	"fmt"
	"reflect"
	"strings"
)

// Scope resolves the names a template refers to.
type Scope map[string]any

// Scoper is implemented by a value that names its own fields. Generated params structs do,
// because only the generator knows both spellings: the template writes activeOnly and
// c.name, while Go writes ActiveOnly and Name.
type Scoper interface {
	TemplateScope() map[string]any
}

// scopeOf converts a value into a Scope. A Scoper says so itself; a map is taken as is; a
// struct is reflected, with its field names folded so that either spelling resolves.
func scopeOf(v any) (Scope, error) {
	switch p := v.(type) {
	case nil:
		return Scope{}, nil
	case Scope:
		return p, nil
	case map[string]any:
		return p, nil
	case Scoper:
		return p.TemplateScope(), nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return Scope{}, nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("template: cannot use %T as parameters (want a struct, a map, or a Scoper)", v)
	}
	return structScope(rv), nil
}

// structScope indexes a struct's exported fields under every spelling a template might use.
// It has to: a condition is evaluated by the expression language against these keys, which
// resolves them exactly, and a plain struct carries no record of what the template wrote.
// Generated code implements Scoper instead and gives the exact keys.
func structScope(rv reflect.Value) Scope {
	t := rv.Type()
	sc := make(Scope, t.NumField()*4)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		v := rv.Field(i).Interface()
		for _, k := range spellings(f.Name) {
			sc[k] = v
		}
	}
	return sc
}

// spellings lists the forms an exported Go name is written in elsewhere: as itself, as
// camelCase in a directive condition, as snake_case in a sqlc parameter, and folded.
func spellings(goName string) []string {
	out := []string{goName}
	for _, s := range []string{lowerFirst(goName), snake(goName), fold(goName)} {
		if !slicesContains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func slicesContains(ss []string, s string) bool {
	for _, e := range ss {
		if e == s {
			return true
		}
	}
	return false
}

// lowerFirst lowercases the leading run of capitals but one, so ActiveOnly becomes
// activeOnly and ID becomes id.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	n := 0
	for n < len(s) && s[n] >= 'A' && s[n] <= 'Z' {
		n++
	}
	if n > 1 && n < len(s) {
		n-- // the last capital starts the next word
	}
	return strings.ToLower(s[:n]) + s[n:]
}

// snake converts an exported Go name to snake_case, keeping a run of capitals together so
// APIURL becomes api_url.
func snake(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		upper := c >= 'A' && c <= 'Z'
		if upper && i > 0 {
			prevLower := s[i-1] >= 'a' && s[i-1] <= 'z' || s[i-1] >= '0' && s[i-1] <= '9'
			nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
			if prevLower || nextLower {
				b.WriteByte('_')
			}
		}
		if upper {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// lookup resolves a dotted path: a scope entry, then a field of whatever it found.
func lookup(sc Scope, path string) (any, bool) {
	head, rest, _ := strings.Cut(path, ".")
	v, ok := entry(sc, head)
	if !ok || rest == "" {
		return v, ok
	}
	for _, seg := range strings.Split(rest, ".") {
		v, ok = field(v, seg)
		if !ok {
			return nil, false
		}
	}
	return v, true
}

func entry(sc Scope, name string) (any, bool) {
	if v, ok := sc[name]; ok {
		return v, true
	}
	v, ok := sc[fold(name)]
	return v, ok
}

// field reads a member of v, matching a folded name so that name finds Name.
func field(v any, name string) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		if e, ok := m[name]; ok {
			return e, true
		}
		e, ok := m[fold(name)]
		return e, ok
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, false
	}
	want := fold(name)
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() && fold(f.Name) == want {
			return rv.Field(i).Interface(), true
		}
	}
	return nil, false
}

// fold collapses the spellings one name arrives in: camelCase from a template, snake_case
// from sqlc, and the exported Go form.
func fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
