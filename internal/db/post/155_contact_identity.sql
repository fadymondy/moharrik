-- MH-561: a contact no longer needs an email. It needs at least one of
-- email / phone (E.164, normalised the way MH-320 already compares phones)
-- / handle (a messaging handle/username on a channel with neither — Telegram,
-- Slack, Discord, a WhatsApp group participant with no saved number). All
-- three are the empty-string-means-absent convention 16_crm.sql already uses
-- for phone, not NULL, so every predicate below stays a plain `<> ''`.
--
-- Safe on existing data: email was already unique per workspace (so it can
-- never collide), but phone and handle were never unique before this ships.
-- Before adding their unique indexes we blank out any duplicate that is not
-- the oldest contact holding that value, so a workspace that already has two
-- contacts sharing a phone (a typo, an import) keeps both contacts and loses
-- only the newer duplicate's copy of the value — never a row.

ALTER TABLE contacts ALTER COLUMN email SET DEFAULT '';
-- email was NOT NULL before; make sure nothing null slipped in some other way.
UPDATE contacts SET email = '' WHERE email IS NULL;
ALTER TABLE contacts ALTER COLUMN email SET NOT NULL;

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS handle text NOT NULL DEFAULT '';

-- The old bare UNIQUE(workspace_id, email) (from mail_marketing.sql's CREATE
-- TABLE) does not allow the empty string to repeat, and email is about to be
-- blank on many rows. Replace it with the same partial-unique shape phone and
-- handle get below.
ALTER TABLE contacts DROP CONSTRAINT IF EXISTS contacts_workspace_id_email_key;

-- Dedupe existing phone/handle collisions per workspace before the unique
-- indexes below would refuse to build.
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY workspace_id, phone ORDER BY created_at, id) AS rn
    FROM contacts WHERE phone <> ''
)
UPDATE contacts SET phone = '' WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY workspace_id, lower(handle) ORDER BY created_at, id) AS rn
    FROM contacts WHERE handle <> ''
)
UPDATE contacts SET handle = '' WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

-- Lowercase existing handles so the unique index and every lookup agree on
-- case-insensitivity without needing an expression index.
UPDATE contacts SET handle = lower(handle) WHERE handle <> lower(handle);

CREATE UNIQUE INDEX IF NOT EXISTS contacts_workspace_email_uniq ON contacts (workspace_id, email) WHERE email <> '';
CREATE UNIQUE INDEX IF NOT EXISTS contacts_workspace_phone_uniq ON contacts (workspace_id, phone) WHERE phone <> '';
CREATE UNIQUE INDEX IF NOT EXISTS contacts_workspace_handle_uniq ON contacts (workspace_id, handle) WHERE handle <> '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_identity_chk') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_identity_chk
            CHECK (email <> '' OR phone <> '' OR handle <> '');
    END IF;
END $$;

-- MH-561: campaigns report how many subscribed contacts they skipped for
-- having no email, alongside sent_count/failed_count.
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS skipped_count bigint NOT NULL DEFAULT 0;
