package db_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/example/db"
)

// errRecorded stops the call once the statement has been captured. What is under test is the
// SQL the generated method builds, not the scan.
var errRecorded = errors.New("recorded")

type recorder struct {
	sql  string
	args []any
}

func (r *recorder) QueryContext(_ context.Context, q string, args ...any) (*sql.Rows, error) {
	r.sql, r.args = q, args
	return nil, errRecorded
}

func (r *recorder) QueryRowContext(_ context.Context, q string, args ...any) *sql.Row {
	r.sql, r.args = q, args
	return nil
}

func (r *recorder) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	r.sql, r.args = q, args
	return nil, errRecorded
}

func run(t *testing.T, arg db.SearchUsersParams) *recorder {
	t.Helper()
	r := &recorder{}
	if _, err := db.New(r).SearchUsers(context.Background(), arg); !errors.Is(err, errRecorded) {
		t.Fatalf("SearchUsers: %v", err)
	}
	return r
}

func TestSearchUsersEveryBranch(t *testing.T) {
	minAge := int32(20)
	got := run(t, db.SearchUsersParams{
		ActiveOnly: true,
		Status:     "active",
		MinAge:     &minAge,
		Ids:        []int64{1, 2, 3},
		Conds:      []db.SearchUsersCond{{Name: "%a%", Status: "active"}},
		ByName:     true,
	})

	want := `select u.id, u.name
from users u
where 1 = 1
   and u.status = $1 
   and u.age >= $2 
   and u.id in ($3, $4, $5) 
   and (u.name like $6 or u.status = $7) 
order by  u.name,  u.id`
	if got.sql != want {
		t.Errorf("SQL\n got:\n%s\nwant:\n%s", got.sql, want)
	}
	wantArgs := []any{"active", &minAge, int64(1), int64(2), int64(3), "%a%", "active"}
	if !reflect.DeepEqual(got.args, wantArgs) {
		t.Errorf("Args\n got: %#v\nwant: %#v", got.args, wantArgs)
	}
}

// The zero params take no branch, and numbering has nothing to number.
func TestSearchUsersNoBranch(t *testing.T) {
	got := run(t, db.SearchUsersParams{})
	want := `select u.id, u.name
from users u
where 1 = 1
  
  
  
  
order by  u.id`
	if got.sql != want {
		t.Errorf("SQL\n got:\n%s\nwant:\n%s", got.sql, want)
	}
	if len(got.args) != 0 {
		t.Errorf("Args = %#v, want none", got.args)
	}
}

// A branch that does not render is not counted, so the numbering has no gap.
func TestSearchUsersNumberingHasNoGaps(t *testing.T) {
	got := run(t, db.SearchUsersParams{
		Conds: []db.SearchUsersCond{{Name: "%a%", Status: "active"}},
	})
	if !contains(got.sql, "(u.name like $1 or u.status = $2)") {
		t.Errorf("SQL = %q, want the loop's binds numbered from one", got.sql)
	}
}

// An empty list has no placeholders to emit and would leave `in ()` invalid.
func TestSearchUsersEmptyList(t *testing.T) {
	got := run(t, db.SearchUsersParams{Ids: []int64{}})
	if !contains(got.sql, "u.id in (null)") {
		t.Errorf("SQL = %q, want an empty list as null", got.sql)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
