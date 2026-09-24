-- Several IMAP mailboxes per workspace (MH-555), and the inbox-rendering
-- columns MH-556 needs on the messages/conversations these mailboxes feed.
--
-- Batch U's reserved block is 150-154. Every statement is idempotent.

-- ---- mailboxes: keys, one primary per workspace, RLS -----------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mailboxes_workspace_fk') THEN
        ALTER TABLE mailboxes ADD CONSTRAINT mailboxes_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, like every in-workspace link (internal/db/schema/crm.sql):
    -- an FK check runs as the table owner and ignores RLS, so a single-column
    -- key could name another workspace's SMTP service.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mailboxes_smtp_service_fk') THEN
        ALTER TABLE mailboxes ADD CONSTRAINT mailboxes_smtp_service_fk
            FOREIGN KEY (workspace_id, smtp_service_id) REFERENCES smtp_services (workspace_id, id)
            ON DELETE SET NULL (smtp_service_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mailbox_legacy_workspace_fk') THEN
        ALTER TABLE mailbox_legacy ADD CONSTRAINT mailbox_legacy_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- At most one primary mailbox per workspace: a partial unique index, so "which
-- one answers a thread with nothing else to go on" can never have two answers.
CREATE UNIQUE INDEX IF NOT EXISTS mailboxes_primary_uniq
    ON mailboxes (workspace_id) WHERE is_primary;

ALTER TABLE mailboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mailboxes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mailboxes;
CREATE POLICY tenant_isolation ON mailboxes
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE mailbox_legacy ENABLE ROW LEVEL SECURITY;
ALTER TABLE mailbox_legacy FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mailbox_legacy;
CREATE POLICY tenant_isolation ON mailbox_legacy
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- ---- the legacy row: copied into mailboxes ONCE -----------------------------
--
-- Each workspace's single mailbox_legacy row becomes its first mailbox, named
-- "Mailbox 1" and marked primary, so every conversation and reply that worked
-- before MH-555 keeps working after it. Copied once: migrated_at marks the
-- row, so deleting the migrated mailbox later does not bring it back on the
-- next `migrate` (the smtp_services precedent, post/115_smtp_services.sql).
WITH copied AS (
    INSERT INTO mailboxes (workspace_id, name, is_primary, imap_host, imap_port, username, password_sealed,
                            tls_mode, folder, enabled, last_polled_at, last_error, last_error_at, message_count,
                            created_by, created_at, updated_at)
    SELECT l.workspace_id, 'Mailbox 1', true, l.imap_host, l.imap_port, l.username, l.password_sealed,
           l.tls_mode, l.folder, l.enabled, l.last_polled_at, l.last_error, l.last_error_at, l.message_count,
           l.created_by, l.created_at, l.updated_at
    FROM mailbox_legacy l
    WHERE l.migrated_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM mailboxes m WHERE m.workspace_id = l.workspace_id AND m.name = 'Mailbox 1')
    RETURNING id, workspace_id
)
UPDATE mailbox_legacy SET migrated_at = now()
WHERE migrated_at IS NULL
  AND workspace_id IN (SELECT workspace_id FROM copied
                       UNION SELECT workspace_id FROM mailboxes WHERE name = 'Mailbox 1');

-- ---- conversations / messages: which mailbox, and the HTML body (MH-556) ---

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS mailbox_id text;
ALTER TABLE messages      ADD COLUMN IF NOT EXISTS mailbox_id text;
ALTER TABLE messages      ADD COLUMN IF NOT EXISTS body_html  text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_mailbox_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_mailbox_fk
            FOREIGN KEY (workspace_id, mailbox_id) REFERENCES mailboxes (workspace_id, id)
            ON DELETE SET NULL (mailbox_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_mailbox_fk') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_mailbox_fk
            FOREIGN KEY (workspace_id, mailbox_id) REFERENCES mailboxes (workspace_id, id)
            ON DELETE SET NULL (mailbox_id);
    END IF;
END $$;

-- Backfill: every email conversation/message that predates MH-555 belongs to
-- the mailbox that was just migrated from mailbox_legacy for its workspace.
UPDATE conversations c SET mailbox_id = m.id
FROM mailboxes m
WHERE c.mailbox_id IS NULL AND c.channel = 'email' AND m.workspace_id = c.workspace_id AND m.name = 'Mailbox 1';

UPDATE messages msg SET mailbox_id = c.mailbox_id
FROM conversations c
WHERE msg.mailbox_id IS NULL AND msg.conversation_id = c.id AND msg.workspace_id = c.workspace_id
  AND c.channel = 'email' AND c.mailbox_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS conversations_mailbox_idx ON conversations (workspace_id, mailbox_id);
CREATE INDEX IF NOT EXISTS messages_mailbox_idx ON messages (workspace_id, mailbox_id);

-- ---- message_attachments: keys, RLS -----------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_attachments_workspace_fk') THEN
        ALTER TABLE message_attachments ADD CONSTRAINT message_attachments_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'message_attachments_message_fk') THEN
        ALTER TABLE message_attachments ADD CONSTRAINT message_attachments_message_fk
            FOREIGN KEY (workspace_id, message_id) REFERENCES messages (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS message_attachments_message_idx ON message_attachments (workspace_id, message_id);

ALTER TABLE message_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_attachments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON message_attachments;
CREATE POLICY tenant_isolation ON message_attachments
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- ---- full-text search (MH-556) ----------------------------------------------
--
-- Two generated tsvector columns rather than a hand-kept one, the same shape
-- as research_evidence.search (post/110_research_evidence.sql): the database
-- keeps them current, so there is no trigger to get out of step with an
-- INSERT that forgets it.
--
-- Message bodies are NOT folded into the conversation's own vector (that would
-- need a trigger, since a message lives in a different row) — instead
-- ListConversations searches messages.search too and matches a conversation
-- that any of its messages match, in internal/inbox/service.go.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple',
        coalesce(customer_name, '') || ' ' || coalesce(customer_email, '') || ' ' ||
        coalesce(customer_phone, '') || ' ' || coalesce(subject, ''))) STORED;
CREATE INDEX IF NOT EXISTS conversations_search_idx ON conversations USING gin (search);

ALTER TABLE messages ADD COLUMN IF NOT EXISTS search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', left(body, 100000))) STORED;
CREATE INDEX IF NOT EXISTS messages_search_idx ON messages USING gin (search);
