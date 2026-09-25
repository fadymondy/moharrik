-- MH-586: these three tables' bare definitions live here, not in
-- internal/db/post/174_contact_context.sql, for the same reason
-- contact_avatars.sql (MH-602) and vcard_imports.sql (MH-584) do:
-- internal/db/migrate.Run applies schema/*.sql, THEN grants the app role DML
-- on every TenantTables entry, THEN applies post/*.sql. A brand-new table
-- added straight to TenantTables must already exist by the time the grant
-- runs.
--
-- Everything else about these tables (columns added later, indexes,
-- constraints, RLS policies) stays in internal/db/post/174_contact_context.sql.

CREATE TABLE IF NOT EXISTS contact_context_imports (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE IF NOT EXISTS contact_context_notes (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text        NOT NULL,
    content      text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE IF NOT EXISTS contact_context_reports (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text        NOT NULL,
    markdown     text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);
