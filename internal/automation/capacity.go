package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
How much automation a workspace may run at once, and how long the record of it
is kept (MH-234).

These sit beside the kill switch rather than in config, for the same reason the
kill switch does: they are a workspace's own limits on its own browsers, and
the person who needs to change them is the person looking at the runs — not
whoever can restart the process.
*/

const (
	// The ceiling when a workspace has never said otherwise. Deliberately low:
	// every run drives a real browser somebody is signed into. A cap that is
	// too low costs a queued run; one too high means twenty browsers moving at
	// once and no way to follow what happened.
	DefaultMaxParallelRuns = 3

	// Long enough to answer "why did this browser open my mail last month",
	// short enough that the table stays readable.
	DefaultRetentionDays = 30

	// How often the sweeper looks. Retention is measured in days, so this is
	// about not accumulating a day's backlog rather than about precision.
	retentionTick = 1 * time.Hour

	// Rows per workspace per sweep. A workspace that has been running for a
	// year has a lot to delete the first time; this keeps any one statement
	// short rather than holding a long lock on a busy table.
	retentionBatch = 500
)

// ErrAtCapacity is the workspace-wide limit answering. It is distinct from
// ErrBusy — that one is "this browser is occupied", which the caller can fix
// by picking another browser. This one means every slot is taken.
var ErrAtCapacity = errors.New("this workspace is already running as much automation as it allows")

// Limits are a workspace's automation ceilings.
type Limits struct {
	MaxParallelRuns int `json:"max_parallel_runs"`
	RetentionDays   int `json:"retention_days"`
}

// ReadLimits returns the workspace's ceilings, falling back to the defaults
// when it has no settings row — the same "absence means untouched" rule the
// kill switch uses.
//
// A failed read returns the DEFAULTS rather than an error the caller might
// treat as "no limit". The kill switch fails closed by refusing to run at all;
// this one cannot do that without making an unreadable settings row stop the
// whole workspace, so it falls back to the conservative number instead.
func ReadLimits(ctx context.Context, a *app.App, workspaceID string) Limits {
	limits := Limits{MaxParallelRuns: DefaultMaxParallelRuns, RetentionDays: DefaultRetentionDays}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var maxParallel, retention int
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT max_parallel_runs, retention_days FROM automation_settings WHERE workspace_id = $1`,
		workspaceID).Scan(&maxParallel, &retention)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return limits
	}
	if maxParallel > 0 {
		limits.MaxParallelRuns = maxParallel
	}
	if retention >= 0 {
		limits.RetentionDays = retention
	}
	return limits
}

// SetLimits records new ceilings. The bounds are checked here as well as by the
// table's constraints, so the caller gets a sentence rather than a Postgres
// error.
func SetLimits(ctx context.Context, a *app.App, workspaceID string, limits Limits) error {
	if limits.MaxParallelRuns < 1 || limits.MaxParallelRuns > 50 {
		return errors.New("parallel runs must be between 1 and 50")
	}
	if limits.RetentionDays < 0 || limits.RetentionDays > 3650 {
		return errors.New("retention must be between 0 and 3650 days")
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, max_parallel_runs, retention_days)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			max_parallel_runs = EXCLUDED.max_parallel_runs,
			retention_days = EXCLUDED.retention_days,
			updated_at = now()`,
		workspaceID, limits.MaxParallelRuns, limits.RetentionDays)
	return err
}

// LiveRuns counts what is queued or running for a workspace right now.
func LiveRuns(ctx context.Context, a *app.App, workspaceID string) (int, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var live int
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM automation_runs
		WHERE workspace_id = $1 AND status IN ('queued', 'running')`, workspaceID).Scan(&live)
	return live, err
}

// ── retention ────────────────────────────────────────────────────────────

// SweepRuns deletes finished runs past each workspace's retention window.
// Steps go with them: automation_run_steps cascades from the run.
//
// It returns how many runs it removed, which is what the tests assert on and
// what the log line reports.
//
// Live runs are never touched, however old they look. A run stuck in "running"
// is a bug worth seeing, and deleting it would hide the evidence — and, now
// that the live index is unique, would also quietly free a slot the guardrail
// is deliberately holding.
func SweepRuns(ctx context.Context, a *app.App, now time.Time) (int, error) {
	// Which workspaces have anything to delete is by definition a cross-tenant
	// question, so the survey runs on the OWNER pool — the same split the
	// scheduler uses. Each deletion then happens inside its own workspace.
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT w.id, COALESCE(s.retention_days, $1)
		FROM workspaces w
		LEFT JOIN automation_settings s ON s.workspace_id = w.id
		WHERE COALESCE(s.retention_days, $1) > 0`, DefaultRetentionDays)
	if err != nil {
		return 0, err
	}

	type target struct {
		id   string
		days int
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.days); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	deleted := 0
	for _, t := range targets {
		cutoff := now.AddDate(0, 0, -t.days)
		sctx := tenant.WithWorkspace(ctx, t.id)

		// COALESCE(finished_at, created_at): a cancelled run that never started
		// has no finish time, and would otherwise be kept for ever by a NULL.
		res, err := a.SQLDB.ExecContext(sctx, `
			DELETE FROM automation_runs
			WHERE workspace_id = $1
			  AND status NOT IN ('queued', 'running')
			  AND COALESCE(finished_at, created_at) < $2
			  AND id IN (
				SELECT id FROM automation_runs
				WHERE workspace_id = $1
				  AND status NOT IN ('queued', 'running')
				  AND COALESCE(finished_at, created_at) < $2
				ORDER BY created_at
				LIMIT $3
			  )`, t.id, cutoff, retentionBatch)
		if err != nil {
			// One workspace's failure must not stop the others: the sweeper
			// runs unattended, and a single bad row should not mean nothing is
			// ever pruned again.
			continue
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}

	return deleted, nil
}

