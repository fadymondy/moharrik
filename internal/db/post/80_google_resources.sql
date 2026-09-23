-- Google resources (MH-440): foreign keys, indexes and row-level security.
-- Batch G owns 80–84. Every statement is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'google_resources_connection_fk') THEN
        ALTER TABLE google_resources ADD CONSTRAINT google_resources_connection_fk
            FOREIGN KEY (workspace_id, connection_id) REFERENCES google_connections (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'google_discoveries_connection_fk') THEN
        ALTER TABLE google_discoveries ADD CONSTRAINT google_discoveries_connection_fk
            FOREIGN KEY (workspace_id, connection_id) REFERENCES google_connections (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS google_resources_kind_idx ON google_resources (workspace_id, kind, enabled);
-- The collector's queue, read cross-tenant on the owner pool.
CREATE INDEX IF NOT EXISTS google_resources_due_idx ON google_resources (next_sync_at) WHERE enabled;

ALTER TABLE google_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_resources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON google_resources;
CREATE POLICY tenant_isolation ON google_resources
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE google_discoveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_discoveries FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON google_discoveries;
CREATE POLICY tenant_isolation ON google_discoveries
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
