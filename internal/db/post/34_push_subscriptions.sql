-- Web Push subscriptions (MH-378): tenant keys and row-level security.
--
-- Numbered 34: 32_leads.sql is MH-390 (lead intake, merged), 33 is held for
-- MH-439 (Google connect, open) and 35_subscriber_admin.sql is MH-408's.
-- Post files apply in name order and each is idempotent, so a gap in the
-- numbering costs nothing; nothing here depends on 32 or 33.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'push_subscriptions_workspace_fk') THEN
        ALTER TABLE push_subscriptions ADD CONSTRAINT push_subscriptions_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- A device belongs to a MEMBERSHIP, not just a user: someone removed from
    -- the workspace stops receiving its pushes the moment their membership
    -- row goes, with nothing left behind to clean up. Composite, so a row can
    -- never name a member of a different workspace.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'push_subscriptions_member_fk') THEN
        ALTER TABLE push_subscriptions ADD CONSTRAINT push_subscriptions_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES workspace_members (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
END $$;

ALTER TABLE push_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_subscriptions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON push_subscriptions;
CREATE POLICY tenant_isolation ON push_subscriptions
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
