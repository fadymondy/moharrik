-- Lead budget (MH-464): what the visitor typed into an inquiry form's optional
-- "Budget" field, kept as typed. Converting the lead turns it into the deal's
-- value when it reads as one amount in the workspace's currency
-- (internal/leads/budget.go); anything else ("10-20k", "EGP 50,000" in a USD
-- workspace) stays on the lead for a person to read, and the deal says "No
-- value yet" instead of pretending the visitor asked for $0.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get the column.
ALTER TABLE leads ADD COLUMN IF NOT EXISTS budget text NOT NULL DEFAULT '';
