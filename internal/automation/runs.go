package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ErrRevoked marks a token that exists but has been taken away.
var ErrRevoked = errors.New("this browser's token was revoked")

// Step is one command as submitted.
type Step struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
	// ContinueOnError keeps the run going when this step fails. Off by default:
	// a run whose first step failed is usually not worth continuing, and a step
	// sequence that carries on regardless produces results nobody can trust.
	ContinueOnError bool `json:"continue_on_error,omitempty"`
}

// Run is the record of what a browser was asked to do.
type Run struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id"`
	AgentName   string     `json:"agent_name"`
	Trigger     string     `json:"trigger"`
	RequestedBy string     `json:"requested_by"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	StepCount   int        `json:"step_count"`
	OKCount     int        `json:"ok_count"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// RunStep is one step and what came back for it.
type RunStep struct {
	Position   int    `json:"position"`
	Command    string `json:"command"`
	Args       string `json:"args"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Result     string `json:"result"`
	Error      string `json:"error"`
	DurationMS int    `json:"duration_ms"`
}

// ── the kill switch ──────────────────────────────────────────────────────

// IsPaused reports whether this workspace's automation is stopped, and why.
// The absence of a row means "not paused", so a workspace that never touched
// automation needs no row.
func IsPaused(ctx context.Context, a *app.App, workspaceID string) (bool, string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var paused bool
	var reason string
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT paused, reason FROM automation_settings WHERE workspace_id = $1`, workspaceID).Scan(&paused, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		// Fail CLOSED: if we cannot tell whether automation is stopped, it is
		// stopped. The alternative is driving someone's signed-in browser on the
		// strength of a failed query.
		return true, "the kill switch could not be read", err
	}
	return paused, reason, nil
}

// Pause throws the kill switch.
func Pause(ctx context.Context, a *app.App, workspaceID, userID, reason string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, paused, paused_at, paused_by, reason)
		VALUES ($1, true, now(), $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			paused = true, paused_at = now(), paused_by = EXCLUDED.paused_by,
			reason = EXCLUDED.reason, updated_at = now()`,
		workspaceID, userID, trim(reason, 200))
	return err
}

// Resume lifts it.
func Resume(ctx context.Context, a *app.App, workspaceID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, paused) VALUES ($1, false)
		ON CONFLICT (workspace_id) DO UPDATE SET
			paused = false, paused_at = NULL, paused_by = '', reason = '', updated_at = now()`, workspaceID)
	return err
}

// CancelWorkspaceRuns marks everything queued or running as cancelled. The
// dispatcher notices on its next step; this is what makes the record honest in
// the meantime, so nothing sits "running" for ever after a stop.
func CancelWorkspaceRuns(ctx context.Context, a *app.App, workspaceID, reason string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET status = 'cancelled', error = $2, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND status IN ('queued', 'running')`, workspaceID, trim(reason, 200))
}

// ── starting a run ───────────────────────────────────────────────────────

// Start checks every guardrail, records the run, and dispatches it in the
// background. It returns as soon as the run exists, because a sequence of
// steps in a real browser takes longer than a request should.
//
// The order of the checks is deliberate: the kill switch first (it is the
// control someone reaches for in a hurry), then the browser, then whether it is
// busy, then the steps themselves. Nothing is written until all of them pass.
func (h *Hub) Start(ctx context.Context, a *app.App, workspaceID, userID, trigger, agentID string, steps []Step) (string, error) {
	if paused, why, _ := IsPaused(ctx, a, workspaceID); paused {
		if why != "" {
			return "", fmt.Errorf("%w: %s", ErrPaused, why)
		}
		return "", ErrPaused
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var name string
	var hosts pgtext.Array
	var revoked sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT name, allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&name, &hosts, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("no such browser")
	}
	if err != nil {
		return "", err
	}
	if revoked.Valid {
		return "", ErrRevoked
	}
	if !h.Online(agentID) {
		return "", ErrOffline
	}
	if err := CheckSteps(steps, hosts); err != nil {
		return "", err
	}

	// One run at a time per browser. Checked here AND enforced by the partial
	// index the query reads, so two requests racing cannot both pass.
	var live int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM automation_runs
		WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('queued', 'running')`,
		workspaceID, agentID).Scan(&live); err != nil {
		return "", err
	}
	if live > 0 {
		return "", ErrBusy
	}

	params, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return "", err
	}

	var runID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, trigger, requested_by, params, status, step_count)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6) RETURNING id`,
		workspaceID, agentID, trigger, userID, string(params), len(steps)).Scan(&runID); err != nil {
		return "", err
	}
	for i, s := range steps {
		args, _ := json.Marshal(s.Args)
		if len(args) == 0 {
			args = []byte("{}")
		}
		if _, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO automation_run_steps (workspace_id, run_id, position, command, args, status)
			VALUES ($1, $2, $3, $4, $5, 'pending')`,
			workspaceID, runID, i, s.Command, string(args)); err != nil {
			return "", err
		}
	}

	// Detached from the request: the caller gets the run id now and watches it.
	go h.dispatch(a, workspaceID, agentID, runID, steps)
	return runID, nil
}

