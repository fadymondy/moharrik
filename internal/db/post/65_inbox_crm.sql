-- The inbox reaches the CRM (MH-379): a conversation links to a contact, and to
-- the deal it opened.
--
-- Everything for this card's conversation columns is in this one file, so
-- renumbering it against another branch is a rename and nothing else. Added by
-- ALTER because the conversations CREATE in internal/db/schema is IF NOT
-- EXISTS, so an existing database would otherwise never get the columns.
-- Every statement is idempotent.

-- ---- MH-379: a conversation links to a contact, and to the deal it opened ----

-- How contact_id came to be set: '' never linked, 'auto' by an exact email or
-- phone match when an email or WhatsApp message was filed, 'manual' by a
-- member (linking, creating a contact, or UNLINKING). An automatic match never
-- overrides 'manual': a member who unlinked a wrong match must not see it come
-- back with the customer's next message.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS contact_link text NOT NULL DEFAULT '';

-- The deal this conversation was turned into, so the thread links to it and a
-- second click does not open a second deal by accident.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS deal_id text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_contact_link_ck') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_contact_link_ck
            CHECK (contact_link IN ('', 'auto', 'manual'));
    END IF;
    -- Composite, for the reason internal/db/schema/crm.sql gives: a single
    -- column key would let one workspace's conversation point at another
    -- workspace's deal. Deleting the deal keeps the conversation.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'conversations_deal_fk') THEN
        ALTER TABLE conversations ADD CONSTRAINT conversations_deal_fk
            FOREIGN KEY (workspace_id, deal_id) REFERENCES deals (workspace_id, id) ON DELETE SET NULL (deal_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS conversations_deal_idx ON conversations (workspace_id, deal_id) WHERE deal_id IS NOT NULL;

-- The automatic match on a WhatsApp number compares digits only (the same
-- normalisation as MH-320's STOP handling, marketing.OnlyDigits), so the index
-- is on the same expression the lookup uses.
CREATE INDEX IF NOT EXISTS contacts_phone_digits_idx
    ON contacts (workspace_id, (regexp_replace(phone, '[^0-9]', '', 'g')))
    WHERE phone <> '';
CREATE INDEX IF NOT EXISTS contacts_email_lower_idx ON contacts (workspace_id, lower(email));

