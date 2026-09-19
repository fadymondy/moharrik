-- Postgres queries for mcp_tokens (tenant predicate required).

-- name: ListUserMcpTokens :many
SELECT * FROM mcp_tokens WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at DESC;

-- name: RevokeMcpToken :exec
UPDATE mcp_tokens SET revoked_at = now(), updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL;
