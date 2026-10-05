package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// The workflow overview (MH-1067): what the page header and the canvas cards
// say about a workflow, read from the runs that exist and nothing else.
//
//   - Window: the last OverviewWindowDays days of runs. Every count and the
//     success rate are over that window and the response says so.
//   - Success rate = succeeded / (succeeded + failed). Cancelled runs and runs
//     still in progress are not outcomes and are left out of the denominator;
//     they are counted separately. With no finished run the percentage is null,
//     never 0 and never 100.
//   - Items extracted = rows the ok extract/read/eval_readonly steps stored in
//     their results, counted the way the run page counts them. Null when no run
//     in the window has such a step: a workflow that extracts nothing has no
//     "items" figure, rather than a zero.
//   - Per-step figures are over the runs of the workflow's CURRENT version in
//     the window, so a card never mixes an old version's steps into a new one's
//     positions.
//
// Read-only: one REPEATABLE READ READ ONLY transaction on the RLS pool,
// workspace_id in every predicate.

// OverviewWindowDays is the span every count in the overview covers.
const OverviewWindowDays = 30

const (
	overviewRunSample = 200  // most recent runs whose steps are read for items and step cards
	overviewStepRows  = 2000 // hard cap on step rows read
)

// WorkflowOverview is the whole answer.
type WorkflowOverview struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Version    int    `json:"version" doc:"The version saved now. Runs are stamped with the version they ran."`
	// Status as stored: enabled or paused. A workflow has no draft state in the
	// database, so none is invented.
	Status          string `json:"status" enum:"enabled,paused"`
	WorkspacePaused bool   `json:"workspace_paused" doc:"The workspace-wide automation kill switch is on."`
	PausedReason    string `json:"paused_reason"`

	WindowDays  int         `json:"window_days"`
	Runs        RunCounts   `json:"runs"`
	SuccessRate SuccessRate `json:"success_rate"`
	LastRun     *LastRun    `json:"last_run" doc:"The newest run of this workflow, whenever it was. Null when it never ran."`
	Items       ItemsStat   `json:"items"`
	Schedules   SchedStat   `json:"schedules"`

	// Steps are per-position figures over the runs of Version in the window.
	Steps []StepStat `json:"steps"`
	// Sampled is true when the window held more runs or step rows than were
	// read for Items and Steps (the run counts above are always over every run).
	Sampled bool `json:"sampled"`
}

// RunCounts are runs created in the window, by outcome.
type RunCounts struct {
	Total      int `json:"total"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	InProgress int `json:"in_progress" doc:"Queued or running."`
}

// SuccessRate shows its working: the percentage is Succeeded of Finished.
type SuccessRate struct {
	Succeeded int      `json:"succeeded"`
	Finished  int      `json:"finished" doc:"Succeeded plus failed. Cancelled and in-progress runs are not counted."`
	Percent   *float64 `json:"percent" doc:"Null when there is no finished run."`
}

// LastRun is the newest run.
type LastRun struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	Version    int        `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	DurationMS *int64     `json:"duration_ms" doc:"Started to finished; null until it has both."`
	Error      string     `json:"error"`
	Items      *int       `json:"items" doc:"Rows its extract steps stored. Null when it has none."`
}

// ItemsStat is the items figure over the window.
type ItemsStat struct {
	Total *int `json:"total" doc:"Null when no run in the window extracted anything."`
	Runs  int  `json:"runs" doc:"How many runs in the sample contributed."`
}

// SchedStat describes the schedules that run this workflow.
type SchedStat struct {
	Total     int        `json:"total"`
	Enabled   int        `json:"enabled"`
	NextRunAt *time.Time `json:"next_run_at"`
}

// StepStat is one node's record: how its step did over the window.
type StepStat struct {
	Position     int    `json:"position"`
	Ran          int    `json:"ran" doc:"Runs in which this step finished, ok or failed."`
	OK           int    `json:"ok"`
	Failed       int    `json:"failed"`
	LastStatus   string `json:"last_status" doc:"Its state in the newest run that reached it."`
	LastItems    *int   `json:"last_items"`
	LastDuration *int   `json:"last_duration_ms"`
}

