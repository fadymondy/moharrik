-- Email marketing: foreign keys and indexes (after every schema fragment).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mail_lists_workspace_fk') THEN
        ALTER TABLE mail_lists ADD CONSTRAINT mail_lists_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_workspace_fk') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'list_subscriptions_workspace_fk') THEN
        ALTER TABLE list_subscriptions ADD CONSTRAINT list_subscriptions_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'list_subscriptions_list_fk') THEN
        ALTER TABLE list_subscriptions ADD CONSTRAINT list_subscriptions_list_fk
            FOREIGN KEY (list_id) REFERENCES mail_lists (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'list_subscriptions_contact_fk') THEN
        ALTER TABLE list_subscriptions ADD CONSTRAINT list_subscriptions_contact_fk
            FOREIGN KEY (contact_id) REFERENCES contacts (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'campaigns_workspace_fk') THEN
        ALTER TABLE campaigns ADD CONSTRAINT campaigns_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'campaigns_list_fk') THEN
        ALTER TABLE campaigns ADD CONSTRAINT campaigns_list_fk
            FOREIGN KEY (list_id) REFERENCES mail_lists (id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'smtp_settings_workspace_fk') THEN
        ALTER TABLE smtp_settings ADD CONSTRAINT smtp_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS contacts_workspace_idx ON contacts (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS list_subscriptions_list_idx ON list_subscriptions (workspace_id, list_id, status);
CREATE INDEX IF NOT EXISTS list_subscriptions_contact_idx ON list_subscriptions (workspace_id, contact_id);
CREATE INDEX IF NOT EXISTS campaigns_workspace_idx ON campaigns (workspace_id, created_at DESC);
