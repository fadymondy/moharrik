-- A workflow's sites are added to the browser it runs in (MH-517).
--
-- A workflow knows which sites it needs: the ones it declares and the ones
-- its steps go to. Before this, the first run of a new workflow failed on a
-- browser that had not been told about them, and someone had to add them by
-- hand and run it again. Now the run adds them itself, under the rights of
-- whoever runs it, and says so as the run's first line (notes).
--
-- auto_add_sites is the workspace's switch for that, on by default.
-- auto_add_input_sites extends it, for runs nobody watches (MCP, schedules),
-- to sites that come from a value given at run time rather than from the
-- saved workflow; off by default, because a value is not a decision anyone
-- reviewed.

ALTER TABLE automation_settings ADD COLUMN IF NOT EXISTS auto_add_sites boolean NOT NULL DEFAULT true;
ALTER TABLE automation_settings ADD COLUMN IF NOT EXISTS auto_add_input_sites boolean NOT NULL DEFAULT false;
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS notes text[] NOT NULL DEFAULT '{}';
