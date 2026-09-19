-- Postgres queries for workspace_members (tenant predicate required).

-- name: GetWorkspaceMember :one
SELECT * FROM workspace_members WHERE workspace_id = $1 AND user_id = $2;

-- name: ListWorkspaceMembers :many
SELECT * FROM workspace_members WHERE workspace_id = $1 ORDER BY created_at ASC;
