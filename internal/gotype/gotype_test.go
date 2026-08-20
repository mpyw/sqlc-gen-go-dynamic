package gotype_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gotype"
)

func TestFor(t *testing.T) {
	for _, c := range []struct {
		engine, name string
		array        bool
		dims         int
		want, imp    string
	}{
		{engine: "postgresql", name: "text", want: "string"},
		{engine: "postgresql", name: "pg_catalog.int8", want: "int64"},
		{engine: "postgresql", name: "timestamptz", want: "time.Time", imp: "time"},
		{engine: "postgresql", name: "text", array: true, want: "[]string"},
		{engine: "postgresql", name: "int4", array: true, dims: 2, want: "[][]int32"},
		{engine: "mysql", name: "bigint", want: "int64"},
		{engine: "sqlite", name: "integer", want: "int64"},
	} {
		t.Run(c.engine+"/"+c.name, func(t *testing.T) {
			got, err := gotype.For(c.engine, c.name, c.array, c.dims)
			if err != nil {
				t.Fatalf("For: %v", err)
			}
			if got.Name != c.want || got.Import != c.imp {
				t.Errorf("For = %+v, want {%q %q}", got, c.want, c.imp)
			}
		})
	}
}

// An unknown type is refused rather than guessed at: a guess that compiles is worse than a
// build error that names the type.
func TestForRefusesTheUnknown(t *testing.T) {
	if _, err := gotype.For("postgresql", "hstore", false, 0); err == nil ||
		!strings.Contains(err.Error(), "no mapping") {
		t.Errorf("error = %v, want it to name the missing mapping", err)
	}
	if _, err := gotype.For("oracle", "varchar2", false, 0); err == nil ||
		!strings.Contains(err.Error(), "unsupported engine") {
		t.Errorf("error = %v, want it to reject the engine", err)
	}
}
