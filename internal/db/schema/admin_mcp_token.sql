-- Hand-written Postgres DDL for AdminMcpToken (model only — no generated CRUD).
-- Tokens for the platform ADMIN MCP (/api/admin/mcp). Not tenant data: the app
-- role has no grant on this table at all, and RLS is enabled with no policy, so
-- nothing but the owner pool (internal/mcp/admin_auth.go) can read it.
-- scopes: JSON array of admin:* scopes (internal/mcp/scopes.go).
CREATE TABLE IF NOT EXISTS admin_mcp_tokens (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
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
