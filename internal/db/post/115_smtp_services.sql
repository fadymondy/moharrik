-- Several SMTP services per workspace (MH-412): keys, one default of each
-- kind, row-level security, a service per campaign, and the one-time copy of
-- the legacy single smtp_settings row.
--
-- Batch N's reserved block is 115–119. Every statement is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'smtp_services_workspace_fk') THEN
        ALTER TABLE smtp_services ADD CONSTRAINT smtp_services_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- At most one default and one campaign default per workspace. Partial unique
-- indexes, so "which one is it" can never have two answers.
CREATE UNIQUE INDEX IF NOT EXISTS smtp_services_default_uniq
    ON smtp_services (workspace_id) WHERE is_default;
CREATE UNIQUE INDEX IF NOT EXISTS smtp_services_campaign_default_uniq
    ON smtp_services (workspace_id) WHERE is_campaign_default;

ALTER TABLE smtp_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE smtp_services FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON smtp_services;
CREATE POLICY tenant_isolation ON smtp_services
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Which service a campaign goes out through. NULL = the campaign default (and,
-- failing that, the default) — which is what every existing campaign means.
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS smtp_service_id text;

DO $$
BEGIN
    -- Composite, like every in-workspace link: the FK check runs as the owner
    -- and ignores RLS, so a single-column key could name another workspace's
    -- service. Deleting a service returns its campaigns to the default
    -- (SET NULL on the service column only, never on workspace_id).
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'campaigns_smtp_service_fk') THEN
        ALTER TABLE campaigns ADD CONSTRAINT campaigns_smtp_service_fk
            FOREIGN KEY (workspace_id, smtp_service_id) REFERENCES smtp_services (workspace_id, id)
            ON DELETE SET NULL (smtp_service_id);
    END IF;
END $$;

-- ---- the legacy row ---------------------------------------------------------
--
-- Each workspace's single smtp_settings row becomes its first service, and
-- both defaults, so everything that sent mail before this migration sends it
-- through exactly the same server after it. Copied ONCE: migrated_at marks
-- the row, so a workspace that later deletes the service does not get it back
-- on the next migrate. The legacy table is kept (not dropped) so a rollback to
-- the previous release still finds its settings.
ALTER TABLE smtp_settings ADD COLUMN IF NOT EXISTS migrated_at timestamptz;

WITH copied AS (
    INSERT INTO smtp_services (workspace_id, name, host, port, username, password_sealed, from_name, from_email,
                               tls_mode, is_default, is_campaign_default, created_at, updated_at)
    SELECT s.workspace_id, 'Default', s.host, s.port, s.username, s.password_sealed, s.from_name, s.from_email,
           s.tls_mode,
           NOT EXISTS (SELECT 1 FROM smtp_services d WHERE d.workspace_id = s.workspace_id AND d.is_default),
           NOT EXISTS (SELECT 1 FROM smtp_services d WHERE d.workspace_id = s.workspace_id AND d.is_campaign_default),
           s.updated_at, s.updated_at
    FROM smtp_settings s
    WHERE s.migrated_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM smtp_services d WHERE d.workspace_id = s.workspace_id AND d.name = 'Default')
    RETURNING workspace_id
)
UPDATE smtp_settings SET migrated_at = now()
WHERE migrated_at IS NULL
  AND workspace_id IN (SELECT workspace_id FROM copied
                       UNION SELECT workspace_id FROM smtp_services WHERE name = 'Default');
