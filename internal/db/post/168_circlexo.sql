-- CircleXO hub integration: the org link's foreign key to workspaces (see schema/circlexo.sql).
-- A workspace is never deleted by the integration; if an admin deletes one, its link goes too.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'circlexo_org_links_workspace_fk') THEN
        ALTER TABLE circlexo_org_links
            ADD CONSTRAINT circlexo_org_links_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;
