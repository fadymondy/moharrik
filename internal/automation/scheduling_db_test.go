package automation_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/automation"
)

/*
Schedules of saved workflows (MH-403), against a real database and a fake
browser on a real socket: the secret value is sealed, never handed back, and
filled in only at the moment the schedule runs; the preview lists the next
five times in the schedule's zone, the workspace's by default.
*/

// ---- schedules of saved workflows (MH-403) ---------------------------------

func TestSchedulingAWorkflowSealsItsSecretAndFillsItAtRunTime(t *testing.T) {
	a := testApp(t)
	var mu = make(chan map[string]any, 10)
	hub, wsID, agentID, _ := connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		mu <- p
		return ok(`{"summary":"fine"}`)
	})
	api, ctx := routes(t, a, hub, wsID, "owner")

	res := api.PostCtx(ctx, "/api/w/workflows", map[string]any{
		"name": "Login " + strconv.FormatInt(time.Now().UnixNano(), 36),
		"steps": []map[string]any{
			{"command": "goto", "args": map[string]any{"url": "https://allowed.test/{{page}}"}},
			{"command": "type", "args": map[string]any{"selector": "#pw", "text": "{{password}}"}},
		},
		"params": []map[string]any{{"name": "page", "required": true}, {"name": "password", "required": true, "secret": true}},
	})
	var wf struct{ Workflow automation.Workflow }
	_ = json.Unmarshal(res.Body.Bytes(), &wf)
	if res.Code != 201 {
		t.Fatalf("workflow: %d %s", res.Code, res.Body)
	}

	const password = "correct-horse-battery-staple"
	res = api.PostCtx(ctx, "/api/w/schedules", map[string]any{
		"name": "Every morning", "agent_id": agentID, "cron": "0 7 * * 1-5", "timezone": "Africa/Cairo",
		"workflow_id": wf.Workflow.ID, "values": map[string]string{"page": "inbox", "password": password},
	})
	if res.Code != 201 {
		t.Fatalf("schedule: %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), password) {
		t.Fatalf("the schedule handed its secret back: %s", res.Body)
	}
	var sc struct{ Schedule automation.Schedule }
	_ = json.Unmarshal(res.Body.Bytes(), &sc)
	if sc.Schedule.WorkflowName == "" || sc.Schedule.Values["page"] != "inbox" || len(sc.Schedule.SecretParams) != 1 || sc.Schedule.SecretParams[0] != "password" {
		t.Fatalf("schedule = %+v", sc.Schedule)
	}

	// Not in the clear at rest.
	var params, sealed string
	if err := a.SysDB.QueryRow(`SELECT params::text, secret_values FROM automation_schedules WHERE id = $1`, sc.Schedule.ID).Scan(&params, &sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(params, password) || strings.Contains(sealed, password) || sealed == "" {
		t.Fatalf("stored in the clear: params=%s sealed=%q", params, sealed)
	}
	if r := api.GetCtx(ctx, "/api/w/schedules"); strings.Contains(r.Body.String(), password) {
		t.Fatalf("the schedule list handed the secret back")
	}

	// Due now: the scheduler fills the secret in for the browser only.
	if _, err := a.SysDB.Exec(`UPDATE automation_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sc.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	hub.Tick(context.Background(), a, time.Now().UTC())

	var first, second map[string]any
	select {
	case first = <-mu:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduled workflow never reached the browser (was it 'missed: no such workflow'?)")
	}
	second = <-mu
	if first["args"].(map[string]any)["url"] != "https://allowed.test/inbox" || second["args"].(map[string]any)["text"] != password {
		t.Fatalf("the browser was sent %v then %v", first, second)
	}

	var runID string
	for i := 0; i < 100 && runID == ""; i++ {
		_ = a.SysDB.QueryRow(`SELECT last_run_id FROM automation_schedules WHERE id = $1`, sc.Schedule.ID).Scan(&runID)
		time.Sleep(20 * time.Millisecond)
	}
	run := waitForRun(t, a, wsID, runID)
	if run.status != "ok" {
		t.Fatalf("scheduled run: %+v", run)
	}
	var stored string
	if err := a.SysDB.QueryRow(`
		SELECT r.params::text || string_agg(s.args::text, '') FROM automation_runs r
		JOIN automation_run_steps s ON s.run_id = r.id WHERE r.id = $1 GROUP BY r.params`, runID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, password) {
		t.Fatalf("the run record kept the secret: %s", stored)
	}

	// Run now, by hand, runs the workflow too — not the schedule's empty steps.
	res = api.PostCtx(ctx, "/api/w/schedules/"+sc.Schedule.ID+"/run", map[string]any{})
	if res.Code != 201 {
		t.Fatalf("run now: %d %s", res.Code, res.Body)
	}

	// The workflow cannot be deleted from under its schedule.
	if r := api.DeleteCtx(ctx, "/api/w/workflows/"+wf.Workflow.ID); r.Code != 409 {
		t.Errorf("deleting a scheduled workflow: %d, want 409", r.Code)
	}
}

func TestSchedulePreviewListsTheNextFiveInTheZone(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	api, ctx := routes(t, a, automation.NewHub(quietLog()), wsID, "member")

	res := api.GetCtx(ctx, "/api/w/schedules/preview?cron=0+7+*+*+1-5&timezone=Africa/Cairo")
	var p automation.SchedulePreview
	_ = json.Unmarshal(res.Body.Bytes(), &p)
	if res.Code != 200 || !p.Valid || len(p.Times) != 5 {
		t.Fatalf("preview: %d %s", res.Code, res.Body)
	}
	cairo, _ := time.LoadLocation("Africa/Cairo")
	for _, at := range p.Times {
		local := at.In(cairo)
		if local.Hour() != 7 || local.Minute() != 0 || local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
			t.Errorf("%s is not 07:00 on a weekday in Cairo", local)
		}
	}

	// The workspace's own zone when none is given.
	if _, err := a.SysDB.Exec(`UPDATE workspaces SET timezone = 'Asia/Tokyo' WHERE id = $1`, wsID); err != nil {
		t.Fatal(err)
	}
	res = api.GetCtx(ctx, "/api/w/schedules/preview?cron=@daily")
	_ = json.Unmarshal(res.Body.Bytes(), &p)
	if p.Timezone != "Asia/Tokyo" || len(p.Times) != 5 {
		t.Fatalf("default zone preview: %s", res.Body)
	}

	// Half-typed: answered, not refused.
	res = api.GetCtx(ctx, "/api/w/schedules/preview?cron=0+7")
	_ = json.Unmarshal(res.Body.Bytes(), &p)
	if res.Code != 200 || p.Valid || p.Error == "" {
		t.Fatalf("invalid preview: %d %s", res.Code, res.Body)
	}
}
