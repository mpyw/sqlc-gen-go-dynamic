// Package ast defines the template tree.
package ast

// Node is one piece of a template.
type Node interface{ node() }

// Text is opaque text, emitted verbatim.
type Text struct{ S string }

// Bind is a parameter marker. List is set for sqlc.slice, which renders as a
// comma-separated run of placeholders with no parentheses of its own: the template already
// carries them, having had to be valid SQL before rendering.
type Bind struct {
	Name string
	List bool
}

// Arm is one branch of a conditional. An empty Cond marks the else arm.
type Arm struct {
	Cond string
	Body []Node
}

// If renders the body of the first arm whose condition holds, else the else arm, else
// nothing.
type If struct{ Arms []Arm }

// For renders Body once per element of Iter with Var bound to the element. Nothing is
// inserted between iterations; each iteration carries its own connector.
type For struct {
	Var  string
	Iter string
	Body []Node
}

func (Text) node() {}
func (Bind) node() {}
func (If) node()   {}
func (For) node()  {}