// itemsInResult counts rows the way the run page does (rowsOf in the canvas
// model): a rows array, a fields record as one, or a values array. ok is false
// when the result holds nothing countable.
func itemsInResult(result string) (int, bool) {
	if strings.TrimSpace(result) == "" {
		return 0, false
	}
	var b struct {
		Rows   []json.RawMessage          `json:"rows"`
		Fields map[string]json.RawMessage `json:"fields"`
		Values []json.RawMessage          `json:"values"`
	}
	if json.Unmarshal([]byte(result), &b) != nil {
		return 0, false
	}
	switch {
	case b.Rows != nil:
		n := 0
		for _, r := range b.Rows {
			if t := strings.TrimSpace(string(r)); strings.HasPrefix(t, "{") {
				n++
			}
		}
		return n, true
	case b.Fields != nil:
		return 1, true
	case b.Values != nil:
		return len(b.Values), true
	}
	return 0, false
}

func canReadAutomation(s *tenant.Scope) bool {
	if !authz.Can(s.Role, authz.AutomationRead) {
		return false
	}
	if s.Via == "mcp" || s.Via == "oauth" {
		for _, sc := range s.Scopes {
			if sc == authz.AutomationRead {
				return true
			}
		}
		return false
	}
	return true
}

// ReadWorkflowOverview builds the overview for the caller in ctx.
func ReadWorkflowOverview(ctx context.Context, db *sql.DB, ref string) (*WorkflowOverview, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !canReadAutomation(s) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	fail := huma.Error500InternalServerError("could not read the workflow overview")
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fail
	}
	defer func() { _ = tx.Rollback() }()
	ws := s.WorkspaceID

	o := &WorkflowOverview{WindowDays: OverviewWindowDays, Steps: []StepStat{}}
	var enabled bool
	err = tx.QueryRowContext(ctx, `
		SELECT id, name, version, enabled FROM automation_workflows
		WHERE workspace_id = $1 AND (id = $2 OR (slug <> '' AND slug = lower($2)))
		ORDER BY (id = $2) DESC LIMIT 1`, ws, strings.TrimSpace(ref)).Scan(&o.WorkflowID, &o.Name, &o.Version, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("no such workflow")
	}
	if err != nil {
		return nil, fail
	}
	o.Status = "paused"
	if enabled {
		o.Status = "enabled"
	}
	switch err := tx.QueryRowContext(ctx, `SELECT paused, reason FROM automation_settings WHERE workspace_id = $1`, ws).
		Scan(&o.WorkspacePaused, &o.PausedReason); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, fail
	}

	since := time.Now().UTC().AddDate(0, 0, -OverviewWindowDays)
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status = 'ok'),
		       count(*) FILTER (WHERE status = 'failed'),
		       count(*) FILTER (WHERE status = 'cancelled'),
		       count(*) FILTER (WHERE status IN ('queued', 'running'))
		FROM automation_runs WHERE workspace_id = $1 AND workflow_id = $2 AND created_at >= $3`,
		ws, o.WorkflowID, since).Scan(&o.Runs.Total, &o.Runs.Succeeded, &o.Runs.Failed, &o.Runs.Cancelled, &o.Runs.InProgress); err != nil {
		return nil, fail
	}
	o.SuccessRate = SuccessRate{Succeeded: o.Runs.Succeeded, Finished: o.Runs.Succeeded + o.Runs.Failed}
	if o.SuccessRate.Finished > 0 {
		p := float64(o.SuccessRate.Succeeded) * 100 / float64(o.SuccessRate.Finished)
		o.SuccessRate.Percent = &p
	}

	// Newest run, in or out of the window.
	var lr LastRun
	var started, finished sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT id, status, trigger, workflow_version, created_at, started_at, finished_at, error
		FROM automation_runs WHERE workspace_id = $1 AND workflow_id = $2
		ORDER BY created_at DESC, id DESC LIMIT 1`, ws, o.WorkflowID).
		Scan(&lr.ID, &lr.Status, &lr.Trigger, &lr.Version, &lr.CreatedAt, &started, &finished, &lr.Error)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, fail
	default:
		lr.FinishedAt = timep(finished)
		if started.Valid && finished.Valid {
			d := finished.Time.Sub(started.Time).Milliseconds()
			lr.DurationMS = &d
		}
		o.LastRun = &lr
	}

	// Steps and items come from a bounded sample of the window's newest runs.
	rr, err := tx.QueryContext(ctx, `
		SELECT id, workflow_version FROM automation_runs
		WHERE workspace_id = $1 AND workflow_id = $2 AND created_at >= $3
		ORDER BY created_at DESC, id DESC LIMIT $4`, ws, o.WorkflowID, since, overviewRunSample)
	if err != nil {
		return nil, fail
	}
	var ids []string
	versionOf := map[string]int{}
	for rr.Next() {
		var id string
		var v int
		if err := rr.Scan(&id, &v); err != nil {
			_ = rr.Close()
			return nil, fail
		}
		ids = append(ids, id)
		versionOf[id] = v
	}
	_ = rr.Close()
	if err := rr.Err(); err != nil {
		return nil, fail
	}
	o.Sampled = o.Runs.Total > len(ids)

	stats := map[int]*StepStat{}
	seenLast := map[int]bool{}
	runItems := map[string]int{}
	if len(ids) > 0 {
		rows, err := tx.QueryContext(ctx, `
			SELECT st.run_id, st.position, st.status, st.duration_ms,
			       CASE WHEN st.status = 'ok' AND st.command IN ('extract', 'read', 'eval_readonly') THEN st.result ELSE '' END
			FROM automation_run_steps st
			JOIN automation_runs r ON r.workspace_id = st.workspace_id AND r.id = st.run_id
			WHERE st.workspace_id = $1 AND st.run_id = ANY($2)
			ORDER BY r.created_at DESC, r.id DESC, st.position
			LIMIT $3`, ws, ids, overviewStepRows)
		if err != nil {
			return nil, fail
		}
		n := 0
		for rows.Next() {
			var runID, status, result string
			var pos, dur int
			if err := rows.Scan(&runID, &pos, &status, &dur, &result); err != nil {
				_ = rows.Close()
				return nil, fail
			}
			n++
			cnt, has := itemsInResult(result)
			if has {
				runItems[runID] += cnt
			}
			if versionOf[runID] != o.Version {
				continue
			}
			st := stats[pos]
			if st == nil {
				st = &StepStat{Position: pos}
				stats[pos] = st
			}
			if status == "ok" || status == "failed" {
				st.Ran++
				if status == "ok" {
					st.OK++
				} else {
					st.Failed++
				}
			}
			if !seenLast[pos] && status != "pending" && status != "skipped" {
				seenLast[pos] = true
				st.LastStatus = status
				d := dur
				st.LastDuration = &d
				if has {
					c := cnt
					st.LastItems = &c
				}
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fail
		}
		if n >= overviewStepRows {
			o.Sampled = true
		}
	}
	for p := 0; p < 10000 && len(o.Steps) < len(stats); p++ {
		if st, ok := stats[p]; ok {
			o.Steps = append(o.Steps, *st)
		}
	}
	if len(runItems) > 0 {
		total := 0
		for _, c := range runItems {
			total += c
		}
		o.Items = ItemsStat{Total: &total, Runs: len(runItems)}
	}
	if o.LastRun != nil {
		if n, ok := runItems[o.LastRun.ID]; ok {
			o.LastRun.Items = &n
		} else if _, inSample := versionOf[o.LastRun.ID]; !inSample {
			n, ok, err := lastRunItems(ctx, tx, ws, o.LastRun.ID)
			if err != nil {
				return nil, fail
			}
			if ok {
				o.LastRun.Items = &n
			}
		}
	}

	var next sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE enabled), min(next_run_at) FILTER (WHERE enabled)
		FROM automation_schedules WHERE workspace_id = $1 AND workflow_id = $2`, ws, o.WorkflowID).
		Scan(&o.Schedules.Total, &o.Schedules.Enabled, &next); err != nil {
		return nil, fail
	}
	o.Schedules.NextRunAt = timep(next)
	return o, nil
}

// lastRunItems counts one run's rows when it fell outside the sample.
func lastRunItems(ctx context.Context, tx *sql.Tx, ws, runID string) (int, bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT result FROM automation_run_steps
		WHERE workspace_id = $1 AND run_id = $2 AND status = 'ok' AND command IN ('extract', 'read', 'eval_readonly') AND result <> ''
		LIMIT 200`, ws, runID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	total, found := 0, false
	for rows.Next() {
		var res string
		if err := rows.Scan(&res); err != nil {
			return 0, false, err
		}
		if n, ok := itemsInResult(res); ok {
			total += n
			found = true
		}
	}
	return total, found, rows.Err()
}

// RegisterOverview mounts GET /api/w/workflows/{id}/overview.
func RegisterOverview(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-overview", Method: http.MethodGet, Path: "/api/w/workflows/{id}/overview",
		Summary: "A workflow's header figures and per-step record, from its real runs (MH-1067)",
		Description: "Everything is computed from automation_runs and automation_run_steps over the last window_days days. " +
			"success_rate.percent is succeeded of finished (succeeded + failed) and is null with no finished run. " +
			"items.total is null when no run extracted anything.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[WorkflowOverview], error) {
		o, err := ReadWorkflowOverview(ctx, a.SQLDB, in.ID)
		if err != nil {
			return nil, err
		}
		return &body[WorkflowOverview]{*o}, nil
	})
}