// StartRetention runs the sweeper for the life of the process, beside the
// scheduler and the collectors. SCHEDULER=off disables it too: the tests and
// the OpenAPI export want no background work at all, and retention is exactly
// the kind of thing that must not delete rows under a test.
//
// In-process, like the other tickers. A second replica would sweep the same
// rows — harmless here, because the delete is idempotent, but the note belongs
// next to the decision rather than in someone's memory.
func StartRetention(a *app.App, log *slog.Logger) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if os.Getenv("SCHEDULER") == "off" {
		return cancel
	}
	go func() {
		ticker := time.NewTicker(retentionTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := SweepRuns(ctx, a, time.Now().UTC())
				if err != nil {
					log.Error("automation retention sweep failed", "err", err)
					continue
				}
				if n > 0 {
					log.Info("automation retention swept", "runs", n)
				}
			}
		}
	}()
	return cancel
}

// ── claiming a slot ──────────────────────────────────────────────────────

// claimSlot takes the run's place in the queue and writes it, or refuses.
//
// Everything that decides whether the run may exist happens inside one
// transaction, behind a per-workspace advisory lock:
//
//   - the workspace's parallel-run ceiling, which is a COUNT and therefore
//     cannot be expressed as a constraint;
//   - one live run per browser, which is now a unique index, but whose
//     violation would otherwise surface as a Postgres error rather than as
//     "that browser is busy";
//   - the insert of the run and its steps, so a run never exists with half
//     its steps missing.
//
// The lock is advisory and transaction-scoped: it is released when the
// transaction ends, however it ends. Starts are infrequent and the work inside
// is two counts and a few inserts, so serialising them per workspace costs
// nothing anybody can perceive.
func (h *Hub) claimSlot(ctx context.Context, a *app.App, workspaceID, agentID, trigger, userID, params string, steps []Step) (string, error) {
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	// hashtext gives a stable int from the workspace id. Two workspaces could
	// in principle collide on it; the only consequence is that their run starts
	// briefly serialise with each other, which is harmless.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, workspaceID); err != nil {
		return "", err
	}

	var agentLive, workspaceLive int
	if err := tx.QueryRowContext(ctx, `
		SELECT
			count(*) FILTER (WHERE agent_id = $2),
			count(*)
		FROM automation_runs
		WHERE workspace_id = $1 AND status IN ('queued', 'running')`,
		workspaceID, agentID).Scan(&agentLive, &workspaceLive); err != nil {
		return "", err
	}

	// The browser first: it is the more specific answer, and the one the
	// caller can act on by choosing a different browser.
	if agentLive > 0 {
		return "", ErrBusy
	}

	limits := ReadLimits(ctx, a, workspaceID)
	if workspaceLive >= limits.MaxParallelRuns {
		return "", ErrAtCapacity
	}

	var runID string
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, trigger, requested_by, params, status, step_count)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6) RETURNING id`,
		workspaceID, agentID, trigger, userID, params, len(steps)).Scan(&runID); err != nil {
		// The unique live index caught a race the lock did not cover — another
		// process, or a workspace-id hash collision. "Busy" is the truthful
		// answer, and better than a constraint name.
		if isLiveRunConflict(err) {
			return "", ErrBusy
		}
		return "", err
	}

	for i, step := range steps {
		args, _ := json.Marshal(step.Args)
		if len(args) == 0 {
			args = []byte("{}")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO automation_run_steps (workspace_id, run_id, position, command, args, status)
			VALUES ($1, $2, $3, $4, $5, 'pending')`,
			workspaceID, runID, i, step.Command, string(args)); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return runID, nil
}

// isLiveRunConflict reports whether err is the unique live-run index refusing a
// second run for one browser. Matched on the index name rather than on a driver
// type, so it holds whichever pgx/lib-pq wrapper the pool is built from.
func isLiveRunConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "automation_runs_live_uniq")
}
