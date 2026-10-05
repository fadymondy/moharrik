package automation_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The workflow overview and "run again" (MH-1067), against a real database and a
fake browser on a real socket.

  - the overview's figures come from the runs that exist: nothing before the
    first run (null, never 0% or 100%), then counts, the success rate with its
    working, items from the extract step, and per-step records
  - a re-run repeats the steps the ORIGINAL run stored — editing the workflow
    afterwards does not change what runs again — and is linked to it
  - a run that used a secret cannot be repeated; a workflow that writes asks
    first; a workflow switched off refuses; a member may read but not re-run;
    another workspace's run is a 404
*/

func overviewAPI(t *testing.T, a *app.App, hub *automation.Hub, wsID, role string) (humatest.TestAPI, context.Context) {
	t.Helper()
	_, api := humatest.New(t)
	automation.Register(api, a, hub)
	automation.RegisterWorkflows(api, a, hub)
	automation.RegisterOverview(api, a)
	automation.RegisterRerun(api, a, hub)
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-" + role, Role: role, Via: "session"})
	return api, ctx
}

type ovOut struct {
	Version     int    `json:"version"`
	Status      string `json:"status"`
	WindowDays  int    `json:"window_days"`
	Sampled     bool   `json:"sampled"`
	Runs        struct{ Total, Succeeded, Failed, Cancelled, InProgress int }
	SuccessRate struct {
		Succeeded, Finished int
		Percent             *float64
	} `json:"success_rate"`
	LastRun *struct {
		ID      string
		Status  string
		Version int
		Items   *int
	} `json:"last_run"`
	Items struct {
		Total *int
		Runs  int
	}
	Steps []struct {
		Position   int
		Ran, OK    int
		Failed     int
		LastStatus string `json:"last_status"`
		LastItems  *int   `json:"last_items"`
	}
}

