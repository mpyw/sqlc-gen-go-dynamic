// Package gotype maps a catalog type onto a Go type.
//
// The table is deliberately small and refuses what it does not know. A wrong type here is a
// wrong type in someone's generated code, and guessing produces one that compiles.
//
// The mapping also depends on the driver, because some types have no standard Go
// counterpart: PostgreSQL's numeric arrives as a decimal string over the wire and pgx scans
// it into pgtype.Numeric, while database/sql has nothing to offer for it, so it is refused
// there rather than aimed at a string that may not scan.
//
// These are not sqlc's own Go codegen's types. That codegen represents a nullable column as
// sql.NullString or pgtype.Text where this uses a pointer, which scans correctly under both
// drivers but is not the same API. Parity belongs to the fork that takes over its type
// table, not to a second table written from guesses.
package gotype

import "fmt"

// Type is a resolved Go type.
type Type struct {
	Name   string // as written in source, e.g. "int64" or "time.Time"
	Import string // the package it needs, empty for a builtin
}

// The catalog reports a type by whichever spelling it was written with, so the abbreviations
// and the SQL-standard names both appear: a column declared bigint arrives as pg_catalog.int8,
// while count(*) arrives as bigint.
var postgres = map[string]Type{
	"text":        {Name: "string"},
	"varchar":     {Name: "string"},
	"char":        {Name: "string"},
	"bpchar":      {Name: "string"},
	"name":        {Name: "string"},
	"citext":      {Name: "string"},
	"uuid":        {Name: "string"},
	"bool":        {Name: "bool"},
	"int2":        {Name: "int16"},
	"int4":        {Name: "int32"},
	"int8":        {Name: "int64"},
	"serial":      {Name: "int32"},
	"bigserial":   {Name: "int64"},
	"float4":      {Name: "float32"},
	"float8":      {Name: "float64"},
	"bytea":       {Name: "[]byte"},
	"json":        {Name: "[]byte"},
	"jsonb":       {Name: "[]byte"},
	"date":        {Name: "time.Time", Import: "time"},
	"time":        {Name: "time.Time", Import: "time"},
	"timestamp":   {Name: "time.Time", Import: "time"},
	"timestamptz": {Name: "time.Time", Import: "time"},

	"smallint":                    {Name: "int16"},
	"integer":                     {Name: "int32"},
	"bigint":                      {Name: "int64"},
	"boolean":                     {Name: "bool"},
	"real":                        {Name: "float32"},
	"double precision":            {Name: "float64"},
	"character varying":           {Name: "string"},
	"character":                   {Name: "string"},
	"timestamp without time zone": {Name: "time.Time", Import: "time"},
	"timestamp with time zone":    {Name: "time.Time", Import: "time"},
	"time without time zone":      {Name: "time.Time", Import: "time"},
	"time with time zone":         {Name: "time.Time", Import: "time"},
}

var mysql = map[string]Type{
	"varchar":   {Name: "string"},
	"text":      {Name: "string"},
	"char":      {Name: "string"},
	"tinyint":   {Name: "int8"},
	"smallint":  {Name: "int16"},
	"int":       {Name: "int32"},
	"bigint":    {Name: "int64"},
	"float":     {Name: "float32"},
	"double":    {Name: "float64"},
	"blob":      {Name: "[]byte"},
	"json":      {Name: "[]byte"},
	"bool":      {Name: "bool"},
	"boolean":   {Name: "bool"},
	"date":      {Name: "time.Time", Import: "time"},
	"datetime":  {Name: "time.Time", Import: "time"},
	"timestamp": {Name: "time.Time", Import: "time"},
}

var sqlite = map[string]Type{
	"text":     {Name: "string"},
	"integer":  {Name: "int64"},
	"int":      {Name: "int64"},
	"real":     {Name: "float64"},
	"blob":     {Name: "[]byte"},
	"boolean":  {Name: "bool"},
	"datetime": {Name: "time.Time", Import: "time"},
}

// pgxOverlay is what pgx maps differently, or maps at all. pgtype is pgx's own, so nothing
// here is a guess about whether it scans.
var pgxOverlay = map[string]Type{
	"numeric":  {Name: "pgtype.Numeric", Import: pgtypeImport},
	"interval": {Name: "pgtype.Interval", Import: pgtypeImport},
	"inet":     {Name: "netip.Addr", Import: "net/netip"},
	"cidr":     {Name: "netip.Prefix", Import: "net/netip"},
}

const pgtypeImport = "github.com/jackc/pgx/v5/pgtype"

// For resolves a catalog type name for an engine and driver. A pg_catalog-qualified name
// arrives with its schema attached; the schema carries no information the table needs.
func For(engine, sqlPackage, name string, isArray bool, arrayDims int) (Type, error) {
	table, ok := map[string]map[string]Type{
		"postgresql": postgres,
		"mysql":      mysql,
		"sqlite":     sqlite,
	}[engine]
	if !ok {
		return Type{}, fmt.Errorf("gotype: unsupported engine %q", engine)
	}
	bare := unqualify(name)
	t, ok := table[bare]
	if pgx(sqlPackage) {
		if over, has := pgxOverlay[bare]; has {
			t, ok = over, true
		}
	}
	if !ok {
		return Type{}, fmt.Errorf("gotype: %s with %s has no mapping for %q",
			engine, driverName(sqlPackage), name)
	}
	if isArray {
		if arrayDims < 1 {
			arrayDims = 1
		}
		for i := 0; i < arrayDims; i++ {
			t.Name = "[]" + t.Name
		}
	}
	return t, nil
}

func pgx(sqlPackage string) bool {
	return sqlPackage == "pgx/v5" || sqlPackage == "pgx/v4"
}

func driverName(sqlPackage string) string {
	if sqlPackage == "" {
		return "database/sql"
	}
	return sqlPackage
}

// unqualify drops a leading schema, so pg_catalog.int8 reads as int8.
func unqualify(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}
