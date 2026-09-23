package automation_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/automation"
)

/*
Editing a schedule (MH-471): the time, the values, the workflow and the browser
change from the page, and a sealed secret is kept unless it is replaced. Before
this a schedule could only be paused or re-timed, and changing anything else
meant deleting it and entering the password again.
*/

func TestEditingAScheduleKeepsItsSecretUnlessReplaced(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a) // allowed host: example.com
	api, ctx := routes(t, a, automation.NewHub(quietLog()), wsID, "owner")

	mkWorkflow := func(name, host string) automation.Workflow {
		t.Helper()
		res := api.PostCtx(ctx, "/api/w/workflows", map[string]any{
			"name": name + " " + strconv.FormatInt(time.Now().UnixNano(), 36),
			"steps": []map[string]any{
				{"command": "goto", "args": map[string]any{"url": "https://" + host + "/{{page}}"}},
				{"command": "type", "args": map[string]any{"selector": "#pw", "text": "{{password}}"}},
			},
			"params": []map[string]any{{"name": "page", "required": true}, {"name": "password", "required": true, "secret": true}},
		})
		if res.Code != 201 {
			t.Fatalf("workflow: %d %s", res.Code, res.Body)
		}
		var wf struct{ Workflow automation.Workflow }
		_ = json.Unmarshal(res.Body.Bytes(), &wf)
		return wf.Workflow
	}
	wf1, wf2 := mkWorkflow("Login", "example.com"), mkWorkflow("Report", "example.com")
	elsewhere := mkWorkflow("Elsewhere", "not-allowed.test")

	const pw1, pw2 = "first-password-1", "second-password-2"
	res := api.PostCtx(ctx, "/api/w/schedules", map[string]any{
		"name": "Morning", "agent_id": agentID, "cron": "0 7 * * *", "timezone": "Africa/Cairo",
		"workflow_id": wf1.ID, "values": map[string]string{"page": "inbox", "password": pw1},
	})
	if res.Code != 201 {
		t.Fatalf("schedule: %d %s", res.Code, res.Body)
	}
	var sc struct{ Schedule automation.Schedule }
	_ = json.Unmarshal(res.Body.Bytes(), &sc)
	id := sc.Schedule.ID

	// What a run would be filled with, read back the way the scheduler reads it.
	runValues := func() map[string]string {
		t.Helper()
		var params, sealed string
		if err := a.SysDB.QueryRow(`SELECT params::text, secret_values FROM automation_schedules WHERE id = $1`, id).Scan(&params, &sealed); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(params, pw1) || strings.Contains(params, pw2) {
			t.Fatalf("a secret was stored in the clear: %s", params)
		}
		plain := map[string]string{}
		_ = json.Unmarshal([]byte(params), &plain)
		v, err := automation.MergeScheduleValues(plain, sealed)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	patch := func(body map[string]any, want int) automation.Schedule {
		t.Helper()
		res := api.PatchCtx(ctx, "/api/w/schedules/"+id, body)
		if res.Code != want {
			t.Fatalf("PATCH %v: %d %s, want %d", body, res.Code, res.Body, want)
		}
		if strings.Contains(res.Body.String(), pw1) || strings.Contains(res.Body.String(), pw2) {
			t.Fatalf("the edit handed a secret back: %s", res.Body)
		}
		var out struct{ Schedule automation.Schedule }
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out.Schedule
	}

	// 1. The time alone: re-timed from now, values untouched.
	got := patch(map[string]any{"cron": "30 9 * * 1-5", "timezone": "Asia/Riyadh"}, 200)
	riyadh, _ := time.LoadLocation("Asia/Riyadh")
	if got.Cron != "30 9 * * 1-5" || got.Timezone != "Asia/Riyadh" || got.NextRunAt == nil || got.NextRunAt.In(riyadh).Hour() != 9 {
		t.Fatalf("re-timed: %+v", got)
	}
	if v := runValues(); v["page"] != "inbox" || v["password"] != pw1 {
		t.Fatalf("after re-timing the values are %v", v)
	}

	// 2. The values, with the secret left blank: the plain one changes, the
	// sealed one is kept.
	got = patch(map[string]any{"name": "Morning (reports)", "values": map[string]string{"page": "reports", "password": ""}}, 200)
	if got.Name != "Morning (reports)" || got.Values["page"] != "reports" || len(got.SecretParams) != 1 {
		t.Fatalf("values edit: %+v", got)
	}
	if v := runValues(); v["page"] != "reports" || v["password"] != pw1 {
		t.Fatalf("a blank secret must keep the sealed one; values are %v", v)
	}

	// 3. The secret replaced.
	patch(map[string]any{"values": map[string]string{"page": "reports", "password": pw2}}, 200)
	if v := runValues(); v["password"] != pw2 {
		t.Fatalf("the secret was not replaced; values are %v", v)
	}

	// 4. Another workflow with the same secret: the secret carries over.
	got = patch(map[string]any{"workflow_id": wf2.ID, "values": map[string]string{"page": "daily"}}, 200)
	if got.WorkflowID != wf2.ID || !strings.HasPrefix(got.WorkflowName, "Report") {
		t.Fatalf("workflow switch: %+v", got)
	}
	if v := runValues(); v["page"] != "daily" || v["password"] != pw2 {
		t.Fatalf("after switching the workflow the values are %v", v)
	}

	// 5. Refused, and nothing changes: a missing required value, a workflow
	// that leaves the browser's allow-list, an unknown workflow or browser, an
	// empty name, a bad cron.
	patch(map[string]any{"values": map[string]string{"page": ""}}, 422)
	patch(map[string]any{"workflow_id": elsewhere.ID}, 422)
	patch(map[string]any{"workflow_id": "00000000-0000-0000-0000-000000000000"}, 404)
	patch(map[string]any{"agent_id": "00000000-0000-0000-0000-000000000000"}, 404)
	patch(map[string]any{"name": "   "}, 422)
	patch(map[string]any{"cron": "not a cron"}, 422)
	if v := runValues(); v["page"] != "daily" || v["password"] != pw2 {
		t.Fatalf("a refused edit changed the values: %v", v)
	}
	var wfNow string
	_ = a.SysDB.QueryRow(`SELECT workflow_id FROM automation_schedules WHERE id = $1`, id).Scan(&wfNow)
	if wfNow != wf2.ID {
		t.Fatalf("a refused edit changed the workflow to %s", wfNow)
	}

	// 6. Another workspace's schedule is not there.
	other, _ := workspace(t, a)
	api2, ctx2 := routes(t, a, automation.NewHub(quietLog()), other, "owner")
	if r := api2.PatchCtx(ctx2, "/api/w/schedules/"+id, map[string]any{"cron": "@daily"}); r.Code != 404 {
		t.Fatalf("editing another workspace's schedule: %d %s", r.Code, r.Body)
	}

	// 7. A member without automation:run cannot edit.
	api3, ctx3 := routes(t, a, automation.NewHub(quietLog()), wsID, "client")
	if r := api3.PatchCtx(ctx3, "/api/w/schedules/"+id, map[string]any{"cron": "@daily"}); r.Code < 400 || r.Code >= 500 {
		t.Fatalf("a client editing a schedule: %d %s", r.Code, r.Body)
	}
}
