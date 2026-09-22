-- Automation capacity and retention (MH-234).
--
-- Three problems this fixes, in the order they bite.
--
-- 1. Nothing capped a WORKSPACE. `Start` allowed one live run per browser, so
--    a workspace with twenty paired browsers could have twenty runs going and
--    twenty detached goroutines, with no ceiling anywhere.
--
-- 2. The one-run-per-browser guardrail did not actually hold. The code said it
--    was "enforced by the partial index the query reads" — but
--    automation_runs_live_idx was a plain index, not a unique one, so it
--    enforced nothing. Two requests arriving together both counted zero live
--    runs and both inserted, and the same signed-in browser got driven twice
--    at once. That is the guardrail this system exists to provide.
--
-- 3. Runs were kept for ever, and a step stores the browser's whole reply
--    verbatim. A busy workspace grows without limit and nobody notices until
--    the table is the biggest one in the database.

-- Capacity and retention live beside the kill switch: they are the same kind
-- of thing — a workspace's own limits on its automation — and a page that
-- reads one reads the others.
--
-- 3 parallel runs is a deliberate default rather than a generous one. Each run
-- drives a real browser somebody is signed into; the cost of a limit that is
-- too low is a queued run, and the cost of one too high is twenty browsers
-- moving at once with no way to follow what happened.
ALTER TABLE automation_settings
    ADD COLUMN IF NOT EXISTS max_parallel_runs integer NOT NULL DEFAULT 3;

-- 30 days of history. Long enough to answer "why did this browser open my mail
-- last month", short enough that the table stays readable. 0 means keep for
-- ever, for anyone who would rather decide themselves.
ALTER TABLE automation_settings
    ADD COLUMN IF NOT EXISTS retention_days integer NOT NULL DEFAULT 30;

-- A cap of zero would mean "no automation at all", which is what the kill
-- switch is for; keeping the two controls separate means neither can be
-- mistaken for the other.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_settings_parallel_ck') THEN
        ALTER TABLE automation_settings ADD CONSTRAINT automation_settings_parallel_ck
            CHECK (max_parallel_runs >= 1 AND max_parallel_runs <= 50);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_settings_retention_ck') THEN
        ALTER TABLE automation_settings ADD CONSTRAINT automation_settings_retention_ck
            CHECK (retention_days >= 0 AND retention_days <= 3650);
    END IF;
END $$;

-- Before the unique index can be built, any browser that ALREADY has two live
-- runs has to be resolved — otherwise the CREATE fails and the migration
-- stops. This is the state the race could produce, so it is not hypothetical.
--
-- The newest live run is kept and the older ones are closed out. Cancelled
-- rather than deleted: those runs really did exist, may have driven a browser,
-- and the record of them is the point of the table.
WITH live AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY workspace_id, agent_id
               ORDER BY created_at DESC, id DESC
           ) AS rank
    FROM automation_runs
    WHERE status IN ('queued', 'running')
)
UPDATE automation_runs r
SET status = 'cancelled',
    error = 'superseded: this browser had more than one run live at once (MH-234)',
    finished_at = COALESCE(r.finished_at, now()),
    updated_at = now()
FROM live
WHERE r.id = live.id AND live.rank > 1;

-- Now the guardrail the comment always claimed. UNIQUE, so the database
-- refuses the second live run for a browser no matter how the two requests
-- are interleaved.
DROP INDEX IF EXISTS automation_runs_live_idx;

CREATE UNIQUE INDEX IF NOT EXISTS automation_runs_live_uniq
    ON automation_runs (workspace_id, agent_id)
    WHERE status IN ('queued', 'running');

-- The sweeper asks "what finished before this date", across every workspace.
CREATE INDEX IF NOT EXISTS automation_runs_retention_idx
    ON automation_runs (workspace_id, finished_at)
    WHERE status NOT IN ('queued', 'running');
