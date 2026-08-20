package exprtype

import (
	"fmt"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
)

// SQLParam is one entry of sqlc's parameter table, already mapped to a Go type.
type SQLParam struct {
	Name    string // sqlc's column name: "status", or "c.name" from sqlc.arg('c.name')
	GoType  string // "string", "int64", "time.Time", ...
	NotNull bool   // false for sqlc.narg() and for a nullable column alike
}

// Diagnostic is something codegen must not silently guess about.
type Diagnostic struct {
	Path string // dotted path of the variable, e.g. "conds.name"
	Msg  string
}

func (d Diagnostic) String() string { return d.Path + ": " + d.Msg }

type scopeEntry struct {
	name string // loop variable
	elem *Type  // the element type it is bound to
}

type inferrer struct {
	root    *Type
	scope   []scopeEntry
	params  map[string]SQLParam
	aliases [][2]*Type // places an expression proved to have the same type
	diags   []Diagnostic
}

// Infer walks a template tree and returns the params struct type together with everything it
// could not decide.
func Infer(nodes []ast.Node, params []SQLParam) (*Type, []Diagnostic) {
	in := &inferrer{root: newStruct(), params: map[string]SQLParam{}}
	for _, p := range params {
		in.params[normalize(p.Name)] = p
	}
	in.nodes(nodes)
	in.settleAliases()
	in.checkUndecided(in.root, "")
	return in.root, in.diags
}

func (in *inferrer) nodes(ns []ast.Node) {
	for _, n := range ns {
		switch n := n.(type) {
		case ast.Bind:
			in.bind(n)
		case ast.If:
			for _, arm := range n.Arms {
				if arm.Cond != "" {
					in.condition(arm.Cond)
				}
				in.nodes(arm.Body)
			}
		case ast.For:
			in.loop(n)
		}
	}
}

// loop resolves the iterable to a slice and binds the loop variable to its element type for
// the duration of the body. The iterable is resolved before the variable is pushed, so a
// `for a in a.ys` nested inside `for a in xs` reads the outer a.
func (in *inferrer) loop(n ast.For) {
	p := in.resolveExpr(n.Iter)
	if !p.ok {
		return
	}
	in.unify(p.typ, Slice, "/*%for*/ iterable", p.path)
	in.scope = append(in.scope, scopeEntry{name: n.Var, elem: p.typ.elem()})
	defer func() { in.scope = in.scope[:len(in.scope)-1] }()
	in.nodes(n.Body)
}

// bind applies sqlc's authoritative type to the path a marker names. Whether the bind
// expands to a list comes from the marker, since that is what renders.
func (in *inferrer) bind(n ast.Bind) {
	p, ok := in.params[normalize(n.Name)]
	if !ok {
		in.diags = append(in.diags, Diagnostic{Path: n.Name, Msg: "no sqlc parameter of this name"})
		return
	}
	t := in.resolvePath(strings.Split(n.Name, "."))
	target := t
	if n.List {
		in.unify(t, Slice, "sqlc.slice()", n.Name)
		target = t.elem()
	}
	in.unify(target, kindOfGoType(p.GoType), "sqlc parameter", n.Name)
	target.GoType = p.GoType

	// Follow sqlc verbatim on nullability. NotNull is false for both sqlc.narg('x') and a
	// plain sqlc.arg('x') against a nullable column — the request does not distinguish them
	// — and there is no need to: a nullable Go type for either is what sqlc's own Go codegen
	// emits, so existing queries keep the types their authors expect.
	if !p.NotNull {
		target.Optional = true
	}

	// Sitting inside /*%if*/ or /*%for*/ is deliberately not a source of optionality: when
	// the branch does not render, nothing reads the value, so the zero value is a fine
	// stand-in. What does force a pointer is a nil test in a condition, where unset and zero
	// must be distinguishable or the branch decision itself is wrong.
}

// condition infers types for the variables of a directive condition, which is
// evaluated for its truth value.
func (in *inferrer) condition(src string) {
	tree, err := parser.Parse(src)
	if err != nil {
		in.diags = append(in.diags, Diagnostic{Path: src, Msg: "cannot parse condition: " + err.Error()})
		return
	}
	in.visit(tree.Node, Bool, src)
}

// place is the result of resolving an expression that denotes a variable, a field
// of one, or an element of one.
type place struct {
	typ  *Type
	path string
	ok   bool
}

