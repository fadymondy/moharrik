-- Google connections (MH-439): foreign keys, indexes and row-level security.
--
-- Numbered 40 rather than next-in-line: 30 and 31 are on main and other open
-- branches claim 32 and 33, so a clear gap keeps this from colliding with
-- them. Every statement is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'google_connections_workspace_fk') THEN
        ALTER TABLE google_connections ADD CONSTRAINT google_connections_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'google_oauth_states_workspace_fk') THEN
        ALTER TABLE google_oauth_states ADD CONSTRAINT google_oauth_states_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS google_connections_workspace_idx
    ON google_connections (workspace_id, created_at);
-- Expired attempts are swept on every new attempt.
CREATE INDEX IF NOT EXISTS google_oauth_states_expires_idx
    ON google_oauth_states (workspace_id, expires_at);

-- Connections: the page, the token source and disconnect all run on the app
-- pool inside the workspace, so this policy governs every one of them.
ALTER TABLE google_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_connections FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON google_connections;
CREATE POLICY tenant_isolation ON google_connections
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Attempts are written on the app pool inside the workspace. The callback
-- arrives with no workspace in hand, so it consumes its row on the OWNER pool
-- by the sha256 of the state it was given, and only then acts inside the
-- workspace that row names: the same shape as a webhook delivery resolving
-- its source (internal/notifications).
ALTER TABLE google_oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_oauth_states FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON google_oauth_states;
CREATE POLICY tenant_isolation ON google_oauth_states
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
