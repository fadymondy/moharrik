package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/triggers"
)

/*
The dispatcher: one in-process loop that claims pending events, runs the rules
listening to them, and fires the schedule rules that are due.

It wakes on its ticker and whenever triggers.Fire nudges it. Events are
claimed with FOR UPDATE SKIP LOCKED and marked processed in the same
statement, so a second replica (or a test driving ProcessWorkspace by hand
while the loop runs) never runs a rule twice on one event. Like the other
in-process tickers, a multi-replica deploy would share the work rather than
duplicate it — the claim is the lock.

RULES=off (or SCHEDULER=off, which the tests and the OpenAPI export set) turns
it off; the tests call ProcessWorkspace and TickSchedules themselves.
*/

// Clock is what "now" is for the schedule trigger. The tests move it.
var Clock = time.Now

// tick is how often the loop looks for work when nothing nudges it: database
// triggers write events without a nudge, so this is their latency.
const tick = 3 * time.Second

// Start runs the dispatcher for the life of the process.
func Start(a *app.App) {
	if os.Getenv("RULES") == "off" || os.Getenv("SCHEDULER") == "off" || a == nil || a.SysDB == nil {
		return
	}
	triggers.Log = a.Log
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		lastSweep := time.Time{}
		for {
			select {
			case <-t.C:
			case <-triggers.Wake():
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			TickSchedules(ctx, a)
			Dispatch(ctx, a)
			if time.Since(lastSweep) > time.Hour {
				Sweep(ctx, a)
				lastSweep = time.Now()
			}
			cancel()
		}
	}()
}

