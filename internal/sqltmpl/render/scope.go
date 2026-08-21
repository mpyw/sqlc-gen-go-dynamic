package render

import (
	"fmt"
	"reflect"
	"strings"
	"unicode"
)

// Scope is the view of a caller's params that the expression language reads. Its keys are
// folded, and so are the names an expression looks up, so a field reaches every spelling of
// itself: what a template writes (activeOnly, min_age) and what Go writes (ActiveOnly, MinAge)
// differ, and guessing which spellings to index was a guess that silently failed — an unfound
// name in a condition is nil, which is a branch that quietly disappears.
//
// Folding has to reach every level, because the expression language resolves a member itself
// and does it by exact name: a nested map or struct left as it came makes `f.minAge` nil.
// A bind does not read this view at all — see rawLookup.
type Scope map[string]any

// Scoper is implemented by a value that names its own fields. Generated params structs do,
// which spares the reflection below; either way the keys end up folded.
type Scoper interface {
	TemplateScope() map[string]any
}

const (
	// maxPointerDepth caps pointer unwrapping, so that a cycle of pointers cannot spin here.
	maxPointerDepth = 32
	// maxFoldDepth caps how far the folded view is built. A params graph is shallow; the cap
	// is what makes a cyclic one terminate.
	maxFoldDepth = 16
)

// scopeOf builds the folded view of params.
func scopeOf(v any) (Scope, error) {
	if v == nil {
		return Scope{}, nil
	}
	if s, ok := v.(Scoper); ok {
		return foldMap(reflect.ValueOf(s.TemplateScope()), 0)
	}
	rv, ok := deref(reflect.ValueOf(v))
	if !ok {
		return nil, fmt.Errorf("template: parameters is a nil %T, so no name resolves; pass a "+
			"value, or nil for none", v)
	}
	switch {
	case stringKeyed(rv):
		return foldMap(rv, 0)
	case rv.Kind() == reflect.Struct:
		return structScope(rv, 0)
	}
	return nil, fmt.Errorf("template: cannot use %T as parameters (want a struct, a "+
		"string-keyed map, or a Scoper)", v)
}

// stringKeyed reports whether rv is a map a template can name the entries of. Any named type
// whose underlying type is one counts, since that is what the expression language indexes too.
func stringKeyed(rv reflect.Value) bool {
	return rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String
}

// foldMap re-keys a map. A collision is two names a template cannot tell apart, which is
// reported rather than resolved by iteration order — the keys are the caller's own, so an
// ambiguity among them is a mistake worth naming.
func foldMap(rv reflect.Value, depth int) (Scope, error) {
	if !rv.IsValid() {
		return Scope{}, nil
	}
	sc := make(Scope, rv.Len())
	from := make(map[string]string, rv.Len())
	for iter := rv.MapRange(); iter.Next(); {
		k := iter.Key().String()
		v := converted(iter.Value().Interface(), depth)
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
// their bare names as Go itself does: shallower wins, so an outer field shadows an embedded
// one. Two that fold together at the same depth are ambiguous, which Go also refuses.
func structScope(rv reflect.Value, depth int) (Scope, error) {
	sc := Scope{}
	from := map[string]string{}
	at := map[string]int{}

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
					if at[key] < d {
						continue // the shallower field wins, as Go's promotion does
					}
					return nil, fmt.Errorf("template: %q and %q are the same name to a template "+
						"(case and underscores carry no meaning, and an embedded struct's fields "+
						"are promoted), so it cannot say which is meant", prev, f.Name)
				}
				from[key], at[key], sc[key] = f.Name, d, converted(sv.Field(i).Interface(), depth)
			}
		}
		level = next
	}
	return sc, nil
}

// converted returns the folded view of one value, or the value itself when there is nothing to
// fold. Maps and structs are walked; a slice is not, because a loop folds each element it
// reaches and a list bound whole has no names to resolve.
//
// A struct with no exported fields is a value rather than a shape — time.Time and the drivers'
// own wrappers are structs, and reading their insides is not what a template means by a name.
//
// An error below the top level keeps the raw value instead of failing the render: a params
// struct may hold a third-party type whose field names are none of the template's business.
func converted(v any, depth int) any {
	if v == nil || depth >= maxFoldDepth {
		return v
	}
	if s, ok := v.(Scoper); ok {
		if sc, err := foldMap(reflect.ValueOf(s.TemplateScope()), depth+1); err == nil {
			return sc
		}
		return v
	}
	rv, ok := deref(reflect.ValueOf(v))
	if !ok {
		return v // a nil pointer stays itself, so that a null test still sees nil
	}
	switch {
	case stringKeyed(rv):
		if sc, err := foldMap(rv, depth+1); err == nil {
			return sc
		}
	case rv.Kind() == reflect.Struct && hasExported(rv.Type()):
		if sc, err := structScope(rv, depth+1); err == nil {
			return sc
		}
	}
	return v
}

func hasExported(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			return true
		}
	}
	return false
}

// rawLookup resolves a dotted path against the value the caller passed, rather than against the
// folded view. A bind has to hand the driver what it was given: a time.Time is a time.Time, and
// a struct the driver knows how to write must not arrive as a map of its fields.
func rawLookup(root any, path string) (any, bool) {
	v := root
	for _, seg := range strings.Split(path, ".") {
		var ok bool
		if v, ok = member(v, seg); !ok {
			return nil, false
		}
	}
	return v, true
}

// member reads one name from a value by its folded spelling: an entry of a Scoper's own map, an
// entry of a string-keyed map, or a struct field, promoting an embedded struct's fields as Go
// does.
func member(v any, name string) (any, bool) {
	want := fold(name)
	if s, ok := v.(Scoper); ok {
		return mapMember(reflect.ValueOf(s.TemplateScope()), want)
	}
	rv, ok := deref(reflect.ValueOf(v))
	if !ok || !rv.IsValid() {
		return nil, false
	}
	switch {
	case stringKeyed(rv):
		return mapMember(rv, want)
	case rv.Kind() == reflect.Struct:
		return structMember(rv, want)
	}
	return nil, false
}

func mapMember(rv reflect.Value, want string) (any, bool) {
	if !rv.IsValid() {
		return nil, false
	}
	for iter := rv.MapRange(); iter.Next(); {
		if fold(iter.Key().String()) == want {
			return iter.Value().Interface(), true
		}
	}
	return nil, false
}

// structMember searches by promotion depth, so that a shallower field wins as Go's own
// promotion does. An ambiguity at one depth was already reported when the folded view was
// built, so the first match at the shallowest depth is the only one.
func structMember(rv reflect.Value, want string) (any, bool) {
	level := []reflect.Value{rv}
	for len(level) > 0 {
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
				if f.IsExported() && fold(f.Name) == want {
					return sv.Field(i).Interface(), true
				}
			}
		}
		level = next
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
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
