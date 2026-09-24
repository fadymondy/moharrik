package automation_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Workflows, schedules, jobs and runs at fadymondy.com's parity (MH-478 phase 3),
against a real database and — where a browser is involved — a fake one on a
real socket.
*/

// member makes a real user a member of the workspace with a role, and returns
// the id the routes act as. A job runs as the member who scheduled it, with
// the role they hold when it fires.
func member(t *testing.T, a *app.App, wsID, role string) string {
	t.Helper()
	id := "u-" + role + "-" + itoa(time.Now().UnixNano())
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text)`, id, id+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, wsID, id, role); err != nil {
		t.Fatal(err)
	}
	return id
}

func routesAs(t *testing.T, a *app.App, hub *automation.Hub, wsID, userID, role string) (func(method, path string, body any) (int, string), context.Context) {
	t.Helper()
	api, _ := routes(t, a, hub, wsID, role)
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: userID, Role: role, Via: "session"})
	return func(method, path string, body any) (int, string) {
		switch method {
		case "GET":
			r := api.GetCtx(ctx, path)
			return r.Code, r.Body.String()
		case "PATCH":
			r := api.PatchCtx(ctx, path, body)
			return r.Code, r.Body.String()
		default:
			r := api.PostCtx(ctx, path, body)
			return r.Code, r.Body.String()
		}
	}, ctx
}

func scheduleRow(t *testing.T, a *app.App, wsID, id string) (status, why string, failures, misses int, next time.Time) {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT last_status, last_error, failure_count, missed_count, next_run_at FROM automation_schedules WHERE workspace_id = $1 AND id = $2`,
		wsID, id).Scan(&status, &why, &failures, &misses, &next); err != nil {
		t.Fatal(err)
	}
	return
}

func TestParityJobScheduleRunsOnTheClockAsItsCreator(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	hub := automation.NewHub(quietLog())
	var calls atomic.Int32
	var sawUser, sawValue atomic.Value
	automation.RegisterJob(automation.Job{
		Key: "test.echo", Permission: "trends:manage",
		Params: []automation.Param{{Name: "word", Required: true}},
		Run: func(ctx context.Context, a *app.App, in automation.JobRun) automation.JobResult {
			// The test database outlives a run: echo schedules of earlier runs'
			// workspaces come due too. Only this workspace's are counted.
			if in.WorkspaceID != wsID {
				return automation.JobResult{Status: automation.JobSkipped}
			}
			calls.Add(1)
			sawUser.Store(in.UserID)
			sawValue.Store(in.Values["word"])
			if in.Values["word"] == "fail" {
				return automation.JobResult{Status: automation.JobFailed, Detail: "asked to fail"}
			}
			return automation.JobResult{Status: automation.JobOK, Detail: "said " + in.Values["word"]}
		},
	})

	// A plain member may not put on a clock what they may not do by hand.
	memberID := member(t, a, wsID, "member")
	call, _ := routesAs(t, a, hub, wsID, memberID, "member")
	if code, body := call("POST", "/api/w/schedules", map[string]any{"name": "m", "cron": "* * * * *", "job_key": "test.echo", "values": map[string]string{"word": "hi"}}); code != 403 {
		t.Fatalf("a member scheduling a manager's job: %d %s", code, body)
	}
	managerID := member(t, a, wsID, "manager")
	call, _ = routesAs(t, a, hub, wsID, managerID, "manager")
	if code, body := call("GET", "/api/w/automation/jobs", nil); code != 200 || !strings.Contains(body, `"key":"test.echo"`) {
		t.Fatalf("jobs list: %d %s", code, body)
	}
	if code, body := call("POST", "/api/w/schedules", map[string]any{"name": "no value", "cron": "* * * * *", "job_key": "test.echo"}); code != 422 {
		t.Fatalf("a job without its required value: %d %s", code, body)
	}
	if code, body := call("POST", "/api/w/schedules", map[string]any{"name": "unknown", "cron": "* * * * *", "job_key": "no.such.job"}); code != 422 {
		t.Fatalf("an unknown job: %d %s", code, body)
	}
	code, body := call("POST", "/api/w/schedules", map[string]any{"name": "Echo", "cron": "*/5 * * * *", "job_key": "test.echo", "values": map[string]string{"word": "hello"}})
	if code != 201 {
		t.Fatalf("job schedule: %d %s", code, body)
	}
	var sc struct{ Schedule automation.Schedule }
	_ = json.Unmarshal([]byte(body), &sc)
	if sc.Schedule.JobKey != "test.echo" || sc.Schedule.AgentID != "" {
		t.Fatalf("schedule = %+v", sc.Schedule)
	}

	// Due: the clock fires it, as its creator, with its value.
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	due := func() {
		_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sc.Schedule.ID)
	}
	due()
	hub.Tick(context.Background(), a, time.Now().UTC())
	automation.WaitJobs()
	if st, why, _, _, next := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "ok" || why != "said hello" || !next.After(time.Now()) {
		t.Fatalf("after the tick: %s %q next %s", st, why, next)
	}
	if calls.Load() != 1 || sawUser.Load() != managerID || sawValue.Load() != "hello" {
		t.Fatalf("the job ran %d times as %v with %v", calls.Load(), sawUser.Load(), sawValue.Load())
	}

	// The kill switch pauses it and keeps it due; resuming runs it.
	if err := automation.Pause(context.Background(), a, wsID, managerID, "test"); err != nil {
		t.Fatal(err)
	}
	due()
	hub.Tick(context.Background(), a, time.Now().UTC())
	automation.WaitJobs()
	if st, _, _, _, next := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "paused" || next.After(time.Now()) {
		t.Fatalf("paused job: %s next %s", st, next)
	}
	if calls.Load() != 1 {
		t.Fatal("a job ran while automation was stopped")
	}
	_ = automation.Resume(context.Background(), a, wsID)
	hub.Tick(context.Background(), a, time.Now().UTC())
	automation.WaitJobs()
	if st, _, _, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "ok" || calls.Load() != 2 {
		t.Fatalf("after resuming: %s, %d calls", st, calls.Load())
	}

	// Run now, by hand.
	if code, body := call("POST", "/api/w/schedules/"+sc.Schedule.ID+"/run", map[string]any{}); code != 201 {
		t.Fatalf("run now: %d %s", code, body)
	}
	automation.WaitJobs()
	if calls.Load() != 3 {
		t.Fatalf("run now ran %d times in all", calls.Load())
	}

	// Failures are counted; five in a row mark the schedule failing.
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE automation_schedules SET params = '{"word":"fail"}' WHERE id = $1`, sc.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		due()
		hub.Tick(context.Background(), a, time.Now().UTC())
		automation.WaitJobs()
	}
	if st, why, failures, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "failed" || why != "asked to fail" || failures != 5 {
		t.Fatalf("after five failures: %s %q %d", st, why, failures)
	}
	_, body = call("GET", "/api/w/schedules", nil)
	if !strings.Contains(body, `"failing":true`) {
		t.Fatalf("five failures do not read as failing: %s", body)
	}

	// A creator who loses the right loses it on the clock too.
	if _, err := a.SysDB.Exec(`UPDATE workspace_members SET role = 'member' WHERE workspace_id = $1 AND user_id = $2`, wsID, managerID); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	due()
	hub.Tick(context.Background(), a, time.Now().UTC())
	automation.WaitJobs()
	if st, why, _, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "failed" || !strings.Contains(why, "may no longer") || calls.Load() != before {
		t.Fatalf("a demoted creator's job: %s %q", st, why)
	}

	// A job this server does not run is missed, never disabled.
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE automation_schedules SET job_key = 'gone.away' WHERE id = $1`, sc.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	due()
	hub.Tick(context.Background(), a, time.Now().UTC())
	if st, why, _, misses, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "missed" || !strings.Contains(why, "does not run") || misses < 1 {
		t.Fatalf("an unknown job: %s %q %d", st, why, misses)
	}
}

