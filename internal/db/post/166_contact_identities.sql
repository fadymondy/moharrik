-- MH-583: a contact matches the Google People API Person shape — several
-- phones/emails/handles per person, free-form key/value meta, and one account
-- per messaging platform (email, WhatsApp, Telegram, Slack, Discord,
-- Messenger, Instagram, LinkedIn, X, the chat widget) so a message from any
-- of them lands on the same contact's one thread.
--
-- contact_identities is that multi-channel table. It doubles as the
-- phone/email/handle child rows the People-schema calls for (channel
-- 'email'/'phone'/'handle') AND the "link an account on every platform" list
-- the coordinator asked for on this card — one mechanism, not two. A contact
-- can carry any number of rows per channel (two WhatsApp numbers = two rows
-- with channel = 'whatsapp'); at most one is_primary per (contact, channel).
--
-- contacts.email/phone/handle stay as the denormalised PRIMARY value (kept in
-- sync by internal/contacts and internal/marketing on write) so existing
-- queries, campaigns and RLS predicates that read those columns directly keep
-- working unchanged.
--
-- Idempotent and safe on production data: CREATE ... IF NOT EXISTS, ALTER ...
-- ADD COLUMN IF NOT EXISTS, constraints guarded by pg_constraint checks, and
-- the backfill only INSERTs rows that are not already there.

-- The bare CREATE TABLE moved to internal/db/schema/contact_identities.sql:
-- it must exist before internal/db/migrate grants the app role DML on it,
-- which happens before this file (or any post/*.sql) ever runs. See that
-- file's header comment for why.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_identities_contact_fk') THEN
        ALTER TABLE contact_identities ADD CONSTRAINT contact_identities_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_identities_channel_ck') THEN
        ALTER TABLE contact_identities ADD CONSTRAINT contact_identities_channel_ck
            CHECK (channel IN ('email', 'phone', 'whatsapp', 'handle', 'telegram', 'slack', 'discord',
                                'messenger', 'instagram', 'linkedin', 'x', 'widget', 'sip', 'other'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_identities_external_id_ck') THEN
        ALTER TABLE contact_identities ADD CONSTRAINT contact_identities_external_id_ck
            CHECK (external_id <> '');
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS contact_identities_contact_idx ON contact_identities (workspace_id, contact_id);

-- One workspace never links the same account on the same channel to two
-- different contacts.
CREATE UNIQUE INDEX IF NOT EXISTS contact_identities_workspace_channel_uniq
    ON contact_identities (workspace_id, channel, external_id);

-- The WhatsApp/phone lookup every filer runs compares digits only (the same
-- normalisation MH-320/155_contact_identity.sql already use), so index that
-- expression too.
CREATE INDEX IF NOT EXISTS contact_identities_digits_idx
    ON contact_identities (workspace_id, (regexp_replace(external_id, '[^0-9]', '', 'g')))
    WHERE channel IN ('phone', 'whatsapp');

ALTER TABLE contact_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_identities FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_identities;
CREATE POLICY tenant_isolation ON contact_identities
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- ---- Google People API scalar parity (the parts that are 1:1, not repeatable) ----

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS given_name           text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS family_name          text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS middle_name          text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS name_prefix          text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS name_suffix          text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS phonetic_given_name  text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS phonetic_family_name text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS nickname             text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS birthday             date;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS biography            text NOT NULL DEFAULT '';

-- Photo: nullable, fetched separately (MH-588 fetches/stores the bytes; this
-- card only adds room for it). photo_attachment_id is an uploaded file (the
-- app's own attachments table); photo_url/photo_source/photo_fetched_at are
-- for a photo pulled from an import (Google/vCard) instead of uploaded here.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS photo_attachment_id text;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS photo_url           text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS photo_source        text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS photo_fetched_at    timestamptz;

-- userDefined[] (free key/value meta, Fady's own words: "add a meta value
-- that i can add key and value of each contact") and externalIds{} (the
-- source id per import provider, so a re-import updates instead of
-- duplicating — google resourceName+etag, vcard UID, csv row hash, ...).
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS meta         jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS external_ids jsonb NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_meta_object_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_meta_object_ck CHECK (jsonb_typeof(meta) = 'object');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_external_ids_object_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_external_ids_object_ck CHECK (jsonb_typeof(external_ids) = 'object');
    END IF;
END $$;

-- ---- backfill: the current email/phone/handle become the primary child rows ----
--
-- ON CONFLICT DO NOTHING against contact_identities_workspace_channel_uniq
-- makes this safe to re-run: a row already backfilled (or already linked by
-- hand since) is left alone.

INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, is_primary)
SELECT workspace_id, id, 'email', lower(email), true FROM contacts WHERE email <> ''
ON CONFLICT (workspace_id, channel, external_id) DO NOTHING;

INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, is_primary)
SELECT workspace_id, id, 'phone', phone, true FROM contacts WHERE phone <> ''
ON CONFLICT (workspace_id, channel, external_id) DO NOTHING;

INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, is_primary)
SELECT workspace_id, id, 'handle', lower(handle), true FROM contacts WHERE handle <> ''
ON CONFLICT (workspace_id, channel, external_id) DO NOTHING;
