-- Directives are indented by one space on purpose: sqlc lifts a block comment that starts at
-- column zero out of the query text, taking the rest of its line with it.

-- name: SearchUsers :many
select id, name, balance, seen_at, tags, note
from users
where 1 = 1
  /*%if activeOnly*/ and status = @status /*%end*/
  /*%if minAge != null*/ and age >= @min_age /*%end*/
  /*%if ids != null*/ and id in (sqlc.slice(ids)) /*%end*/
  /*%for c in conds*/ and (name like sqlc.arg('c.name') or status = sqlc.arg('c.status')) /*%end*/
order by /*%if byName*/ name, /*%end*/ id;

-- name: CountUsers :one
select count(*) as total from users where status = sqlc.arg(status);

-- name: TouchUser :exec
update users set seen_at = now() where id = @id;
