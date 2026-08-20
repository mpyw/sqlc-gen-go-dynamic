// Package exprtype infers Go parameter types for a bisql template rendered in
// "sqlc mode": values are bound with sqlc's own parameter syntax (so sqlc types
// them from the catalog) while structure is expressed with /*%if*/ and /*%for*/
// directive comments, whose meaning sqlc never sees.
//
// The type of a bind comes from sqlc and is authoritative. The type of a
// *condition* variable — one that appears only inside a directive expression and
// never in the SQL — has to be inferred here, from three sources and no others:
//
//   - the boolean position a variable is used in, which pins bool;
//   - a literal it is compared against, which pins one of string, int64, float64,
//     bool — the only types expr's literals can express;
//   - a /*%for*/ over it whose body binds something, which pins a slice of
//     whatever sqlc says those binds are.
//
// Everything else only *constrains* a variable without determining a Go type —
// len() accepts strings, slices and maps alike — and a constraint is not a type.
// Inference refuses those rather than guessing, because a plausible wrong type is
// worse than a build error that says what to change.
package exprtype

import "strings"

// Kind is the shape of a value, once inference has pinned one.
type Kind uint8

const (
	Unknown Kind = iota
	Bool
	String
	Int
	Float
	Opaque // a scalar whose Go type sqlc gave us but whose shape we cannot reason about
	Slice
	Struct
)

func (k Kind) String() string {
	switch k {
	case Bool:
		return "bool"
	case String:
		return "string"
	case Int:
		return "int"
	case Float:
		return "float"
	case Opaque:
		return "scalar"
	case Slice:
		return "slice"
	case Struct:
		return "struct"
	}
	return "unknown"
}

// Constraint is a fact about a value that narrows it to a family of types without
// determining one. Constraints never produce a Go type; they exist so that a
// refusal can explain itself.
type Constraint uint8

const (
	// Sized is what len() proves: expr's len accepts strings, slices and maps.
	Sized Constraint = 1 << iota
	// Container is what the "in" operator proves: its right side may be a slice or
	// a map, but not a string.
	Container
	// Numeric is what unary minus proves: int or float, with nothing to choose.
	Numeric
)

// describe renders the constraints as a phrase for a diagnostic.
func (c Constraint) describe() string {
	var parts []string
	if c&Sized != 0 {
		parts = append(parts, "has a length (len() accepts strings, slices and maps alike)")
	}
	if c&Container != 0 {
		parts = append(parts, "is a container (the \"in\" operator accepts slices and maps)")
	}
	if c&Numeric != 0 {
		parts = append(parts, "is numeric (int and float are both possible)")
	}
	return strings.Join(parts, ", and ")
}

// Type is an inferred type. Struct and Slice are the only composites: a Struct
// models the params struct and every loop element struct, a Slice models a
// /*%for*/ iterable or a sqlc.slice() bind.
type Type struct {
	Kind        Kind
	Constraints Constraint
	Optional    bool   // nil-tested in a condition, or nullable per sqlc
	GoType      string // set when sqlc supplied a concrete Go type
	Elem        *Type  // Slice
	Name        string // Struct: generated type name, filled in by naming

	fields map[string]*field // Struct, keyed by normalized name
	order  []string          // normalized keys, first-seen order
	why    string            // provenance of Kind, for conflict messages
}

type field struct {
	name string   // display name, preferring snake_case when both spellings are seen
	seen []string // every spelling the template named it by, in first-seen order
	typ  *Type
}

func newStruct() *Type {
	return &Type{Kind: Struct, fields: map[string]*field{}}
}

// field returns the named member of a struct type, creating it if absent. It
// promotes t to a struct if its shape was not yet known.
func (t *Type) field(name string) *Type {
	if t.fields == nil {
		t.fields = map[string]*field{}
	}
	if t.Kind == Unknown {
		t.Kind = Struct
		t.why = "member access"
	}
	key := normalize(name)
	if f, ok := t.fields[key]; ok {
		// sqlc spells names with underscores; prefer that spelling for Go naming.
		if strings.Contains(name, "_") && !strings.Contains(f.name, "_") {
			f.name = name
		}
		if !contains(f.seen, name) {
			f.seen = append(f.seen, name)
		}
		return f.typ
	}
	ft := &Type{}
	t.fields[key] = &field{name: name, seen: []string{name}, typ: ft}
	t.order = append(t.order, key)
	return ft
}

// elem returns a slice's element type, creating it on first use.
func (t *Type) elem() *Type {
	if t.Elem == nil {
		t.Elem = &Type{}
	}
	return t.Elem
}

// Member is one field of a struct type. Spellings lists every name the template used for it,
// which is what a generated scope has to be keyed by: a condition may write minAge while the
// marker beside it writes min_age, and both have to resolve.
type Member struct {
	Name      string
	Spellings []string
	Type      *Type
}

func contains(ss []string, s string) bool {
	for _, e := range ss {
		if e == s {
			return true
		}
	}
	return false
}

// Fields returns the struct's members in first-seen order.
func (t *Type) Fields() []Member {
	out := make([]Member, 0, len(t.order))
	for _, k := range t.order {
		f := t.fields[k]
		out = append(out, Member{Name: f.name, Spellings: f.seen, Type: f.typ})
	}
	return out
}

// normalize folds the two spellings a name arrives in — camelCase from directive
// expressions, snake_case from sqlc parameters — onto one key.
func normalize(s string) string {
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

// kindOfGoType maps a Go type name from sqlc onto a Kind.
func kindOfGoType(goType string) Kind {
	switch goType {
	case "bool":
		return Bool
	case "string":
		return String
	case "int", "int16", "int32", "int64", "uint32", "uint64":
		return Int
	case "float32", "float64":
		return Float
	}
	return Opaque
}
