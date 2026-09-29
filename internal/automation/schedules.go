package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pglock"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Schedules (MH-232): run a saved sequence of steps on a clock.

The clock is the easy half. The hard half is that the browser a schedule needs
is a LAPTOP, and laptops are shut:

	A MISSED RUN IS RECORDED, NOT DROPPED. "There is no data for Tuesday" has to
	have an answer, and "that browser was offline at 6am" is one. Silence is not.

	CATCH-UP IS A CHOICE PER SCHEDULE, not a global guess. A daily report wants
	running once when the machine comes back; a routine chore does not want
	eleven of itself at 9am. So catch_up is skip or once, set where the schedule
	is set.

	NEVER TWICE AT ONCE. next_run_at is advanced BEFORE anything is enqueued,
	and a schedule whose previous run is still going is passed over. A five
	minute schedule whose steps take six minutes must not stack up.

Times are computed in the schedule's OWN timezone. "Every day at 07:00" means
07:00 where the operator is, and a server in UTC that ignores that is wrong
twice a year in a way nobody notices until the clocks change.

A schedule carries its own steps rather than pointing at a saved workflow:
plugins are MH-233, and a schedule that cannot run until they exist would be
half a feature.
*/

// parser accepts the five-field cron everyone writes, plus @daily-style
// descriptors, and no seconds field — a browser schedule wanting second-level
// precision wants something other than a browser.
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Catch-up policies.
const (
	CatchUpSkip = "skip"
	CatchUpOnce = "once"
)

const (
	// How often the scheduler looks for due work. A minute is cron's own
	// resolution, so anything finer only burns queries.
	scheduleTick = 30 * time.Second

	// A run that has not finished in this long stops counting as "in flight"
	// for the overlap check — otherwise one wedged run stops a schedule for
	// ever.
	inFlightGrace = 30 * time.Minute
)

