-- Webhook signing per kind, and replay protection for generic sources (MH-381).
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get these columns. A fresh
-- database already has them from internal/db/schema/notifications.sql, and
-- then every statement below is a no-op.

-- Generic deliveries are now signed over timestamp + "." + body and refused
-- outside a 5-minute window. Before this, every source accepted a signature
-- over the body alone, which a captured request satisfies for ever.
--
-- Sources that already exist have senders configured for that old scheme, and
-- switching them on deploy would break live integrations without a word. So:
--
--   * the column is ADDED with DEFAULT true, which marks every existing row
--     legacy (Postgres fills existing rows from the ADD COLUMN's default), and
--   * the default is then set to false, so every source created from now on
--     uses the timestamped scheme only.
--
-- The sources page flags legacy sources as replayable, shows when one last
-- received a legacy-signed delivery, and lets the owner switch it off. It
-- cannot be switched back on through the API.
--
-- Order matters and is idempotent: on a re-run the ADD is skipped (the column
-- exists), so existing rows are never re-marked, and SET DEFAULT is repeated
-- harmlessly.
ALTER TABLE webhook_sources ADD COLUMN IF NOT EXISTS legacy_signing boolean NOT NULL DEFAULT true;
ALTER TABLE webhook_sources ALTER COLUMN legacy_signing SET DEFAULT false;

-- The last delivery that verified only by the legacy scheme: whether switching
-- a source off would break a sender still using it.
ALTER TABLE webhook_sources ADD COLUMN IF NOT EXISTS last_legacy_at timestamptz;

-- kind has carried this CHECK since the table was created (schema/notifications.sql);
-- restated here by name so a database that somehow lacks it gets it.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'webhook_sources'::regclass AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%kind%generic%github%sentry%'
    ) THEN
        ALTER TABLE webhook_sources ADD CONSTRAINT webhook_sources_kind_ck
            CHECK (kind IN ('generic', 'github', 'sentry'));
    END IF;
END $$;