func TestParityAnyBrowserScheduleAndItsRunRecordTheSchedule(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, _ := connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		return ok(`{"summary":"fine"}`)
	})
	// A second, offline browser that sorts first: passed over.
	if _, err := a.SysDB.Exec(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts, created_at)
		VALUES ($1, 'offline one', $2, '', '{allowed.test}', now() - interval '1 day')`, wsID, "offline-"+itoa(time.Now().UnixNano())); err != nil {
		t.Fatal(err)
	}
	ownerID := member(t, a, wsID, "owner")
	call, _ := routesAs(t, a, hub, wsID, ownerID, "owner")

	code, body := call("POST", "/api/w/workflows", map[string]any{
		"name": "Check the inbox", "steps": []map[string]any{{"command": "goto", "args": map[string]any{"url": "https://allowed.test/inbox"}}},
	})
	if code != 201 {
		t.Fatalf("workflow: %d %s", code, body)
	}
	var wf struct{ Workflow automation.Workflow }
	_ = json.Unmarshal([]byte(body), &wf)
	if wf.Workflow.Slug != "check-the-inbox" || !wf.Workflow.Enabled {
		t.Fatalf("workflow = %+v", wf.Workflow)
	}
	// A second of the same name gets its own slug.
	_, body2 := call("POST", "/api/w/workflows", map[string]any{
		"name": "Check the Inbox!", "steps": []map[string]any{{"command": "goto", "args": map[string]any{"url": "https://allowed.test/"}}},
	})
	if !strings.Contains(body2, `"slug":"check-the-inbox-2"`) {
		t.Fatalf("second slug: %s", body2)
	}
	// No browser allows the host: refused when saved, not at 4am.
	if code, body := call("POST", "/api/w/schedules", map[string]any{"name": "evil", "cron": "0 7 * * *",
		"steps": []map[string]any{{"command": "goto", "args": map[string]any{"url": "https://evil.test/"}}}}); code != 422 {
		t.Fatalf("an any-browser schedule no browser may run: %d %s", code, body)
	}
	code, body = call("POST", "/api/w/schedules", map[string]any{"name": "Any", "cron": "0 7 * * *", "workflow_id": wf.Workflow.ID})
	if code != 201 {
		t.Fatalf("any-browser schedule: %d %s", code, body)
	}
	var sc struct{ Schedule automation.Schedule }
	_ = json.Unmarshal([]byte(body), &sc)

	sctx := tenant.WithWorkspace(context.Background(), wsID)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sc.Schedule.ID)
	hub.Tick(context.Background(), a, time.Now().UTC())
	var runID, ranOn, scheduleID string
	var version int
	for i := 0; i < 100; i++ {
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT id, agent_id, schedule_id, workflow_version FROM automation_runs WHERE workspace_id = $1 AND workflow_id = $2`,
			wsID, wf.Workflow.ID).Scan(&runID, &ranOn, &scheduleID, &version)
		if runID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ranOn != agentID || scheduleID != sc.Schedule.ID || version != 1 {
		t.Fatalf("the run: on %s (want %s), schedule %q, version %d", ranOn, agentID, scheduleID, version)
	}
	waitForRun(t, a, wsID, runID)
	for i := 0; i < 50; i++ {
		if st, _, _, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st == "ok" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st, _, _, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "ok" {
		t.Fatalf("the schedule was not settled by its run: %s", st)
	}

	// Filtered runs: by workflow and by schedule, with the names a person reads.
	_, body = call("GET", "/api/w/runs?workflow="+wf.Workflow.ID, nil)
	if !strings.Contains(body, `"workflow_name":"Check the inbox"`) || !strings.Contains(body, `"schedule_name":"Any"`) || !strings.Contains(body, `"workflow_version":1`) {
		t.Fatalf("runs by workflow: %s", body)
	}
	if _, body = call("GET", "/api/w/runs?schedule=nope", nil); !strings.Contains(body, `"runs":[]`) {
		t.Fatalf("runs by another schedule: %s", body)
	}

	// Turned off: the workflow keeps its history and refuses to run.
	if code, body := call("PATCH", "/api/w/workflows/"+wf.Workflow.ID, map[string]any{"enabled": false}); code != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("turning it off: %d %s", code, body)
	}
	if code, body := call("POST", "/api/w/workflows/"+wf.Workflow.ID+"/run", map[string]any{"agent_id": agentID}); code != 409 || !strings.Contains(body, "turned off") {
		t.Fatalf("running a workflow that is off: %d %s", code, body)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1`, sc.Schedule.ID)
	hub.Tick(context.Background(), a, time.Now().UTC())
	if st, why, failures, _, _ := scheduleRow(t, a, wsID, sc.Schedule.ID); st != "failed" || !strings.Contains(why, "turned off") || failures != 1 {
		t.Fatalf("a schedule of a workflow that is off: %s %q %d", st, why, failures)
	}
	// And by slug, the way a tool call names it.
	if w, err := automation.FindWorkflow(context.Background(), a, wsID, "CHECK-THE-INBOX"); err != nil || w.ID != wf.Workflow.ID {
		t.Fatalf("by slug: %v %+v", err, w)
	}
}

func TestParityRunsPerHourAndRunData(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, _ := connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		switch p["command"] {
		case "extract":
			args, _ := p["args"].(map[string]any)
			if args["list"] != nil {
				return ok(`{"ok":true,"rows":[{"name":"Order 1","total":"120"},{"name":"طلب ٢","total":"80","note":"rush"}],"total":2,"summary":"2 rows"}`)
			}
			return ok(`{"ok":true,"value":"Orders for today","summary":"Orders for today"}`)
		}
		return ok(`{"ok":true,"summary":"fine"}`)
	})
	runID, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}},
		{Command: "extract", Args: map[string]any{"selector": "h1"}},
		{Command: "extract", Args: map[string]any{"list": "li", "fields": map[string]any{"name": "a", "total": ".t"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, a, wsID, runID)

	data, err := automation.ReadRunData(context.Background(), a, wsID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Steps) != 2 || len(data.Rows) != 2 || len(data.Values) != 1 || string(data.Values[0]) != `"Orders for today"` {
		t.Fatalf("run data: %+v", data)
	}
	csv := string(data.CSV())
	// Columns in the order fields first appear (each row's own, sorted).
	if csv != "name,total,note\nOrder 1,120,\nطلب ٢,80,rush\n" {
		t.Fatalf("csv: %q", csv)
	}
	if _, err := automation.ReadRunData(context.Background(), a, "another-workspace", runID); err == nil {
		t.Fatal("another workspace read this run's data")
	}

	// The hourly ceiling: set to what this browser has already had.
	if err := automation.SetLimits(context.Background(), a, wsID, automation.Limits{MaxParallelRuns: 3, RetentionDays: 30, RunsPerHour: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}}}); err == nil || !strings.Contains(err.Error(), "in the last hour") {
		t.Fatalf("a run over the hourly ceiling: %v", err)
	}
	if l := automation.ReadLimits(context.Background(), a, wsID); l.RunsPerHour != 1 {
		t.Fatalf("limits: %+v", l)
	}
}