// Schedule is one saved, recurring run.
type Schedule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	// The saved workflow this schedule runs, when it runs one (MH-403), and
	// the values it fills it with. Values holds only the PLAIN ones; the
	// secret ones are sealed at rest and never sent back — SecretParams names
	// which of them are set, so a page can say "set" without knowing them.
	WorkflowID   string            `json:"workflow_id"`
	WorkflowName string            `json:"workflow_name"`
	Values       map[string]string `json:"values"`
	SecretParams []string          `json:"secret_params"`
	Cron       string     `json:"cron"`
	Timezone   string     `json:"timezone"`
	CatchUp    string     `json:"catch_up" doc:"skip | once"`
	Enabled    bool       `json:"enabled"`
	Steps      []Step     `json:"steps"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus string     `json:"last_status" doc:"running | ok | failed | missed | waiting | paused"`
	LastError  string     `json:"last_error"`
	LastRunID  string     `json:"last_run_id"`
	CreatedAt  time.Time  `json:"created_at"`
	// A server job instead of a browser run (MH-478), and the counters that
	// make "this has stopped working" visible on the row.
	JobKey       string `json:"job_key"`
	FailureCount int    `json:"failure_count"`
	MissedCount  int    `json:"missed_count"`
	// Five consecutive failures: the schedule has stopped working.
	Failing bool `json:"failing"`
}

// NextAfter computes the next due time for a cron expression in a timezone.
func NextAfter(expression, timezone string, after time.Time) (time.Time, error) {
	if strings.TrimSpace(expression) == "" {
		return time.Time{}, errors.New("a schedule needs a cron expression")
	}
	loc, err := time.LoadLocation(strings.TrimSpace(firstNonEmpty(timezone, "UTC")))
	if err != nil {
		// An unknown zone is a typo, not a reason to silently schedule in the
		// wrong hour.
		return time.Time{}, fmt.Errorf("unknown timezone %q", timezone)
	}
	s, err := parser.Parse(strings.TrimSpace(expression))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a cron expression: %w", expression, err)
	}
	return s.Next(after.In(loc)).UTC(), nil
}

// NextTimes returns the next n due times after `after`, in UTC — what the
// schedule form shows before anything is saved, so "0 7 * * 1-5" is seen to
// mean 07:00 on weekdays, in the zone it will actually run in.
func NextTimes(expression, timezone string, after time.Time, n int) ([]time.Time, error) {
	out := make([]time.Time, 0, n)
	at := after
	for i := 0; i < n; i++ {
		next, err := NextAfter(expression, timezone, at)
		if err != nil {
			return nil, err
		}
		if next.IsZero() {
			break // an expression that never fires again (Feb 30th)
		}
		out = append(out, next)
		at = next
	}
	return out, nil
}

// ValidateSchedule is what the API calls before saving one.
func ValidateSchedule(expression, timezone, catchUp string) error {
	if catchUp != CatchUpSkip && catchUp != CatchUpOnce {
		return fmt.Errorf("catch_up must be %q or %q", CatchUpSkip, CatchUpOnce)
	}
	_, err := NextAfter(expression, timezone, time.Now())
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// StartScheduler runs the clock. SCHEDULER=off disables it, which is what the
// tests and the OpenAPI export want.
//
// This stays in the web process (see internal/server/server.go's Boot) because
// h.fire dispatches to h's own live browser WebSocket registry — but Hosbah
// can still run more than one web replica, each with its OWN Hub and its OWN
// set of connected browsers, so a schedule due at the same moment could fire
// twice: once from whichever replica happens to hold the browser its steps
// run on. The tick itself still takes the cluster-wide lock so only one
// replica even LOOKS at the due queue per tick; the other replicas' browsers
// remain reachable for the run itself (pickAgents), just not for deciding
// whether to start one.
func (h *Hub) StartScheduler(a *app.App) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(scheduleTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:automation:scheduler", func() {
					h.Tick(ctx, a, time.Now().UTC())
				})
				if err != nil {
					a.Log.Error("automation scheduler: could not acquire the cluster lock", "err", err)
				}
			}
		}
	}()
	return cancel
}

// due is one row of the scheduler's queue.
type due struct {
	ID          string
	WorkspaceID string
	// Empty: any connected browser of the workspace (MH-478).
	AgentID     string
	Cron        string
	Timezone    string
	CatchUp     string
	LastStatus  string
	Steps       []Step
	RequestedBy string
	// A schedule either carries its own steps or names a saved workflow and
	// the values to fill it with (MH-233) — or runs a server job (MH-478).
	WorkflowID string
	JobKey     string
	Values     map[string]string
	// The sealed secret values (MH-403), opened only at the moment of running.
	Sealed string
}

const dueColumns = `id, workspace_id, COALESCE(agent_id, ''), cron, timezone, catch_up, last_status, steps::text, created_by,
	workflow_id, job_key, params::text, secret_values`

func scanDue(r rowScanner) (due, error) {
	var d due
	var steps, values string
	if err := r.Scan(&d.ID, &d.WorkspaceID, &d.AgentID, &d.Cron, &d.Timezone, &d.CatchUp, &d.LastStatus, &steps, &d.RequestedBy,
		&d.WorkflowID, &d.JobKey, &values, &d.Sealed); err != nil {
		return d, err
	}
	_ = json.Unmarshal([]byte(steps), &d.Steps)
	_ = json.Unmarshal([]byte(values), &d.Values)
	if d.Values == nil {
		d.Values = map[string]string{}
	}
	return d, nil
}

// Tick fires everything due at `now`. Exported so the tests drive the clock
// themselves rather than sleeping.
func (h *Hub) Tick(ctx context.Context, a *app.App, now time.Time) {
	// The queue is read on the OWNER pool: "which workspaces have work due" is
	// by definition cross-tenant. Everything each schedule then does runs on
	// the app pool inside its own workspace.
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT `+dueColumns+`
		FROM automation_schedules
		WHERE enabled AND next_run_at <= $1
		ORDER BY next_run_at LIMIT 100`, now)
	if err != nil {
		return
	}
	var list []due
	for rows.Next() {
		if d, err := scanDue(rows); err == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		h.fire(ctx, a, d, now)
	}
}

