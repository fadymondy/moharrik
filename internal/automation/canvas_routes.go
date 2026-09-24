package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

/*
The workflow canvas (MH-480): the two things a graph editor needs that the
workflow itself does not carry.

	WHERE THE NODES SIT. A layout per workflow, beside it and never inside its
	steps: the steps are what a browser runs, and a coordinate has no business
	travelling with a click. The layout names the commands it was drawn for
	(its signature); one that no longer matches is ignored by the canvas, which
	then lays the graph out by itself. Saving a layout is not a new version of
	the workflow — moving a node changes nothing that runs.

	WHAT IT DID. The runs of one workflow, newest first, for the canvas's
	Executions tab, and the workspace's runs of any workflow, for the list page.
	Each run's own steps (GET /api/w/runs/{id}) are what an execution is replayed
	from, so an old run is drawn as it ran, not as the workflow reads today.
*/

// Point is a node's position on the canvas, in canvas units.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Layout is where a workflow's nodes sit.
type Layout struct {
	Trigger *Point  `json:"trigger,omitempty" doc:"The trigger node. Absent: drawn before the first step."`
	Nodes   []Point `json:"nodes" doc:"One per step, in step order."`
}

// LayoutOut is a workflow's saved layout, if it has one.
type LayoutOut struct {
	Layout    Layout     `json:"layout"`
	Signature string     `json:"signature" doc:"The step commands the layout was drawn for, joined with |. Empty: no layout saved."`
	UpdatedAt *time.Time `json:"updated_at"`
}

// Execution is one run of a saved workflow.
type Execution struct {
	ID           string     `json:"id"`
	WorkflowID   string     `json:"workflow_id"`
	WorkflowName string     `json:"workflow_name" doc:"The workflow's name now, or the name it had when it ran if it has been deleted since."`
	AgentID      string     `json:"agent_id"`
	AgentName    string     `json:"agent_name"`
	Trigger      string     `json:"trigger" doc:"manual | schedule | mcp | test"`
	Status       string     `json:"status"`
	Error        string     `json:"error"`
	StepCount    int        `json:"step_count"`
	OKCount      int        `json:"ok_count"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type executionsOut struct {
	Executions []Execution `json:"executions"`
	NextBefore string      `json:"next_before,omitempty" doc:"Pass back as before for the next page."`
}

// maxCoordinate keeps a layout to numbers a canvas can draw. A node dragged a
// million units away is somebody's mistake, not a layout worth storing.
const maxCoordinate = 1_000_000

// ValidateLayout checks a layout before it is stored.
func ValidateLayout(l Layout) error {
	ok := func(p Point) bool {
		return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) &&
			math.Abs(p.X) <= maxCoordinate && math.Abs(p.Y) <= maxCoordinate
	}
	if l.Trigger != nil && !ok(*l.Trigger) {
		return errors.New("the trigger's position is off the canvas")
	}
	if len(l.Nodes) > MaxSteps {
		return errors.New("a layout can place at most one node per step")
	}
	for _, p := range l.Nodes {
		if !ok(p) {
			return errors.New("a node's position is off the canvas")
		}
	}
	return nil
}

// RegisterCanvas mounts the canvas's endpoints.
//
// Reading is automation:read; placing nodes is automation:run, the right that
// saves the workflow itself — a layout is part of editing it.
func RegisterCanvas(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-layout", Method: http.MethodGet, Path: "/api/w/workflows/{id}/layout",
		Summary: "Where a workflow's nodes sit on the canvas (MH-480)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[LayoutOut], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		if _, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID); err != nil {
			return nil, err
		}
		o := LayoutOut{Layout: Layout{Nodes: []Point{}}}
		var raw string
		var updated time.Time
		err = db().QueryRowContext(ctx, `
			SELECT layout::text, signature, updated_at FROM automation_workflow_layouts
			WHERE workspace_id = $1 AND workflow_id = $2`, s.WorkspaceID, in.ID).Scan(&raw, &o.Signature, &updated)
		if errors.Is(err, sql.ErrNoRows) {
			return out(o), nil // never placed: the canvas lays it out
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the layout")
		}
		_ = json.Unmarshal([]byte(raw), &o.Layout)
		if o.Layout.Nodes == nil {
			o.Layout.Nodes = []Point{}
		}
		o.UpdatedAt = &updated
		return out(o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-save-workflow-layout", Method: http.MethodPut, Path: "/api/w/workflows/{id}/layout",
		Summary: "Keep where the nodes sit — not a new version: nothing that runs changes (MH-480)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Layout
			Signature string `json:"signature" maxLength:"4000" doc:"The step commands this layout was drawn for, joined with |."`
		}
	}) (*body[LayoutOut], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if _, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID); err != nil {
			return nil, err
		}
		l := in.Body.Layout
		if l.Nodes == nil {
			l.Nodes = []Point{}
		}
		if err := ValidateLayout(l); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		raw, err := json.Marshal(l)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("the layout is not usable")
		}
		var updated time.Time
		// The workflow's own (workspace_id, id) key is what the row references,
		// so a layout for another workspace's workflow cannot be written even
		// if the check above were ever skipped.
		err = db().QueryRowContext(ctx, `
			INSERT INTO automation_workflow_layouts (workspace_id, workflow_id, layout, signature, updated_by)
			VALUES ($1, $2, $3::jsonb, $4, $5)
			ON CONFLICT (workspace_id, workflow_id) DO UPDATE SET
				layout = EXCLUDED.layout, signature = EXCLUDED.signature,
				updated_by = EXCLUDED.updated_by, updated_at = now()
			RETURNING updated_at`,
			s.WorkspaceID, in.ID, string(raw), in.Body.Signature, s.UserID).Scan(&updated)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the layout")
		}
		return out(LayoutOut{Layout: l, Signature: in.Body.Signature, UpdatedAt: &updated}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-executions", Method: http.MethodGet, Path: "/api/w/workflows/{id}/executions",
		Summary: "One workflow's runs, newest first, a page at a time (MH-480)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Before string `query:"before" doc:"next_before from the previous page."`
		Limit  int    `query:"limit" minimum:"1" maximum:"100"`
	}) (*body[executionsOut], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		// Asked through the workflow, so another workspace's id is a 404 like
		// everywhere else rather than an empty list.
		if _, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID); err != nil {
			return nil, err
		}
		o, err := listExecutions(ctx, db(), s.WorkspaceID, in.ID, in.Before, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-adopt-workflow-executions", Method: http.MethodPost, Path: "/api/w/workflows/{id}/executions/adopt",
		Summary: "Give a just-saved workflow the test runs made while it was being built (MH-480)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			RunIDs []string `json:"run_ids" maxItems:"50" doc:"Test runs of the unsaved draft, made by the caller."`
		}
	}) (*body[struct {
		Adopted int `json:"adopted"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		w, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		n, err := AdoptTestRuns(ctx, db(), s.WorkspaceID, s.UserID, w.ID, w.Name, in.Body.RunIDs)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not attach the test runs")
		}
		return out(struct {
			Adopted int `json:"adopted"`
		}{n}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-executions-all", Method: http.MethodGet, Path: "/api/w/workflow-executions",
		Summary: "The workspace's runs of saved workflows, newest first, a page at a time (MH-480)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Before string `query:"before" doc:"next_before from the previous page."`
		Limit  int    `query:"limit" minimum:"1" maximum:"100"`
	}) (*body[executionsOut], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		o, err := listExecutions(ctx, db(), s.WorkspaceID, "", in.Before, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(o), nil
	})
}

