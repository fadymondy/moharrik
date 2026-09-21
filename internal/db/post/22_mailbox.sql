-- The email channel's foreign key and the two indexes that make threading work.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'mailboxes_workspace_fk') THEN
        ALTER TABLE mailboxes ADD CONSTRAINT mailboxes_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- A redelivery of the same email is the same row. Partial, because chat
-- messages carry no upstream id and would all collide on ''.
CREATE UNIQUE INDEX IF NOT EXISTS messages_external_uniq
    ON messages (workspace_id, external_id) WHERE external_id <> '';

-- "Which message carries this Message-ID" is the first threading question, and
-- it is asked once per inbound email.
CREATE INDEX IF NOT EXISTS messages_external_idx
    ON messages (workspace_id, external_id) WHERE external_id <> '';

-- The fallback threading question: "has this address written to us before".
CREATE INDEX IF NOT EXISTS conversations_email_idx
    ON conversations (workspace_id, lower(customer_email), channel)
    WHERE customer_email <> '';