func (h *Hub) fire(ctx context.Context, a *app.App, d due, now time.Time) {
	// A scheduled run is the scheduler acting for whoever set the schedule up:
	// recorded in the audit log as an agent (MH-392), not as nobody.
	ctx = tenant.WithActor(ctx, tenant.Actor{Kind: "agent", ID: "schedule:" + d.ID, Name: "Scheduler", UserID: d.RequestedBy, Via: "agent"})
	sctx := tenant.WithWorkspace(ctx, d.WorkspaceID)

	// Advance the clock FIRST. If the run were enqueued first and anything
	// below failed — the browser is gone, the workspace is paused — next_run_at
	// would still point at a time in the past and the next tick would fire the
	// same schedule again, and again.
	next, err := NextAfter(d.Cron, d.Timezone, now)
	if err != nil {
		h.disable(sctx, a, d, "this schedule's cron expression is not valid: "+err.Error())
		return
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_schedules SET next_run_at = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, next); err != nil {
		return
	}

	// A server job shares the clock but not the browser path (jobs.go).
	if d.JobKey != "" {
		h.fireJob(ctx, a, d)
		return
	}

	// Overlap: a schedule whose previous run is still going is passed over, not
	// queued behind itself.
	if h.inFlight(sctx, a, d) {
		h.record(sctx, a, d, "missed", "the previous run was still going", "")
		return
	}

	// The browser being shut is the NORMAL case, and the reason a miss is
	// recorded: a gap in the data must have a stated cause.
	candidates, why := h.pickAgents(sctx, a, d)
	if len(candidates) == 0 {
		if d.CatchUp == CatchUpOnce {
			// Recorded ONCE, then quiet: re-checking every 30s and counting each
			// check as a new miss would say "missed 3" after ninety seconds of
			// one closed laptop. It waits, and runs when the browser returns.
			if d.LastStatus != "waiting" {
				h.record(sctx, a, d, "waiting", why+"; this will run when it comes back", "")
			}
			h.stayDue(sctx, a, d)
			return
		}
		h.record(sctx, a, d, "missed", why, "")
		return
	}

	runID, err := h.startScheduled(ctx, a, d, candidates)
	if err != nil {
		// Paused, busy, off the allow-list, a workflow turned off: the
		// guardrails answer here exactly as they do for a person, and their
		// words are what gets recorded.
		status := "missed"
		if errors.Is(err, ErrWorkflowOff) || errors.Is(err, ErrNoWorkflow) {
			status = "failed"
		}
		h.record(sctx, a, d, status, err.Error(), "")
		return
	}
	h.record(sctx, a, d, "running", "", runID)
	// A run can finish before the line above; settling again keeps "running"
	// from overwriting its result.
	var st string
	if a.SQLDB.QueryRowContext(sctx, `SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, runID).Scan(&st) == nil &&
		st != "queued" && st != "running" {
		Settle(ctx, a, d.WorkspaceID, runID)
	}
	a.Log.Info("schedule fired", "schedule", d.ID, "workspace", d.WorkspaceID, "run", runID, "next", next)
}

// stayDue pulls next_run_at back to now: "run once when it comes back", or
// "run when the kill switch is lifted". Eleven missed hours make ONE run.
func (h *Hub) stayDue(ctx context.Context, a *app.App, d due) {
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_schedules SET next_run_at = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, time.Now().UTC().Add(-time.Second))
}

/*
pickAgents is which browsers may run this, in order.

A named browser is strict: if that machine is not there, the run is missed.
Quietly using a different browser would run a workflow against somebody else's
signed-in session. With no browser named, any connected browser of the
workspace will do (fadymondy.com's rule), oldest first; startScheduled tries
them in turn, so one whose allowed sites do not cover the steps is passed over
for one whose do.
*/
func (h *Hub) pickAgents(ctx context.Context, a *app.App, d due) ([]string, string) {
	if d.AgentID != "" {
		if h.Online(d.AgentID) {
			return []string{d.AgentID}, ""
		}
		name := "that browser"
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT name FROM browser_agents WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.AgentID).Scan(&name)
		return nil, name + " was not connected"
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id FROM browser_agents WHERE workspace_id = $1 AND revoked_at IS NULL ORDER BY created_at`, d.WorkspaceID)
	if err != nil {
		return nil, "the browsers could not be read"
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && h.Online(id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, "no browser of this workspace was connected"
	}
	return out, ""
}

// startScheduled starts the schedule's run on the first candidate that takes
// it. Only a refusal that another browser could avoid moves on to the next.
func (h *Hub) startScheduled(ctx context.Context, a *app.App, d due, candidates []string) (string, error) {
	var lastErr error
	for _, agentID := range candidates {
		var runID string
		var err error
		if d.WorkflowID != "" {
			var values map[string]string
			values, err = MergeScheduleValues(d.Values, d.Sealed)
			if err == nil {
				runID, err = RunWorkflowFrom(ctx, a, h, d.WorkspaceID, d.RequestedBy, d.WorkflowID, agentID, "schedule", d.ID, values)
			}
		} else {
			runID, err = h.Start(ctx, a, d.WorkspaceID, d.RequestedBy, "schedule", agentID, d.Steps)
			if err == nil {
				sctx := tenant.WithWorkspace(ctx, d.WorkspaceID)
				_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_runs SET schedule_id = $3 WHERE workspace_id = $1 AND id = $2`,
					d.WorkspaceID, runID, d.ID)
			}
		}
		if err == nil {
			return runID, nil
		}
		lastErr = err
		if !(errors.Is(err, ErrBusy) || errors.Is(err, ErrHostNotAllowed) || errors.Is(err, ErrOffline) || errors.Is(err, ErrHourly)) {
			return "", err
		}
	}
	return "", lastErr
}

// inFlight reports whether this schedule's last run is still going. By the
// SCHEDULE, never by the workflow: two schedules sharing a workflow is
// normal. The grace window stops one wedged run from freezing it for ever.
func (h *Hub) inFlight(ctx context.Context, a *app.App, d due) bool {
	var n int
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT count(*) FROM automation_runs r
		WHERE r.workspace_id = $1
		  AND (r.schedule_id = $2 OR r.id = (SELECT last_run_id FROM automation_schedules WHERE workspace_id = $1 AND id = $2))
		  AND r.status IN ('queued', 'running')
		  AND r.created_at > now() - $3::interval`,
		d.WorkspaceID, d.ID, inFlightGrace.String()).Scan(&n)
	return err == nil && n > 0
}

// notify tells the workspace's open tabs that a schedule moved. A schedule
// fires with nobody watching by definition, so a page left open showed
// "waiting" long after the run had finished — it had no reason to refetch.
func notify(a *app.App, workspaceID string) {
	if a.Events != nil {
		a.Events.Publish(workspaceID, realtime.Event{Kind: "schedule", Permission: authz.AutomationRead})
	}
}

func (h *Hub) record(ctx context.Context, a *app.App, d due, status, why, runID string) {
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_schedules SET
			last_status = $3, last_error = $4,
			last_run_at = CASE WHEN $3 = 'running' THEN now() ELSE last_run_at END,
			last_run_id = CASE WHEN $5 = '' THEN last_run_id ELSE $5 END,
			missed_count = CASE WHEN $3 IN ('missed', 'waiting') THEN missed_count + 1 ELSE missed_count END,
			failure_count = CASE WHEN $3 = 'failed' THEN failure_count + 1 ELSE failure_count END,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, status, trim(why, 300), runID)
	if status == "failed" {
		h.warnIfFailing(ctx, a, d.WorkspaceID, d.ID)
	}
	notify(a, d.WorkspaceID)
}

// failureNotifyAt is how many consecutive failures make a schedule "failing".
// One is a flaky page; five in a row is a schedule that has stopped working.
const failureNotifyAt = 5

// warnIfFailing says so, once, the moment a schedule reaches failureNotifyAt
// consecutive failures: in the log, and to the workspace's open pages.
func (h *Hub) warnIfFailing(ctx context.Context, a *app.App, workspaceID, scheduleID string) {
	var n int
	var name, why string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT failure_count, name, last_error FROM automation_schedules WHERE workspace_id = $1 AND id = $2`,
		workspaceID, scheduleID).Scan(&n, &name, &why); err != nil || n != failureNotifyAt {
		return
	}
	a.Log.Warn("schedule has failed repeatedly", "workspace", workspaceID, "schedule", scheduleID, "name", name, "failures", n, "error", why)
	notify(a, workspaceID)
}

// disable turns a schedule off and says why. Used when a schedule can never
// work again — a cron expression that no longer parses — rather than for the
// ordinary "not right now".
func (h *Hub) disable(ctx context.Context, a *app.App, d due, why string) {
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_schedules SET enabled = false, last_status = 'failed', last_error = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, trim(why, 300))
}

// Settle writes a finished run's outcome back onto the schedule that started
// it, so the list shows what happened rather than "running" for ever — and
// counts a failure against the schedule for what the browser actually did,
// not merely for the run having been accepted.
//
// By the run's own schedule_id when it has one: two schedules may share a
// workflow, and matching anything else once wrote one schedule's success over
// its neighbour's misses (fadymondy.com FM-95).
func Settle(ctx context.Context, a *app.App, workspaceID, runID string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var status, runErr, scheduleID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT status, error, schedule_id FROM automation_runs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID).Scan(&status, &runErr, &scheduleID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			a.Log.Warn("schedule settle", "run", runID, "err", err)
		}
		return
	}
	if status == "queued" || status == "running" {
		return
	}
	res, _ := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_schedules SET last_status = $3, last_error = $4,
		    failure_count = CASE WHEN $3 = 'failed' THEN failure_count + 1 WHEN $3 = 'ok' THEN 0 ELSE failure_count END,
		    updated_at = now()
		WHERE workspace_id = $1 AND last_run_id = $2 AND ($5 = '' OR id = $5)
		  AND last_status = 'running'`, // once: a run is settled by whichever of finish and fire sees it last
		workspaceID, runID, status, trim(runErr, 300), scheduleID)
	if res != nil {
		if n, _ := res.RowsAffected(); n > 0 && status == "failed" && scheduleID != "" {
			if hub := Default(); hub != nil {
				hub.warnIfFailing(sctx, a, workspaceID, scheduleID)
			}
		}
	}
	notify(a, workspaceID)
}
