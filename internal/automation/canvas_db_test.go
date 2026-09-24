package automation_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The workflow canvas (MH-480), against a real database:

  - a layout is kept beside the workflow, never in its steps, and saving one is
    not a new version; another workspace can neither read nor write it, and it
    goes when the workflow does
  - a workflow's executions come newest first, a page at a time, and the
    workspace-wide list holds only runs of workflows
  - a just-saved workflow adopts only the caller's own unattached test runs
*/

func canvasRoutes(t *testing.T, a *app.App, wsID, role string) (humatest.TestAPI, context.Context) {
	t.Helper()
	_, api := humatest.New(t)
	hub := automation.NewHub(quietLog())
	automation.RegisterWorkflows(api, a, hub)
	automation.RegisterCanvas(api, a)
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-" + role, Role: role, Via: "session"})
	return api, ctx
}

func createWorkflow(t *testing.T, api humatest.TestAPI, ctx context.Context, name string) automation.Workflow {
	t.Helper()
	res := api.PostCtx(ctx, "/api/w/workflows", map[string]any{
		"name": name,
		"steps": []map[string]any{
			{"command": "goto", "args": map[string]any{"url": "https://example.com/"}},
			{"command": "extract", "args": map[string]any{"list": "li", "fields": map[string]any{"name": "a"}}},
		},
	})
	if res.Code != 201 {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var out struct{ Workflow automation.Workflow }
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return out.Workflow
}

func TestCanvasLayoutIsKeptApartAndIsNotAVersion(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	api, ctx := canvasRoutes(t, a, wsID, "owner")
	wf := createWorkflow(t, api, ctx, "Laid out")
	path := "/api/w/workflows/" + wf.ID + "/layout"

	// Never placed: an empty layout, which the canvas lays out by itself.
	res := api.GetCtx(ctx, path)
	var got automation.LayoutOut
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if res.Code != 200 || got.Signature != "" || len(got.Layout.Nodes) != 0 || got.UpdatedAt != nil {
		t.Fatalf("an unplaced workflow's layout: %d %s", res.Code, res.Body)
	}

	body := map[string]any{"trigger": map[string]any{"x": -200, "y": 0}, "nodes": []map[string]any{{"x": 0, "y": 0}, {"x": 208, "y": 64}}, "signature": "navigate|extract"}
	if r := api.PutCtx(ctx, path, body); r.Code != 200 {
		t.Fatalf("save the layout: %d %s", r.Code, r.Body)
	}
	res = api.GetCtx(ctx, path)
	got = automation.LayoutOut{}
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if got.Signature != "navigate|extract" || len(got.Layout.Nodes) != 2 || got.Layout.Nodes[1].Y != 64 || got.Layout.Trigger == nil || got.Layout.Trigger.X != -200 {
		t.Fatalf("the layout read back: %s", res.Body)
	}
	// Saved again over itself: one row, the new places.
	body["nodes"] = []map[string]any{{"x": 0, "y": 0}, {"x": 300, "y": 0}}
	if r := api.PutCtx(ctx, path, body); r.Code != 200 {
		t.Fatalf("save the layout again: %d %s", r.Code, r.Body)
	}

	// Moving nodes is not a change to the workflow: no new version, and the
	// steps carry no coordinate.
	res = api.GetCtx(ctx, "/api/w/workflows/"+wf.ID)
	var now struct{ Workflow automation.Workflow }
	_ = json.Unmarshal(res.Body.Bytes(), &now)
	if now.Workflow.Version != 1 {
		t.Errorf("saving a layout made version %d", now.Workflow.Version)
	}
	if steps, _ := json.Marshal(now.Workflow.Steps); strings.Contains(string(steps), `"x"`) || strings.Contains(string(steps), "300") {
		t.Errorf("the layout leaked into the steps: %s", steps)
	}
	var rows int
	_ = a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM automation_workflow_layouts WHERE workflow_id = $1`, wf.ID).Scan(&rows)
	if rows != 1 {
		t.Errorf("layout rows = %d, want 1", rows)
	}

	// Off the canvas, or more nodes than a workflow can have: refused.
	if r := api.PutCtx(ctx, path, map[string]any{"nodes": []map[string]any{{"x": 2e6, "y": 0}}, "signature": "x"}); r.Code != 422 {
		t.Errorf("a node a million units away: %d, want 422", r.Code)
	}
	many := make([]map[string]any, automation.MaxSteps+1)
	for i := range many {
		many[i] = map[string]any{"x": i, "y": 0}
	}
	if r := api.PutCtx(ctx, path, map[string]any{"nodes": many, "signature": "x"}); r.Code != 422 {
		t.Errorf("more nodes than steps allowed: %d, want 422", r.Code)
	}
	// A member without the run right reads it but cannot place nodes.
	vapi, vctx := canvasRoutes(t, a, wsID, "member")
	if r := vapi.GetCtx(vctx, path); r.Code != 200 {
		t.Errorf("a member reading the layout: %d", r.Code)
	}
	if r := vapi.PutCtx(vctx, path, body); r.Code != 403 {
		t.Errorf("a member placing nodes: %d, want 403", r.Code)
	}

	// Another workspace: the id is simply not there.
	other, _ := workspace(t, a)
	oapi, octx := canvasRoutes(t, a, other, "owner")
	if r := oapi.GetCtx(octx, path); r.Code != 404 {
		t.Errorf("another workspace reading the layout: %d, want 404", r.Code)
	}
	if r := oapi.PutCtx(octx, path, body); r.Code != 404 {
		t.Errorf("another workspace writing the layout: %d, want 404", r.Code)
	}

	// It goes with the workflow.
	if r := api.DeleteCtx(ctx, "/api/w/workflows/"+wf.ID); r.Code != 204 {
		t.Fatalf("delete: %d %s", r.Code, r.Body)
	}
	_ = a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM automation_workflow_layouts WHERE workflow_id = $1`, wf.ID).Scan(&rows)
	if rows != 0 {
		t.Errorf("the layout outlived its workflow")
	}
}

