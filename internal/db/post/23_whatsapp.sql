-- The WhatsApp channel's foreign key, and the phone a conversation belongs to.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_links_workspace_fk') THEN
        ALTER TABLE whatsapp_links ADD CONSTRAINT whatsapp_links_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- A WhatsApp conversation is identified by the customer's number. The column
-- is added here rather than in the conversations fragment because that CREATE
-- is IF NOT EXISTS, so an existing database would never get a new column.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS customer_phone text NOT NULL DEFAULT '';

-- One thread per number per workspace: a customer writing again next week
-- continues the conversation they already had.
CREATE UNIQUE INDEX IF NOT EXISTS conversations_phone_uniq
    ON conversations (workspace_id, customer_phone)
    WHERE customer_phone <> '' AND channel = 'whatsapp';
