-- MH-562: the foreign keys and indexes for internal/db/schema/import.sql's
-- tables, plus the generic external-thread identity every import source
-- shares (added here, not in the schema fragment, because conversations is
-- created by internal/db/schema/inbox.sql, and that CREATE is IF NOT EXISTS —
-- same reason 23_whatsapp.sql adds customer_phone here instead).
--
-- Idempotent re-import needs a stable key for "this external chat/thread is
-- already a conversation here", independent of channel: customer_phone's
-- existing unique index only covers a 1:1 WhatsApp chat, not a group, an
-- email thread, or a future Telegram chat. external_thread_id is that key for
-- every source: the WhatsApp chat/group JID, the email thread's root
-- Message-ID, and so on — unique per (workspace, channel) when present, so a
-- second import of the same chat finds the same conversation instead of
-- creating a duplicate.

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_group boolean NOT NULL DEFAULT false;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS external_thread_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS conversations_external_thread_uniq
    ON conversations (workspace_id, channel, external_thread_id) WHERE external_thread_id <> '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_participants_workspace_fk') THEN
        ALTER TABLE conversation_participants ADD CONSTRAINT conversation_participants_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_participants_conversation_fk') THEN
        ALTER TABLE conversation_participants ADD CONSTRAINT conversation_participants_conversation_fk
            FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversation_participants_contact_fk') THEN
        ALTER TABLE conversation_participants ADD CONSTRAINT conversation_participants_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE SET NULL (contact_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'import_jobs_workspace_fk') THEN
        ALTER TABLE import_jobs ADD CONSTRAINT import_jobs_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS conversation_participants_conv_idx ON conversation_participants (workspace_id, conversation_id);
CREATE INDEX IF NOT EXISTS import_jobs_workspace_idx ON import_jobs (workspace_id, created_at DESC);
