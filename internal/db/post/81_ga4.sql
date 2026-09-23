-- GA4 reports (MH-444): foreign keys and row-level security. Idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ga4_reports_resource_fk') THEN
        ALTER TABLE ga4_reports ADD CONSTRAINT ga4_reports_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ga4_daily_resource_fk') THEN
        ALTER TABLE ga4_daily ADD CONSTRAINT ga4_daily_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

ALTER TABLE ga4_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE ga4_reports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON ga4_reports;
CREATE POLICY tenant_isolation ON ga4_reports
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE ga4_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE ga4_daily FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON ga4_daily;
CREATE POLICY tenant_isolation ON ga4_daily
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
