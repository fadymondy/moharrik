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
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	AgentID    string     `json:"agent_id"`
	AgentName  string     `json:"agent_name"`
	Cron       string     `json:"cron"`
	Timezone   string     `json:"timezone"`
	CatchUp    string     `json:"catch_up" doc:"skip | once"`
	Enabled    bool       `json:"enabled"`
	Steps      []Step     `json:"steps"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus string     `json:"last_status" doc:"running | ok | failed | missed | waiting"`
	LastError  string     `json:"last_error"`
	LastRunID  string     `json:"last_run_id"`
	CreatedAt  time.Time  `json:"created_at"`
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
				h.Tick(ctx, a, time.Now().UTC())
			}
		}
	}()
	return cancel
}

// due is one row of the scheduler's queue.
type due struct {
	ID          string
	WorkspaceID string
	AgentID     string
	Cron        string
	Timezone    string
	CatchUp     string
	LastStatus  string
	Steps       []Step
	RequestedBy string
}

// Tick fires everything due at `now`. Exported so the tests drive the clock
// themselves rather than sleeping.
func (h *Hub) Tick(ctx context.Context, a *app.App, now time.Time) {
	// The queue is read on the OWNER pool: "which workspaces have work due" is
	// by definition cross-tenant. Everything each schedule then does runs on
	// the app pool inside its own workspace.
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id, agent_id, cron, timezone, catch_up, last_status, steps::text, created_by
		FROM automation_schedules
		WHERE enabled AND next_run_at <= $1
		ORDER BY next_run_at LIMIT 100`, now)
	if err != nil {
		return
	}
	defer rows.Close()

	var list []due
	for rows.Next() {
		var d due
		var steps string
		if err := rows.Scan(&d.ID, &d.WorkspaceID, &d.AgentID, &d.Cron, &d.Timezone, &d.CatchUp, &d.LastStatus, &steps, &d.RequestedBy); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(steps), &d.Steps)
		list = append(list, d)
	}
	for _, d := range list {
		h.fire(ctx, a, d, now)
	}
}

func (h *Hub) fire(ctx context.Context, a *app.App, d due, now time.Time) {
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

	// Overlap: a schedule whose previous run is still going is passed over, not
	// queued behind itself.
	if h.inFlight(sctx, a, d) {
		h.record(sctx, a, d, "missed", "the previous run was still going", "")
		return
	}

	// The browser being shut is the NORMAL case, and the reason a miss is
	// recorded: a gap in the data must have a stated cause.
	if !h.Online(d.AgentID) {
		if d.CatchUp == CatchUpOnce {
			// Recorded ONCE, then quiet: re-checking every 30s and counting each
			// check as a new miss would say "missed 3" after ninety seconds of
			// one closed laptop. It waits, and runs when the browser returns.
			if d.LastStatus != "waiting" {
				h.record(sctx, a, d, "waiting", "that browser is not connected; this will run when it comes back", "")
			}
			// Left due, so the next tick tries again.
			_, _ = a.SQLDB.ExecContext(sctx, `
				UPDATE automation_schedules SET next_run_at = $3, updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, now.Add(-time.Second))
			return
		}
		h.record(sctx, a, d, "missed", "that browser was not connected", "")
		return
	}

	runID, err := h.Start(ctx, a, d.WorkspaceID, d.RequestedBy, "schedule", d.AgentID, d.Steps)
	if err != nil {
		// Paused, busy, off the allow-list: the guardrails answer here exactly
		// as they do for a person, and their words are what gets recorded.
		h.record(sctx, a, d, "missed", err.Error(), "")
		return
	}
	h.record(sctx, a, d, "running", "", runID)
	notify(a, d.WorkspaceID)
	a.Log.Info("schedule fired", "schedule", d.ID, "workspace", d.WorkspaceID, "run", runID, "next", next)
}

// inFlight reports whether this schedule's last run is still going. The grace
// window stops one wedged run from freezing a schedule for ever.
func (h *Hub) inFlight(ctx context.Context, a *app.App, d due) bool {
	var n int
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT count(*) FROM automation_runs r
		JOIN automation_schedules s ON s.workspace_id = r.workspace_id AND s.last_run_id = r.id
		WHERE r.workspace_id = $1 AND s.id = $2
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
		a.Events.Publish(workspaceID, realtime.Event{Kind: "schedule"})
	}
}

func (h *Hub) record(ctx context.Context, a *app.App, d due, status, why, runID string) {
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_schedules SET
			last_status = $3, last_error = $4,
			last_run_at = CASE WHEN $3 = 'running' THEN now() ELSE last_run_at END,
			last_run_id = CASE WHEN $5 = '' THEN last_run_id ELSE $5 END,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, status, trim(why, 300), runID)
	notify(a, d.WorkspaceID)
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
// it, so the list shows what happened rather than "running" for ever.
func Settle(ctx context.Context, a *app.App, workspaceID, runID string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var status, runErr string
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT status, error FROM automation_runs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID).Scan(&status, &runErr); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			a.Log.Warn("schedule settle", "run", runID, "err", err)
		}
		return
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_schedules SET last_status = $3, last_error = $4, updated_at = now()
		WHERE workspace_id = $1 AND last_run_id = $2`, workspaceID, runID, status, trim(runErr, 300))
	notify(a, workspaceID)
}
