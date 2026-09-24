-- CRM reporting (MH-394): deal stage history, recorded by a trigger.
--
-- Everything for this card is in this one file (the foreign keys, the trigger,
-- the backfill and row-level security), so renumbering it against another
-- branch is a rename and nothing else. Every statement is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deal_stage_events_workspace_fk') THEN
        ALTER TABLE deal_stage_events ADD CONSTRAINT deal_stage_events_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, for the reason internal/db/schema/crm.sql gives: a foreign-key
    -- check runs as the table owner and ignores RLS.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deal_stage_events_deal_fk') THEN
        ALTER TABLE deal_stage_events ADD CONSTRAINT deal_stage_events_deal_fk
            FOREIGN KEY (workspace_id, deal_id) REFERENCES deals (workspace_id, id) ON DELETE CASCADE;
    END IF;
    -- A stage can only be removed once no deal is in it; its history goes with
    -- it, because a stage that no longer exists has no place in the funnel.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deal_stage_events_stage_fk') THEN
        ALTER TABLE deal_stage_events ADD CONSTRAINT deal_stage_events_stage_fk
            FOREIGN KEY (workspace_id, stage_id) REFERENCES pipeline_stages (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deal_stage_events_from_fk') THEN
        ALTER TABLE deal_stage_events ADD CONSTRAINT deal_stage_events_from_fk
            FOREIGN KEY (workspace_id, from_stage_id) REFERENCES pipeline_stages (workspace_id, id) ON DELETE SET NULL (from_stage_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS deal_stage_events_deal_idx ON deal_stage_events (workspace_id, deal_id, entered_at);
CREATE INDEX IF NOT EXISTS deal_stage_events_stage_idx ON deal_stage_events (workspace_id, stage_id);
-- The reports read closed deals by close time and new deals by open time.
CREATE INDEX IF NOT EXISTS deals_closed_idx ON deals (workspace_id, closed_at) WHERE closed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS deals_created_idx ON deals (workspace_id, created_at);

-- The trigger runs as whoever changed the deal. On the app pool that is the
-- RLS role with the deal's own workspace set, and the row it writes carries
-- that same workspace_id, so the policy below admits it.
CREATE OR REPLACE FUNCTION crm_record_deal_stage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, from_stage_id, entered_at)
        VALUES (NEW.workspace_id, NEW.id, NEW.stage_id, NULL, now());
    ELSIF NEW.stage_id IS DISTINCT FROM OLD.stage_id THEN
        INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, from_stage_id, entered_at)
        VALUES (NEW.workspace_id, NEW.id, NEW.stage_id, OLD.stage_id, now());
    END IF;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS deals_record_stage ON deals;
CREATE TRIGGER deals_record_stage AFTER INSERT OR UPDATE OF stage_id ON deals
    FOR EACH ROW EXECUTE FUNCTION crm_record_deal_stage();

-- Backfill: one row per deal that has none, for the stage it is in now, at the
-- time it was created. Only the current stage is known; nothing earlier is made up.
INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, entered_at, source)
SELECT d.workspace_id, d.id, d.stage_id, d.created_at, 'backfill'
FROM deals d
WHERE NOT EXISTS (SELECT 1 FROM deal_stage_events e WHERE e.workspace_id = d.workspace_id AND e.deal_id = d.id);

ALTER TABLE deal_stage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE deal_stage_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON deal_stage_events;
CREATE POLICY tenant_isolation ON deal_stage_events
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
