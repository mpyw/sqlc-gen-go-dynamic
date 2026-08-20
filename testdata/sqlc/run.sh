#!/bin/sh
# Generates with real sqlc and compiles the result.
#
# The design rests on the claim that what sqlc hands a plugin is enough, so the claim is
# tested against sqlc itself rather than against a recorded request. Set SQLC to reuse a
# built binary; otherwise it is fetched, which needs cgo for libpg_query.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/sqlc-gen-go-dynamic" "$root/cmd/sqlc-gen-go-dynamic"
cp "$here/schema.sql" "$here/query.sql" "$work/"
sed "s|@PLUGIN@|$work/sqlc-gen-go-dynamic|" "$here/sqlc.yaml.tmpl" >"$work/sqlc.yaml"
mkdir -p "$work/gen"

sqlc=${SQLC:-}
if [ -z "$sqlc" ]; then
    sqlc="$work/sqlc"
    GOFLAGS= go build -o "$sqlc" github.com/sqlc-dev/sqlc/cmd/sqlc 2>/dev/null ||
        GOBIN="$work" go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
fi

(cd "$work" && "$sqlc" generate)
echo "--- generated ---"
cat "$work/gen/queries.gen.go"

# The generated file has to compile against the runtime, which is the point of running this
# at all: a plugin that emits plausible Go is not enough.
cat >"$work/gen/db.go" <<'EOF'
package gen

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Queries struct{ db DBTX }
EOF
cat >"$work/go.mod" <<EOF
module sqlccheck

go 1.25.0

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/mpyw/sqlc-gen-go-dynamic v0.0.0-00010101000000-000000000000
)

replace github.com/mpyw/sqlc-gen-go-dynamic => $root
EOF
(cd "$work" && GOWORK=off GOFLAGS=-mod=mod go mod tidy >/dev/null && GOWORK=off go build ./gen/)
echo "--- compiles ---"
