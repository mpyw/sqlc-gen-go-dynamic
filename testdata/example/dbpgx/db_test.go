package dbpgx_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mpyw/sqlc-gen-go-dynamic/example/dbpgx"
)

// errRecorded stops the call once the statement is captured. What is under test is the SQL the
// generated method builds against pgx's method names, not the scan.
var errRecorded = errors.New("recorded")

type recorder struct {
	sql  string
	args []any
}

func (r *recorder) Query(_ context.Context, q string, args ...any) (pgx.Rows, error) {
	r.sql, r.args = q, args
	return nil, errRecorded
}

func (r *recorder) QueryRow(_ context.Context, q string, args ...any) pgx.Row {
	r.sql, r.args = q, args
	return nil
}

func (r *recorder) Exec(_ context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	r.sql, r.args = q, args
	return pgconn.CommandTag{}, errRecorded
}

func TestSearchUsers(t *testing.T) {
	minAge := int32(20)
	r := &recorder{}
	_, err := dbpgx.New(r).SearchUsers(context.Background(), dbpgx.SearchUsersParams{
		ActiveOnly: true,
		Status:     "active",
		MinAge:     &minAge,
		Ids:        []int64{1, 2},
		Conds:      []dbpgx.SearchUsersCond{{Name: "%a%", Status: "active"}},
	})
	if !errors.Is(err, errRecorded) {
		t.Fatalf("SearchUsers: %v", err)
	}
	want := `select u.id, u.name
from users u
where 1 = 1
   and u.status = $1 
   and u.age >= $2 
   and u.id in ($3, $4) 
   and (u.name like $5 or u.status = $6) 
order by  u.id`
	if r.sql != want {
		t.Errorf("SQL\n got:\n%s\nwant:\n%s", r.sql, want)
	}
	wantArgs := []any{"active", &minAge, int64(1), int64(2), "%a%", "active"}
	if !reflect.DeepEqual(r.args, wantArgs) {
		t.Errorf("Args\n got: %#v\nwant: %#v", r.args, wantArgs)
	}
}
