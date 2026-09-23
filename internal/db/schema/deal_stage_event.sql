-- Deal stage history (MH-394): every stage a deal has entered, and when.
--
-- The deals table only knows a deal's CURRENT stage, so "how many deals that
-- reached Qualified went on to Proposal" could not be answered: a lost deal
-- forgets how far it got. A trigger on deals (internal/db/post/105_crm_reporting.sql)
-- writes one row here on every insert and every stage change, whichever code
-- path made it (the board, the API, MCP, a converted lead, an inbox thread), so
-- nothing has to remember to call it. Existing deals are backfilled with one row
-- for the stage they are in, at the time they were created: their earlier
-- history was never recorded and is not invented.
--
-- Every row carries workspace_id and is isolated by RLS; the composite foreign
-- keys and the policy are in the post file.
CREATE TABLE IF NOT EXISTS deal_stage_events (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    deal_id       text        NOT NULL,
    stage_id      text        NOT NULL,
    -- The stage it left; NULL for the stage it was created in.
    from_stage_id text,
    entered_at    timestamptz NOT NULL DEFAULT now(),
    -- 'change' (a live insert or move) or 'backfill' (MH-394's one row per
    -- deal that existed before this table did).
    source        text        NOT NULL DEFAULT 'change',
    CHECK (source IN ('change', 'backfill'))
);
