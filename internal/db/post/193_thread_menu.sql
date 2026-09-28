-- MH-662: the thread ⋮ menu + Contact info panel — search, starred messages,
-- multi-select delete-for-us, and a per-contact notes field.
--
-- message_stars/message_hides tables are created in
-- internal/db/schema/thread_menu.sql (schema runs before the app-role grant
-- step, which needs the tenant tables to already exist — post/ runs after).
-- contacts.notes: free text the team keeps on a contact, shown in the panel.
--
-- Idempotent and safe on production data.

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS notes text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_stars_workspace_fk') THEN
        ALTER TABLE message_stars ADD CONSTRAINT message_stars_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_stars_message_fk') THEN
        ALTER TABLE message_stars ADD CONSTRAINT message_stars_message_fk
            FOREIGN KEY (workspace_id, message_id) REFERENCES messages (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_stars_user_fk') THEN
        ALTER TABLE message_stars ADD CONSTRAINT message_stars_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_hides_workspace_fk') THEN
        ALTER TABLE message_hides ADD CONSTRAINT message_hides_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_hides_message_fk') THEN
        ALTER TABLE message_hides ADD CONSTRAINT message_hides_message_fk
            FOREIGN KEY (workspace_id, message_id) REFERENCES messages (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_hides_user_fk') THEN
        ALTER TABLE message_hides ADD CONSTRAINT message_hides_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS message_stars_user_idx ON message_stars (workspace_id, user_id);
CREATE INDEX IF NOT EXISTS message_hides_user_idx ON message_hides (workspace_id, user_id);

ALTER TABLE message_stars ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_stars FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON message_stars;
CREATE POLICY tenant_isolation ON message_stars
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE message_hides ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_hides FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON message_hides;
CREATE POLICY tenant_isolation ON message_hides
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