// dispatch walks the steps, one command at a time, and records each result as
// it arrives rather than at the end — a run that dies halfway should still show
// what it managed to do.
func (h *Hub) dispatch(a *app.App, workspaceID, agentID, runID string, steps []Step) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultRunDeadline)
	defer cancel()
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET status = 'running', started_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'queued'`, workspaceID, runID)

	okCount := 0
	runErr := ""

	for i, step := range steps {
		// The kill switch is re-read between steps: "stop" has to stop a run
		// that is already going, not only refuse the next one.
		if paused, why, _ := IsPaused(ctx, a, workspaceID); paused {
			runErr = "automation was stopped"
			if why != "" {
				runErr += ": " + why
			}
			h.finish(sctx, a, workspaceID, runID, "cancelled", runErr, okCount)
			return
		}
		if cancelled(sctx, a, workspaceID, runID) {
			return // someone cancelled it; the row already says so
		}

		args, _ := json.Marshal(step.Args)
		payload, _ := json.Marshal(map[string]any{"command": step.Command, "args": json.RawMessage(args)})

		started := time.Now()
		reply, err := h.Call(ctx, workspaceID, agentID, Envelope{Type: "run.step", Run: runID, Payload: payload}, DefaultStepTimeout)
		duration := int(time.Since(started).Milliseconds())

		status, summary, result, stepErr := "ok", "", "", ""
		if err != nil {
			status, stepErr = "failed", err.Error()
		} else {
			// Stored as it arrived. A JSON round trip through a Go map reorders
			// keys and renders numbers differently — the kind of quiet difference
			// that makes a model's answer unreproducible.
			result = string(reply.Payload)
			summary = summarise(reply.Payload)
			okCount++
		}

		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_run_steps SET status = $4, summary = $5, result = $6, error = $7, duration_ms = $8, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, status, trim(summary, 300), trim(result, 200_000), trim(stepErr, 500), duration)

		if err != nil && !step.ContinueOnError {
			// The remaining steps are marked skipped rather than left pending:
			// "pending" on a finished run reads as "still going".
			_, _ = a.SQLDB.ExecContext(sctx, `
				UPDATE automation_run_steps SET status = 'skipped', updated_at = now()
				WHERE workspace_id = $1 AND run_id = $2 AND position > $3 AND status = 'pending'`,
				workspaceID, runID, i)
			h.finish(sctx, a, workspaceID, runID, "failed", (&StepError{Position: i, Err: err}).Error(), okCount)
			return
		}
		if err != nil {
			runErr = (&StepError{Position: i, Err: err}).Error() // kept, but the run goes on
		}
	}

	status := "ok"
	if runErr != "" {
		status = "failed" // a step failed and was continued past; the run is not "ok"
	}
	h.finish(sctx, a, workspaceID, runID, status, runErr, okCount)
}

func (h *Hub) finish(ctx context.Context, a *app.App, workspaceID, runID, status, errText string, okCount int) {
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE automation_runs SET status = $3, error = $4, ok_count = $5, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, runID, status, trim(errText, 500), okCount); err != nil {
		h.log.Error("run finish write failed", "run", runID, "err", err)
	}
}

func cancelled(ctx context.Context, a *app.App, workspaceID, runID string) bool {
	var status string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).Scan(&status); err != nil {
		return false
	}
	return status == "cancelled"
}

// summarise turns a reply into the one line a list can show. The full payload
// is kept on the step; this is only what fits on a row without opening it.
func summarise(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var generic map[string]any
	if json.Unmarshal(payload, &generic) == nil {
		for _, key := range []string{"summary", "title", "url", "text", "value"} {
			if v, ok := generic[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.Join(strings.Fields(v), " ")
			}
		}
	}
	return strings.Join(strings.Fields(string(payload)), " ")
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
