#!/bin/sh
# Generates with real sqlc and compiles the result.
#
# The design rests on the claim that what sqlc hands a plugin is enough, so the claim is
# tested against sqlc itself. It also checks the other half of being a fork: a query with no
# directives has to come out exactly as it would without this plugin.
#
# Set SQLC to reuse a built binary; otherwise it is fetched, which needs cgo for libpg_query.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/plugin" "$root/plugin"
cp "$here/schema.sql" "$here/query.sql" "$here/static.sql" "$work/"
sed "s|@PLUGIN@|$work/plugin|" "$here/sqlc.yaml.tmpl" >"$work/sqlc.yaml"
mkdir -p "$work/gen"

sqlc=${SQLC:-}
if [ -z "$sqlc" ]; then
    sqlc="$work/sqlc"
    GOBIN="$work" go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
fi

(cd "$work" && "$sqlc" generate)
echo "--- generated ---"
cat "$work/gen/query.sql.go"

# A static query must not have acquired a params struct or the runtime.
if grep -q "GetUserParams" "$work/gen/query.sql.go"; then
    echo "FAIL: a query with no directives grew a params struct" >&2
    exit 1
fi
if ! grep -q "func (q \*Queries) GetUser(ctx context.Context, id int64)" "$work/gen/query.sql.go"; then
    echo "FAIL: a query with no directives lost its positional argument" >&2
    exit 1
fi
# A dynamic one must have both.
if ! grep -q "searchUsersTemplate = dyn.MustParse" "$work/gen/query.sql.go"; then
    echo "FAIL: a query with directives did not get a template" >&2
    exit 1
fi
# A parameter whose type is not a builtin has to bring its import with it.
if ! grep -qE "Since +\*pgtype\.Timestamptz" "$work/gen/query.sql.go"; then
    echo "FAIL: a nil-tested nullable parameter did not get its own type" >&2
    exit 1
fi

# Byte-for-byte with sqlc's own generator, for each driver.
for driver in pgx std; do
    if ! diff -r "$work/g_$driver" "$work/p_$driver"; then
        echo "FAIL: static output differs from gen: go ($driver)" >&2
        exit 1
    fi
done
echo "--- byte-for-byte with gen: go (pgx, database/sql) ---"

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
