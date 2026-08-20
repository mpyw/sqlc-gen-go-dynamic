package gotype_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gotype"
)

func overrides(t *testing.T, s string) gotype.Overrides {
	t.Helper()
	var o gotype.Overrides
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return o
}

// An override is what turns a type the table does not know from a blocker into one line of
// configuration, which is why the table can stay small and refuse things.
func TestOverrideAnswersTheUnknown(t *testing.T) {
	o := overrides(t, `[{"db_type":"hstore","go_type":"github.com/jackc/pgx/v5/pgtype.Hstore"}]`)
	got, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "hstore", Overrides: o})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got.Name != "pgtype.Hstore" || got.Import != "github.com/jackc/pgx/v5/pgtype" {
		t.Errorf("For = %+v, want pgtype.Hstore", got)
	}
}

// An override beats the built-in table, which is how a project keeps the types it already has.
func TestOverrideBeatsTheTable(t *testing.T) {
	o := overrides(t, `[{"db_type":"timestamptz","go_type":"github.com/jackc/pgx/v5/pgtype.Timestamptz"}]`)
	got, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "timestamptz", Overrides: o})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got.Name != "pgtype.Timestamptz" {
		t.Errorf("For = %+v, want the override", got)
	}
}

// nullable: true narrows an override to the nullable case, so a column that can be NULL takes
// a different target from one that cannot.
func TestOverrideOnNullability(t *testing.T) {
	o := overrides(t, `[
		{"db_type":"timestamptz","nullable":true,"go_type":"github.com/jackc/pgx/v5/pgtype.Timestamptz"},
		{"db_type":"timestamptz","go_type":"time.Time"}
	]`)
	nullable, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "timestamptz", Overrides: o})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if nullable.Name != "pgtype.Timestamptz" {
		t.Errorf("nullable = %+v, want pgtype.Timestamptz", nullable)
	}
	notNull, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "timestamptz", NotNull: true, Overrides: o})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if notNull.Name != "time.Time" {
		t.Errorf("not null = %+v, want time.Time", notNull)
	}
}

func TestOverrideShapes(t *testing.T) {
	for _, c := range []struct{ name, in, want, imp string }{
		{"qualified string", `"github.com/google/uuid.UUID"`, "uuid.UUID", "github.com/google/uuid"},
		{"bare string", `"string"`, "string", ""},
		{"object", `{"import":"time","package":"time","type":"Time"}`, "time.Time", "time"},
		{"pointer", `{"import":"time","package":"time","type":"Time","pointer":true}`, "*time.Time", "time"},
		{"slice", `{"type":"byte","slice":true}`, "[]byte", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := overrides(t, `[{"db_type":"custom","go_type":`+c.in+`}]`)
			got, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "custom", Overrides: o})
			if err != nil {
				t.Fatalf("For: %v", err)
			}
			if got.Name != c.want || got.Import != c.imp {
				t.Errorf("For = %+v, want {%q %q}", got, c.want, c.imp)
			}
		})
	}
}

// An array of an overridden type is still an array.
func TestOverrideWithArray(t *testing.T) {
	o := overrides(t, `[{"db_type":"custom","go_type":"github.com/x/y.Z"}]`)
	got, err := gotype.For(gotype.Request{Engine: "postgresql", Name: "custom", IsArray: true, Overrides: o})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got.Name != "[]y.Z" {
		t.Errorf("For = %+v, want []y.Z", got)
	}
}

func TestGoTypeRejectsNonsense(t *testing.T) {
	var o gotype.Overrides
	if err := json.Unmarshal([]byte(`[{"db_type":"x","go_type":123}]`), &o); err == nil ||
		!strings.Contains(err.Error(), "neither a string nor an object") {
		t.Errorf("error = %v, want it to say what go_type may be", err)
	}
}
