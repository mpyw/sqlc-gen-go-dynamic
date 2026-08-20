// Package directive rebuilds a query's /*%if*/ and /*%for*/ structure from the
// SQL text sqlc hands to a codegen plugin.
//
// sqlc parses the template as ordinary SQL — the directives are comments, so it
// simply ignores them — and passes the body back verbatim in Query.text with each
// bind rewritten to the placeholder it assigned. Everything typing needs is
// therefore already in the request: this package recovers the directive nesting
// from the comments and pairs each placeholder with the parameter name sqlc gave
// it, which is what turns a flat parameter list into a shape.
//
// One layout rule matters. sqlc lifts a block comment that starts at column zero
// out of the text and into Query.comments — greedily, taking the rest of the line
// with it — so a directive written flush left loses the SQL beside it. Indenting
// every directive by at least one space keeps the text intact, and CheckComments
// reports the case where that did not happen rather than silently producing a
// tree with holes in it.
package directive

import (
	"fmt"
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/exprtype"
)

// Param is one entry of sqlc's parameter table: the placeholder number it was
// assigned and the name it carries.
type Param struct {
	Number int    // 1-based, as in plugin.Parameter.number
	Name   string // plugin.Parameter.column.name, e.g. "status" or "c.name"
}

// Parse recovers the directive tree of a query body. Placeholders are matched to params by
// the number they carry, except under the one style that carries none, where position
// decides; see Style.
func Parse(text string, params []Param, style Style) (*exprtype.Node, error) {
	byNumber := make(map[int]string, len(params))
	for _, p := range params {
		byNumber[p.Number] = p.Name
	}

	root := &exprtype.Node{Kind: exprtype.Root}
	// stack[len-1] is the node whose body we are inside.
	stack := []*exprtype.Node{root}
	positional := 0

	s := &scanner{src: text, style: style}
	for {
		tok, ok := s.next()
		if !ok {
			break
		}
		top := stack[len(stack)-1]

		switch tok.kind {
		case tokPlaceholder:
			n := tok.number
			if n == 0 {
				positional++
				n = positional
			}
			name, ok := byNumber[n]
			if !ok {
				return nil, fmt.Errorf("placeholder %d at offset %d has no parameter", n, tok.pos)
			}
			top.Binds = append(top.Binds, name)

		case tokDirective:
			node, closes, err := directiveNode(tok.text)
			if err != nil {
				return nil, fmt.Errorf("offset %d: %w", tok.pos, err)
			}
			switch {
			case closes:
				if len(stack) == 1 {
					return nil, fmt.Errorf("offset %d: /*%%end*/ without a matching /*%%if*/ or /*%%for*/", tok.pos)
				}
				stack = stack[:len(stack)-1]

			case node.Kind == exprtype.ElseIf || node.Kind == exprtype.Else:
				// An arm is a sibling of the branch it continues, so it replaces the
				// open node rather than nesting inside it.
				if len(stack) == 1 {
					return nil, fmt.Errorf("offset %d: arm without a matching /*%%if*/", tok.pos)
				}
				stack = stack[:len(stack)-1]
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
				stack = append(stack, node)

			default:
				top.Children = append(top.Children, node)
				stack = append(stack, node)
			}
		}
	}

	if len(stack) != 1 {
		return nil, fmt.Errorf("%d directive block(s) left unclosed", len(stack)-1)
	}
	return root, nil
}

// directiveNode interprets the body of a directive comment, with the leading "%"
// already stripped. It reports whether the directive closes a block instead of
// opening one.
func directiveNode(body string) (*exprtype.Node, bool, error) {
	body = strings.TrimSpace(body)
	word, rest := split2(body)
	switch word {
	case "end":
		return nil, true, nil
	case "if":
		if rest == "" {
			return nil, false, fmt.Errorf("/*%%if*/ has no condition")
		}
		return &exprtype.Node{Kind: exprtype.If, Cond: rest}, false, nil
	case "elseif":
		if rest == "" {
			return nil, false, fmt.Errorf("/*%%elseif*/ has no condition")
		}
		return &exprtype.Node{Kind: exprtype.ElseIf, Cond: rest}, false, nil
	case "else":
		return &exprtype.Node{Kind: exprtype.Else}, false, nil
	case "for":
		v, iter, err := forHeader(rest)
		if err != nil {
			return nil, false, err
		}
		return &exprtype.Node{Kind: exprtype.For, Var: v, Iter: iter}, false, nil
	}
	return nil, false, fmt.Errorf("unknown directive %q", word)
}

// forHeader splits "x in xs" into its loop variable and iterable.
func forHeader(rest string) (string, string, error) {
	v, tail := split2(rest)
	kw, iter := split2(tail)
	if v == "" || kw != "in" || iter == "" {
		return "", "", fmt.Errorf("/*%%for*/ wants \"<var> in <expr>\", got %q", rest)
	}
	return v, iter, nil
}

// split2 splits off the first whitespace-delimited word.
func split2(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n\r"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}
