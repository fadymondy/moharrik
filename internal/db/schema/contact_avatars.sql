-- MH-602: contact_avatars' bare table definition lives here, not in
-- internal/db/post/171_contact_avatars.sql, for the same reason
-- vcard_imports.sql (MH-584) does: internal/db/migrate.Run applies
-- schema/*.sql, THEN grants the app role DML on every TenantTables entry,
-- THEN applies post/*.sql. A brand-new table added straight to TenantTables
-- must already exist by the time the grant runs.
--
-- Everything else about this table (constraints, RLS policy) stays in
-- internal/db/post/171_contact_avatars.sql.

CREATE TABLE IF NOT EXISTS contact_avatars (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text        NOT NULL,
    content_type text        NOT NULL DEFAULT 'application/octet-stream',
    size_bytes   bigint      NOT NULL DEFAULT 0,
    data         bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, contact_id)
);
