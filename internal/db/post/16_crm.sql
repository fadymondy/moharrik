-- CRM: the contact columns, same-tenant foreign keys and indexes (after every schema fragment).

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id    text;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS phone         text   NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS title         text   NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id text;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS tags          text[] NOT NULL DEFAULT '{}';

-- Composite target for the same-tenant FKs.
CREATE UNIQUE INDEX IF NOT EXISTS contacts_workspace_id_uniq ON contacts (workspace_id, id);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'companies_workspace_fk') THEN
        ALTER TABLE companies ADD CONSTRAINT companies_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'companies_owner_fk') THEN
        ALTER TABLE companies ADD CONSTRAINT companies_owner_fk
            FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_company_fk') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_company_fk
            FOREIGN KEY (workspace_id, company_id) REFERENCES companies (workspace_id, id) ON DELETE SET NULL (company_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_owner_fk') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_owner_fk
            FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pipeline_stages_workspace_fk') THEN
        ALTER TABLE pipeline_stages ADD CONSTRAINT pipeline_stages_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deals_workspace_fk') THEN
        ALTER TABLE deals ADD CONSTRAINT deals_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- A stage holding deals cannot be deleted; the API asks for them to be moved first.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deals_stage_fk') THEN
        ALTER TABLE deals ADD CONSTRAINT deals_stage_fk
            FOREIGN KEY (workspace_id, stage_id) REFERENCES pipeline_stages (workspace_id, id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deals_contact_fk') THEN
        ALTER TABLE deals ADD CONSTRAINT deals_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE SET NULL (contact_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deals_company_fk') THEN
        ALTER TABLE deals ADD CONSTRAINT deals_company_fk
            FOREIGN KEY (workspace_id, company_id) REFERENCES companies (workspace_id, id) ON DELETE SET NULL (company_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deals_owner_fk') THEN
        ALTER TABLE deals ADD CONSTRAINT deals_owner_fk
            FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'activities_workspace_fk') THEN
        ALTER TABLE activities ADD CONSTRAINT activities_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'activities_contact_fk') THEN
        ALTER TABLE activities ADD CONSTRAINT activities_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'activities_company_fk') THEN
        ALTER TABLE activities ADD CONSTRAINT activities_company_fk
            FOREIGN KEY (workspace_id, company_id) REFERENCES companies (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'activities_deal_fk') THEN
        ALTER TABLE activities ADD CONSTRAINT activities_deal_fk
            FOREIGN KEY (workspace_id, deal_id) REFERENCES deals (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

-- One company per domain per workspace (blank domains may repeat).
CREATE UNIQUE INDEX IF NOT EXISTS companies_domain_uniq ON companies (workspace_id, lower(domain)) WHERE domain <> '';
CREATE INDEX IF NOT EXISTS companies_workspace_idx ON companies (workspace_id, lower(name));
CREATE INDEX IF NOT EXISTS contacts_company_idx ON contacts (workspace_id, company_id) WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS pipeline_stages_workspace_idx ON pipeline_stages (workspace_id, position);
CREATE INDEX IF NOT EXISTS deals_stage_idx ON deals (workspace_id, stage_id, position);
CREATE INDEX IF NOT EXISTS deals_contact_idx ON deals (workspace_id, contact_id) WHERE contact_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS deals_company_idx ON deals (workspace_id, company_id) WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activities_contact_idx ON activities (workspace_id, contact_id, created_at DESC) WHERE contact_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activities_company_idx ON activities (workspace_id, company_id, created_at DESC) WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activities_deal_idx ON activities (workspace_id, deal_id, created_at DESC) WHERE deal_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS activities_open_tasks_idx ON activities (workspace_id, due_at) WHERE kind = 'task' AND done_at IS NULL;
