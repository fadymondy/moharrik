-- Connecting a browser by approval (MH-478): the platform table's lock-down.
--
-- agent_connects has no grant to the app role (it is not in TenantTables), and
-- RLS is on with NO policy, the same as admin_mcp_tokens: if a grant is ever
-- added by mistake, the app role still sees nothing. Everything that touches
-- it runs on the owner pool with an explicit predicate on a hash.

ALTER TABLE agent_connects ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_connects FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    -- Once approved, a request belongs to a workspace; deleting the workspace
    -- takes it along.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_connects_workspace_fk') THEN
        ALTER TABLE agent_connects ADD CONSTRAINT agent_connects_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;
