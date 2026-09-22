-- Subscribe forms: tenant foreign keys (MH-318).

-- mail_lists never carried UNIQUE (workspace_id, id), unlike the other tenant
-- tables, so nothing could point at it with a composite key. Added here rather
-- than in the schema file because that CREATE is IF NOT EXISTS and an existing
-- database would never get it.
CREATE UNIQUE INDEX IF NOT EXISTS mail_lists_workspace_id_uniq ON mail_lists (workspace_id, id);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscribe_forms_workspace_fk') THEN
        ALTER TABLE subscribe_forms ADD CONSTRAINT subscribe_forms_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;

    -- Composite, for the reason internal/db/schema/crm.sql gives: a foreign-key
    -- check runs as the table OWNER and ignores RLS, so a single-column FK would
    -- let one workspace point a form at another workspace's mailing list — and
    -- for a PUBLIC endpoint that means a stranger's site writing into it.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscribe_forms_list_fk') THEN
        ALTER TABLE subscribe_forms ADD CONSTRAINT subscribe_forms_list_fk
            FOREIGN KEY (workspace_id, list_id) REFERENCES mail_lists (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;
