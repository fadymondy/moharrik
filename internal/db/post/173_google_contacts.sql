-- MH-585: Google Contacts import/sync (People API OAuth + Google CSV),
-- feeding the same preview -> commit flow MH-584 built for vCard
-- (internal/vcard.PreviewCards/CommitCards; internal/google/contacts.go is
-- the People-API-shaped caller). Two additions:
--
--   1. contacts.google_deleted_at: a person Google reports as deleted on an
--      incremental (syncToken) sync is FLAGGED, never hard-deleted — the
--      card was explicit about this. NULL means "not deleted at Google" (or
--      never imported from Google at all); a contact this flags is still an
--      ordinary contact everywhere else in the app.
--
--   2. google_contacts_sync: one row per (workspace, google_connections
--      row), holding the syncToken people.connections.list handed back on
--      the last page of the most recent sync, plus bookkeeping for the
--      "last synced" / "sync failed" the UI shows. A 410 EXPIRED_SYNC_TOKEN
--      clears sync_token (see internal/google/contacts.go's Sync) and the
--      next call is a full resync, exactly like the first ever sync.
--
-- Idempotent and safe on production data: ADD COLUMN IF NOT EXISTS, CREATE
-- TABLE IF NOT EXISTS, constraints/policies guarded, matching the idiom
-- every other post/*.sql in this directory already uses.

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS google_deleted_at timestamptz;

-- google_oauth_states.product's CHECK (schema/google.sql) predates this card
-- and only knew analytics/search_console/youtube; a database migrated before
-- this file never gets that CHECK re-run (schema/*.sql only applies to a
-- brand-new database), so it is widened here too, or /api/w/google/connect
-- with product=contacts 500s on an existing database.
ALTER TABLE google_oauth_states DROP CONSTRAINT IF EXISTS google_oauth_states_product_check;
ALTER TABLE google_oauth_states ADD CONSTRAINT google_oauth_states_product_check
    CHECK (product IN ('analytics', 'search_console', 'youtube', 'contacts'));

-- Bare CREATE TABLE is in internal/db/schema/google_contacts_sync.sql (see
-- that file's header for why: TenantTables grants DML before post/*.sql runs).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'google_contacts_sync_connection_fk') THEN
        ALTER TABLE google_contacts_sync ADD CONSTRAINT google_contacts_sync_connection_fk
            FOREIGN KEY (workspace_id, connection_id) REFERENCES google_connections (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

ALTER TABLE google_contacts_sync ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_contacts_sync FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON google_contacts_sync;
CREATE POLICY tenant_isolation ON google_contacts_sync
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
