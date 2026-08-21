package golang

import (
	"strings"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/opts"
	"github.com/sqlc-dev/plugin-sdk-go/plugin"
	"github.com/sqlc-dev/plugin-sdk-go/sdk"
)

func addExtraGoStructTags(tags map[string]string, req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) {
	for _, override := range options.Overrides {
		oride := override.ShimOverride
		if oride.GoType.StructTags == nil {
			continue
		}
		if override.MatchesColumn(col) {
			for k, v := range oride.GoType.StructTags {
				tags[k] = v
			}
			continue
		}
		if !override.Matches(col.Table, req.Catalog.DefaultSchema) {
			// Different table.
			continue
		}
		cname := col.Name
		if col.OriginalName != "" {
			cname = col.OriginalName
		}
		if !sdk.MatchString(oride.ColumnName, cname) {
			// Different column.
			continue
		}
		// Add the extra tags.
		for k, v := range oride.GoType.StructTags {
			tags[k] = v
		}
	}
}

func goType(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) string {
	// Check if the column's type has been overridden
	for _, override := range options.Overrides {
		oride := override.ShimOverride

		if oride.GoType.TypeName == "" {
			continue
		}
		cname := col.Name
		if col.OriginalName != "" {
			cname = col.OriginalName
		}
		sameTable := override.Matches(col.Table, req.Catalog.DefaultSchema)
		if oride.Column != "" && sdk.MatchString(oride.ColumnName, cname) && sameTable {
			if col.IsSqlcSlice {
				return "[]" + oride.GoType.TypeName
			}
			return oride.GoType.TypeName
		}
	}
	typ := goInnerType(req, options, col)
	if col.IsSqlcSlice {
		return "[]" + typ
	}
	if col.IsArray {
		return strings.Repeat("[]", int(col.ArrayDims)) + typ
	}
	return typ
}

// overridden reports whether the column's Go type came from an override, which means it is
// rendered as written and nothing — no pointer for a nil test — is added to it. It is decided by
// asking what the type would be without the overrides rather than by re-implementing the
// matching, which has three separate forms and would drift.
func overridden(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) bool {
	if len(options.Overrides) == 0 {
		return false
	}
	bare := *options
	bare.Overrides = nil
	return goType(req, options, col) != goType(req, &bare, col)
}

func goInnerType(req *plugin.GenerateRequest, options *opts.Options, col *plugin.Column) string {
	// package overrides have a higher precedence
	for _, override := range options.Overrides {
		oride := override.ShimOverride
		if oride.GoType.TypeName == "" {
			continue
		}
		if override.MatchesColumn(col) {
			return oride.GoType.TypeName
		}
	}

	// TODO: Extend the engine interface to handle types
	switch req.Settings.Engine {
	case "mysql":
		return mysqlType(req, options, col)
	case "postgresql":
		return postgresType(req, options, col)
	case "sqlite":
		return sqliteType(req, options, col)
	default:
		return "interface{}"
	}
}
