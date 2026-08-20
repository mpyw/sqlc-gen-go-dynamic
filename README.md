# sqlc-gen-go-dynamic

A fork of [sqlc-gen-go](https://github.com/sqlc-dev/sqlc-gen-go) that gives sqlc queries
`/*%if*/` and `/*%for*/`, so a query with branches keeps its type safety instead of losing it.

A query with no directives is emitted byte for byte as `gen: go` would emit it. Switching is
one line of configuration, and nothing already working changes.

**Status: v0, exploratory.**

## The problem

sqlc types a query completely, but a query has to be one finished piece of static SQL. A
search with five optional filters therefore becomes either a catch-all query — one no
optimizer can plan well, because the plan has to serve every combination — or five
hand-written queries. The usual escape is a query builder or a template engine, and both give
up the typing.

The escape here is to keep sqlc's frontend and add structure sqlc cannot see the meaning of,
because it is written in comments:

```sql
-- name: SearchUsers :many
select id, name, seen_at, note
from users
where 1 = 1
  /*%if activeOnly*/ and status = @status /*%end*/
  /*%if minAge != null*/ and age >= @min_age /*%end*/
  /*%if ids != null*/ and id in (sqlc.slice(ids)) /*%end*/
  /*%for c in conds*/ and (name like sqlc.arg('c.name') or status = sqlc.arg('c.status')) /*%end*/
order by /*%if byName*/ name, /*%end*/ id;
```

sqlc parses that as ordinary SQL — the directives are comments — resolves every marker against
the catalog, and hands back both the typed parameter list and the body verbatim. What this
adds is the shape: which parameters a branch reaches, and which repeat.

```go
type SearchUsersParams struct {
	ActiveOnly bool
	Status     string
	MinAge     *int32
	Ids        []int64
	Conds      []SearchUsersCond
	ByName     bool
}

type SearchUsersCond struct {
	Name   string
	Status string
}

func (q *Queries) SearchUsers(ctx context.Context, arg SearchUsersParams) ([]SearchUsersRow, error)
```

`MinAge` is a pointer for one reason: the condition nil-tests it, so unset has to be
distinguishable from zero or the branch decision is wrong. `Status` is a value even though it
sits inside a branch — when the branch does not render, nothing reads the value.

## Configuration

```yaml
version: "2"
plugins:
  - name: dynamic
    process:
      cmd: sqlc-gen-go-dynamic
sql:
  - engine: postgresql
    schema: schema.sql
    queries: query.sql
    codegen:
      - plugin: dynamic
        out: gen
        options:
          package: gen
          sql_package: pgx/v5
```

Every option `sqlc-gen-go` accepts is accepted here, `overrides` included, because it is the
same codegen. WASM works too: `mise run wasm` builds both transports.

## Directives

| | |
| --- | --- |
| `/*%if e*/ … /*%elseif e*/ … /*%else*/ … /*%end*/` | renders the first arm whose condition holds |
| `/*%for x in xs*/ … /*%end*/` | renders the body once per element, with nothing inserted between iterations |
| `/*%! … */` | a comment that is stripped before the SQL is sent |

Conditions are [expr](https://github.com/expr-lang/expr) expressions. The null literal is
`nil`, and Komapper's `x != null` also works, because an undefined identifier resolves to nil.

The engine **emits text verbatim**: it evaluates directives and does nothing else. No
empty-clause removal, no dangling-connector cleanup, no whitespace normalization, and nothing
inserted between loop iterations. Templates anchor their dynamic fragments instead — a `1 = 1`
seed, a connector at the head of each fragment — which is what makes the result predictable
enough to read next to the query it came from.

Placeholder numbering runs off one counter per render, so a bind in a branch that did not
render is never counted and the numbering has no gaps.

## Authoring rules

- **Indent every directive by at least one space.** sqlc treats a block comment starting at
  column zero as standalone: it moves the comment into `Query.comments` and drops the whole
  line from `Query.text`, taking the SQL beside it. The parameter table survives, so nothing
  about the generated types looks wrong — a branch has simply vanished. It is reported rather
  than tolerated.
- **Anchor every dynamic fragment.** `where 1 = 1` then `and …` per fragment; `order by` ends
  with a stable key; a comma list gets a base element.
- **Keep the select list static.** Toggling a column changes the row struct, and there is only
  one of those.
- **Name a nil-tested variable after the bind it guards.** `minAge != null` and `@min_age`
  have to land on the same field for the type (from sqlc) and the optionality (from the
  condition) to meet.
- **Cast a bind used in a row comparison or an array context.** sqlc mistypes `(a, b) = ($1, $2)`
  — it gives `$2` the type of `a` — and marks a bind under `= any(...)` or `array[...]` as an
  array. `sqlc.arg(x)::text` fixes both. For row comparisons the better fix is an anchored
  list: a `1 = 0` seed with one `or (a = @x and b = @y)` per element, which types correctly
  with no casts.

## How a type is decided

A bind's type is sqlc's, from the same table every other query uses. What has to be inferred
is the type of a variable that appears **only** in a condition, and only three things decide
one:

| Source | Pins |
| --- | --- |
| sqlc's parameter table | whatever the catalog says |
| a literal in a condition | `string`, `int64`, `float64`, `bool` — all expr's literals can express |
| a boolean position | `bool` |
| a `/*%for*/` whose body binds something | a slice of what those binds are |

A construct that narrows a variable without determining it is a *constraint*, and a constraint
is not a type. `len(x)` accepts strings, slices and maps alike, so on its own it decides
nothing:

```
keywords: cannot infer a type: all that is known is that it has a length
(len() accepts strings, slices and maps alike). Bind it in the SQL, compare it
with a literal, iterate it with /*%for*/ and bind something in the body, or
replace the condition with a boolean gate
```

In practice a `len` guard sits on a collection the query also iterates, and the loop pins it.
Refusal is reserved for a variable that genuinely appears nowhere else — where there is no
type to be found, only one to be invented.

## What is not supported

- `/*%if*/` or `/*%for*/` in `:copyfrom` and the `:batch` forms, which build their SQL by
  other means. Refused rather than silently ignoring the directives.
- `@include`, and it cannot be here: the directive is a comment to sqlc, so an included
  fragment is never analyzed — a bind inside it would get no type while the runtime bound it
  anyway. Composition has to run upstream of sqlc, which makes it a separate tool.

## Layout

Upstream's tree is unmodified except where the dynamic path is spliced in, so a sync is a
merge rather than a re-application.

| | |
| --- | --- |
| `internal/*.go`, `internal/opts`, `internal/templates` | upstream, with `{{if .Dynamic}}` branches added to `queryCode.tmpl` and a hook at the end of `buildQueries` |
| `internal/dynamic.go` | the hook: sqlc's query in, the template and the parameter shape out |
| `internal/query` | the pipeline: restore the markers, parse once, type the result |
| `internal/placeholder` | restores the markers sqlc replaced, which yields the canonical template |
| `internal/bind` | recognizes sqlc's markers, mirroring sqlc exactly |
| `internal/sqltmpl` | the engine: `scan`, `token`, `ast`, `lexer`, `parser`, `render` |
| `internal/exprtype` | types the variables that appear only in conditions, and refuses the rest with a reason |
| `dyn` | the runtime the generated code calls |

### One text, one parser

`Query.text` differs from the template only in that sqlc replaced each marker with a
placeholder. Restoring the names — a text edit driven by the parameter table — yields the
canonical template: the text the generated code embeds, the runtime reads, and typing walks.
Build time and run time therefore cannot disagree about what a template means.

```
Query.text + params ──restore──▶ template ──parse──▶ AST ──▶ typing (build time)
                                     │                   └──▶ render (run time)
                                     └── embedded in the generated code
```

## Syncing with upstream

```bash
git remote add upstream https://github.com/sqlc-dev/sqlc-gen-go.git
git fetch upstream && git merge upstream/main
```

Two things diverge from upstream by construction and so conflict on a sync. The module path is
rewritten in the files that import it, and the tree is `gofmt`-clean where upstream is not —
its import blocks are out of order. The resolution for both is to take upstream's version and
re-apply, which `gofmt -w .` and one `sed` do.

Upstream's files are also excluded from `golangci-lint`, in `.golangci.yml`. sqlc-gen-go does
not run it, and satisfying it here would put a diff in every file upstream is most likely to
change. A new upstream file fails lint until it is listed, which is the signal that it needs
adding.

## What running real sqlc found

Every one of these was a measurement, not a deduction.

- The `@name` shortcut **does not exist for MySQL**, where `@name` is a user variable, so it
  is not a bind there either. A spelling one side binds and the other does not is the failure
  this whole arrangement exists to prevent.
- `sqlc.arg(name)` with a bare name is sqlc's own documented spelling and accepted by every
  engine; only the quoted form may carry a dot, because an unquoted `a.b` parses as a column
  reference and sqlc rejects it.
- Placeholder spellings are per-engine and cannot be accepted together: `?` is a jsonb
  operator in PostgreSQL, and `:3` is ordinary syntax there inside `a[1:3]`.
- A column-zero directive is lifted out of `Query.text` with the rest of its line, and the
  parameter table survives, so nothing looks wrong.
- `sqlc.embed` needs no work: sqlc expands it into an explicit column list in the text it
  returns. A renderer fed that text never meets the call — one fed the *original* template
  would, and would send it to the database.
- `sqlc.slice` parameters arrive already typed as a slice, so the shape must not make one
  twice.

## Development

```bash
mise run check   # fmt, build, vet, test
mise run lint    # golangci-lint
mise run sqlc    # real sqlc, real generation, real compilation
mise run wasm    # both transports
```

## License

MIT, as [sqlc-gen-go](https://github.com/sqlc-dev/sqlc-gen-go) and
[sqlc](https://github.com/sqlc-dev/sqlc) are. `LICENSE` is upstream's, kept as it was.
