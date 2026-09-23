-- Search Console URL inspection (MH-446): foreign keys, indexes, RLS. Idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'gsc_url_inspections_resource_fk') THEN
        ALTER TABLE gsc_url_inspections ADD CONSTRAINT gsc_url_inspections_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'gsc_inspection_quota_workspace_fk') THEN
        ALTER TABLE gsc_inspection_quota ADD CONSTRAINT gsc_inspection_quota_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS gsc_url_inspections_recent_idx ON gsc_url_inspections (workspace_id, resource_id, inspected_at DESC);

ALTER TABLE gsc_url_inspections ENABLE ROW LEVEL SECURITY;
ALTER TABLE gsc_url_inspections FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON gsc_url_inspections;
CREATE POLICY tenant_isolation ON gsc_url_inspections
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- The ledger is the owner pool's alone (see the schema file).
ALTER TABLE gsc_inspection_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE gsc_inspection_quota FORCE ROW LEVEL SECURITY;
