// Package exprlang evaluates directive expressions with github.com/expr-lang/expr.
//
// Only conditions and /*%for*/ iterables are expressions. A bind marker is a name, resolved
// by the renderer, so nothing here decides what gets bound.
package exprlang

import (
	"strings"
	"sync"
	"unicode"

	goexpr "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

// Evaluator compiles and caches expressions. The zero value is ready and safe for
// concurrent use.
type Evaluator struct {
	cache sync.Map // expression -> *vm.Program
}

// Eval evaluates expression against scope. A nil scope is an empty one, so an absent name is
// false rather than an error — the same reading a nil params gets.
func (e *Evaluator) Eval(expression string, scope map[string]any) (any, error) {
	prog, err := e.compile(expression)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		scope = map[string]any{}
	}
	return vm.Run(prog, scope)
}

// foldNames rewrites every identifier and member name to its folded form, matching how a
// scope is keyed.
//
// A method call is the exception. Go's method names are the ones the caller declared and
// nothing folds them, so the callee of a call keeps its spelling — the walk reaches a node
// after its children, which is what lets the fold be undone here.
type foldNames struct {
	orig map[*ast.StringNode]string
}

func (f foldNames) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		n.Value = fold(n.Value)
	case *ast.MemberNode:
		if p, ok := n.Property.(*ast.StringNode); ok {
			f.orig[p] = p.Value
			p.Value = fold(p.Value)
		}
	case *ast.CallNode:
		if m, ok := n.Callee.(*ast.MemberNode); ok {
			if p, ok := m.Property.(*ast.StringNode); ok {
				if was, seen := f.orig[p]; seen {
					p.Value = was
				}
			}
		}
	}
}

// fold matches render's folding: case and underscores carry no meaning in a name.
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

func (e *Evaluator) compile(expression string) (*vm.Program, error) {
	if p, ok := e.cache.Load(expression); ok {
		return p.(*vm.Program), nil
	}
	// AllowUndefinedVariables makes an absent name nil rather than an error, so `x != nil`
	// works when the key is simply missing — and so `null`, which sqlc-flavored templates
	// write for nil, resolves as an undefined identifier.
	//
	// The folding patch is what lets a template and Go disagree about spelling. Names are
	// looked up exactly, so an expression writing ownerId would miss a field indexed as
	// OwnerID and read nil — a branch that silently disappears. Folding both sides at compile
	// time removes the guesswork, and costs nothing at run time.
	prog, err := goexpr.Compile(expression,
		goexpr.AllowUndefinedVariables(),
		goexpr.Patch(foldNames{orig: map[*ast.StringNode]string{}}))
	if err != nil {
		return nil, err
	}
	actual, _ := e.cache.LoadOrStore(expression, prog)
	return actual.(*vm.Program), nil
}
