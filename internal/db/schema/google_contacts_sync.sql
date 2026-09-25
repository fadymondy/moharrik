-- MH-585: google_contacts_sync's bare table definition lives here, not in
-- internal/db/post/173_google_contacts.sql, for the same reason
-- vcard_imports.sql (MH-584) does: internal/db/migrate.Run applies
-- schema/*.sql, THEN grants the app role DML on every TenantTables entry,
-- THEN applies post/*.sql. A brand-new table added straight to TenantTables
-- must already exist by the time the grant runs.
--
-- Everything else about this table (FK, RLS policy) stays in
-- internal/db/post/173_google_contacts.sql.

CREATE TABLE IF NOT EXISTS google_contacts_sync (
    workspace_id    text        NOT NULL,
    connection_id   text        NOT NULL,
    sync_token      text        NOT NULL DEFAULT '',
    last_synced_at  timestamptz,
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, connection_id)
);
