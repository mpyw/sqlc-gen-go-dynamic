package bind_test

import (
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
)

// This file pins the findings of an adversarial review that compared recognition against real
// sqlc. The invariant is that the two agree: a spelling one side binds and the other does not
// is either an invented parameter or a lost one.

// PostgreSQL has operators ending in @, and the name after one belongs to the operator. Reading
// it as a marker invents a parameter sqlc never reported.
func TestAnOperatorEndingInAtIsNotAMarker(t *testing.T) {
	for _, in := range []string{
		"@ status",  // a space after @ is not a name
		"@\tstatus", // nor a tab
		"@\nstatus", // nor a newline
		"@@status",  // the full-text search operator
		"@> '{a}'",  // containment
		"@",         // on its own
	} {
		t.Run(in, func(t *testing.T) {
			if m, ok := pg.Recognize(in); ok {
				t.Errorf("Recognize(%q) = %+v, want not recognized", in, m)
			}
			if reason, bad := pg.Malformed(in); bad {
				t.Errorf("Malformed(%q) = %q, want not malformed", in, reason)
			}
		})
	}
}

// The byte before a marker decides whether it is one, which is what the lexer consults.
func TestOperatorByte(t *testing.T) {
	for _, b := range []byte{'<', '>', '@', '=', '~', '!', '|', '&', '#', '?', '-', '+'} {
		if !bind.OperatorByte(b) {
			t.Errorf("OperatorByte(%q) = false, want true", b)
		}
	}
	for _, b := range []byte{' ', '(', ',', 'a', '9', '\'', '\n', ';'} {
		if bind.OperatorByte(b) {
			t.Errorf("OperatorByte(%q) = true, want false", b)
		}
	}
}

// The at-form exists for the engines sqlc supports it on, and an engine this does not know gets
// the narrower reading: recognizing a marker sqlc did not is the failure worth avoiding.
func TestRulesForFailsClosed(t *testing.T) {
	for engine, wantAt := range map[string]bool{
		"postgresql": true,
		"sqlite":     true,
		"mysql":      false,
		"oracle":     false,
		"":           false,
		"postgres":   false, // a near-miss spelling is not an engine
	} {
		if got := bind.RulesFor(engine).AtForm; got != wantAt {
			t.Errorf("RulesFor(%q).AtForm = %v, want %v", engine, got, wantAt)
		}
	}
	if !bind.RulesFor("mysql").HashComments {
		t.Error("MySQL treats # as a line comment")
	}
	if bind.RulesFor("postgresql").HashComments {
		t.Error("# is an operator in PostgreSQL, not a comment")
	}
}