// visit infers the type of node given the kind the surrounding expression expects
// of it, recording what it learns about any variable it reaches.
func (in *inferrer) visit(n exprast.Node, want Kind, src string) Kind {
	switch n := n.(type) {
	case *exprast.NilNode:
		return Unknown

	case *exprast.BoolNode:
		return Bool
	case *exprast.StringNode:
		return String
	case *exprast.IntegerNode:
		return Int
	case *exprast.FloatNode:
		return Float

	case *exprast.IdentifierNode, *exprast.MemberNode:
		if isNil(n) {
			return Unknown
		}
		p := in.resolveNode(n, src)
		if !p.ok {
			return Unknown
		}
		in.unify(p.typ, want, "condition expression", p.path)
		return p.typ.Kind

	case *exprast.ChainNode: // a?.b
		return in.visit(n.Node, want, src)

	case *exprast.UnaryNode:
		switch n.Operator {
		case "!", "not":
			in.visit(n.Node, Bool, src)
			return Bool
		case "-", "+":
			// A numeric context passes through the sign; without one, all that is
			// known is that the operand is numeric, and int and float are equally
			// possible — a constraint, not a type.
			if want == Int || want == Float {
				return in.visit(n.Node, want, src)
			}
			in.constrain(n.Node, Numeric, src)
			return Unknown
		}
		return Unknown

	case *exprast.BinaryNode:
		return in.binary(n, src)

	case *exprast.BuiltinNode:
		return in.builtin(n, src)

	case *exprast.ConditionalNode:
		in.visit(n.Cond, Bool, src)
		k := in.visit(n.Exp1, want, src)
		in.visit(n.Exp2, want, src)
		return k

	case *exprast.ArrayNode:
		for _, e := range n.Nodes {
			in.visit(e, Unknown, src)
		}
		return Slice

	case *exprast.PredicateNode:
		in.visit(n.Node, Bool, src)
		return Bool

	case *exprast.PointerNode:
		// The "#" of a predicate: bound by the enclosing builtin, not a variable.
		return Unknown

	case *exprast.CallNode:
		// A generated params struct contributes fields, never functions or methods —
		// bisql's scope is built from exported fields — so there is nothing here that
		// could be called. Type the arguments, which may well be inferable, and
		// report the call itself.
		for _, a := range n.Arguments {
			in.visit(a, Unknown, src)
		}
		in.diags = append(in.diags, Diagnostic{
			Path: src,
			Msg:  "calls a function or method, which a generated params struct cannot provide; replace the condition with a boolean gate",
		})
		return Unknown
	}

	in.diags = append(in.diags, Diagnostic{Path: src, Msg: fmt.Sprintf("unsupported expression form %T", n)})
	return Unknown
}

// builtin types a call to one of expr's builtins, which are a closed set and so
// can simply be tabulated. The distinction that matters is whether a builtin pins
// its argument's type or merely constrains it: len() accepts strings, slices and
// maps alike, while all() and its relatives fail on anything but a slice.
func (in *inferrer) builtin(n *exprast.BuiltinNode, src string) Kind {
	var (
		arg0Kind   = Unknown
		arg0Constr Constraint
		result     Kind
	)
	switch n.Name {
	case "len":
		arg0Constr, result = Sized, Int
	case "all", "any", "none", "one":
		arg0Kind, result = Slice, Bool
	case "count":
		arg0Kind, result = Slice, Int
	case "filter", "map", "sort", "sortBy", "reverse", "uniq", "concat":
		arg0Kind, result = Slice, Slice
	case "first", "last", "find", "findLast", "min", "max", "sum":
		arg0Kind, result = Slice, Unknown
	default:
		for _, a := range n.Arguments {
			in.visit(a, Unknown, src)
		}
		in.diags = append(in.diags, Diagnostic{Path: src, Msg: "unsupported builtin " + n.Name})
		return Unknown
	}

	if len(n.Arguments) > 0 {
		if arg0Constr != 0 {
			in.constrain(n.Arguments[0], arg0Constr, src)
		} else {
			in.visit(n.Arguments[0], arg0Kind, src)
		}
		for _, a := range n.Arguments[1:] {
			in.visit(a, Unknown, src)
		}
	}
	return result
}

