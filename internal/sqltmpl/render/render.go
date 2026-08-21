// Package render turns a template tree into SQL and arguments.
//
// Text is emitted verbatim. Directives are evaluated and nothing else happens: no
// empty-clause removal, no dangling-connector cleanup, no whitespace normalization, and
// nothing inserted between loop iterations. Templates anchor their dynamic fragments
// instead — a 1 = 1 seed, a connector at the head of each fragment.
//
// Placeholder numbering runs off one counter for the whole render, so a bind in a branch
// that did not render is never counted and the numbering has no gaps.
package render

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/dialect"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
)

// Evaluator evaluates a directive condition or iterable against a scope.
type Evaluator interface {
	Eval(expression string, scope map[string]any) (any, error)
}

// Result is a rendered statement.
type Result struct {
	SQL  string
	Args []any
}

// Render renders nodes with params, which may be a struct, a map, or a Scoper.
func Render(nodes []ast.Node, params any, d dialect.Dialect, ev Evaluator) (Result, error) {
	sc, err := scopeOf(params)
	if err != nil {
		return Result{}, err
	}
	r := &renderer{d: d, ev: ev, raw: params, elems: map[string]any{}}
	if err := r.nodes(nodes, sc); err != nil {
		return Result{}, err
	}
	return Result{SQL: r.sql.String(), Args: r.args}, nil
}

type renderer struct {
	d   dialect.Dialect
	ev  Evaluator
	sql strings.Builder
	// raw is the params value as the caller passed it. A bind resolves from here rather than
	// from the scope, so that what reaches the driver is what was given: the scope is a
	// folded view built for the expression language, and handing one of its maps to a driver
	// in place of a struct is a wrong value, not a wrong name.
	raw any
	// elems holds each loop variable's element as it came, keyed by the folded name, for the
	// same reason.
	elems map[string]any
	args  []any
}

func (r *renderer) nodes(ns []ast.Node, sc Scope) error {
	for _, n := range ns {
		if err := r.node(n, sc); err != nil {
			return err
		}
	}
	return nil
}

func (r *renderer) node(n ast.Node, sc Scope) error {
	switch n := n.(type) {
	case ast.Text:
		r.sql.WriteString(n.S)
		return nil
	case ast.Bind:
		return r.bind(n)
	case ast.If:
		return r.conditional(n, sc)
	case ast.For:
		return r.loop(n, sc)
	}
	return fmt.Errorf("template: unknown node %T", n)
}

// bind resolves the marker's name as a path rather than an expression: a marker is always a
// name, so it needs no evaluator, and resolving it here keeps the folded-name rules in one
// place.
func (r *renderer) bind(n ast.Bind) error {
	v, ok := r.resolve(n.Name)
	if !ok {
		return fmt.Errorf("template: no value for parameter %q", n.Name)
	}
	if !n.List {
		r.arg(v)
		return nil
	}
	elems, ok := iterable(v)
	if !ok {
		elems = []any{v} // a scalar where a list was asked for
	}
	// An empty list has no placeholders to emit and would leave the enclosing `in ()`
	// invalid, so it renders as null — which matches nothing, as an empty IN list means.
	if len(elems) == 0 {
		r.sql.WriteString("null")
		return nil
	}
	for i, e := range elems {
		if i > 0 {
			r.sql.WriteString(", ")
		}
		r.arg(e)
	}
	return nil
}

// resolve reads the value a marker names, from the values the caller passed. A loop variable
// is looked up first, since it shadows a param of the same name for the length of its body.
func (r *renderer) resolve(path string) (any, bool) {
	head, rest, _ := strings.Cut(path, ".")
	if v, ok := r.elems[fold(head)]; ok {
		if rest == "" {
			return v, true
		}
		return rawLookup(v, rest)
	}
	return rawLookup(r.raw, path)
}

func (r *renderer) conditional(n ast.If, sc Scope) error {
	for _, arm := range n.Arms {
		if arm.Cond == "" { // else
			return r.nodes(arm.Body, sc)
		}
		v, err := r.eval(arm.Cond, sc)
		if err != nil {
			return err
		}
		ok, err := truthy(arm.Cond, v)
		if err != nil {
			return err
		}
		if ok {
			return r.nodes(arm.Body, sc)
		}
	}
	return nil
}

// loop renders the body once per element, with the loop variable added to a copy of the
// scope so that it does not outlive the loop. A nil or absent iterable yields no iterations.
func (r *renderer) loop(n ast.For, sc Scope) error {
	v, err := r.eval(n.Iter, sc)
	if err != nil {
		return err
	}
	elems, ok := iterable(v)
	if !ok {
		if v == nil {
			return nil
		}
		return fmt.Errorf("template: /*%%for %s in %s*/ needs a slice, got %T", n.Var, n.Iter, v)
	}
	inner := make(Scope, len(sc)+1)
	for k, e := range sc {
		inner[k] = e
	}
	// The loop variable is a name like any other, so it is keyed folded on both sides: a
	// condition or a marker inside the body may spell it either way, and the compile-time
	// inference already assumes they agree.
	key := fold(n.Var)
	// A nested loop may reuse the name, so the outer element is restored on the way out.
	outer, hadOuter := r.elems[key]
	defer func() {
		if hadOuter {
			r.elems[key] = outer
			return
		}
		delete(r.elems, key)
	}()

	for _, e := range elems {
		inner[key] = converted(e, 0)
		r.elems[key] = e
		if err := r.nodes(n.Body, inner); err != nil {
			return err
		}
	}
	return nil
}

func (r *renderer) arg(v any) {
	r.args = append(r.args, v)
	r.sql.WriteString(r.d.Placeholder(len(r.args)))
}

func (r *renderer) eval(expression string, sc Scope) (any, error) {
	v, err := r.ev.Eval(expression, sc)
	if err != nil {
		return nil, fmt.Errorf("template: evaluating %q: %w", expression, err)
	}
	return v, nil
}

// truthy reads a condition's value. A nil condition is false, which is what makes an absent
// parameter a skipped branch rather than an error.
func truthy(expression string, v any) (bool, error) {
	switch v := v.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	}
	return false, fmt.Errorf("template: condition %q evaluated to %T, want bool", expression, v)
}

// iterable reports whether v is a slice or array and returns its elements. Strings and
// []byte are scalars.
func iterable(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		// Bytes are a value, not a list of values, whatever the slice type is named.
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return nil, false
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out, true
	}
	return nil, false
}
