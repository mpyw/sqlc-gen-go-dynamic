-- Every query here is static. This file is generated twice — once through the plugin and once
-- through gen: go — and the two outputs are compared byte for byte. Being a drop-in is the
-- claim; a stray newline in a template broke it, and the blank line it left between a query's
-- doc comment and its func silently detached the comment as well.

-- ListNames returns the names, in order.
-- name: ListNames :many
select id, name from users order by id;

-- name: NameOf :one
select name from users where id = @id;

-- CountActive counts the ones with a status.
-- name: CountActive :one
select count(*) as total from users where status = @status;

-- name: TouchSeen :exec
update users set seen_at = now() where id = @id;
