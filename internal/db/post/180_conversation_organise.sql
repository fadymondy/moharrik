-- MH-657: the chat-row menu — pin, archive, mute, favourite (per member),
-- colour, icon, labels, block and clear (shared). Tables are in
-- internal/db/schema/conversation_organise.sql.
--
-- conversations.color / icon: a name from the tag palette / a named icon,
-- '' for none. blocked_at: inbound messages from a blocked customer are
-- dropped and the composer is closed. cleared_at: "Clear chat" deleted the
-- messages but the chat stays listed (an empty conversation is otherwise
-- hidden, MH-599).
--
-- Idempotent and safe on production data.

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS color text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS icon text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS blocked_at timestamptz;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS cleared_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_prefs_conversation_fk') THEN
        ALTER TABLE conversation_prefs ADD CONSTRAINT conversation_prefs_conversation_fk
            FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_prefs_user_fk') THEN
        ALTER TABLE conversation_prefs ADD CONSTRAINT conversation_prefs_user_fk
            FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_labels_workspace_fk') THEN
        ALTER TABLE conversation_labels ADD CONSTRAINT conversation_labels_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_labels_color_ck') THEN
        ALTER TABLE conversation_labels ADD CONSTRAINT conversation_labels_color_ck
            CHECK (color IN ('gray', 'red', 'orange', 'amber', 'green', 'teal', 'blue', 'violet', 'pink'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_color_ck') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_color_ck
            CHECK (color IN ('', 'gray', 'red', 'orange', 'amber', 'green', 'teal', 'blue', 'violet', 'pink'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_label_links_conversation_fk') THEN
        ALTER TABLE conversation_label_links ADD CONSTRAINT conversation_label_links_conversation_fk
            FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_label_links_label_fk') THEN
        ALTER TABLE conversation_label_links ADD CONSTRAINT conversation_label_links_label_fk
            FOREIGN KEY (workspace_id, label_id) REFERENCES conversation_labels (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS conversation_labels_name_uq ON conversation_labels (workspace_id, lower(name));
CREATE INDEX IF NOT EXISTS conversation_prefs_user_idx ON conversation_prefs (workspace_id, user_id);
CREATE INDEX IF NOT EXISTS conversation_label_links_label_idx ON conversation_label_links (workspace_id, label_id);

ALTER TABLE conversation_prefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_prefs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON conversation_prefs;
CREATE POLICY tenant_isolation ON conversation_prefs
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE conversation_labels ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_labels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON conversation_labels;
CREATE POLICY tenant_isolation ON conversation_labels
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE conversation_label_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_label_links FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON conversation_label_links;
CREATE POLICY tenant_isolation ON conversation_label_links
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
