// Package db stands in for what sqlc's own Go codegen emits around the generated queries.
// The plugin does not produce this yet; the example carries it so the generated file can be
// compiled and run.
package db

import (
	"context"
	"database/sql"
)

// DBTX is the subset of a database handle the generated queries use.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Queries is the generated queries' receiver.
type Queries struct {
	db DBTX
}

// New returns Queries over db.
func New(db DBTX) *Queries { return &Queries{db: db} }
