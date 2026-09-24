-- Automation rules (MH-427): when something happens, do something.
--
-- A rule is one trigger (an event type Moharrik raises — a new lead, a deal
-- moved, a notification item arrived…), conditions on that event's fields, and
-- an ordered list of actions carried out through the same services a member's
-- request uses. Schedules (automation_schedules) are time → browser; rules are
-- event → anything.
--
-- Foreign keys, indexes and row-level security:
-- internal/db/post/125_automation_rules.sql.

CREATE TABLE IF NOT EXISTS automation_rules (
    id                text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id      text        NOT NULL,
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    -- A new rule is off until someone has tested it and switched it on.
    enabled           boolean     NOT NULL DEFAULT false,
    -- The event type it listens to (internal/triggers), e.g. lead.created.
    trigger           text        NOT NULL,
    -- all = every condition must hold; any = one is enough.
    match             text        NOT NULL DEFAULT 'all',
    -- [{"field":"severity","op":"equals","value":"error"}, ...]
    conditions        jsonb       NOT NULL DEFAULT '[]',
    -- [{"type":"create_deal","params":{"title":"{{name}}"}}, ...] in order.
    actions           jsonb       NOT NULL DEFAULT '[]',
    -- The schedule trigger only: a five-field cron in an IANA zone, and when
    -- it is next due.
    cron              text        NOT NULL DEFAULT '',
    timezone          text        NOT NULL DEFAULT 'UTC',
    next_run_at       timestamptz,
    -- Run at most once for the same record (a lead, a deal, a trend story…).
    once_per_subject  boolean     NOT NULL DEFAULT false,
    -- The rule's own brake: runs that did something, per rolling hour.
    max_runs_per_hour integer     NOT NULL DEFAULT 30,
    -- The signing secret for its webhook actions, sealed (internal/secret).
    webhook_secret    text        NOT NULL DEFAULT '',
    run_count         bigint      NOT NULL DEFAULT 0,
    last_run_at       timestamptz,
    last_status       text        NOT NULL DEFAULT '',
    last_error        text        NOT NULL DEFAULT '',
    -- A rule acts as the member who last saved it, with their role at the
    -- moment it runs: it can never do what that person could not.
    created_by        text        NOT NULL,
    updated_by        text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    CHECK (match IN ('all', 'any')),
    CHECK (max_runs_per_hour BETWEEN 1 AND 1000)
);

-- The outbox: one row per event some enabled rule listens to. Written by
-- internal/triggers where the thing happened, read and marked by the
-- dispatcher (internal/rules). Nothing is written when no rule listens.
CREATE TABLE IF NOT EXISTS automation_rule_events (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    type         text        NOT NULL,
    -- The record it is about (a lead id, a deal id, …); '' when none.
    subject_id   text        NOT NULL DEFAULT '',
    -- The event's fields, as the conditions and templates read them.
    data         jsonb       NOT NULL DEFAULT '{}',
    -- The rules whose actions caused this event, oldest first: the loop guard.
    chain        text[]      NOT NULL DEFAULT '{}',
    -- A schedule event is for one rule only.
    rule_id      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    UNIQUE (workspace_id, id)
);

-- The run log: every evaluation of a rule against an event, with what it was
-- given, which conditions held, and what each action did.
CREATE TABLE IF NOT EXISTS automation_rule_runs (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    rule_id      text        NOT NULL,
    -- A snapshot, so the log still reads after a rename.
    rule_name    text        NOT NULL DEFAULT '',
    event_id     text        NOT NULL DEFAULT '',
    trigger      text        NOT NULL,
    subject_id   text        NOT NULL DEFAULT '',
    -- ok | partial | failed | skipped | rate_limited | loop_blocked
    status       text        NOT NULL,
    -- A test against a sample event: nothing was carried out.
    dry_run      boolean     NOT NULL DEFAULT false,
    -- Why it was skipped, limited or blocked, or the first failure.
    reason       text        NOT NULL DEFAULT '',
    event        jsonb       NOT NULL DEFAULT '{}',
    conditions   jsonb       NOT NULL DEFAULT '[]',
    actions      jsonb       NOT NULL DEFAULT '[]',
    -- How many rules deep in a chain this run was (0 = a real-world event).
    depth        integer     NOT NULL DEFAULT 0,
    -- Who asked for a test run; '' for a real one.
    tested_by    text        NOT NULL DEFAULT '',
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    UNIQUE (workspace_id, id),
    CHECK (status IN ('ok', 'partial', 'failed', 'skipped', 'rate_limited', 'loop_blocked'))
);