func TestCanvasValidateLayout(t *testing.T) {
	ok := automation.Layout{Nodes: []automation.Point{{X: 0, Y: 0}, {X: -999999, Y: 999999}}}
	if err := automation.ValidateLayout(ok); err != nil {
		t.Errorf("a layout on the canvas was refused: %v", err)
	}
	for name, l := range map[string]automation.Layout{
		"NaN":         {Nodes: []automation.Point{{X: math.NaN()}}},
		"infinite":    {Nodes: []automation.Point{{Y: math.Inf(1)}}},
		"far trigger": {Trigger: &automation.Point{X: -2e6}, Nodes: []automation.Point{}},
	} {
		if automation.ValidateLayout(l) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// insertRun writes a finished run straight into the table: the point is the
// listing, not the dispatcher.
func insertRun(t *testing.T, a *app.App, wsID, agentID, workflowID, trigger, by string, at time.Time) string {
	t.Helper()
	var id string
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO automation_runs (workspace_id, agent_id, trigger, requested_by, params, status, step_count, ok_count, workflow_id, created_at, started_at, finished_at)
		VALUES ($1, $2, $3, $4, '{"workflow":"Old name"}'::jsonb, 'ok', 2, 2, $5, $6::timestamptz, $6::timestamptz, $6::timestamptz + interval '3 seconds') RETURNING id`,
		wsID, agentID, trigger, by, workflowID, at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCanvasExecutionsNewestFirstAPageAtATime(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a)
	api, ctx := canvasRoutes(t, a, wsID, "owner")
	wf := createWorkflow(t, api, ctx, "Executed")
	other := createWorkflow(t, api, ctx, "Other")

	t0 := time.Now().Add(-time.Hour)
	first := insertRun(t, a, wsID, agentID, wf.ID, "test", "u-owner", t0)
	second := insertRun(t, a, wsID, agentID, wf.ID, "manual", "u-owner", t0.Add(time.Minute))
	third := insertRun(t, a, wsID, agentID, wf.ID, "schedule", "", t0.Add(2*time.Minute))
	insertRun(t, a, wsID, agentID, other.ID, "manual", "u-owner", t0.Add(3*time.Minute))
	insertRun(t, a, wsID, agentID, "", "manual", "u-owner", t0.Add(4*time.Minute)) // a one-off run, of no workflow

	type page struct {
		Executions []automation.Execution
		NextBefore string `json:"next_before"`
	}
	read := func(path string) page {
		t.Helper()
		res := api.GetCtx(ctx, path)
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
		var p page
		_ = json.Unmarshal(res.Body.Bytes(), &p)
		return p
	}

	p1 := read("/api/w/workflows/" + wf.ID + "/executions?limit=2")
	if len(p1.Executions) != 2 || p1.Executions[0].ID != third || p1.Executions[1].ID != second || p1.NextBefore == "" {
		t.Fatalf("page one: %+v", p1)
	}
	// The workflow's name as it is now, not as the run recorded it.
	if p1.Executions[0].WorkflowName != "Executed" || p1.Executions[0].Trigger != "schedule" || p1.Executions[0].FinishedAt == nil {
		t.Errorf("an execution: %+v", p1.Executions[0])
	}
	p2 := read("/api/w/workflows/" + wf.ID + "/executions?limit=2&before=" + p1.NextBefore)
	if len(p2.Executions) != 1 || p2.Executions[0].ID != first || p2.NextBefore != "" {
		t.Fatalf("page two: %+v", p2)
	}

	all := read("/api/w/workflow-executions")
	if len(all.Executions) != 4 {
		t.Fatalf("the workspace's workflow runs: %d, want 4 (not the one-off run)", len(all.Executions))
	}
	if all.Executions[0].WorkflowName != "Other" {
		t.Errorf("newest first: %+v", all.Executions[0])
	}

	if r := api.GetCtx(ctx, "/api/w/workflows/"+wf.ID+"/executions?before=yesterday"); r.Code != 422 {
		t.Errorf("a cursor that is not a time: %d, want 422", r.Code)
	}

	// Another workspace sees none of it.
	ows, _ := workspace(t, a)
	oapi, octx := canvasRoutes(t, a, ows, "owner")
	if r := oapi.GetCtx(octx, "/api/w/workflows/"+wf.ID+"/executions"); r.Code != 404 {
		t.Errorf("another workspace reading the executions: %d, want 404", r.Code)
	}
	res := oapi.GetCtx(octx, "/api/w/workflow-executions")
	if res.Code != 200 || strings.Contains(res.Body.String(), wf.ID) || strings.Contains(res.Body.String(), third) {
		t.Errorf("another workspace's list: %d %s", res.Code, res.Body)
	}
}

func TestCanvasAdoptTakesOnlyTheCallersOwnUnattachedTestRuns(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a)
	api, ctx := canvasRoutes(t, a, wsID, "owner")
	wf := createWorkflow(t, api, ctx, "Just saved")
	elsewhere := createWorkflow(t, api, ctx, "Elsewhere")
	at := time.Now().Add(-time.Minute)

	mine := insertRun(t, a, wsID, agentID, "", "test", "u-owner", at)
	theirs := insertRun(t, a, wsID, agentID, "", "test", "u-someone", at)
	notATest := insertRun(t, a, wsID, agentID, "", "manual", "u-owner", at)
	attached := insertRun(t, a, wsID, agentID, elsewhere.ID, "test", "u-owner", at)
	ows, oagent := workspace(t, a)
	foreign := insertRun(t, a, ows, oagent, "", "test", "u-owner", at)

	res := api.PostCtx(ctx, "/api/w/workflows/"+wf.ID+"/executions/adopt", map[string]any{"run_ids": []string{mine, theirs, notATest, attached, foreign}})
	var out struct{ Adopted int }
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if res.Code != 200 || out.Adopted != 1 {
		t.Fatalf("adopt: %d %s, want exactly the caller's own unattached test run", res.Code, res.Body)
	}
	owner := func(id string) (string, string) {
		var wfID, name string
		_ = a.SysDB.QueryRowContext(context.Background(), `SELECT workflow_id, COALESCE(params->>'workflow', '') FROM automation_runs WHERE id = $1`, id).Scan(&wfID, &name)
		return wfID, name
	}
	if w, n := owner(mine); w != wf.ID || n != "Just saved (test)" {
		t.Errorf("the adopted run: workflow %q, name %q", w, n)
	}
	for _, id := range []string{theirs, notATest, foreign} {
		if w, _ := owner(id); w != "" {
			t.Errorf("run %s was adopted", id)
		}
	}
	if w, _ := owner(attached); w != elsewhere.ID {
		t.Errorf("a run of another workflow moved to this one")
	}
	// Another workspace cannot adopt into this workflow at all.
	oapi, octx := canvasRoutes(t, a, ows, "owner")
	if r := oapi.PostCtx(octx, "/api/w/workflows/"+wf.ID+"/executions/adopt", map[string]any{"run_ids": []string{foreign}}); r.Code != 404 {
		t.Errorf("adopting into another workspace's workflow: %d, want 404", r.Code)
	}
}