// Dispatch processes every workspace with pending events.
func Dispatch(ctx context.Context, a *app.App) {
	// Which workspaces have work: the one owner-pool read, like every
	// collector's; everything after it runs on the app pool in its workspace.
	rows, err := a.SysDB.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM automation_rule_events WHERE processed_at IS NULL LIMIT 200`)
	if err != nil {
		a.Log.Warn("rules: could not list pending work", "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, ws := range ids {
		if _, err := ProcessWorkspace(ctx, a, ws); err != nil {
			a.Log.Warn("rules: processing failed", "workspace", ws, "err", err)
		}
	}
}

type pendingEvent struct {
	ID, Type, SubjectID, RuleID string
	Data                        map[string]any
	Chain                       []string
	At                          time.Time
}

// ProcessWorkspace claims one workspace's pending events and runs the rules
// that listen to them. It returns how many runs it logged.
func ProcessWorkspace(ctx context.Context, a *app.App, wsID string) (int, error) {
	sctx := tenant.WithWorkspace(ctx, wsID)
	runs := 0
	for round := 0; round < 10; round++ {
		rows, err := a.SQLDB.QueryContext(sctx, `
			UPDATE automation_rule_events SET processed_at = now()
			WHERE id IN (SELECT id FROM automation_rule_events WHERE workspace_id = $1 AND processed_at IS NULL
			             ORDER BY created_at LIMIT 50 FOR UPDATE SKIP LOCKED)
			RETURNING id, type, subject_id, coalesce(rule_id, ''), data::text, chain, created_at`, wsID)
		if err != nil {
			return runs, err
		}
		var evs []pendingEvent
		for rows.Next() {
			var e pendingEvent
			var data string
			var chain pgtext.Array
			if err := rows.Scan(&e.ID, &e.Type, &e.SubjectID, &e.RuleID, &data, &chain, &e.At); err != nil {
				rows.Close()
				return runs, err
			}
			_ = json.Unmarshal([]byte(data), &e.Data)
			if e.Data == nil {
				e.Data = map[string]any{}
			}
			e.Chain = chain
			evs = append(evs, e)
		}
		rows.Close()
		if len(evs) == 0 {
			break
		}
		slices.SortFunc(evs, func(x, y pendingEvent) int { return x.At.Compare(y.At) })
		for _, e := range evs {
			n, err := processEvent(ctx, a, wsID, e)
			runs += n
			if err != nil {
				return runs, err
			}
		}
	}
	if runs > 0 && a.Events != nil {
		a.Events.Publish(wsID, realtime.Event{Kind: "rules", Permission: authz.AutomationRead})
	}
	return runs, nil
}

func processEvent(ctx context.Context, a *app.App, wsID string, e pendingEvent) (int, error) {
	sctx := tenant.WithWorkspace(ctx, wsID)
	q := `SELECT ` + ruleCols + ` FROM automation_rules WHERE workspace_id = $1 AND enabled AND trigger = $2`
	args := []any{wsID, e.Type}
	if e.RuleID != "" {
		q += ` AND id = $3`
		args = append(args, e.RuleID)
	}
	rows, err := a.SQLDB.QueryContext(sctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return 0, err
	}
	var list []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	n := 0
	for _, r := range list {
		run := Execute(ctx, a, wsID, r, Event{ID: e.ID, Type: e.Type, SubjectID: e.SubjectID, Data: e.Data, Chain: e.Chain}, "")
		if _, err := logRun(sctx, a, wsID, r, run); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Event is what a rule is evaluated against.
type Event struct {
	ID        string
	Type      string
	SubjectID string
	Data      map[string]any
	Chain     []string
}

// Execute evaluates one rule against one event and, unless testedBy names who
// is testing it, carries out its actions. It never returns an error: every
// outcome — including "that member is gone" — is a run, logged and shown.
func Execute(ctx context.Context, a *app.App, wsID string, r Rule, ev Event, testedBy string) RuleRun {
	dry := testedBy != ""
	data := map[string]any{}
	for k, v := range ev.Data {
		data[k] = v
	}
	run := RuleRun{RuleID: r.ID, RuleName: r.Name, EventID: ev.ID, Trigger: ev.Type, SubjectID: ev.SubjectID, DryRun: dry,
		Event: ev.Data, Conditions: []RuleConditionResult{}, Actions: []RuleActionResult{}, Depth: len(ev.Chain), TestedBy: testedBy,
		StartedAt: time.Now().UTC()}
	finish := func(status, reason string) RuleRun {
		run.Status, run.Reason = status, clip(reason, 500)
		t := time.Now().UTC()
		run.FinishedAt = &t
		return run
	}
	sctx := tenant.WithWorkspace(ctx, wsID)

	// 1. The loop guard, before anything else is even read.
	if !dry && slices.Contains(ev.Chain, r.ID) {
		return finish("loop_blocked", "this event was caused by this rule's own action; a rule never re-triggers itself")
	}
	if !dry && len(ev.Chain) >= triggers.MaxDepth {
		return finish("loop_blocked", fmt.Sprintf("this event is %d rules deep in a chain (the limit is %d)", len(ev.Chain), triggers.MaxDepth))
	}

	// 2. The conditions.
	matched, results := Evaluate(r.Match, r.Conditions, data)
	run.Conditions = results
	if !matched {
		return finish("skipped", "the conditions did not hold")
	}

	// 3. Once per record.
	if r.OncePerSubject && ev.SubjectID != "" && r.ID != "" {
		var done int
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM automation_rule_runs
			WHERE workspace_id = $1 AND rule_id = $2 AND subject_id = $3 AND NOT dry_run AND status IN ('ok', 'partial')`,
			wsID, r.ID, ev.SubjectID).Scan(&done)
		if done > 0 {
			if !dry {
				return finish("skipped", "this rule already ran for this record, and runs once per record")
			}
			run.Reason = "note: a real event for this record would be skipped — this rule already ran for it"
		}
	}

	// 4. The brakes: the rule's own, then the workspace's.
	if !dry && r.ID != "" {
		var mine, all int
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT
			count(*) FILTER (WHERE rule_id = $2),
			count(*)
			FROM automation_rule_runs WHERE workspace_id = $1 AND NOT dry_run AND status IN ('ok', 'partial', 'failed')
			  AND started_at > now() - interval '1 hour'`, wsID, r.ID).Scan(&mine, &all)
		if mine >= r.MaxRunsPerHour {
			return finish("rate_limited", fmt.Sprintf("this rule already ran %d times in the last hour (its limit)", r.MaxRunsPerHour))
		}
		if all >= WorkspaceHourlyCap {
			return finish("rate_limited", fmt.Sprintf("this workspace's rules already ran %d times in the last hour", WorkspaceHourlyCap))
		}
	}

	// 5. Who it acts as, now.
	rctx, s, err := runnerContext(ctx, a, wsID, r, ev.Chain)
	if err != nil {
		return finish("failed", err.Error())
	}
	def, _ := triggerDef(r.Trigger)
	x := &execCtx{a: a, ctx: rctx, ws: wsID, s: s, rule: r, def: def, data: data, subjectID: ev.SubjectID, eventID: ev.ID, dry: dry}
	failed, done := 0, 0
	first := ""
	for i, act := range r.Actions {
		res := x.run(i, act)
		switch res.Status {
		case "failed":
			failed++
			if first == "" {
				first = fmt.Sprintf("step %d (%s): %s", i+1, act.Type, res.Error)
			}
		case "ok", "would":
			done++
		}
		run.Actions = append(run.Actions, res)
	}
	switch {
	case failed == 0:
		if run.Reason != "" {
			return finish("ok", run.Reason)
		}
		return finish("ok", "")
	case done == 0:
		return finish("failed", first)
	default:
		return finish("partial", first)
	}
}

// ErrMemberGone is a rule whose member is no longer in the workspace.
var ErrMemberGone = errors.New("the member this rule acts as is no longer an active member of this workspace; save the rule again to take it over")

// runnerContext is the context a rule's actions run under: the workspace, the
// member it acts as with their role as of now, the audit actor naming the
// rule, and the chain that the events its actions cause will carry.
func runnerContext(ctx context.Context, a *app.App, wsID string, r Rule, chain []string) (context.Context, *tenant.Scope, error) {
	var role, slug, name, email string
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT wm.role, w.slug, w.name, coalesce(u.email, '')
		FROM workspace_members wm JOIN workspaces w ON w.id = wm.workspace_id
		LEFT JOIN users u ON u.id::text = wm.user_id
		WHERE wm.workspace_id = $1 AND wm.user_id = $2 AND wm.status = 'active'`, wsID, r.UpdatedBy).Scan(&role, &slug, &name, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrMemberGone
	}
	if err != nil {
		return nil, nil, err
	}
	if !authz.Can(role, authz.AutomationRun) {
		return nil, nil, fmt.Errorf("the member this rule acts as (%s) no longer holds a role that may run rules", role)
	}
	s := &tenant.Scope{WorkspaceID: wsID, WorkspaceSlug: slug, WorkspaceName: name, UserID: r.UpdatedBy, Email: email, Role: role, Via: "system"}
	rctx := tenant.With(context.WithoutCancel(ctx), s)
	rctx = tenant.WithActor(rctx, tenant.Actor{Kind: "agent", ID: "rule:" + r.ID, Name: "Rule: " + r.Name, UserID: r.UpdatedBy, Via: "agent"})
	if r.ID != "" {
		rctx = triggers.WithRule(rctx, chain, r.ID)
	}
	return rctx, s, nil
}

