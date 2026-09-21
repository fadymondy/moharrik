-- Automation schedules (MH-232): a saved sequence of steps run on a clock.
--
-- The steps live here rather than pointing at a saved workflow: plugins are a
-- separate card, and a schedule that could not run until they existed would be
-- half a feature.
CREATE TABLE IF NOT EXISTS automation_schedules (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    name         text        NOT NULL,
    agent_id     text        NOT NULL,
    steps        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    cron         text        NOT NULL,
    -- The schedule's OWN timezone. "Every day at 07:00" means 07:00 where the
    -- operator is; a server in UTC that ignores this is wrong twice a year.
    timezone     text        NOT NULL DEFAULT 'UTC',
    -- skip | once. What to do when the browser was shut at the due time: let
    -- the slot go, or run once when it comes back.
    catch_up     text        NOT NULL DEFAULT 'skip',
    enabled      boolean     NOT NULL DEFAULT true,
    next_run_at  timestamptz NOT NULL,
    last_run_at  timestamptz,
    -- running | ok | failed | missed | waiting. "missed" and "waiting" are
    -- first-class: a gap in the data has to have a stated cause.
    last_status  text        NOT NULL DEFAULT '',
    last_error   text        NOT NULL DEFAULT '',
    last_run_id  text        NOT NULL DEFAULT '',
    created_by   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (catch_up IN ('skip', 'once'))
);
