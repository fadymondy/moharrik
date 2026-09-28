-- Email templates (MH-661): foreign keys, the picker's order, and row-level
-- security. The table is internal/db/schema/email_template.sql.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'email_templates_workspace_fk') THEN
        ALTER TABLE email_templates ADD CONSTRAINT email_templates_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- The author may leave; the template stays the workspace's.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'email_templates_created_by_fk') THEN
        ALTER TABLE email_templates ADD CONSTRAINT email_templates_created_by_fk
            FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS email_templates_order_idx ON email_templates (workspace_id, position, lower(name));

ALTER TABLE email_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_templates FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON email_templates;
CREATE POLICY tenant_isolation ON email_templates
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
