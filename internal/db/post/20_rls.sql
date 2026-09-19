-- Row-level security: the tenancy backstop.
--
-- The API talks to Postgres through two pools (internal/app/app.go):
--   * the APP pool (APP_DATABASE_URL) — a role that owns nothing, is not a
--     superuser and has no BYPASSRLS. Every generated handler, resolver and ORM
--     query goes through it. Before each statement the connection wrapper in
--     internal/tenant/rlsdb.go runs set_config('app.workspace_id', <ctx>, false),
--     so a request can only ever see its own workspace's rows.
--   * the OWNER pool (DATABASE_URL) — used only for tenant resolution, auth,
--     token lookup and the admin MCP. It bypasses RLS by being the table owner.
--
-- No workspace in context => the setting is '' => every policy is false.
-- current_setting(..., true) returns NULL when the setting was never defined,
-- which is also false. Default-deny either way.
--
-- Grants to the app role are applied by cmd/migrate (the role name comes from
-- APP_DATABASE_URL, not from this file).

ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workspaces;
CREATE POLICY tenant_isolation ON workspaces
    USING (id = current_setting('app.workspace_id', true))
    WITH CHECK (id = current_setting('app.workspace_id', true));

ALTER TABLE workspace_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_members FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workspace_members;
CREATE POLICY tenant_isolation ON workspace_members
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON invitations;
CREATE POLICY tenant_isolation ON invitations
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mcp_tokens;
CREATE POLICY tenant_isolation ON mcp_tokens
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Platform-only tables: RLS on with NO policy = nothing visible to the app role
-- even if a grant is ever added by mistake.
ALTER TABLE admin_mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_mcp_tokens FORCE ROW LEVEL SECURITY;
