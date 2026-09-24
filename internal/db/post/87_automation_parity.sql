-- Workflows, schedules, jobs and runs at fadymondy.com's parity (MH-478 phase 3).
--
-- A workflow is what fadymondy.com calls a plugin: it gains a SLUG (so a tool
-- call can name it), an ENABLED switch (a workflow can be turned off without
-- losing it or its schedules), and when it last ran. A run records WHICH
-- version of the workflow it ran and which schedule started it, so history
-- stays readable after the workflow is edited and two schedules of one
-- workflow never answer for each other.
--
-- A schedule may now run a SERVER JOB (job_key) — in-process work on the same
-- clock, with the same "advance the clock first", overlap and miss rules —
-- and may name no browser at all, which means "whichever of this workspace's
-- browsers is connected at the time". It also counts its misses and its
-- consecutive failures, so "this has stopped working" is visible on the row.
--
-- The workspace gets a per-browser hourly ceiling on runs, fadymondy.com's
-- RunsPerHour guardrail, set where the other limits are set.

ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS slug text NOT NULL DEFAULT '';
ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS last_run_at timestamptz;

-- Every existing workflow gets a slug: its latin name made safe, or "workflow"
-- for a name with none, and the row's id tail when two would collide.
UPDATE automation_workflows w
SET slug = s.slug
FROM (
    SELECT id,
           CASE WHEN row_number() OVER (PARTITION BY workspace_id, base ORDER BY created_at, id) = 1 THEN base
                ELSE base || '-' || left(id, 6) END AS slug
    FROM (
        SELECT id, workspace_id, created_at,
               COALESCE(NULLIF(trim(both '-' from regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g')), ''), 'workflow') AS base
        FROM automation_workflows
        WHERE slug = ''
    ) named
) s
WHERE w.id = s.id AND w.slug = '';

CREATE UNIQUE INDEX IF NOT EXISTS automation_workflows_slug_uniq
    ON automation_workflows (workspace_id, slug) WHERE slug <> '';

ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS workflow_version integer NOT NULL DEFAULT 0;
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS schedule_id text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS automation_runs_schedule_idx
    ON automation_runs (workspace_id, schedule_id, created_at DESC) WHERE schedule_id <> '';

-- No browser named: any connected browser of the workspace. The composite
-- foreign key is MATCH SIMPLE, so a NULL agent_id is simply not checked.
ALTER TABLE automation_schedules ALTER COLUMN agent_id DROP NOT NULL;
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS job_key text NOT NULL DEFAULT '';
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0;
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS missed_count integer NOT NULL DEFAULT 0;

ALTER TABLE automation_settings ADD COLUMN IF NOT EXISTS runs_per_hour integer NOT NULL DEFAULT 60;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_settings_hourly_ck') THEN
        ALTER TABLE automation_settings ADD CONSTRAINT automation_settings_hourly_ck
            CHECK (runs_per_hour >= 1 AND runs_per_hour <= 1000);
    END IF;
END $$;

-- "How many runs has this browser had in the last hour" is asked before every run.
CREATE INDEX IF NOT EXISTS automation_runs_hourly_idx ON automation_runs (workspace_id, agent_id, created_at DESC);
