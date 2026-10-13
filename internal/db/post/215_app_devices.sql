-- The Moharrik apps (docs/apps-plan.md): tenant keys and row-level security for
-- push_prefs. app_signin_codes and device_tokens are platform tables (owner
-- pool only, no app-role grant), like users.

DO $$
BEGIN
    -- Preferences belong to a MEMBERSHIP: someone removed from the workspace
    -- leaves nothing behind. Composite, so a row can never name a member of a
    -- different workspace.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'push_prefs_member_fk') THEN
        ALTER TABLE push_prefs ADD CONSTRAINT push_prefs_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES workspace_members (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
END $$;

ALTER TABLE push_prefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_prefs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON push_prefs;
CREATE POLICY tenant_isolation ON push_prefs
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
