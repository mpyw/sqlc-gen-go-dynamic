package lint_test

import (
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/bind"
	"github.com/mpyw/sqlc-gen-go-dynamic/internal/lint"
)

// A directive that swaps one selected column for another is the worst shape this project can
// produce: sqlc reads `id, name status` as `name AS status`, so the count and the types agree,
// the code compiles, the query runs, and the wrong column arrives in the field named after the
// other one. It is refused rather than documented.
func TestSelectListRefusesADirectiveAmongTheColumns(t *testing.T) {
	pg := bind.RulesFor("postgresql")
	for _, c := range []struct {
		name string
		cmd  string
		src  string
	}{
		{"a column swapped", ":many", "select id, /*%if useName*/ name /*%else*/ status /*%end*/ from users"},
		{"a column added", ":many", "select id /*%if withName*/, name /*%end*/ from users"},
		{"a single row", ":one", "select /*%if a*/ id /*%end*/ from users"},
		{"no from at all", ":many", "select 1 /*%if a*/ + 1 /*%end*/"},
		{"a loop over the columns", ":many", "select id /*%for c in cs*/, sqlc.arg('c.x') /*%end*/ from users"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := lint.SelectList(c.src, c.cmd, pg)
			if err == nil {
				t.Fatal("want the directive refused")
			}
			if !strings.Contains(err.Error(), "select list") {
				t.Errorf("error = %v, want it to name the select list", err)
			}
		})
	}
}

// The check is narrow on purpose: it aborts a generate, so everything it is not sure about has
// to pass. These all pass.
func TestSelectListPassesWhatItCannotBeSureOf(t *testing.T) {
	for _, c := range []struct {
		name, cmd, engine, src string
	}{
		{"the ordinary shape, directives after from", ":many", "postgresql",
			"select id, name from users where 1 = 1 /*%if a*/ and status = @st /*%end*/"},
		{"a directive inside a subquery in the select list", ":many", "postgresql",
			"select id, (select count(*) from o where o.u = users.id /*%if a*/ and o.paid /*%end*/) from users"},
		{"a CTE, which does not begin with select", ":many", "postgresql",
			"with c as (select id from users) select /*%if a*/ id /*%end*/ from c"},
		{"a statement that returns no rows", ":exec", "postgresql",
			"update users set seen = now() /*%if a*/ where id = @id /*%end*/"},
		{"insert … returning", ":one", "postgresql",
			"insert into users (name) values (@name) returning /*%if a*/ id /*%end*/"},
		{"from inside a function call before the real from", ":many", "postgresql",
			"select extract(year from created_at) from users where 1 = 1 /*%if a*/ and x /*%end*/"},
		{"upper case keywords", ":many", "postgresql",
			"SELECT id FROM users WHERE 1 = 1 /*%if a*/ AND status = @st /*%end*/"},
		{"a column named fromage does not end the list", ":many", "postgresql",
			"select fromage, id from users where 1 = 1 /*%if a*/ and x /*%end*/"},
		{"a template that does not lex is left to the parser", ":many", "postgresql",
			"select 'unterminated, /*%if a*/ x from t"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := lint.SelectList(c.src, c.cmd, bind.RulesFor(c.engine)); err != nil {
				t.Errorf("want no complaint, got %v", err)
			}
		})
	}
}
