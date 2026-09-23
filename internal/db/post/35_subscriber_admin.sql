-- Subscriber admin (MH-408): who took somebody off a list, and an index that
-- lets the contacts page walk every contact instead of stopping at a cap.
--
-- Numbered 35 rather than 33 because MH-378 and MH-439 were open when this was
-- written and may take 33/34; nothing here depends on either of them.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get the column.

-- The member who unsubscribed this address from this one list, from the
-- workspace rather than through the link in an email. NULL means the person
-- did it themselves (or it predates this column). It is a plain user id, not a
-- foreign key: the record of who did it must outlive the member leaving.
--
-- An admin unsubscribe is otherwise identical to a self-unsubscribe — same
-- status, same unsubscribed_at — so a later re-add goes back through consent
-- exactly as it would after the person clicked the link (MH-318).
ALTER TABLE list_subscriptions ADD COLUMN IF NOT EXISTS unsubscribed_by text;

-- Keyset paging: the contacts list orders by (created_at, id) newest first and
-- resumes after the last row it returned. id is the tie-break, which matters
-- more than it looks: an import writes a whole batch in one transaction, so
-- every row in it has the SAME created_at, and paging on the timestamp alone
-- would skip or repeat them.
CREATE INDEX IF NOT EXISTS contacts_keyset_idx ON contacts (workspace_id, created_at DESC, id DESC);
