-- Inbox: same-tenant foreign keys and indexes (after every schema fragment).
-- 18 sorts after 17_notifications.sql and before 20_rls.sql.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_workspace_fk') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_workspace_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Deleting a widget keeps its conversations (the history is the record);
    -- the link is allowed to dangle, like a notification item's source.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_widget_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_widget_fk
            FOREIGN KEY (workspace_id, widget_id) REFERENCES chat_widgets (workspace_id, id) ON DELETE SET NULL (widget_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_contact_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE SET NULL (contact_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_assignee_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_assignee_fk
            FOREIGN KEY (assignee_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_workspace_fk') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- A conversation's messages go with it.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_conversation_fk') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_conversation_fk
            FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_author_fk') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_author_fk
            FOREIGN KEY (author_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

-- The visitor's resume handle: unguessable, and looked up on its own.
CREATE UNIQUE INDEX IF NOT EXISTS conversations_visitor_token_uq ON conversations (visitor_token) WHERE visitor_token <> '';
-- The inbox list: newest activity first, and the unread filter.
CREATE INDEX IF NOT EXISTS conversations_recent_idx ON conversations (workspace_id, last_message_at DESC);
CREATE INDEX IF NOT EXISTS conversations_unread_idx ON conversations (workspace_id, last_message_at DESC) WHERE unread;
CREATE INDEX IF NOT EXISTS conversations_status_idx ON conversations (workspace_id, status, last_message_at DESC);
CREATE INDEX IF NOT EXISTS conversations_contact_idx ON conversations (workspace_id, contact_id) WHERE contact_id IS NOT NULL;
-- A thread reads oldest first.
CREATE INDEX IF NOT EXISTS messages_thread_idx ON messages (workspace_id, conversation_id, created_at);
-- A redelivery from email/WhatsApp is the same row.
CREATE UNIQUE INDEX IF NOT EXISTS messages_external_uq ON messages (workspace_id, external_id) WHERE external_id <> '';
