-- Hand-written Postgres DDL for McpToken (see workspace.sql for why).
-- A user MCP token belongs to one user in one workspace. Only the SHA-256 hash
-- is stored; `prefix` is the visible "mhr_" + 8 chars used to recognise it.
-- scopes is a JSON array of scope names (the manifest types it as text; the
-- plan's text[] would need a custom sqlc/gqlgen mapping the generator lacks).
CREATE TABLE IF NOT EXISTS mcp_tokens (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    user_id      text        NOT NULL,
    name         text        NOT NULL,
    token_hash   text        NOT NULL UNIQUE,
    prefix       text        NOT NULL,
    scopes       text        NOT NULL DEFAULT '[]',
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
