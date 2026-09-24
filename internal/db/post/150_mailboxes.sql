-- Several IMAP mailboxes per workspace (MH-555), and the inbox-rendering
-- columns MH-556 needs on the messages/conversations these mailboxes feed.
--
-- Batch U's reserved block is 150-154. Every statement is idempotent, and —
-- this is the part that was wrong in the first version of this file — every
-- statement is written to also upgrade a database that already ran MH-133's
-- original migration, where `mailboxes` has workspace_id as its primary key
-- and none of the columns below exist yet. On such a database, schema.sql's
-- CREATE TABLE IF NOT EXISTS is a no-op (the table already exists), so this
-- file is the ONLY place those columns get added. On a fresh database,
-- schema/mailbox.sql already created every column and the id primary key, so
-- every ALTER TABLE ADD COLUMN / the primary-key move below is a no-op there.
--
-- Each pre-existing row is, by MH-133's own design, the ONE mailbox its
-- workspace had — so it becomes that workspace's "Mailbox 1", primary, with
-- everything else (host, password, folder, poll state) carried over exactly
-- because these are ALTERs on the same row, not a copy into a new one.

-- ---- upgrade the OLD mailboxes table in place, column by column -----------

ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS id text;
UPDATE mailboxes SET id = gen_random_uuid()::text WHERE id IS NULL;
ALTER TABLE mailboxes ALTER COLUMN id SET NOT NULL;
ALTER TABLE mailboxes ALTER COLUMN id SET DEFAULT gen_random_uuid()::text;

ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS name text;
UPDATE mailboxes SET name = 'Mailbox 1' WHERE name IS NULL;
ALTER TABLE mailboxes ALTER COLUMN name SET NOT NULL;
ALTER TABLE mailboxes ALTER COLUMN name SET DEFAULT 'Mailbox';

ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS is_primary boolean;
UPDATE mailboxes SET is_primary = true WHERE is_primary IS NULL;
ALTER TABLE mailboxes ALTER COLUMN is_primary SET NOT NULL;
ALTER TABLE mailboxes ALTER COLUMN is_primary SET DEFAULT false;

ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS smtp_service_id text;
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS last_test_at    timestamptz;
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS last_test_ok    boolean;
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS last_test_error text NOT NULL DEFAULT '';

-- Move the primary key from workspace_id (MH-133) to id (MH-555), if it has
-- not moved already (a fresh database's schema.sql already put it on id, so
-- this whole block is a no-op there — the search below finds nothing to move).
DO $$
DECLARE old_pk text;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'mailboxes'::regclass AND c.contype = 'p' AND a.attname = 'id'
    ) THEN
        SELECT conname INTO old_pk FROM pg_constraint WHERE conrelid = 'mailboxes'::regclass AND contype = 'p';
        IF old_pk IS NOT NULL THEN
            EXECUTE format('ALTER TABLE mailboxes DROP CONSTRAINT %I', old_pk);
        END IF;
        ALTER TABLE mailboxes ADD PRIMARY KEY (id);
    END IF;
END $$;

-- The composite keys every in-workspace foreign key here points at
-- (internal/db/schema/crm.sql explains why: a single-column FK could name
-- another workspace's row). Plain unique INDEXES, not ADD CONSTRAINT UNIQUE —
-- a foreign key accepts either, and CREATE UNIQUE INDEX has an IF NOT EXISTS.
-- Named to match exactly what Postgres would auto-name the table-level UNIQUE
-- constraints in schema/mailbox.sql, so a fresh database (which gets those
-- constraints straight from CREATE TABLE) finds these already satisfied
-- rather than creating a redundant second index.
CREATE UNIQUE INDEX IF NOT EXISTS mailboxes_workspace_id_id_key ON mailboxes (workspace_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS mailboxes_workspace_id_name_key ON mailboxes (workspace_id, name);

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
-- the mailbox that was just upgraded in place from the workspace's MH-133 row
-- — "Mailbox 1", exactly (the UPDATE ... WHERE name IS NULL above gave it
-- that name whether the row was upgraded today or ten releases ago).
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
