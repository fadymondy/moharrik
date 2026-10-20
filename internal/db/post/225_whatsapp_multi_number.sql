-- Several WhatsApp numbers per workspace.
--
-- whatsapp_links was one row per workspace (workspace_id the primary key).
-- It becomes one row per linked number: a row id, a label, a default-sender
-- flag and a daily send cap. The existing row keeps its instance, hook id and
-- token untouched, so the number linked today keeps its Evolution session and
-- its webhook URL and never notices the change; it becomes the default.
--
-- Every statement is idempotent: this file runs on every migrate. The old
-- build is still serving while the release command runs it and keeps reading
-- and filing messages as before (it only ever sees one row per workspace until
-- a second number is linked after the deploy). Two of its writes need the old
-- unique keys and fail until the new build takes over, a matter of seconds:
-- starting a brand-new WhatsApp thread with send_whatsapp (ON CONFLICT
-- (workspace_id, customer_phone)) and linking a first number (ON CONFLICT
-- (workspace_id)). Both are safe to retry.

-- 1. The link rows.
ALTER TABLE whatsapp_links ADD COLUMN IF NOT EXISTS id         text    NOT NULL DEFAULT gen_random_uuid()::text;
ALTER TABLE whatsapp_links ADD COLUMN IF NOT EXISTS label      text    NOT NULL DEFAULT '';
ALTER TABLE whatsapp_links ADD COLUMN IF NOT EXISTS is_default boolean NOT NULL DEFAULT false;
ALTER TABLE whatsapp_links ADD COLUMN IF NOT EXISTS daily_cap  integer NOT NULL DEFAULT 0;

DO $$
DECLARE
    pk_cols text;
BEGIN
    SELECT string_agg(a.attname, ',') INTO pk_cols
    FROM pg_constraint c
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
    WHERE c.conrelid = 'whatsapp_links'::regclass AND c.contype = 'p';
    IF pk_cols IS DISTINCT FROM 'id' THEN
        IF pk_cols IS NOT NULL THEN
            EXECUTE (SELECT format('ALTER TABLE whatsapp_links DROP CONSTRAINT %I', conname)
                     FROM pg_constraint WHERE conrelid = 'whatsapp_links'::regclass AND contype = 'p');
        END IF;
        ALTER TABLE whatsapp_links ADD CONSTRAINT whatsapp_links_pkey PRIMARY KEY (id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_links_daily_cap_ck') THEN
        ALTER TABLE whatsapp_links ADD CONSTRAINT whatsapp_links_daily_cap_ck CHECK (daily_cap >= 0);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS whatsapp_links_workspace_idx ON whatsapp_links (workspace_id, created_at);
-- At most one default per workspace.
CREATE UNIQUE INDEX IF NOT EXISTS whatsapp_links_default_uniq
    ON whatsapp_links (workspace_id) WHERE is_default;

-- A workspace with numbers but no default (the existing single link, or the
-- default was unlinked by an older build) gets its oldest number as default.
UPDATE whatsapp_links l SET is_default = true
WHERE l.id = (SELECT d.id FROM whatsapp_links d WHERE d.workspace_id = l.workspace_id
              ORDER BY d.created_at, d.id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM whatsapp_links x WHERE x.workspace_id = l.workspace_id AND x.is_default);

-- 2. Which number a WhatsApp thread is on. '' = not known (a thread from
-- before this migration, or filed by the old build during the deploy): the
-- next message on it adopts it for the number it arrived on, and a reply
-- from it goes from the default number.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS whatsapp_link_id text NOT NULL DEFAULT '';

-- Existing threads are on the workspace's one number.
UPDATE conversations c SET whatsapp_link_id = l.id
FROM whatsapp_links l
WHERE c.channel = 'whatsapp' AND c.whatsapp_link_id = ''
  AND l.workspace_id = c.workspace_id AND l.is_default
  AND (SELECT count(*) FROM whatsapp_links n WHERE n.workspace_id = c.workspace_id) = 1;

-- One thread per customer per linked number (was: per workspace). The new
-- index first, then the old one goes: there is never a moment without one.
CREATE UNIQUE INDEX IF NOT EXISTS conversations_whatsapp_phone_uniq
    ON conversations (workspace_id, whatsapp_link_id, customer_phone)
    WHERE customer_phone <> '' AND channel = 'whatsapp';
DROP INDEX IF EXISTS conversations_phone_uniq;
CREATE INDEX IF NOT EXISTS conversations_whatsapp_link_idx
    ON conversations (workspace_id, whatsapp_link_id) WHERE channel = 'whatsapp';

-- 3. Which number sent each message, for per-number counts, caps and pacing.
ALTER TABLE whatsapp_sends ADD COLUMN IF NOT EXISTS link_id text NOT NULL DEFAULT '';
UPDATE whatsapp_sends s SET link_id = l.id
FROM whatsapp_links l
WHERE s.link_id = '' AND l.workspace_id = s.workspace_id AND l.is_default
  AND (SELECT count(*) FROM whatsapp_links n WHERE n.workspace_id = s.workspace_id) = 1;
CREATE INDEX IF NOT EXISTS whatsapp_sends_link_created_idx
    ON whatsapp_sends (workspace_id, link_id, created_at DESC);

-- 4. The number a WhatsApp campaign goes from. '' = the default number.
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS whatsapp_link_id text NOT NULL DEFAULT '';
