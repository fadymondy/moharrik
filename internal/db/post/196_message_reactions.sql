-- MH-664: emoji reactions on inbox messages (table in
-- internal/db/schema/message_reactions.sql — schema runs before the app-role
-- grant step, which needs tenant tables to exist; post/ runs after).
--
-- Idempotent and safe on production data.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_reactions_workspace_fk') THEN
        ALTER TABLE message_reactions ADD CONSTRAINT message_reactions_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_reactions_message_fk') THEN
        ALTER TABLE message_reactions ADD CONSTRAINT message_reactions_message_fk
            FOREIGN KEY (workspace_id, message_id) REFERENCES messages (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_reactions_user_fk') THEN
        ALTER TABLE message_reactions ADD CONSTRAINT message_reactions_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_reactions_emoji_check') THEN
        ALTER TABLE message_reactions ADD CONSTRAINT message_reactions_emoji_check
            CHECK (emoji <> '' AND length(emoji) <= 32);
    END IF;
END $$;

ALTER TABLE message_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_reactions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON message_reactions;
CREATE POLICY tenant_isolation ON message_reactions
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
