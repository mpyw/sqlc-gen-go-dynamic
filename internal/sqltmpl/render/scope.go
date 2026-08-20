package render

import (
	"fmt"
	"reflect"
	"strings"
)

// Scope resolves the names a template refers to. Its keys are folded, and so are the names the
// expression language looks up, so a field reaches every spelling of itself: what a template
// writes (activeOnly, min_age) and what Go writes (ActiveOnly, MinAge) differ, and guessing
// which spellings to index was a guess that silently failed — an unfound name in a condition is
// nil, which is a branch that quietly disappears.
type Scope map[string]any

// Scoper is implemented by a value that names its own fields. Generated params structs do,
// which spares the reflection below; either way the keys end up folded.
type Scoper interface {
	TemplateScope() map[string]any
}

// maxPointerDepth caps the pointer unwrapping. A self-referential pointer type is legal Go and
// would otherwise spin forever inside a public API.
const maxPointerDepth = 32

// scopeOf converts a value into a Scope.
func scopeOf(v any) (Scope, error) {
	switch p := v.(type) {
	case nil:
		return Scope{}, nil
	case Scope:
		return folded(p)
	case map[string]any:
		return folded(p)
	case Scoper:
		return folded(p.TemplateScope())
	}
	rv, ok := deref(reflect.ValueOf(v))
	if !ok {
		return Scope{}, nil
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("template: cannot use %T as parameters (want a struct, a map, or a Scoper)", v)
	}
	return structScope(rv)
}

// folded re-keys a caller's map. A collision means two names that a template cannot tell apart,
// which is reported rather than resolved by iteration order.
func folded(m map[string]any) (Scope, error) {
	sc := make(Scope, len(m))
	from := make(map[string]string, len(m))
	for k, v := range m {
		f := fold(k)
		if prev, dup := from[f]; dup && prev != k && !reflect.DeepEqual(sc[f], v) {
			return nil, fmt.Errorf("template: %q and %q are the same name once case and "+
				"underscores are folded, so a template cannot say which is meant", prev, k)
		}
		from[f], sc[f] = k, v
	}
	return sc, nil
}

// structScope indexes a struct's exported fields, promoting an embedded struct's fields to
// their bare names as Go itself does: shallower wins, so an outer field shadows an embedded one
// rather than colliding with it.
func structScope(rv reflect.Value) (Scope, error) {
	sc := Scope{}
	from := map[string]string{}
	depth := map[string]int{}

	level := []reflect.Value{rv}
	for d := 0; len(level) > 0; d++ {
		var next []reflect.Value
		for _, sv := range level {
			t := sv.Type()
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Anonymous {
					if ev, ok := deref(sv.Field(i)); ok && ev.Kind() == reflect.Struct {
						next = append(next, ev)
					}
				}
				if !f.IsExported() {
					continue
				}
				key := fold(f.Name)
				if prev, seen := from[key]; seen {
					if depth[key] < d {
						continue // the shallower field wins, as Go's promotion does
					}
					if prev != f.Name {
						return nil, fmt.Errorf("template: fields %q and %q are the same name "+
							"once case and underscores are folded, so a template cannot say "+
							"which is meant", prev, f.Name)
					}
				}
				from[key], depth[key], sc[key] = f.Name, d, sv.Field(i).Interface()
			}
		}
		level = next
	}
	return sc, nil
}

// elementScope prepares a loop element for the scope. A struct has to become a Scope, since the
// expression language reads a member by name; the element itself is kept elsewhere for a bind
// that takes it whole.
func elementScope(v any) any {
	switch v.(type) {
	case nil, Scope, map[string]any:
		return v
	}
	if sc, err := scopeOf(v); err == nil {
		return sc
	}
	return v
}

// lookup resolves a dotted path: a scope entry, then a field of whatever it found.
func lookup(sc Scope, path string) (any, bool) {
	head, rest, _ := strings.Cut(path, ".")
	v, ok := sc[fold(head)]
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

// field reads a member of v by its folded name.
func field(v any, name string) (any, bool) {
	if m, ok := asMap(v); ok {
		e, ok := m[fold(name)]
		return e, ok
	}
	rv, ok := deref(reflect.ValueOf(v))
	if !ok || rv.Kind() != reflect.Struct {
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

// asMap unwraps the two map forms a scope entry can hold: a Scope, from a converted loop
// element, and a plain map, from a caller.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case Scope:
		return m, true
	case map[string]any:
		return m, true
	}
	return nil, false
}

// deref unwraps pointers up to the depth cap, reporting false for a nil along the way.
func deref(rv reflect.Value) (reflect.Value, bool) {
	for i := 0; rv.Kind() == reflect.Pointer; i++ {
		if rv.IsNil() || i == maxPointerDepth {
			return rv, false
		}
		rv = rv.Elem()
	}
	return rv, true
}

// fold collapses the spellings one name arrives in: camelCase from a template, snake_case from
// sqlc, and the exported Go form.
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
