-- MH-584: vcard_imports' bare table definition lives here, not in
-- internal/db/post/167_vcard_contacts.sql, for the same reason
-- contact_identities.sql (MH-583) does: internal/db/migrate.Run applies
-- schema/*.sql, THEN grants the app role DML on every TenantTables entry,
-- THEN applies post/*.sql. A brand-new table added straight to TenantTables
-- must already exist by the time the grant runs.
--
-- Everything else about this table (indexes, RLS policy) stays in
-- internal/db/post/167_vcard_contacts.sql.

CREATE TABLE IF NOT EXISTS vcard_imports (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    file_hash     text        NOT NULL,
    card_count    bigint      NOT NULL DEFAULT 0,
    created_count bigint      NOT NULL DEFAULT 0,
    merged_count  bigint      NOT NULL DEFAULT 0,
    invalid_count bigint      NOT NULL DEFAULT 0,
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);