func (in *inferrer) binary(n *exprast.BinaryNode, src string) Kind {
	switch n.Operator {
	case "&&", "||", "and", "or":
		in.visit(n.Left, Bool, src)
		in.visit(n.Right, Bool, src)
		return Bool

	case "??":
		in.markOptional(n.Left, src)
		k := in.visit(n.Left, Unknown, src)
		in.visit(n.Right, k, src)
		return k

	case "==", "!=":
		// A nil comparison says nothing about the shape, only that the value is
		// allowed to be absent.
		if isNil(n.Right) {
			in.markOptional(n.Left, src)
			return Bool
		}
		if isNil(n.Left) {
			in.markOptional(n.Right, src)
			return Bool
		}
		fallthrough

	case "<", ">", "<=", ">=":
		in.compare(n.Left, n.Right, src)
		return Bool

	case "in", "not in":
		// A literal set on the right tells us the element shape outright.
		if arr, ok := n.Right.(*exprast.ArrayNode); ok && len(arr.Nodes) > 0 {
			if k := literalKind(arr.Nodes[0]); k != Unknown {
				in.visit(n.Left, k, src)
				return Bool
			}
		}
		// Otherwise the right side is only known to be a container: expr's "in"
		// accepts slices and maps, so it does not pin a slice.
		in.constrain(n.Right, Container, src)
		in.visit(n.Left, Unknown, src)
		return Bool

	case "+", "-", "*", "/", "%", "**", "^":
		// expr's + concatenates strings as well as adding numbers, so only a literal
		// operand pins anything here.
		in.compare(n.Left, n.Right, src)
		return Unknown

	case "matches", "contains", "startsWith", "endsWith":
		in.visit(n.Left, String, src)
		in.visit(n.Right, String, src)
		return Bool
	}

	in.diags = append(in.diags, Diagnostic{Path: src, Msg: "unsupported operator " + n.Operator})
	return Unknown
}

// compare types the two sides of a comparison or an arithmetic operation. A
// literal on either side fixes the shape for both; two variables only prove that
// they agree, which is recorded so that whichever one is resolved elsewhere
// resolves the other too.
func (in *inferrer) compare(left, right exprast.Node, src string) {
	lk, rk := literalKind(left), literalKind(right)
	switch {
	case lk != Unknown:
		in.visit(right, lk, src)
		in.visit(left, lk, src)
	case rk != Unknown:
		in.visit(left, rk, src)
		in.visit(right, rk, src)
	default:
		lp, rp := in.resolveNode(left, src), in.resolveNode(right, src)
		if lp.ok && rp.ok {
			in.aliases = append(in.aliases, [2]*Type{lp.typ, rp.typ})
			return
		}
		in.visit(left, Unknown, src)
		in.visit(right, Unknown, src)
	}
}

// constrain records a fact about an expression's variable that narrows it without
// determining a Go type.
func (in *inferrer) constrain(n exprast.Node, c Constraint, src string) {
	if p := in.resolveNode(n, src); p.ok {
		p.typ.Constraints |= c
	}
}

// settleAliases propagates what is known across every proved-equal pair until
// nothing more can be learned.
func (in *inferrer) settleAliases() {
	for changed := true; changed; {
		changed = false
		for _, pair := range in.aliases {
			if copyKnown(pair[0], pair[1]) || copyKnown(pair[1], pair[0]) {
				changed = true
			}
		}
	}
}

// copyKnown fills in what dst is missing from src, reporting whether it learned
// anything.
func copyKnown(src, dst *Type) bool {
	learned := false
	if dst.Kind == Unknown && src.Kind != Unknown {
		dst.Kind, dst.why = src.Kind, src.why+" (via a comparison)"
		learned = true
	}
	if dst.GoType == "" && src.GoType != "" {
		dst.GoType = src.GoType
		learned = true
	}
	if dst.Constraints|src.Constraints != dst.Constraints {
		dst.Constraints |= src.Constraints
		learned = true
	}
	return learned
}

// markOptional records that a place may be absent, without constraining its shape.
func (in *inferrer) markOptional(n exprast.Node, src string) {
	if p := in.resolveNode(n, src); p.ok {
		p.typ.Optional = true
	}
}

// literalKind reports the type a literal pins, unwrapping a leading sign so that
// -1 reads as an integer rather than as an operation. expr's parser emits exactly
// four literal shapes, so these are the only types a literal can ever pin.
func literalKind(n exprast.Node) Kind {
	if u, ok := n.(*exprast.UnaryNode); ok && (u.Operator == "-" || u.Operator == "+") {
		switch u.Node.(type) {
		case *exprast.IntegerNode, *exprast.FloatNode:
			return literalKind(u.Node)
		}
		return Unknown
	}
	switch n.(type) {
	case *exprast.StringNode:
		return String
	case *exprast.IntegerNode:
		return Int
	case *exprast.FloatNode:
		return Float
	case *exprast.BoolNode:
		return Bool
	}
	return Unknown
}

// isNil reports whether n is the nil literal. bisql's evaluator spells it "nil"
// but also accepts Komapper's "null", which reaches the parser as an ordinary
// identifier that resolves to nil at evaluation time; inference must read it the
// same way rather than mistaking it for a variable.
func isNil(n exprast.Node) bool {
	switch n := n.(type) {
	case *exprast.NilNode:
		return true
	case *exprast.IdentifierNode:
		return n.Value == "null"
	}
	return false
}