func getOverview(t *testing.T, api humatest.TestAPI, ctx context.Context, id string) ovOut {
	t.Helper()
	res := api.GetCtx(ctx, "/api/w/workflows/"+id+"/overview")
	if res.Code != 200 {
		t.Fatalf("overview: %d %s", res.Code, res.Body)
	}
	var o ovOut
	if err := json.Unmarshal(res.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

type runOut struct {
	Run struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		Trigger         string `json:"trigger"`
		WorkflowID      string `json:"workflow_id"`
		WorkflowVersion int    `json:"workflow_version"`
		RerunOf         string `json:"rerun_of"`
	} `json:"run"`
	Steps []struct {
		Position int    `json:"position"`
		Command  string `json:"command"`
		Args     string `json:"args"`
		Status   string `json:"status"`
	} `json:"steps"`
}

func getRun(t *testing.T, api humatest.TestAPI, ctx context.Context, id string) runOut {
	t.Helper()
	res := api.GetCtx(ctx, "/api/w/runs/"+id)
	if res.Code != 200 {
		t.Fatalf("run: %d %s", res.Code, res.Body)
	}
	var r runOut
	if err := json.Unmarshal(res.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func rerun(api humatest.TestAPI, ctx context.Context, id string, body map[string]any) *httptest.ResponseRecorder {
	return api.PostCtx(ctx, "/api/w/runs/"+id+"/rerun", body)
}

func TestOverviewAndRerun(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, fb := connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		args, _ := p["args"].(map[string]any)
		if sel, _ := args["selector"].(string); sel == "#missing" {
			return automation.Envelope{Error: "nothing on the page matches #missing"}
		}
		if p["command"] == "extract" {
			return ok(`{"summary":"2 rows","rows":[{"price":1},{"price":2}]}`)
		}
		return ok(`{"summary":"fine"}`)
	})
	ctx := context.Background()
	api, owner := overviewAPI(t, a, hub, wsID, "owner")

	prices, err := automation.CreateWorkflow(ctx, a, wsID, "u1", automation.WorkflowInput{
		Name: "Prices", Steps: []automation.Step{
			{Command: "goto", Args: map[string]any{"url": "https://allowed.test/v1"}},
			{Command: "extract", Args: map[string]any{"selector": ".price"}},
		}}, automation.NoteCreated)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Nothing has run: the figures say so rather than inventing a rate.
	o := getOverview(t, api, owner, prices)
	if o.Status != "enabled" || o.WindowDays != 30 || o.Runs.Total != 0 || o.LastRun != nil {
		t.Fatalf("empty overview = %+v", o)
	}
	if o.SuccessRate.Percent != nil || o.SuccessRate.Finished != 0 {
		t.Fatalf("a workflow with no runs has a success rate: %+v", o.SuccessRate)
	}
	if o.Items.Total != nil || len(o.Steps) != 0 {
		t.Fatalf("a workflow with no runs has items or steps: %+v", o)
	}

	// 2. One succeeded run.
	runID, err := automation.RunWorkflow(ctx, a, hub, wsID, "u1", prices, agentID, "manual", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := waitForRun(t, a, wsID, runID); r.status != "ok" {
		t.Fatalf("run = %+v", r)
	}
	o = getOverview(t, api, owner, prices)
	if o.Runs.Total != 1 || o.Runs.Succeeded != 1 || o.SuccessRate.Finished != 1 || o.SuccessRate.Percent == nil || *o.SuccessRate.Percent != 100 {
		t.Fatalf("after one ok run: %+v", o)
	}
	if o.LastRun == nil || o.LastRun.ID != runID || o.LastRun.Items == nil || *o.LastRun.Items != 2 {
		t.Fatalf("last run = %+v", o.LastRun)
	}
	if o.Items.Total == nil || *o.Items.Total != 2 || o.Items.Runs != 1 {
		t.Fatalf("items = %+v", o.Items)
	}
	if len(o.Steps) != 2 || o.Steps[1].OK != 1 || o.Steps[1].LastItems == nil || *o.Steps[1].LastItems != 2 || o.Steps[0].LastItems != nil {
		t.Fatalf("steps = %+v", o.Steps)
	}

	// 3. Re-run: linked to the original, and it repeats what the original ran,
	// not what the workflow says now.
	if _, err := automation.SaveWorkflow(ctx, a, wsID, "u1", prices, automation.WorkflowInput{
		Name: "Prices", Steps: []automation.Step{
			{Command: "goto", Args: map[string]any{"url": "https://allowed.test/v2"}},
			{Command: "extract", Args: map[string]any{"selector": ".price"}},
		}}, 1); err != nil {
		t.Fatal(err)
	}
	res := rerun(api, owner, runID, map[string]any{})
	if res.Code != 201 {
		t.Fatalf("rerun: %d %s", res.Code, res.Body)
	}
	var created struct {
		RunID   string `json:"run_id"`
		RerunOf string `json:"rerun_of"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.RunID == "" || created.RunID == runID || created.RerunOf != runID {
		t.Fatalf("rerun answer = %+v", created)
	}
	if r := waitForRun(t, a, wsID, created.RunID); r.status != "ok" {
		t.Fatalf("re-run = %+v", r)
	}
	again := getRun(t, api, owner, created.RunID)
	if again.Run.RerunOf != runID || again.Run.WorkflowID != prices || again.Run.WorkflowVersion != 1 || again.Run.Trigger != "manual" {
		t.Fatalf("re-run record = %+v", again.Run)
	}
	if orig := getRun(t, api, owner, runID); orig.Run.RerunOf != "" {
		t.Fatalf("the original claims to be a re-run: %+v", orig.Run)
	}
	var sentURLs []string
	for _, p := range fb.payloads() {
		if p["command"] == "goto" {
			args, _ := p["args"].(map[string]any)
			sentURLs = append(sentURLs, args["url"].(string))
		}
	}
	if strings.Join(sentURLs, ",") != "https://allowed.test/v1,https://allowed.test/v1" {
		t.Fatalf("the browser was sent %v: a re-run must repeat the stored steps, not the edited workflow", sentURLs)
	}
	if len(again.Steps) != 2 || !strings.Contains(again.Steps[0].Args, "/v1") {
		t.Fatalf("re-run steps = %+v", again.Steps)
	}
	// The re-run counts as a run of the workflow, against the version it ran.
	if o = getOverview(t, api, owner, prices); o.Runs.Total != 2 || o.Version != 2 {
		t.Fatalf("after the re-run: %+v", o)
	}
	// Per-step figures are for the CURRENT version only; neither run was of version 2.
	if len(o.Steps) != 0 {
		t.Fatalf("version 2 has no runs yet but the overview has steps: %+v", o.Steps)
	}

	// 4. A failed run says which step, and can be repeated.
	bad, err := automation.CreateWorkflow(ctx, a, wsID, "u1", automation.WorkflowInput{
		Name: "Broken", Steps: []automation.Step{
			{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}},
			{Command: "click", Args: map[string]any{"selector": "#missing"}},
			{Command: "extract", Args: map[string]any{"selector": "body"}},
		}}, automation.NoteCreated)
	if err != nil {
		t.Fatal(err)
	}
	failedID, err := automation.RunWorkflow(ctx, a, hub, wsID, "u1", bad, agentID, "manual", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := waitForRun(t, a, wsID, failedID); r.status != "failed" || r.steps[1].status != "failed" || r.steps[2].status != "skipped" {
		t.Fatalf("failed run = %+v", r)
	}
	o = getOverview(t, api, owner, bad)
	if o.Runs.Failed != 1 || o.SuccessRate.Finished != 1 || o.SuccessRate.Percent == nil || *o.SuccessRate.Percent != 0 {
		t.Fatalf("a failed-only workflow: %+v", o)
	}
	if o.Items.Total != nil {
		t.Fatalf("items for a run that extracted nothing: %+v", o.Items)
	}
	if o.LastRun == nil || o.LastRun.Status != "failed" || len(o.Steps) != 3 || o.Steps[1].LastStatus != "failed" || o.Steps[2].Ran != 0 {
		t.Fatalf("failed overview = %+v %+v", o.LastRun, o.Steps)
	}
	res = rerun(api, owner, failedID, map[string]any{})
	if res.Code != 201 {
		t.Fatalf("rerun of a failed run: %d %s", res.Code, res.Body)
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	waitForRun(t, a, wsID, created.RunID)

	// 5. A secret value is never stored, so such a run cannot be repeated.
	secret, err := automation.CreateWorkflow(ctx, a, wsID, "u1", automation.WorkflowInput{
		Name: "Login", Params: []automation.Param{{Name: "pw", Secret: true, Required: true}},
		Steps: []automation.Step{
			{Command: "goto", Args: map[string]any{"url": "https://allowed.test/login"}},
			{Command: "type", Args: map[string]any{"selector": "#pw", "text": "{{pw}}"}},
		}}, automation.NoteCreated)
	if err != nil {
		t.Fatal(err)
	}
	secretRun, err := automation.RunWorkflow(ctx, a, hub, wsID, "u1", secret, agentID, "manual", map[string]string{"pw": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, a, wsID, secretRun)
	before := len(fb.payloads())
	if res := rerun(api, owner, secretRun, map[string]any{}); res.Code != 422 || strings.Contains(res.Body.String(), "hunter2") {
		t.Fatalf("secret rerun: %d %s", res.Code, res.Body)
	}
	if len(fb.payloads()) != before {
		t.Fatal("a secret run was repeated with its mask")
	}

	// 6. A workflow that writes asks first.
	if _, err := a.SysDB.ExecContext(ctx, `UPDATE automation_workflows SET writes = true WHERE workspace_id = $1 AND id = $2`, wsID, prices); err != nil {
		t.Fatal(err)
	}
	if res := rerun(api, owner, runID, map[string]any{}); res.Code != 409 {
		t.Fatalf("writing workflow without confirm: %d %s", res.Code, res.Body)
	}
	res = rerun(api, owner, runID, map[string]any{"confirm": true})
	if res.Code != 201 {
		t.Fatalf("writing workflow with confirm: %d %s", res.Code, res.Body)
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	waitForRun(t, a, wsID, created.RunID)

	// 7. Switched off means off.
	if _, err := a.SysDB.ExecContext(ctx, `UPDATE automation_workflows SET enabled = false, writes = false WHERE workspace_id = $1 AND id = $2`, wsID, prices); err != nil {
		t.Fatal(err)
	}
	if res := rerun(api, owner, runID, map[string]any{}); res.Code != 409 {
		t.Fatalf("disabled workflow: %d %s", res.Code, res.Body)
	}
	if o = getOverview(t, api, owner, prices); o.Status != "paused" {
		t.Fatalf("status = %q, want paused", o.Status)
	}

	// 8. Who may do what, and whose runs these are.
	memberAPI, member := overviewAPI(t, a, hub, wsID, "member")
	if res := rerun(memberAPI, member, failedID, map[string]any{}); res.Code != 403 {
		t.Fatalf("member rerun: %d, want 403", res.Code)
	}
	if res := memberAPI.GetCtx(member, "/api/w/workflows/"+bad+"/overview"); res.Code != 200 {
		t.Fatalf("member overview: %d", res.Code)
	}
	otherWS, _ := workspace(t, a)
	otherAPI, other := overviewAPI(t, a, hub, otherWS, "owner")
	if res := rerun(otherAPI, other, failedID, map[string]any{}); res.Code != 404 {
		t.Fatalf("another workspace's run: %d, want 404", res.Code)
	}
	if res := otherAPI.GetCtx(other, "/api/w/workflows/"+bad+"/overview"); res.Code != 404 {
		t.Fatalf("another workspace's workflow overview: %d, want 404", res.Code)
	}
	if res := rerun(api, owner, "no-such-run", map[string]any{}); res.Code != 404 {
		t.Fatalf("unknown run: %d, want 404", res.Code)
	}
}
