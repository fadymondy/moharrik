-- Postgres queries for admin_mcp_tokens. Platform table, not tenant data: it is
-- read only through the owner pool.

-- name: GetAdminMcpTokenByHash :one
-- guard:cross-tenant platform token lookup; admin_mcp_tokens has no workspace
SELECT * FROM admin_mcp_tokens WHERE token_hash = $1;