// resolveNode resolves an expression that denotes a place: a variable, a field of
// one, or an element of one. Anything else is not a place, and is left alone
// rather than guessed at.
func (in *inferrer) resolveNode(n exprast.Node, src string) place {
	switch n := n.(type) {
	case *exprast.IdentifierNode:
		return place{typ: in.rootOf(n.Value), path: n.Value, ok: true}

	case *exprast.ChainNode:
		return in.resolveNode(n.Node, src)

	case *exprast.MemberNode:
		base := in.resolveNode(n.Node, src)
		if !base.ok {
			return base
		}
		if n.Optional {
			base.typ.Optional = true
		}
		if prop, ok := n.Property.(*exprast.StringNode); ok {
			return place{typ: base.typ.field(prop.Value), path: base.path + "." + prop.Value, ok: true}
		}
		// Indexing. An integer index means a slice; a computed or string index could
		// just as well be a map, which is not a shape this models.
		in.visit(n.Property, Unknown, src)
		if literalKind(n.Property) == Int {
			in.unify(base.typ, Slice, "integer indexing", base.path)
			return place{typ: base.typ.elem(), path: base.path + "[]", ok: true}
		}
		base.typ.Constraints |= Container
		return place{path: base.path + "[?]"}
	}

	return place{path: src}
}

// rootOf resolves the first segment of a path: a loop variable if one is in scope,
// otherwise a field of the params struct.
func (in *inferrer) rootOf(name string) *Type {
	for i := len(in.scope) - 1; i >= 0; i-- {
		if in.scope[i].name == name {
			return in.scope[i].elem
		}
	}
	return in.root.field(name)
}

// resolveExpr resolves an expression that must name a place: a /*%for*/ iterable.
func (in *inferrer) resolveExpr(src string) place {
	tree, err := parser.Parse(src)
	if err != nil {
		in.diags = append(in.diags, Diagnostic{Path: src, Msg: "cannot parse iterable: " + err.Error()})
		return place{path: src}
	}
	p := in.resolveNode(tree.Node, src)
	if !p.ok {
		in.diags = append(in.diags, Diagnostic{Path: src, Msg: "iterable must name a variable or a field of one"})
	}
	return p
}

// resolvePath walks a dotted bind name to its Type, rooting it at a loop variable
// in scope when the first segment names one.
func (in *inferrer) resolvePath(path []string) *Type {
	cur := in.rootOf(path[0])
	for _, seg := range path[1:] {
		cur = cur.field(seg)
	}
	return cur
}

// unify narrows t to kind, or records a conflict if the two disagree.
func (in *inferrer) unify(t *Type, kind Kind, why, path string) {
	if kind == Unknown {
		return
	}
	if t.Kind == Unknown {
		t.Kind, t.why = kind, why
		return
	}
	if t.Kind == kind {
		return
	}
	// Opaque is a scalar sqlc named but we cannot reason about; an expression
	// claiming a different scalar shape for it is a real disagreement.
	if t.Kind == Opaque && why == "condition expression" {
		in.diags = append(in.diags, Diagnostic{
			Path: path,
			Msg:  fmt.Sprintf("condition uses it as %s but sqlc types it as %s", kind, t.GoType),
		})
		return
	}
	if kind == Opaque {
		t.Kind, t.why = kind, why
		return
	}
	in.diags = append(in.diags, Diagnostic{
		Path: path,
		Msg:  fmt.Sprintf("conflicting types: %s (%s) vs %s (%s)", t.Kind, t.why, kind, why),
	})
}

// checkUndecided reports every place inference bottomed out, naming the
// constraints it did gather so the author can see what is missing.
func (in *inferrer) checkUndecided(t *Type, path string) {
	switch t.Kind {
	case Unknown:
		in.diags = append(in.diags, Diagnostic{Path: path, Msg: refusal(t)})
	case Slice:
		if t.Elem == nil || t.Elem.Kind == Unknown {
			in.diags = append(in.diags, Diagnostic{
				Path: path,
				Msg:  "nothing inside the loop over it is bound, so its element type is unknown; bind a value in the body or drop the loop",
			})
			return
		}
		in.checkUndecided(t.Elem, path+"[]")
	case Struct:
		for _, m := range t.Fields() {
			sub := m.Name
			if path != "" {
				sub = path + "." + m.Name
			}
			in.checkUndecided(m.Type, sub)
		}
	}
}

// refusal explains why a variable has no type and what the author can do about it.
func refusal(t *Type) string {
	var b strings.Builder
	b.WriteString("cannot infer a type")
	if d := t.Constraints.describe(); d != "" {
		b.WriteString(": all that is known is that it ")
		b.WriteString(d)
	}
	b.WriteString(". Bind it in the SQL, compare it with a literal, iterate it with " +
		"/*%for*/ and bind something in the body, or replace the condition with a boolean gate")
	return b.String()
}
