-- MH-586: contact context — a WhatsApp export import, a searchable per-contact
-- index (notes/messages/deals/meta/uploads), and generated reports.
--
-- Migration number: 174. MH-585 (built in parallel) claims 173; this file
-- deliberately starts at 174 so the two never collide. Idempotent throughout
-- (IF NOT EXISTS / guarded CREATE POLICY / CREATE OR REPLACE) since every file
-- in internal/db/post reruns on every `go run ./cmd/migrate` and before each
-- DB-backed test (internal/testdb).

-- ---------------------------------------------------------------------------
-- 1. messages: tag imported rows so they can be found and removed by import,
--    and so the automation-rule trigger (126_automation_rule_triggers.sql)
--    can refuse to fire for them explicitly, not just by their old timestamp.
-- ---------------------------------------------------------------------------
ALTER TABLE messages ADD COLUMN IF NOT EXISTS source     text NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS import_id  text NOT NULL DEFAULT '';
-- Dedupe key: normalized-text hash + sender direction + timestamp truncated
-- to the minute (WhatsApp exports carry no seconds). Empty for every message
-- that is not an import, so the partial unique index below never constrains
-- the chat widget/email/live-WhatsApp paths, which already dedupe on
-- external_id.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS dedupe_key text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS messages_import_dedupe_idx
    ON messages (workspace_id, conversation_id, dedupe_key) WHERE dedupe_key <> '';

CREATE INDEX IF NOT EXISTS messages_import_id_idx
    ON messages (workspace_id, import_id) WHERE import_id <> '';

-- Explicit automation bypass (MH-586): an imported row never raises
-- message.received/conversation.created, regardless of its created_at. The
-- old age check stays as a second, independent line of defense for every
-- other channel.
CREATE OR REPLACE FUNCTION rule_on_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    data  jsonb;
    first boolean;
BEGIN
    IF NEW.direction <> 'in' OR NEW.source = 'whatsapp_export' OR NEW.import_id <> ''
       OR NEW.created_at < now() - interval '1 day' THEN
        RETURN NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM automation_rules WHERE workspace_id = NEW.workspace_id AND enabled
                   AND trigger IN ('message.received', 'conversation.created')) THEN
        RETURN NULL;
    END IF;
    SELECT jsonb_build_object('conversation_id', c.id, 'message_id', NEW.id, 'channel', c.channel,
            'text', rule_clip(NEW.body, 1000), 'customer_name', c.customer_name, 'customer_email', c.customer_email,
            'customer_phone', c.customer_phone, 'contact_id', c.contact_id, 'assignee_user_id', c.assignee_user_id,
            'subject', c.subject, 'locale', c.locale)
      INTO data
      FROM conversations c WHERE c.workspace_id = NEW.workspace_id AND c.id = NEW.conversation_id;
    first := NOT EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = NEW.workspace_id
                         AND m.conversation_id = NEW.conversation_id AND m.id <> NEW.id);
    IF first THEN
        PERFORM rule_event(NEW.workspace_id, 'conversation.created', NEW.conversation_id, data);
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'message.received', NEW.conversation_id, data);
    RETURN NULL;
END $$;