// AdoptTestRuns attaches test runs of a draft to the workflow it became.
//
// A workflow built on the canvas is usually tested before it is ever saved,
// and those runs had no workflow to belong to; without this, the Executions tab
// of a workflow someone had just built and run showed nothing. Only runs that
// are the caller's own, are tests, and belong to no workflow yet are taken —
// nothing another person ran, and nothing already another workflow's.
func AdoptTestRuns(ctx context.Context, db *sql.DB, workspaceID, userID, workflowID, name string, runIDs []string) (int, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}
	res, err := db.ExecContext(ctx, `
		UPDATE automation_runs
		SET workflow_id = $3, params = jsonb_set(params, '{workflow}', to_jsonb($5::text)), updated_at = now()
		WHERE workspace_id = $1 AND requested_by = $2 AND id = ANY($4::text[])
		  AND workflow_id = '' AND trigger = 'test'`,
		workspaceID, userID, workflowID, pgtext.Literal(runIDs), name+" (test)")
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// listExecutions reads runs of saved workflows — one workflow's, or any when
// workflowID is empty — newest first, limit at a time, before a cursor.
func listExecutions(ctx context.Context, db *sql.DB, workspaceID, workflowID, before string, limit int) (executionsOut, error) {
	if limit <= 0 {
		limit = 30
	}
	var cursor any
	if before != "" {
		t, err := time.Parse(time.RFC3339Nano, before)
		if err != nil {
			return executionsOut{}, huma.Error422UnprocessableEntity("before is not a time")
		}
		cursor = t
	}
	rows, err := db.QueryContext(ctx, `
		SELECT r.id, r.workflow_id, COALESCE(w.name, r.params->>'workflow', ''), r.agent_id, COALESCE(b.name, ''),
		       r.trigger, r.status, r.error, r.step_count, r.ok_count, r.started_at, r.finished_at, r.created_at
		FROM automation_runs r
		LEFT JOIN automation_workflows w ON w.workspace_id = r.workspace_id AND w.id = r.workflow_id
		LEFT JOIN browser_agents b ON b.workspace_id = r.workspace_id AND b.id = r.agent_id
		WHERE r.workspace_id = $1
		  AND r.workflow_id <> ''
		  AND ($2 = '' OR r.workflow_id = $2)
		  AND ($3::timestamptz IS NULL OR r.created_at < $3)
		ORDER BY r.created_at DESC LIMIT $4`,
		workspaceID, workflowID, cursor, limit+1)
	if err != nil {
		return executionsOut{}, huma.Error500InternalServerError("could not read the executions")
	}
	defer rows.Close()
	o := executionsOut{Executions: []Execution{}}
	for rows.Next() {
		var e Execution
		var started, finished sql.NullTime
		if err := rows.Scan(&e.ID, &e.WorkflowID, &e.WorkflowName, &e.AgentID, &e.AgentName, &e.Trigger, &e.Status, &e.Error,
			&e.StepCount, &e.OKCount, &started, &finished, &e.CreatedAt); err != nil {
			return executionsOut{}, huma.Error500InternalServerError("could not read the executions")
		}
		e.StartedAt, e.FinishedAt = timep(started), timep(finished)
		o.Executions = append(o.Executions, e)
	}
	if err := rows.Err(); err != nil {
		return executionsOut{}, huma.Error500InternalServerError("could not read the executions")
	}
	if len(o.Executions) > limit {
		o.Executions = o.Executions[:limit]
		o.NextBefore = o.Executions[limit-1].CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return o, nil
}
