-- Workflow canvas layouts (MH-480): where each node sits on the canvas.
--
-- The steps stay the one source of truth for what a workflow does; this is
-- only how the canvas draws them. It lives beside the workflow rather than in
-- its steps, so nothing a browser runs ever carries a coordinate, and a layout
-- that no longer matches the steps (they were edited over MCP, say) is simply
-- ignored and the canvas lays the graph out again by itself.
--
-- One row per workflow, overwritten on each save of the canvas. Not versioned:
-- moving a node is not a change to the workflow.
CREATE TABLE IF NOT EXISTS automation_workflow_layouts (
    workspace_id text        NOT NULL,
    workflow_id  text        NOT NULL,
    -- {"trigger": {"x":0,"y":0}, "nodes": [{"x":..,"y":..}, ...]} — one entry
    -- per step, in step order.
    layout       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- The step commands the layout was drawn for, joined with "|". A layout
    -- whose signature is not the workflow's is stale and not used.
    signature    text        NOT NULL DEFAULT '',
    updated_by   text        NOT NULL DEFAULT '',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, workflow_id)
);
