-- Workflow versions (MH-400): every save of a workflow, kept.
--
-- A workflow is edited by people who did not write it, often weeks after it
-- was recorded, and an edit that breaks a schedule at 4am has to be undoable
-- by someone who cannot remember what the steps were. So each save writes the
-- WHOLE workflow here as a new version, and restoring an old one is itself a
-- new save: history only ever grows, and "restore v1" can be undone too.
--
-- The rows are immutable. Nothing updates a version once it is written.
CREATE TABLE IF NOT EXISTS automation_workflow_versions (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    workflow_id   text        NOT NULL,
    -- 1, 2, 3 … per workflow, as a person counts them.
    version       integer     NOT NULL,
    name          text        NOT NULL,
    description   text        NOT NULL DEFAULT '',
    steps         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    params        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- created | recorded | edited | restored — how this version came to be.
    note          text        NOT NULL DEFAULT '',
    -- For a restore: which version's content this is.
    restored_from integer,
    -- users.id, or "agent:<id>" for a recording. Never joined from the app pool.
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, workflow_id, version)
);
