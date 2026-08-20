package exprtype

import (
	"fmt"
	"strings"
)

// initialisms are spelled in full caps in Go field names.
var initialisms = map[string]string{
	"id": "ID", "url": "URL", "uri": "URI", "api": "API", "db": "DB",
	"sql": "SQL", "http": "HTTP", "json": "JSON", "uuid": "UUID", "ip": "IP",
}

// GoName converts a bind or condition variable name to an exported Go field name.
func GoName(s string) string {
	var b strings.Builder
	for _, part := range splitWords(s) {
		if up, ok := initialisms[strings.ToLower(part)]; ok {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// splitWords breaks both snake_case and camelCase into their words.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i, r := range s {
		switch {
		case r == '_':
			flush()
		case r >= 'A' && r <= 'Z' && i > 0:
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return words
}

// singular is a deliberately crude de-pluralizer for naming loop element structs.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses") && len(s) > 3:
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s + "Item"
}

// namer assigns a unique Go type name to every struct in a type tree. A name is
// derived from the query name and the path that reaches the struct, which is
// lossy — groups[].tags[] and groupTags[] both read as "GroupTag" — so a
// collision falls back to the un-singularized path and then to a numeric suffix.
// Resolution depends only on first-seen field order, which is stable, so the
// generated file does not churn between runs.
type namer struct {
	query string
	taken map[string]bool
}

// NameQuery names the params struct after the query and every nested element
// struct after the path that reaches it.
func NameQuery(t *Type, query string) {
	n := &namer{query: query, taken: map[string]bool{}}
	t.Name = query + "Params"
	n.taken[t.Name] = true
	n.children(t, query, nil)
}

// children names every struct reachable through t's fields.
func (n *namer) children(t *Type, prefix string, path []string) {
	if t.Kind != Struct {
		return
	}
	for _, m := range t.Fields() {
		sub := append(append([]string{}, path...), m.Name)
		n.name(m.Type, prefix+GoName(singular(m.Name)), sub)
	}
}

// name names t itself when it is a struct, descending through slices to reach it.
func (n *namer) name(t *Type, candidate string, path []string) {
	switch t.Kind {
	case Slice:
		if t.Elem != nil {
			n.name(t.Elem, candidate, path)
		}
	case Struct:
		t.Name = n.unique(candidate, path)
		n.children(t, candidate, path)
	}
}

// unique returns candidate, or the first alternative still free.
func (n *namer) unique(candidate string, path []string) string {
	if !n.taken[candidate] {
		n.taken[candidate] = true
		return candidate
	}
	var b strings.Builder
	b.WriteString(n.query)
	for _, seg := range path {
		b.WriteString(GoName(seg))
	}
	if c := b.String(); !n.taken[c] {
		n.taken[c] = true
		return c
	}
	for i := 2; ; i++ {
		if c := fmt.Sprintf("%s%d", candidate, i); !n.taken[c] {
			n.taken[c] = true
			return c
		}
	}
}

// GoType renders the Go type expression for t.
func GoType(t *Type) string {
	switch t.Kind {
	case Struct:
		return t.Name
	case Slice:
		// A nil slice already expresses "absent", so optionality needs no pointer.
		if t.Elem == nil {
			return "[]any"
		}
		return "[]" + GoType(t.Elem)
	}
	base := t.GoType
	if base == "" {
		switch t.Kind {
		case Bool:
			base = "bool"
		case String:
			base = "string"
		case Int:
			base = "int64"
		case Float:
			base = "float64"
		default:
			base = "any"
		}
	}
	if t.Optional && !t.Explicit {
		return "*" + base
	}
	return base
}

// Declare renders t and every struct reachable from it as Go type declarations,
// parents before children.
func Declare(t *Type) string {
	var out []string
	declare(t, &out)
	return strings.Join(out, "\n\n")
}

func declare(t *Type, out *[]string) {
	switch t.Kind {
	case Slice:
		if t.Elem != nil {
			declare(t.Elem, out)
		}
		return
	case Struct:
	default:
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "type %s struct {\n", t.Name)
	members := t.Fields()
	width := 0
	for _, m := range members {
		if n := len(GoName(m.Name)); n > width {
			width = n
		}
	}
	for _, m := range members {
		fmt.Fprintf(&b, "\t%-*s %s\n", width, GoName(m.Name), GoType(m.Type))
	}
	b.WriteString("}")
	*out = append(*out, b.String())

	for _, m := range members {
		declare(m.Type, out)
	}
}
