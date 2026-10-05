package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Re-run (MH-1067): "run it again, exactly as it ran".
//
// What is repeated is what the run STORED — the steps it actually ran, read
// from automation_runs.params — and not the workflow as it is saved today. So
// the original version is genuinely what runs again, even when the workflow has
// been edited since; the version is not looked up, it is carried in the steps.
//
// The price of that honesty: a secret parameter's value is never stored (the
// run keeps "••••••" in its place), so a run that used one cannot be repeated,
// and says so rather than running with the mask typed into a login form.

var (
	// ErrRerunSecret: the run's stored steps carry a redacted secret.
	ErrRerunSecret = errors.New("this run used a secret value, which is never stored, so it cannot be run again — start the workflow again and enter it")
	// ErrRerunActive: the original has not finished.
	ErrRerunActive = errors.New("this run has not finished yet")
	// ErrRerunNoSteps: nothing stored to repeat.
	ErrRerunNoSteps = errors.New("this run stored no steps to repeat")
)

// NeedsConfirm: the workflow publishes, sends or submits, and the caller did not confirm.
type NeedsConfirm struct {
	Workflow string
	Hosts    []string
}

func (e *NeedsConfirm) Error() string {
	return fmt.Sprintf("%s publishes, sends or submits as the signed-in account on %s. Pass confirm: true to run it.",
		e.Workflow, strings.Join(e.Hosts, ", "))
}

// RerunRun starts a new run from the steps run `runID` stored, on agentID (the
// original's browser when empty), records it as a re-run of that run, and
// returns the new run's id. It goes through the same Hub.Start as every other
// way of starting a run, so the kill switch, the allow-list, the one-at-a-time
// rule and the capacity limits all apply unchanged.
func RerunRun(ctx context.Context, a *app.App, hub *Hub, workspaceID, userID, runID, agentID string, confirm bool) (string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var params, origAgent, workflowID, status string
	var version int
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT params::text, agent_id, workflow_id, workflow_version, status
		FROM automation_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).
		Scan(&params, &origAgent, &workflowID, &version, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", huma.Error404NotFound("no such run")
	}
	if err != nil {
		return "", err
	}
	if status == "queued" || status == "running" {
		return "", ErrRerunActive
	}
	var stored struct {
		Steps    []Step `json:"steps"`
		Workflow string `json:"workflow"`
	}
	if json.Unmarshal([]byte(params), &stored) != nil || len(stored.Steps) == 0 {
		return "", ErrRerunNoSteps
	}
	if raw, _ := json.Marshal(stored.Steps); strings.Contains(string(raw), "••••••") {
		return "", ErrRerunSecret
	}
	if agentID == "" {
		agentID = origAgent
	}

	// The workflow's own rules still apply: switched off means off, and a
	// workflow that writes still asks first. A workflow deleted since is
	// repeated as the plain run it was.
	hosts := []string(nil)
	name := stored.Workflow
	if workflowID != "" {
		w, err := FindWorkflow(ctx, a, workspaceID, workflowID)
		switch {
		case err == nil:
			if !w.Enabled {
				return "", fmt.Errorf("%w: %s", ErrWorkflowOff, w.Name)
			}
			if w.Writes && !confirm {
				return "", &NeedsConfirm{Workflow: w.Name, Hosts: w.Hosts}
			}
			hosts, name = w.Hosts, w.Name
		case isNotFound(err):
			workflowID = ""
		default:
			return "", err
		}
	}

	note, err := addRequiredSites(ctx, a, hub, workspaceID, userID, "manual", agentID, hosts, stored.Steps, stored.Steps)
	if err != nil {
		return "", err
	}
	newID, err := hub.Start(ctx, a, workspaceID, userID, "manual", agentID, stored.Steps)
	if err != nil {
		return "", err
	}
	addRunNote(sctx, a, workspaceID, newID, note)
	if workflowID != "" {
		recordRedacted(sctx, a, workspaceID, newID, stored.Steps, name, workflowID)
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_workflows SET run_count = run_count + 1, last_run_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, workflowID)
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET rerun_of = $3, workflow_version = $4 WHERE workspace_id = $1 AND id = $2`,
		workspaceID, newID, runID, version); err != nil {
		a.Log.Warn("could not link a re-run to its original", "run", newID, "original", runID, "err", err)
	}
	return newID, nil
}

func isNotFound(err error) bool {
	var se huma.StatusError
	return errors.As(err, &se) && se.GetStatus() == http.StatusNotFound
}

// rerunError turns RerunRun's errors into the answers a client can act on.
func rerunError(err error) error {
	var nc *NeedsConfirm
	switch {
	case errors.As(err, &nc):
		return huma.Error409Conflict(nc.Error())
	case errors.Is(err, ErrRerunSecret), errors.Is(err, ErrRerunNoSteps):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, ErrRerunActive):
		return huma.Error409Conflict(err.Error())
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return runError(err)
}

// RegisterRerun mounts POST /api/w/runs/{id}/rerun.
func RegisterRerun(api huma.API, a *app.App, hub *Hub) {
	huma.Register(api, huma.Operation{
		OperationID: "w-run-rerun", Method: http.MethodPost, Path: "/api/w/runs/{id}/rerun",
		DefaultStatus: http.StatusCreated,
		Summary:       "Run a finished run again from the steps it stored (MH-1067)",
		Description: "Repeats the steps the run stored, so the workflow version it ran is the one that runs again, " +
			"even if the workflow has been edited since. A run that used a secret value cannot be repeated: the value is never stored. " +
			"The new run records which run it repeated (rerun_of). Needs the same permission as starting a workflow.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			AgentID string `json:"agent_id,omitempty" doc:"The browser to run on. Defaults to the one the original ran on."`
			Confirm bool   `json:"confirm,omitempty" doc:"Required when the workflow publishes, sends or submits."`
		}
	}) (*body[struct {
		RunID   string `json:"run_id"`
		RerunOf string `json:"rerun_of"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		id, err := RerunRun(ctx, a, hub, s.WorkspaceID, s.UserID, in.ID, in.Body.AgentID, in.Body.Confirm)
		if err != nil {
			if !isNotFound(err) {
				a.Log.Warn("re-run failed", "run", in.ID, "workspace", s.WorkspaceID, "err", err)
			}
			return nil, rerunError(err)
		}
		return out(struct {
			RunID   string `json:"run_id"`
			RerunOf string `json:"rerun_of"`
		}{id, in.ID}), nil
	})
}
