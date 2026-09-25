-- MH-584: vCard import/export needs a few contact fields the Google-People
-- parity migration (166_contact_identities.sql) did not add, plus a
-- workspace-level default country so a phone with no country code (an
-- Android/iPhone address book export never qualifies its local numbers)
-- normalises to E.164 the same way internal/marketing.NormalizePhone already
-- expects contacts.phone to be stored.
--
-- ORG maps to contacts.company_id (find-or-create by name, MH-583's existing
-- column); TITLE to the existing title column; ROLE gets its own column since
-- vCard treats it as distinct from TITLE; NOTE folds into the existing
-- biography column (Google People API's own home for free text); PHOTO
-- (BASE64) folds into the existing photo_url/photo_source as a data: URI;
-- CATEGORIES folds into the existing tags column; UID folds into the
-- existing external_ids jsonb map under the "vcard" key. ADR, URL and
-- ANNIVERSARY have nowhere to go yet, so this file adds them.
--
-- Idempotent and safe on production data: ADD COLUMN IF NOT EXISTS,
-- constraints guarded by pg_constraint checks.

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS anniversary date;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS job_role     text  NOT NULL DEFAULT '';

-- One contact can have several addresses (home, work, ...), each
-- {"type":"home","street":"","locality":"","region":"","postal_code":"","country":"","label":""}.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS addresses jsonb NOT NULL DEFAULT '[]'::jsonb;
-- And several URLs (vCard URL can repeat): ["https://example.test", ...].
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS urls jsonb NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_addresses_array_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_addresses_array_ck CHECK (jsonb_typeof(addresses) = 'array');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_urls_array_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_urls_array_ck CHECK (jsonb_typeof(urls) = 'array');
    END IF;
END $$;

-- Workspace default country (ISO 3166-1 alpha-2, e.g. "EG"), used to turn a
-- vCard TEL with no country code into E.164. Empty means "unknown" — a local
-- number then imports with a warning instead of a guessed country code.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS default_country text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'workspaces_default_country_ck') THEN
        ALTER TABLE workspaces ADD CONSTRAINT workspaces_default_country_ck CHECK (default_country ~ '^([A-Z]{2})?$');
    END IF;
END $$;

-- Per-workspace vCard import bookkeeping: one row per commit, keyed on a hash
-- of the file, so a repeat commit of the exact same file is visible in the
-- history (the parser itself is already idempotent via contacts.Upsert; this
-- is bookkeeping for the MCP rate limiter and an audit trail). Bare table in
-- internal/db/schema/vcard_imports.sql (see that file's header).

CREATE INDEX IF NOT EXISTS vcard_imports_workspace_created_idx ON vcard_imports (workspace_id, created_at DESC);

ALTER TABLE vcard_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE vcard_imports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON vcard_imports;
CREATE POLICY tenant_isolation ON vcard_imports
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
