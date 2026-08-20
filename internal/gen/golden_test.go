package gen_test

import (
	"flag"
	"os"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/gen"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/query"
)

// The example is a separate module, so `go build` compiles the generated file and its test
// runs it. That is the only check that the emitted source is valid Go which type-checks
// against the runtime; a golden comparison here keeps it from drifting.
//
// Regenerate with: go test ./internal/gen -run TestGolden -update
var update = flag.Bool("update", false, "update the golden file")

const goldenPath = "../../testdata/example/db/search_users.gen.go"

func exampleInput() query.Input {
	return query.Input{
		Name:   "SearchUsers",
		Cmd:    ":many",
		Engine: "postgresql",
		Text: `select u.id, u.name
from users u
where 1 = 1
  /*%if activeOnly*/ and u.status = $1 /*%end*/
  /*%if minAge != null*/ and u.age >= $2 /*%end*/
  /*%if ids != null*/ and u.id in ($3) /*%end*/
  /*%for c in conds*/ and (u.name like $4 or u.status = $5) /*%end*/
order by /*%if byName*/ u.name, /*%end*/ u.id`,
		Params: []query.Param{
			{Number: 1, Name: "status", GoType: "string", NotNull: true},
			{Number: 2, Name: "min_age", GoType: "int32", NotNull: true},
			{Number: 3, Name: "ids", GoType: "int64", NotNull: true, IsSlice: true},
			{Number: 4, Name: "c.name", GoType: "string", NotNull: true},
			{Number: 5, Name: "c.status", GoType: "string", NotNull: true},
		},
		Row: []query.Column{
			{Name: "id", GoType: "int64"},
			{Name: "name", GoType: "string"},
		},
	}
}

func TestGolden(t *testing.T) {
	out, err := gen.File(gen.Options{Package: "db"}, []*query.Query{prepare(t, exampleInput())})
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if *update {
		if err := os.WriteFile(goldenPath, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate with -update)", err)
	}
	if string(out) != string(want) {
		t.Errorf("%s is stale\n--- got ---\n%s\n--- want ---\n%s", goldenPath, out, want)
	}
}
