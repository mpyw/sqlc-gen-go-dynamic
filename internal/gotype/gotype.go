// Package gotype maps a catalog type onto a Go type.
//
// The table is deliberately small and refuses what it does not know. A wrong type here is a
// wrong type in someone's generated code, and guessing produces one that compiles.
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
	"numeric":     {Name: "string"},
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
	"decimal":   {Name: "string"},
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

// For resolves a catalog type name for an engine. A pg_catalog-qualified name arrives with
// its schema attached; the schema carries no information the table needs.
func For(engine, name string, isArray bool, arrayDims int) (Type, error) {
	table, ok := map[string]map[string]Type{
		"postgresql": postgres,
		"mysql":      mysql,
		"sqlite":     sqlite,
	}[engine]
	if !ok {
		return Type{}, fmt.Errorf("gotype: unsupported engine %q", engine)
	}
	t, ok := table[unqualify(name)]
	if !ok {
		return Type{}, fmt.Errorf("gotype: %s has no mapping for %q", engine, name)
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

// unqualify drops a leading schema, so pg_catalog.int8 reads as int8.
func unqualify(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}
