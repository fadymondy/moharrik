-- The chat widget asks who the visitor is before the first message: a name,
-- and an email or a phone number (with its country code), so every chat can
-- be followed up by email or WhatsApp. Per widget, on by default — existing
-- widgets start asking too. A widget that wants anonymous chat turns it off.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get the column.
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS require_contact boolean NOT NULL DEFAULT true;
