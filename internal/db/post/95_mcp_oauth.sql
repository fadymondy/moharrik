-- OAuth for MCP (MH-391): the lock-down. See internal/db/schema/oauth.sql.
--
-- The platform tables have no grant to the app role (they are not in
-- migrate.TenantTables) and RLS on with NO policy, like admin_mcp_tokens and
-- agent_connects: if a grant is ever added by mistake, the app role still sees
-- nothing. Everything that touches them runs on the owner pool with an explicit
-- predicate on a hash or an id.
ALTER TABLE oauth_clients ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_clients FORCE ROW LEVEL SECURITY;
ALTER TABLE oauth_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_codes FORCE ROW LEVEL SECURITY;
ALTER TABLE oauth_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_tokens FORCE ROW LEVEL SECURITY;
ALTER TABLE oauth_client_assertions ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_client_assertions FORCE ROW LEVEL SECURITY;

-- The grants are the workspace's: listed and revoked in Settings → MCP through
-- the app pool, so a workspace can only ever see its own connected apps.
ALTER TABLE oauth_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_grants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON oauth_grants;
CREATE POLICY tenant_isolation ON oauth_grants
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

DO $$
BEGIN
    -- A deleted workspace or user takes its connections along; a deleted
    -- grant takes its codes and tokens.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'oauth_grants_workspace_fk') THEN
        ALTER TABLE oauth_grants ADD CONSTRAINT oauth_grants_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'oauth_grants_user_fk') THEN
        ALTER TABLE oauth_grants ADD CONSTRAINT oauth_grants_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'oauth_grants_client_fk') THEN
        ALTER TABLE oauth_grants ADD CONSTRAINT oauth_grants_client_fk
            FOREIGN KEY (client_id) REFERENCES oauth_clients (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'oauth_codes_grant_fk') THEN
        ALTER TABLE oauth_codes ADD CONSTRAINT oauth_codes_grant_fk
            FOREIGN KEY (grant_id) REFERENCES oauth_grants (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'oauth_tokens_grant_fk') THEN
        ALTER TABLE oauth_tokens ADD CONSTRAINT oauth_tokens_grant_fk
            FOREIGN KEY (grant_id) REFERENCES oauth_grants (id) ON DELETE CASCADE;
    END IF;
END $$;
