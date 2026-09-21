-- Saved workflows (MH-233): a named sequence of steps with placeholders.
--
-- The steps keep their {{placeholders}} for ever; a run gets a filled-in copy.
-- Rewriting the stored steps would mean the first run consumed the workflow.
CREATE TABLE IF NOT EXISTS automation_workflows (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    steps        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- What the placeholders mean: name, label, required, secret, default.
    -- A parameter marked secret is redacted from the run record.
    params       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    run_count    integer     NOT NULL DEFAULT 0,
    created_by   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);
