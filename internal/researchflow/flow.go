// Package researchflow is the numbers behind the Research home (MH-1066): what the research
// funnel holds right now, and for each recent run its stages and how far it got.
//
// Nothing is scored or estimated here. Every number is a count of rows that exist:
//
//	found       research_companies: companies research named. For a run: the companies whose
//	            company_key appears in that run's research_evidence (the set the run detail lists).
//	evidence    research_evidence rows (for a run: the rows with that run_id).
//	prospects   prospects rows, i.e. companies discovery scored. For a run: prospects whose
//	            first_run_id is that run. Only discovery runs (origin = 'discovery') score
//	            prospects, so a manual or scheduled run has 0 prospects by design.
//	high_fit    prospects whose score is at least HighFitScore (home.HighFitScore, 0-100),
//	            whatever their status.
//	accepted    prospects already accepted into the CRM (status = 'accepted').
//
// Stages are the run's own stages jsonb, passed through exactly as the engine wrote it.
//
// Read-only, one REPEATABLE READ READ ONLY transaction on the RLS pool, workspace_id in every
// predicate, research:read (and, for an MCP token or OAuth grant, the token must carry it).
package researchflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/home"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

const (
	HighFitScore   = home.HighFitScore
	DefaultRuns    = 8
	MaxRuns        = 25
	prospectScored = `p.workspace_id = r.workspace_id AND p.first_run_id = r.id`
)

// FlowCounts are the funnel numbers, for the workspace or for one run.
type FlowCounts struct {
	Found     int64 `json:"found" doc:"Companies research named."`
	Evidence  int64 `json:"evidence" doc:"Evidence rows kept."`
	Prospects int64 `json:"prospects" doc:"Prospects discovery scored."`
	HighFit   int64 `json:"high_fit" doc:"Prospects scoring at least thresholds.high_fit_score."`
	Accepted  int64 `json:"accepted" doc:"Prospects already accepted into the CRM."`
}

// FlowRun is one recent run: its stages as stored and how far it got.
type FlowRun struct {
	ID         string                   `json:"id"`
	Topic      string                   `json:"topic"`
	Status     string                   `json:"status" enum:"queued,running,ok,failed,cancelled"`
	Origin     string                   `json:"origin" enum:"manual,schedule,discovery"`
	Mode       string                   `json:"mode" enum:"search,urls"`
	Stage      string                   `json:"stage" doc:"The stage it is in while it runs; empty once settled."`
	Stages     []research.ResearchStage `json:"stages" doc:"The run's stages exactly as the engine stored them: stage, status, count, note, started_at, finished_at."`
	Error      string                   `json:"error"`
	Counts     FlowCounts               `json:"counts"`
	StartedAt  *time.Time               `json:"started_at"`
	FinishedAt *time.Time               `json:"finished_at"`
	CreatedAt  time.Time                `json:"created_at"`
}

// FlowSchedules is how much research runs on its own.
type FlowSchedules struct {
	Total     int64      `json:"total"`
	Enabled   int64      `json:"enabled"`
	NextRunAt *time.Time `json:"next_run_at" doc:"The soonest next run among the enabled schedules."`
}

// FlowThresholds are the numbers the counts were derived with.
type FlowThresholds struct {
	HighFitScore int `json:"high_fit_score" doc:"A prospect scoring at least this (0-100) is high-fit."`
}

// ResearchOverview is the whole answer.
type ResearchOverview struct {
	Thresholds FlowThresholds `json:"thresholds"`
	Totals     FlowCounts     `json:"totals" doc:"The whole workspace, every run."`
	Runs       []FlowRun      `json:"runs" doc:"Recent runs, newest first, each with its own counts."`
	Schedules  FlowSchedules  `json:"schedules"`
}

func canRead(s *tenant.Scope) bool {
	if !authz.Can(s.Role, authz.ResearchRead) {
		return false
	}
	if s.Via == "mcp" || s.Via == "oauth" {
		for _, sc := range s.Scopes {
			if sc == authz.ResearchRead {
				return true
			}
		}
		return false
	}
	return true
}

// Read returns the overview for the caller in ctx. runs is how many recent runs to list (0 = default).
func Read(ctx context.Context, db *sql.DB, runs int) (*ResearchOverview, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !canRead(s) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	n, err := paging.Limit(runs, DefaultRuns, MaxRuns)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	fail := huma.Error500InternalServerError("could not read the research overview")
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, fail
	}
	defer func() { _ = tx.Rollback() }()

	ws := s.WorkspaceID
	out := &ResearchOverview{Runs: []FlowRun{}, Thresholds: FlowThresholds{HighFitScore: HighFitScore}}
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM research_companies WHERE workspace_id = $1),
		  (SELECT count(*) FROM research_evidence WHERE workspace_id = $1),
		  (SELECT count(*) FROM prospects WHERE workspace_id = $1),
		  (SELECT count(*) FROM prospects WHERE workspace_id = $1 AND score >= $2),
		  (SELECT count(*) FROM prospects WHERE workspace_id = $1 AND status = 'accepted')`,
		ws, HighFitScore).Scan(&out.Totals.Found, &out.Totals.Evidence, &out.Totals.Prospects, &out.Totals.HighFit, &out.Totals.Accepted); err != nil {
		return nil, fail
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE enabled), min(next_run_at) FILTER (WHERE enabled)
		FROM research_schedules WHERE workspace_id = $1`, ws).Scan(&out.Schedules.Total, &out.Schedules.Enabled, &out.Schedules.NextRunAt); err != nil {
		return nil, fail
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, r.topic, r.status, r.origin, r.mode, r.stage, r.stages::text, r.error, r.started_at, r.finished_at, r.created_at,
		  (SELECT count(*) FROM research_companies c WHERE c.workspace_id = r.workspace_id AND c.company_key IN (
		      SELECT e.company_key FROM research_evidence e WHERE e.workspace_id = r.workspace_id AND e.run_id = r.id AND e.company_key <> '')),
		  (SELECT count(*) FROM research_evidence e WHERE e.workspace_id = r.workspace_id AND e.run_id = r.id),
		  (SELECT count(*) FROM prospects p WHERE `+prospectScored+`),
		  (SELECT count(*) FROM prospects p WHERE `+prospectScored+` AND p.score >= $2),
		  (SELECT count(*) FROM prospects p WHERE `+prospectScored+` AND p.status = 'accepted')
		FROM research_runs r WHERE r.workspace_id = $1
		ORDER BY r.created_at DESC, r.id LIMIT $3`, ws, HighFitScore, n)
	if err != nil {
		return nil, fail
	}
	defer rows.Close()
	for rows.Next() {
		var r FlowRun
		var stages string
		var started, finished sql.NullTime
		if err := rows.Scan(&r.ID, &r.Topic, &r.Status, &r.Origin, &r.Mode, &r.Stage, &stages, &r.Error, &started, &finished, &r.CreatedAt,
			&r.Counts.Found, &r.Counts.Evidence, &r.Counts.Prospects, &r.Counts.HighFit, &r.Counts.Accepted); err != nil {
			return nil, fail
		}
		_ = json.Unmarshal([]byte(stages), &r.Stages)
		if r.Stages == nil {
			r.Stages = []research.ResearchStage{}
		}
		if started.Valid {
			r.StartedAt = &started.Time
		}
		if finished.Valid {
			r.FinishedAt = &finished.Time
		}
		out.Runs = append(out.Runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fail
	}
	return out, nil
}
