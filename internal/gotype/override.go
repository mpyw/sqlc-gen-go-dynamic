package gotype

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Override maps a database type onto a Go type by hand. It exists because the built-in table
// is small and refuses what it does not know: an override turns an unmapped type from a
// blocker into one line of configuration, without a guess being written into the table.
//
// The shape matches sqlc's own Go codegen, so a project switching over keeps its overrides.
type Override struct {
	DBType   string `json:"db_type"`
	Nullable bool   `json:"nullable"`
	GoType   GoType `json:"go_type"`
}

// GoType is an override's target. sqlc accepts either a qualified string or an object, so
// both are read here.
type GoType struct {
	Import  string `json:"import"`
	Package string `json:"package"`
	Type    string `json:"type"`
	Slice   bool   `json:"slice"`
	Pointer bool   `json:"pointer"`
}

// UnmarshalJSON reads the string form, "github.com/shopspring/decimal.Decimal", as well as
// the object form.
func (g *GoType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*g = parseQualified(s)
		return nil
	}
	type plain GoType
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("go_type is neither a string nor an object: %w", err)
	}
	*g = GoType(p)
	return nil
}

// parseQualified splits "path/to/pkg.Type" into its import and its qualified name. A name
// with no path is a builtin or a type already in scope.
func parseQualified(s string) GoType {
	dot := strings.LastIndex(s, ".")
	if dot < 0 || !strings.Contains(s[:dot], "/") {
		return GoType{Type: s}
	}
	path := s[:dot]
	return GoType{Import: path, Package: path[strings.LastIndex(path, "/")+1:], Type: s[dot+1:]}
}

// resolve renders the override as a Type.
func (g GoType) resolve() Type {
	name := g.Type
	if g.Package != "" {
		name = g.Package + "." + name
	}
	if g.Pointer {
		name = "*" + name
	}
	if g.Slice {
		name = "[]" + name
	}
	return Type{Name: name, Import: g.Import, Explicit: true}
}

// Overrides is a set of overrides, consulted before the built-in table.
type Overrides []Override

// find returns the override for a type, preferring one that also matches on nullability so
// that a nullable column can be given its own target.
func (os Overrides) find(dbType string, notNull bool) (Type, bool) {
	var fallback *Override
	for i := range os {
		o := &os[i]
		if unqualify(o.DBType) != dbType {
			continue
		}
		if o.Nullable {
			if !notNull {
				return o.GoType.resolve(), true
			}
			continue
		}
		if fallback == nil {
			fallback = o
		}
	}
	if fallback != nil {
		return fallback.GoType.resolve(), true
	}
	return Type{}, false
}
