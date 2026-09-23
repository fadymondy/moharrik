package automation_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Editing workflows (MH-400), against a real database and — for the picker —
a fake browser on a real socket.

  - a save is a new version, a save against a stale version is refused, and a
    restore is itself a new version whose content is the old one's
  - a recording starts its history as version 1
  - the picker is a direct command that passes the guardrails a run does
*/

func wfInput(name string, urls ...string) automation.WorkflowInput {
	in := automation.WorkflowInput{Name: name}
	for _, u := range urls {
		in.Steps = append(in.Steps, automation.Step{Command: "goto", Args: map[string]any{"url": u}})
	}
	in.Steps = append(in.Steps, automation.Step{Command: "click", Args: map[string]any{"selector": "#go"}})
	return in
}

func TestEditingASaveIsANewVersionAndRestoreIsToo(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	ctx := context.Background()

	id, err := automation.CreateWorkflow(ctx, a, wsID, "u1", wfInput("Report", "https://example.com/v1"), automation.NoteCreated)
	if err != nil {
		t.Fatal(err)
	}

	edited := wfInput("Report", "https://example.com/v2")
	edited.Steps[0], edited.Steps[1] = edited.Steps[1], edited.Steps[0] // reordered
	v, err := automation.SaveWorkflow(ctx, a, wsID, "u2", id, edited, 1)
	if err != nil || v != 2 {
		t.Fatalf("save: v=%d err=%v", v, err)
	}

	// A second editor who opened version 1 does not overwrite version 2.
	if _, err := automation.SaveWorkflow(ctx, a, wsID, "u3", id, wfInput("Report", "https://example.com/stale"), 1); !errors.Is(err, automation.ErrStaleVersion) {
		t.Fatalf("stale save: %v, want ErrStaleVersion", err)
	}

	v, err = automation.RestoreWorkflow(ctx, a, wsID, "u1", id, 1, 2)
	if err != nil || v != 3 {
		t.Fatalf("restore: v=%d err=%v", v, err)
	}

	list, err := automation.ListWorkflowVersions(ctx, a, wsID, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Version != 3 || list[2].Version != 1 {
		t.Fatalf("versions: %+v", list)
	}
	notes := []string{list[0].Note, list[1].Note, list[2].Note}
	if strings.Join(notes, ",") != "restored,edited,created" {
		t.Errorf("notes = %v", notes)
	}
	if list[0].RestoredFrom == nil || *list[0].RestoredFrom != 1 {
		t.Errorf("v3 restored_from = %v, want 1", list[0].RestoredFrom)
	}
	// v2 keeps the reorder; v3 is v1's content again.
	if list[1].Steps[0].Command != "click" {
		t.Errorf("v2 steps = %+v, want the reordered click first", list[1].Steps)
	}
	var steps string
	var version int
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, wsID), `
		SELECT steps::text, version FROM automation_workflows WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&steps, &version); err != nil {
		t.Fatal(err)
	}
	if version != 3 || !strings.Contains(steps, "example.com/v1") || strings.Index(steps, "goto") > strings.Index(steps, "click") {
		t.Errorf("current = v%d %s, want v3 with v1's steps in v1's order", version, steps)
	}

	if _, err := automation.RestoreWorkflow(ctx, a, wsID, "u1", id, 9, 0); !errors.Is(err, automation.ErrNoVersion) {
		t.Errorf("restoring a version that does not exist: %v", err)
	}
	// Another workspace cannot save over it.
	other, _ := workspace(t, a)
	if _, err := automation.SaveWorkflow(ctx, a, other, "u9", id, wfInput("Hijack", "https://example.com/"), 0); !errors.Is(err, automation.ErrNoWorkflow) {
		t.Errorf("save from another workspace: %v, want ErrNoWorkflow", err)
	}
}

func TestEditingValidationRefusesWhatWouldNeverRun(t *testing.T) {
	cases := map[string]automation.WorkflowInput{
		"no steps":       {Name: "x"},
		"unknown":        {Name: "x", Steps: []automation.Step{{Command: "hover"}}},
		"bad param name": {Name: "x", Steps: []automation.Step{{Command: "wait"}}, Params: []automation.Param{{Name: "a-b"}}},
		"twice":          {Name: "x", Steps: []automation.Step{{Command: "wait"}}, Params: []automation.Param{{Name: "a"}, {Name: "a"}}},
		"secret default": {Name: "x", Steps: []automation.Step{{Command: "wait"}}, Params: []automation.Param{{Name: "pw", Secret: true, Default: "hunter2"}}},
	}
	for name, in := range cases {
		if automation.ValidateWorkflow(in) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := automation.WorkflowInput{Name: "x", Steps: []automation.Step{{Command: "Navigate"}, {Command: "press"}, {Command: "extract"}},
		Params: []automation.Param{{Name: "pw", Secret: true}, {Name: "who", Default: "me"}}}
	if err := automation.ValidateWorkflow(ok); err != nil {
		t.Errorf("a valid workflow was refused: %v", err)
	}
}

func TestEditingARecordingStartsAtVersionOne(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a)
	saved, err := automation.SaveRecording(context.Background(), a, wsID, agentID, recording(
		automation.RecordedStep{Command: "click", Args: rawArgs(t, map[string]any{"selector": "#a"})},
	))
	if err != nil {
		t.Fatal(err)
	}
	list, err := automation.ListWorkflowVersions(context.Background(), a, wsID, saved.WorkflowID)
	if err != nil || len(list) != 1 || list[0].Version != 1 || list[0].Note != "recorded" {
		t.Fatalf("a recording's history: %+v %v", list, err)
	}
}

// ---- through the routes ----------------------------------------------------

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func routes(t *testing.T, a *app.App, hub *automation.Hub, wsID, role string) (humatest.TestAPI, context.Context) {
	t.Helper()
	_, api := humatest.New(t)
	automation.Register(api, a, hub)
	automation.RegisterWorkflows(api, a, hub)
	automation.RegisterSchedules(api, a, hub)
	automation.RegisterPick(api, a, hub)
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-" + role, Role: role, Via: "session"})
	return api, ctx
}

func TestEditingRoutesPutVersionsAndRestore(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	api, ctx := routes(t, a, automation.NewHub(quietLog()), wsID, "owner")

	res := api.PostCtx(ctx, "/api/w/workflows", map[string]any{
		"name": "Routes", "steps": []map[string]any{{"command": "goto", "args": map[string]any{"url": "https://example.com/{{page}}"}}},
	})
	if res.Code != 201 {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var created struct{ Workflow automation.Workflow }
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	id := created.Workflow.ID
	if created.Workflow.Version != 1 {
		t.Fatalf("created at version %d", created.Workflow.Version)
	}

	put := func(base int, url string) *httptest.ResponseRecorder {
		return api.PutCtx(ctx, "/api/w/workflows/"+id, map[string]any{
			"name": "Routes", "base_version": base,
			"steps":  []map[string]any{{"command": "goto", "args": map[string]any{"url": url}}, {"command": "click", "args": map[string]any{"selector": "#x"}}},
			"params": []map[string]any{{"name": "page", "label": "Page", "required": true}},
		})
	}
	if r := put(1, "https://example.com/{{page}}/edited"); r.Code != 200 {
		t.Fatalf("put: %d %s", r.Code, r.Body)
	}
	if r := put(1, "https://example.com/late"); r.Code != 409 {
		t.Fatalf("stale put: %d, want 409", r.Code)
	}
	if r := api.PutCtx(ctx, "/api/w/workflows/"+id, map[string]any{"name": "Routes", "steps": []map[string]any{{"command": "hover"}}}); r.Code != 422 {
		t.Fatalf("unknown command: %d, want 422", r.Code)
	}

	res = api.GetCtx(ctx, "/api/w/workflows/"+id+"/versions")
	var versions struct{ Versions []automation.WorkflowVersion }
	_ = json.Unmarshal(res.Body.Bytes(), &versions)
	if res.Code != 200 || len(versions.Versions) != 2 {
		t.Fatalf("versions: %d %s", res.Code, res.Body)
	}

	res = api.PostCtx(ctx, "/api/w/workflows/"+id+"/versions/1/restore", map[string]any{"base_version": 2})
	var restored struct{ Workflow automation.Workflow }
	_ = json.Unmarshal(res.Body.Bytes(), &restored)
	if res.Code != 200 || restored.Workflow.Version != 3 || len(restored.Workflow.Steps) != 1 {
		t.Fatalf("restore: %d %s", res.Code, res.Body)
	}
	if r := api.PostCtx(ctx, "/api/w/workflows/"+id+"/versions/nope/restore", map[string]any{}); r.Code != 404 {
		t.Fatalf("restore of a non-number: %d, want 404", r.Code)
	}

	// A member can read but not edit.
	api2, memberCtx := routes(t, a, automation.NewHub(quietLog()), wsID, "member")
	if r := api2.PutCtx(memberCtx, "/api/w/workflows/"+id, map[string]any{"name": "M", "steps": []map[string]any{{"command": "wait"}}}); r.Code != 403 {
		t.Fatalf("member put: %d, want 403", r.Code)
	}
}

// ---- the picker ------------------------------------------------------------

func TestEditingPickIsADirectCommandBehindTheGuardrails(t *testing.T) {
	a := testApp(t)
	var got map[string]any
	hub, wsID, agentID, fb := connectBrowser(t, a, "{allowed.test}", func(int, map[string]any) automation.Envelope {
		return ok(`{}`)
	})
	fb.setOther(func(env automation.Envelope) *automation.Envelope {
		if env.Type != "pick" {
			return nil
		}
		_ = json.Unmarshal(env.Payload, &got)
		return &automation.Envelope{Payload: json.RawMessage(`{"selector":"#login-button","tag":"button","text":"  Sign   in  ","host":"allowed.test"}`)}
	})
	api, ctx := routes(t, a, hub, wsID, "owner")

	res := api.PostCtx(ctx, "/api/w/browsers/"+agentID+"/pick", map[string]any{"url": "https://www.allowed.test/login"})
	if res.Code != http.StatusAccepted {
		t.Fatalf("pick: %d %s", res.Code, res.Body)
	}
	var started struct{ Pick automation.Pick }
	_ = json.Unmarshal(res.Body.Bytes(), &started)

	var pick automation.Pick
	for i := 0; i < 100; i++ {
		r := api.GetCtx(ctx, "/api/w/browsers/"+agentID+"/pick/"+started.Pick.ID)
		var body struct{ Pick automation.Pick }
		_ = json.Unmarshal(r.Body.Bytes(), &body)
		pick = body.Pick
		if pick.Status != "waiting" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pick.Status != "picked" || pick.Result == nil || pick.Result.Selector != "#login-button" || pick.Result.Text != "Sign in" {
		t.Fatalf("pick = %+v", pick)
	}
	// The browser was told where it may go, so it can check where the page lands.
	if hosts, _ := got["allowed_hosts"].([]any); len(hosts) != 1 || hosts[0] != "allowed.test" || got["url"] != "https://www.allowed.test/login" {
		t.Errorf("pick payload = %v", got)
	}

	// Off the allowed sites: refused before anything reaches the browser.
	if r := api.PostCtx(ctx, "/api/w/browsers/"+agentID+"/pick", map[string]any{"url": "https://evil.test/"}); r.Code != 422 {
		t.Errorf("off-list pick: %d, want 422", r.Code)
	}
	// A placeholder is not a page.
	if r := api.PostCtx(ctx, "/api/w/browsers/"+agentID+"/pick", map[string]any{"url": "https://allowed.test/{{page}}"}); r.Code != 422 {
		t.Errorf("placeholder pick: %d, want 422", r.Code)
	}
	// Another workspace cannot read this pick.
	other, _ := workspace(t, a)
	api3, otherCtx := routes(t, a, hub, other, "owner")
	if r := api3.GetCtx(otherCtx, "/api/w/browsers/"+agentID+"/pick/"+started.Pick.ID); r.Code != 404 {
		t.Errorf("another workspace reading the pick: %d, want 404", r.Code)
	}
	// The kill switch: refused.
	if err := automation.Pause(context.Background(), a, wsID, "", "test"); err != nil {
		t.Fatal(err)
	}
	if r := api.PostCtx(ctx, "/api/w/browsers/"+agentID+"/pick", map[string]any{"url": "https://allowed.test/"}); r.Code != 409 {
		t.Errorf("pick while stopped: %d, want 409", r.Code)
	}
}
