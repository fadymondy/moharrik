-- Postgres queries for workspaces. Every query on a tenant table must carry its
-- tenant predicate (internal/guards/sqlc_guard_test.go); for workspaces that is
-- the row's own id.

-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = $1;

-- name: UpdateWorkspaceName :one
UPDATE workspaces SET name = $2, updated_at = now() WHERE id = $1 RETURNING *;
