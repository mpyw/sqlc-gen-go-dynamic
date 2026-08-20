module github.com/mpyw/sqlc-gen-go-dynamic/example

go 1.25.0

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/mpyw/sqlc-gen-go-dynamic v0.0.0-00010101000000-000000000000
)

require (
	github.com/expr-lang/expr v1.17.8 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/mpyw/sqlc-gen-go-dynamic => ../..
