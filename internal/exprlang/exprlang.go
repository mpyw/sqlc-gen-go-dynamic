// Package exprlang evaluates directive expressions with github.com/expr-lang/expr.
//
// Only conditions and /*%for*/ iterables are expressions. A bind marker is a name, resolved
// by the renderer, so nothing here decides what gets bound.
package exprlang

import (
	"strings"
	"sync"

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
type foldNames struct{}

func (foldNames) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		n.Value = fold(n.Value)
	case *ast.MemberNode:
		if p, ok := n.Property.(*ast.StringNode); ok {
			p.Value = fold(p.Value)
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
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
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
		goexpr.Patch(foldNames{}))
	if err != nil {
		return nil, err
	}
	actual, _ := e.cache.LoadOrStore(expression, prog)
	return actual.(*vm.Program), nil
}
