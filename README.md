# sqlc-gen-bisql

A [sqlc](https://sqlc.dev) codegen plugin that gives sqlc queries `/*%if*/` and
`/*%for*/`, so a query with branches keeps sqlc's type safety instead of losing it.

**Status: v0, exploratory.** Two of the four pieces exist. Nothing generates code yet.

## The problem

sqlc types a query completely, but a query has to be one finished piece of static
SQL. A search with five optional filters therefore becomes either a catch-all
query — one that no optimizer can plan well, because the plan has to serve every
combination — or five hand-written queries. The usual escape is a query builder or
a template engine, and both give up the typing.

The escape here is to keep sqlc's frontend and add structure that sqlc cannot see
the meaning of, because it is written in comments:

```sql
-- name: SearchUsers :many
select u.id, u.name
from users u
where 1 = 1
  /*%if activeOnly*/ and u.status = @status /*%end*/
  /*%if departmentId != null*/ and u.department_id = @department_id /*%end*/
  /*%for c in conds*/ and (u.name like sqlc.arg('c.name') or u.status = sqlc.arg('c.status')) /*%end*/
order by /*%if byName*/ u.name, /*%end*/ u.id;
```

sqlc parses that as ordinary SQL — the directives are comments — resolves every
`@name` against the catalog, and hands the plugin both the typed parameter list
and the body verbatim. The plugin recovers the branch structure from the comments
and emits a typed API; at run time [bisql](https://github.com/mpyw/bisql)
evaluates the directives and renders `(SQL, args)`.

```go
type SearchUsersParams struct {
	ActiveOnly   bool                // gates a branch
	Status       string              // text, from users.status
	DepartmentID *int64              // nullable column, and nil-tested in a condition
	Conds        []SearchUsersCond   // one element per loop iteration
	ByName       bool
}

type SearchUsersCond struct {
	Name   string
	Status string
}
```

Three layers, and each one does only what it is good at: **sqlc** owns types and
catalog checking, **the plugin** owns the mapping from branch structure to Go
types, and **bisql** owns rendering at run time. The plugin is the thin part.

## Why not the other designs

- **Teach sqlc to parse 2-way templates.** Plugins run at codegen, after parsing;
  there is no hook into the frontend, and `GenerateRequest` carries the catalog but
  not the query AST, so a plugin cannot resolve "which column is this literal
  compared against". That needs a fork of sqlc itself.
- **Keep bisql's 2-way bind syntax** (`/*status*/'active'`). Two-way binding needs a
  literal at the bind site; sqlc needs a parameter marker. No single text is both,
  so bridging them takes a pre-parse rewrite — which is, again, outside the plugin.
  Giving up two-way *for values only* removes the conflict entirely, and the
  structure directives stay comments either way.
- **Feed sqlc the rendered SQL.** Workable, and it type-checks, but it only yields
  row structs: the parameters are already flattened into `$1..$n` by then.

## What is here

| | |
|---|---|
| `internal/exprtype` | Infers Go types for the variables that appear only in directive conditions, and refuses — with a reason — the ones it cannot. |
| `internal/directive` | Recovers the directive tree from `Query.text` and pairs each placeholder with sqlc's parameter name. |
| _(not written)_ | The codegen itself, and the sqlc plugin entry point. |

### How a type is decided

Only three things determine a type, and everything else is refused rather than
guessed:

| Source | Pins |
|---|---|
| sqlc's parameter table | whatever the catalog says, including `sqlc.narg` nullability |
| a literal in a condition | `string`, `int64`, `float64`, `bool` — all expr's literals can express |
| a boolean position | `bool` |
| a `/*%for*/` whose body binds something | a slice of what those binds are |

A construct that narrows a variable without determining it is recorded as a
*constraint*, and a constraint is not a type. `len(x)` accepts strings, slices and
maps alike, so on its own it decides nothing:

```
keywords: cannot infer a type: all that is known is that it has a length
(len() accepts strings, slices and maps alike). Bind it in the SQL, compare it
with a literal, iterate it with /*%for*/ and bind something in the body, or
replace the condition with a boolean gate
```

In practice a `len` guard sits on a collection the query also iterates, and the
loop pins it. Refusal is reserved for a variable that genuinely appears nowhere
else — where there is no type to be found, only one to be invented.

### Optionality

A parameter becomes a pointer for two reasons and no others:

- **sqlc says it is nullable** — from `sqlc.narg()` or from a nullable column,
  which the request does not distinguish. Following sqlc verbatim is what keeps
  the generated types identical to what `gen: go` already produces.
- **a condition nil-tests it** — `/*%if departmentId != null*/`. Here unset and
  zero must be distinguishable, or the branch decision itself is wrong: an
  `int64` is never nil, so the branch would always be taken.

Sitting inside `/*%if*/` or `/*%for*/` is deliberately *not* a reason. When the
branch does not render, nothing reads the value, so the zero value is a fine
stand-in and a pointer buys nothing.

## Authoring rules

- **Indent every directive by at least one space.** sqlc treats a block comment
  starting at column zero as standalone: it moves the comment into
  `Query.comments` and drops the whole line from `Query.text`, taking the SQL
  beside it. The parameter table survives, so the damage is invisible in the
  types. `directive.CheckComments` reports it.
- **Cast a bind used in a row comparison or an array context.** sqlc mistypes
  `(a, b) = ($1, $2)` — it gives `$2` the type of `a` — and marks a bind under
  `= any(...)` or `array[...]` as an array. `sqlc.arg('x')::text` fixes both. For
  row comparisons the better fix is bisql's usual anchored list: a `1 = 0` seed
  with one `or (a = @x and b = @y)` per element, which types correctly with no
  casts at all.
- **Keep the select list static.** Toggling a column changes the row struct, and
  there is only one of those.
- **Name a nil-tested variable after the bind it guards.** `departmentId != null`
  and `@department_id` have to land on the same field for the type (from sqlc) and
  the optionality (from the condition) to meet; the two spellings are folded
  together, but only if they are the same name.

## Prior measurements

Every claim above about sqlc's behaviour was measured against sqlc v1.31.1 with
the PostgreSQL engine, dumping `GenerateRequest` via `gen: json`. The findings
that shaped the design:

- `Query.text` keeps indented directives verbatim, with `@name` and
  `sqlc.arg('name')` replaced in place by the placeholder — so the plugin needs no
  filesystem access and can be a WASM plugin.
- `sqlc.arg('c.name')` carries a dotted name through to `column.name`; `@c.name`
  is a syntax error. This is what makes loop element structs possible.
- Every arm of an `/*%if*/ /*%elseif*/ /*%else*/` chain is typed at once, because
  sqlc sees the union of the branch texts. There is no combinatorial explosion to
  manage: one pass types every branch.
- `not_null` is false for `sqlc.narg()` and for a nullable column alike; the two
  are indistinguishable in the request.
