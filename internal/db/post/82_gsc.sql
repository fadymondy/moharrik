-- Search Console performance (MH-445): foreign keys, indexes, RLS. Idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'gsc_detail_resource_fk') THEN
        ALTER TABLE gsc_search_analytics_daily ADD CONSTRAINT gsc_detail_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'gsc_totals_resource_fk') THEN
        ALTER TABLE gsc_search_totals_daily ADD CONSTRAINT gsc_totals_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'gsc_import_days_resource_fk') THEN
        ALTER TABLE gsc_import_days ADD CONSTRAINT gsc_import_days_resource_fk
            FOREIGN KEY (workspace_id, resource_id) REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS gsc_detail_day_idx ON gsc_search_analytics_daily (workspace_id, resource_id, date);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['gsc_search_analytics_daily', 'gsc_search_totals_daily', 'gsc_import_days'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (workspace_id = current_setting('app.workspace_id', true))
            WITH CHECK (workspace_id = current_setting('app.workspace_id', true))$p$, t);
    END LOOP;
END $$;