-- ---------------------------------------------------------------------------
-- 2. contact_context_imports: one row per committed WhatsApp-export import, so
--    "remove this import" can find exactly its messages/attachments/chunks.
--    The bare table (id/workspace_id/contact_id/created_at) is created in
--    internal/db/schema/contact_context.sql so it exists before migrate.Run
--    grants the app role DML on it; every other column is added here.
-- ---------------------------------------------------------------------------
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS conversation_id text NOT NULL DEFAULT '';
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS kind            text NOT NULL DEFAULT 'whatsapp_export';
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS filename        text NOT NULL DEFAULT '';
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS contact_sender  text NOT NULL DEFAULT '';
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS message_count   int  NOT NULL DEFAULT 0;
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS media_count     int  NOT NULL DEFAULT 0;
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS skipped_count   int  NOT NULL DEFAULT 0;
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS date_from       timestamptz;
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS date_to         timestamptz;
ALTER TABLE contact_context_imports ADD COLUMN IF NOT EXISTS created_by      text NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_imports_kind_check') THEN
        ALTER TABLE contact_context_imports ADD CONSTRAINT contact_context_imports_kind_check
            CHECK (kind IN ('whatsapp_export'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS contact_context_imports_contact_idx
    ON contact_context_imports (workspace_id, contact_id);

ALTER TABLE contact_context_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_context_imports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_context_imports;
CREATE POLICY tenant_isolation ON contact_context_imports
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- ---------------------------------------------------------------------------
-- 3. contact_context_notes: a contact's free-text context that has no other
--    home — notes/files added over MCP (add_contact_context) or the "Ask
--    about this contact" panel's upload. 'simple' text-search config so
--    Arabic is indexed by exact token, not stemmed as if it were English.
--    The bare table lives in internal/db/schema/contact_context.sql (see the
--    note on contact_context_imports above); everything else is added here.
-- ---------------------------------------------------------------------------
ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS kind       text NOT NULL DEFAULT 'note';
ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS title      text NOT NULL DEFAULT '';
ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS filename   text NOT NULL DEFAULT '';
ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS import_id  text NOT NULL DEFAULT '';
ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS created_by text NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_notes_kind_check') THEN
        ALTER TABLE contact_context_notes ADD CONSTRAINT contact_context_notes_kind_check
            CHECK (kind IN ('note', 'file'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS contact_context_notes_contact_idx
    ON contact_context_notes (workspace_id, contact_id);

ALTER TABLE contact_context_notes ADD COLUMN IF NOT EXISTS tsv tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))) STORED;
CREATE INDEX IF NOT EXISTS contact_context_notes_tsv_idx ON contact_context_notes USING gin (tsv);

ALTER TABLE contact_context_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_context_notes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_context_notes;
CREATE POLICY tenant_isolation ON contact_context_notes
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- A 'simple'-config search column on messages themselves, so retrieval can
-- rank a contact's WhatsApp/chat/email history alongside their notes without
-- a second copy of the text. Generated, so it never drifts from body.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS search_simple tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', coalesce(body, ''))) STORED;
CREATE INDEX IF NOT EXISTS messages_search_simple_idx ON messages USING gin (search_simple);

-- ---------------------------------------------------------------------------
-- 4. contact_context_reports: generated relationship reports, versioned per
--    contact (regenerating adds a row rather than overwriting) so an older
--    report stays retrievable, matching how automation_workflow_versions
--    (internal/db/post/75_automation_workflow_versions.sql) keeps history
--    instead of overwriting in place. The bare table lives in
--    internal/db/schema/contact_context.sql (see the note above).
-- ---------------------------------------------------------------------------
ALTER TABLE contact_context_reports ADD COLUMN IF NOT EXISTS version    int     NOT NULL DEFAULT 1;
ALTER TABLE contact_context_reports ADD COLUMN IF NOT EXISTS provider   text    NOT NULL DEFAULT '';
ALTER TABLE contact_context_reports ADD COLUMN IF NOT EXISTS model      text    NOT NULL DEFAULT '';
ALTER TABLE contact_context_reports ADD COLUMN IF NOT EXISTS no_model   boolean NOT NULL DEFAULT true;
ALTER TABLE contact_context_reports ADD COLUMN IF NOT EXISTS created_by text    NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_reports_version_uq') THEN
        ALTER TABLE contact_context_reports ADD CONSTRAINT contact_context_reports_version_uq
            UNIQUE (workspace_id, contact_id, version);
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS contact_context_reports_contact_idx
    ON contact_context_reports (workspace_id, contact_id, version DESC);

ALTER TABLE contact_context_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_context_reports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_context_reports;
CREATE POLICY tenant_isolation ON contact_context_reports
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_imports_contact_fk') THEN
        ALTER TABLE contact_context_imports ADD CONSTRAINT contact_context_imports_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_notes_contact_fk') THEN
        ALTER TABLE contact_context_notes ADD CONSTRAINT contact_context_notes_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_context_reports_contact_fk') THEN
        ALTER TABLE contact_context_reports ADD CONSTRAINT contact_context_reports_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;
