-- Bring-your-own-agent research (the Opportunity Finder over MCP): work that
-- the CALLING agent does with its own model and web tools, and the record of
-- what it handed back.
--
-- Tenant tables: workspace_id on every row, RLS in
-- internal/db/post/210_agent_provider.sql, composite foreign keys.

-- One job for an agent: a discovery query or a research question. Claimed
-- with a lease so two agents do not run it twice; its results land in a
-- research_runs row (run_id) with author_kind = 'agent', so every read tool
-- (find_opportunities, get_research, find_evidence, …) sees them unchanged.
CREATE TABLE IF NOT EXISTS agent_tasks (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    -- discovery: find companies that fit the ICP; research: answer a question.
    kind          text        NOT NULL,
    -- pending -> claimed -> done | failed; cancelled by a person.
    status        text        NOT NULL DEFAULT 'pending',
    topic         text        NOT NULL,
    mode          text        NOT NULL DEFAULT 'search',
    urls          text[]      NOT NULL DEFAULT '{}',
    -- Where it came from: a person (manual), a schedule, discovery's clock, or
    -- an agent asking for work when the queue was empty (assistant).
    origin        text        NOT NULL DEFAULT 'manual',
    schedule_id   text        NOT NULL DEFAULT '',
    requested_by  text        NOT NULL DEFAULT '',
    run_id        text        NOT NULL DEFAULT '',
    -- The credential holding the lease (mcp_tokens.id / oauth_grants.id), and
    -- what the agent said it is.
    claimed_by    text        NOT NULL DEFAULT '',
    agent_name    text        NOT NULL DEFAULT '',
    agent_model   text        NOT NULL DEFAULT '',
    lease_until   timestamptz,
    attempts      integer     NOT NULL DEFAULT 0,
    received      integer     NOT NULL DEFAULT 0,
    stored        integer     NOT NULL DEFAULT 0,
    note          text        NOT NULL DEFAULT '',
    error         text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('discovery', 'research', 'summary')),
    CHECK (status IN ('pending', 'claimed', 'done', 'failed', 'cancelled')),
    CHECK (mode IN ('search', 'urls')),
    CHECK (origin IN ('manual', 'schedule', 'discovery', 'assistant')),
    CHECK (topic <> '')
);

-- Every submission an agent made: who (token, user, agent, model), to which
-- task, and what became of it. The provenance record, and what the per-token
-- rate limit counts.
CREATE TABLE IF NOT EXISTS agent_submissions (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    task_id       text        NOT NULL,
    kind          text        NOT NULL,
    token_id      text        NOT NULL DEFAULT '',
    token_name    text        NOT NULL DEFAULT '',
    via           text        NOT NULL DEFAULT '',
    user_id       text        NOT NULL DEFAULT '',
    agent_name    text        NOT NULL DEFAULT '',
    agent_model   text        NOT NULL DEFAULT '',
    received      integer     NOT NULL DEFAULT 0,
    stored        integer     NOT NULL DEFAULT 0,
    duplicates    integer     NOT NULL DEFAULT 0,
    rejected      integer     NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('opportunities', 'research'))
);
