// Package parser builds the template tree, using a stack for the block directives.
package parser

import (
	"fmt"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/ast"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/lexer"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/sqltmpl/token"
)

// Parse parses a template, reading the marker spellings the rules allow.
func Parse(src string, rules bind.Rules) ([]ast.Node, error) {
	p := &parser{lex: lexer.New(src, rules)}
	nodes, err := p.parse()
	if err != nil {
		return nil, err
	}
	if len(p.stack) != 0 {
		return nil, fmt.Errorf("template: %d unclosed directive block(s)", len(p.stack))
	}
	return nodes, nil
}

// frame is an open block: a conditional collecting arms, or a loop collecting one body.
type frame struct {
	isFor bool
	arms  []ast.Arm // completed arms of a conditional
	cond  string    // condition of the arm being collected; empty in an else arm
	loop  ast.For
	body  []ast.Node
}

type parser struct {
	lex   *lexer.Lexer
	stack []*frame
}

func (p *parser) parse() ([]ast.Node, error) {
	var top []ast.Node
	emit := func(n ast.Node) {
		if len(p.stack) == 0 {
			top = append(top, n)
			return
		}
		f := p.stack[len(p.stack)-1]
		f.body = append(f.body, n)
	}

	for {
		tok, err := p.lex.Next()
		if err != nil {
			return nil, err
		}
		switch tok.Kind {
		case token.EOF:
			return top, nil

		case token.Text:
			emit(ast.Text{S: tok.Text})

		case token.Bind:
			emit(ast.Bind{Name: tok.Name, List: tok.List})

		case token.If:
			p.stack = append(p.stack, &frame{cond: tok.Text})

		case token.Elseif, token.Else:
			f, err := p.openConditional(tok.Kind)
			if err != nil {
				return nil, err
			}
			f.arms = append(f.arms, ast.Arm{Cond: f.cond, Body: f.body})
			f.cond, f.body = tok.Text, nil

		case token.For:
			p.stack = append(p.stack, &frame{isFor: true, loop: ast.For{Var: tok.Name, Iter: tok.Text}})

		case token.End:
			if len(p.stack) == 0 {
				return nil, fmt.Errorf("template: /*%%end*/ without a matching /*%%if*/ or /*%%for*/")
			}
			f := p.stack[len(p.stack)-1]
			p.stack = p.stack[:len(p.stack)-1]
			if f.isFor {
				f.loop.Body = f.body
				emit(f.loop)
				continue
			}
			emit(ast.If{Arms: append(f.arms, ast.Arm{Cond: f.cond, Body: f.body})})
		}
	}
}

// openConditional returns the innermost open conditional, refusing an arm with no branch to
// continue and one that follows an else.
func (p *parser) openConditional(k token.Kind) (*frame, error) {
	name := "/*%elseif*/"
	if k == token.Else {
		name = "/*%else*/"
	}
	if len(p.stack) == 0 {
		return nil, fmt.Errorf("template: %s without a matching /*%%if*/", name)
	}
	f := p.stack[len(p.stack)-1]
	if f.isFor {
		return nil, fmt.Errorf("template: %s inside /*%%for*/ without a matching /*%%if*/", name)
	}
	if f.cond == "" {
		return nil, fmt.Errorf("template: %s after /*%%else*/", name)
	}
	return f, nil
}