func logRun(ctx context.Context, a *app.App, wsID string, r Rule, run RuleRun) (string, error) {
	ev, _ := json.Marshal(run.Event)
	conds, _ := json.Marshal(run.Conditions)
	acts, _ := json.Marshal(run.Actions)
	var id string
	err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO automation_rule_runs (workspace_id, rule_id, rule_name, event_id, trigger, subject_id, status, dry_run, reason,
		                                  event, conditions, actions, depth, tested_by, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb, $13, $14, $15, $16) RETURNING id`,
		wsID, r.ID, r.Name, run.EventID, run.Trigger, run.SubjectID, run.Status, run.DryRun, run.Reason,
		string(ev), string(conds), string(acts), run.Depth, run.TestedBy, run.StartedAt, run.FinishedAt).Scan(&id)
	if err != nil {
		return "", err
	}
	if !run.DryRun {
		// What the rule list shows. A run that did nothing (skipped, limited,
		// blocked) is in the log but does not move "last ran".
		if run.Status == "ok" || run.Status == "partial" || run.Status == "failed" {
			_, err = a.SQLDB.ExecContext(ctx, `UPDATE automation_rules SET run_count = run_count + 1, last_run_at = $3,
				last_status = $4, last_error = $5 WHERE workspace_id = $1 AND id = $2`,
				wsID, r.ID, run.StartedAt, run.Status, clip(run.Reason, 300))
		} else if run.Status == "rate_limited" {
			// Held back is worth showing on the list: the rule did not do its
			// job. A blocked loop is not — it is the guard stopping the rule's
			// own echo, and the run it echoes is what "last ran" should say.
			_, err = a.SQLDB.ExecContext(ctx, `UPDATE automation_rules SET last_status = $3, last_error = $4 WHERE workspace_id = $1 AND id = $2`,
				wsID, r.ID, run.Status, clip(run.Reason, 300))
		}
	}
	return id, err
}

// ---- the schedule trigger -------------------------------------------------------------

// TickSchedules fires every schedule rule that is due, advancing each first so
// a failure below can never make it fire again and again.
func TickSchedules(ctx context.Context, a *app.App) {
	now := Clock().UTC()
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id, cron, timezone FROM automation_rules
		WHERE enabled AND trigger = 'schedule' AND next_run_at <= $1 ORDER BY next_run_at LIMIT 100`, now)
	if err != nil {
		return
	}
	type due struct{ id, ws, cron, tz string }
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.ws, &d.cron, &d.tz) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		sctx := tenant.WithWorkspace(ctx, d.ws)
		next, err := nextAfter(d.cron, d.tz, now)
		if err != nil {
			_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_rules SET enabled = false, last_status = 'failed', last_error = $3
				WHERE workspace_id = $1 AND id = $2`, d.ws, d.id, clip("the schedule is not valid: "+err.Error(), 300))
			continue
		}
		if _, err := a.SQLDB.ExecContext(sctx, `UPDATE automation_rules SET next_run_at = $3 WHERE workspace_id = $1 AND id = $2`, d.ws, d.id, next); err != nil {
			continue
		}
		loc, _ := time.LoadLocation(d.tz)
		if loc == nil {
			loc = time.UTC
		}
		local := now.In(loc)
		_ = triggers.FireFor(ctx, a.SQLDB, d.ws, d.id, triggers.Event{Type: triggers.Schedule, Data: map[string]any{
			"fired_at": now.Format(time.RFC3339), "date": local.Format("2006-01-02"),
			"weekday": local.Weekday().String(), "time": local.Format("15:04"),
		}})
	}
}

// Sweep deletes processed events after a week and the run log after 90 days.
func Sweep(ctx context.Context, a *app.App) {
	if _, err := a.SysDB.ExecContext(ctx, `DELETE FROM automation_rule_events WHERE processed_at < now() - interval '7 days'`); err != nil {
		a.Log.Warn("rules: event sweep failed", "err", err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `DELETE FROM automation_rule_runs WHERE started_at < now() - interval '90 days'`); err != nil {
		a.Log.Warn("rules: run sweep failed", "err", err)
	}
}

// Sample is a trigger's sample event with the given fields laid over it.
func Sample(trigger string, over map[string]any) map[string]any {
	out := map[string]any{}
	if d, ok := triggerDef(trigger); ok {
		for k, v := range d.Sample {
			out[k] = v
		}
	}
	for k, v := range over {
		if s, ok := v.(string); ok {
			v = strings.TrimSpace(s)
		}
		out[k] = v
	}
	return out
}
