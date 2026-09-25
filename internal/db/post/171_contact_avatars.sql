-- MH-602: "if the number connected to a contact and contact have a name you
-- must showup the name on the chat and for avatar alwase showup avatar
-- placeholder and try to get avatar from chats or provider if it's exsits or
-- add avatar upload on the contact" (Fady, verbatim).
--
-- Three avatar sources, in priority order: an uploaded photo (this file),
-- a provider photo fetched from WhatsApp via Evolution and cached (166_
-- contact_identities.sql already reserved contacts.photo_url/photo_source/
-- photo_fetched_at for exactly this; this file only adds photo_fetch_status
-- so a FAILED fetch is cached too and does not retry every request), and
-- initials as the last resort (rendered client-side, needs no column).
--
-- contact_avatars holds the UPLOADED bytes, the same "small binary in
-- Postgres, not a separate object store" call internal/db/schema/inbox.sql
-- already made for message_attachments — a contact photo is smaller still
-- (capped at 2MB by internal/crm/avatar.go) and this product has no other use
-- for an object store yet. One row per contact (UNIQUE): a re-upload
-- replaces it, which is what "replace" in the upload control means.
--
-- conversations gets its own photo_url/photo_source/photo_fetched_at/
-- photo_fetch_status so an UNLINKED WhatsApp conversation (no contact_id yet)
-- can still cache the customer's WhatsApp photo instead of hotlinking
-- Evolution's CDN URL (which expires) or re-fetching it on every request
-- (which would hammer Evolution).
--
-- Idempotent and safe on production data: CREATE ... IF NOT EXISTS, ALTER ...
-- ADD COLUMN IF NOT EXISTS, constraints guarded by pg_constraint checks.
--
-- contact_avatars' bare CREATE TABLE lives in
-- internal/db/schema/contact_avatars.sql instead of here, so it already
-- exists by the time internal/db/migrate.Run grants the app role DML on it
-- (contact_avatars is in TenantTables) — see that file's own header.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_avatars_contact_fk') THEN
        ALTER TABLE contact_avatars ADD CONSTRAINT contact_avatars_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_avatars_content_type_ck') THEN
        ALTER TABLE contact_avatars ADD CONSTRAINT contact_avatars_content_type_ck
            CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp'));
    END IF;
END $$;

ALTER TABLE contact_avatars ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_avatars FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_avatars;
CREATE POLICY tenant_isolation ON contact_avatars
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- ---- provider-fetch caching --------------------------------------------------

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS photo_fetch_status text NOT NULL DEFAULT '';

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS photo_url         text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS photo_source      text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS photo_fetched_at  timestamptz;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS photo_fetch_status text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_photo_fetch_status_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_photo_fetch_status_ck
            CHECK (photo_fetch_status IN ('', 'ok', 'failed'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_photo_fetch_status_ck') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_photo_fetch_status_ck
            CHECK (photo_fetch_status IN ('', 'ok', 'failed'));
    END IF;
END $$;
