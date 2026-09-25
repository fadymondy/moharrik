-- MH-583: contact_identities' bare table definition lives here, not in
-- internal/db/post/166_contact_identities.sql, because internal/db/migrate.Run
-- applies schema/*.sql, THEN grants the app role DML on every TenantTables
-- entry, THEN applies post/*.sql. A brand-new table added straight to
-- TenantTables (as this one is) must already exist by the time the grant
-- runs, or `GRANT ... ON contact_identities ...` fails with "relation
-- contact_identities does not exist" on a fresh database — the grant runs
-- before post/ has ever had a chance to create it.
--
-- Everything else about this table (foreign key, CHECK constraints, indexes,
-- RLS policy, and the columns' comments) stays in
-- internal/db/post/166_contact_identities.sql, unchanged, because none of
-- that needs to run before the app-role grant — only the table's existence
-- does. This file is deliberately minimal so 166's comments stay the single
-- source of truth for why the table looks the way it does.

CREATE TABLE IF NOT EXISTS contact_identities (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text        NOT NULL,
    channel      text        NOT NULL,
    external_id  text        NOT NULL DEFAULT '',
    label        text        NOT NULL DEFAULT '',
    is_primary   boolean     NOT NULL DEFAULT false,
    position     int         NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
