// Package dbpgx is the pgx flavour of the example: the same query, generated with
// sql_package: pgx/v5. pgx drops the Context suffix from every method, since all of them take
// one, so the DBTX interface differs and the generated bodies follow it.
package dbpgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the subset of a pgx handle the generated queries use.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Queries is the generated queries' receiver.
type Queries struct {
	db DBTX
}

// New returns Queries over db.
func New(db DBTX) *Queries { return &Queries{db: db} }
