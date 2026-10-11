-- Lead external id: the id the sending system gave the inquiry (a CMS
-- inquiry id), so a server that forwards its own contact-form inquiries can
-- retry or backfill without filing the same lead twice. Empty for inquiries
-- typed into the embed snippet, which have no id of their own.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get the column.
ALTER TABLE leads ADD COLUMN IF NOT EXISTS external_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS leads_external_id_idx
    ON leads (workspace_id, form_id, external_id) WHERE external_id <> '';
