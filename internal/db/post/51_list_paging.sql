-- Keyset paging for the lists that used to stop at a fixed number without
-- saying so (MH-467): campaigns (500), deals (2,000), companies (2,000) and the
-- activity timeline (500). Each index is the list's sort order with id as the
-- tie-break, which is what makes "the row after this one" exact when two rows
-- share a name, a position or a timestamp (an import writes a batch with one
-- created_at; the same lesson as MH-408's contacts_keyset_idx).
CREATE INDEX IF NOT EXISTS companies_keyset_idx ON companies (workspace_id, lower(name), id);
CREATE INDEX IF NOT EXISTS deals_keyset_idx ON deals (workspace_id, stage_id, position, id);
CREATE INDEX IF NOT EXISTS campaigns_keyset_idx ON campaigns (workspace_id, created_at DESC, id DESC);
