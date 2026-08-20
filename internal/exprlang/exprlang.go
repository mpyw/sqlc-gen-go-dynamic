// Package exprlang evaluates directive expressions with github.com/expr-lang/expr.
//
// Only conditions and /*%for*/ iterables are expressions. A bind marker is a name, resolved
// by the renderer, so nothing here decides what gets bound.
package exprlang

import (
	"sync"

	goexpr "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Evaluator compiles and caches expressions. The zero value is ready and safe for
// concurrent use.
type Evaluator struct {
	cache sync.Map // expression -> *vm.Program
}

// Eval evaluates expression against scope.
func (e *Evaluator) Eval(expression string, scope map[string]any) (any, error) {
	prog, err := e.compile(expression)
	if err != nil {
		return nil, err
	}
	var env any
	if scope != nil {
		env = scope
	}
	return vm.Run(prog, env)
}

func (e *Evaluator) compile(expression string) (*vm.Program, error) {
	if p, ok := e.cache.Load(expression); ok {
		return p.(*vm.Program), nil
	}
	// AllowUndefinedVariables makes an absent name nil rather than an error, so `x != nil`
	// works when the key is simply missing — and so `null`, which sqlc-flavored templates
	// write for nil, resolves as an undefined identifier.
	prog, err := goexpr.Compile(expression, goexpr.AllowUndefinedVariables())
	if err != nil {
		return nil, err
	}
	actual, _ := e.cache.LoadOrStore(expression, prog)
	return actual.(*vm.Program), nil
}
