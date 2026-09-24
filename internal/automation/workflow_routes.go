package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Workflow is a saved, parameterised sequence.
type Workflow struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Steps        []Step    `json:"steps"`
	Params       []Param   `json:"params"`
	Placeholders []string  `json:"placeholders" doc:"Every {{placeholder}} the steps actually use, in order."`
	RunCount     int       `json:"run_count"`
	Version      int       `json:"version" doc:"The current version. Every save adds one (MH-400)."`
	// How a tool call names it, and whether it may run at all (MH-478).
	Slug      string     `json:"slug"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at"`
	// What a preset carries besides its steps (MH-478): the sites it goes to,
	// shown before anyone runs it; its icon; whether it publishes, sends or
	// submits as the signed-in account; and which preset it came from.
	Hosts     []string `json:"hosts"`
	Icon      string   `json:"icon"`
	Writes    bool     `json:"writes"`
	Preset    string   `json:"preset"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type workflowOut struct {
	Workflow Workflow `json:"workflow"`
}

// RegisterWorkflows mounts the saved-workflow endpoints.
//
// Reading is automation:read; saving, editing and running are automation:run —
// a workflow is a run someone else will press, so saving one is not a lighter
// right than starting one.
func RegisterWorkflows(api huma.API, a *app.App, hub *Hub) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-workflows", Method: http.MethodGet, Path: "/api/w/workflows",
		Summary: "Saved workflows", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Workflows []Workflow `json:"workflows"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT `+workflowColumns+`
			FROM automation_workflows WHERE workspace_id = $1 ORDER BY created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the workflows")
		}
		defer rows.Close()

		list := []Workflow{}
		for rows.Next() {
			w, err := scanWorkflow(rows)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read the workflows")
			}
			list = append(list, w)
		}
		return out(struct {
			Workflows []Workflow `json:"workflows"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-workflow", Method: http.MethodGet, Path: "/api/w/workflows/{id}",
		Summary: "One saved workflow, as the editor opens it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[workflowOut], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		return out(workflowOut{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-workflow", Method: http.MethodPost, Path: "/api/w/workflows",
		DefaultStatus: http.StatusCreated,
		Summary:       "Save a sequence of steps to run again", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body WorkflowInput
	}) (*body[workflowOut], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if err := ValidateWorkflow(in.Body); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		id, err := CreateWorkflow(ctx, a, s.WorkspaceID, s.UserID, in.Body, NoteCreated)
		if err != nil {
			return nil, workflowError(err, "could not save the workflow")
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, err
		}
		return out(workflowOut{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-workflow", Method: http.MethodPut, Path: "/api/w/workflows/{id}",
		Summary: "Save a workflow over itself, as a new version (MH-400)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			WorkflowInput
			BaseVersion int `json:"base_version,omitempty" minimum:"0" doc:"The version the editor opened. A save against an older one is refused (409) rather than overwriting someone else's edit. 0 skips the check."`
		}
	}) (*body[workflowOut], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if err := ValidateWorkflow(in.Body.WorkflowInput); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if _, err := SaveWorkflow(ctx, a, s.WorkspaceID, s.UserID, in.ID, in.Body.WorkflowInput, in.Body.BaseVersion); err != nil {
			return nil, workflowError(err, "could not save the workflow")
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		return out(workflowOut{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-versions", Method: http.MethodGet, Path: "/api/w/workflows/{id}/versions",
		Summary: "Every saved version of a workflow, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Versions []WorkflowVersion `json:"versions"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		// Asked through the workflow, so another workspace's id is a 404 like
		// everywhere else rather than an empty list.
		if _, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID); err != nil {
			return nil, err
		}
		list, err := ListWorkflowVersions(ctx, a, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the versions")
		}
		return out(struct {
			Versions []WorkflowVersion `json:"versions"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-restore-workflow-version", Method: http.MethodPost, Path: "/api/w/workflows/{id}/versions/{version}/restore",
		Summary: "Make an old version current again — as a new version, so the restore can be undone too", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
		// A string, parsed here: huma's own integer validation echoes the
		// path back in its error, and the isolation sweep rightly counts that
		// as handing another workspace's id back to the caller.
		Version string `path:"version"`
		Body    struct {
			BaseVersion int `json:"base_version,omitempty" minimum:"0" doc:"The version the page was showing as current."`
		}
	}) (*body[workflowOut], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if _, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID); err != nil {
			return nil, err
		}
		version, perr := strconv.Atoi(in.Version)
		if perr != nil || version < 1 {
			return nil, huma.Error404NotFound(ErrNoVersion.Error())
		}
		if _, err := RestoreWorkflow(ctx, a, s.WorkspaceID, s.UserID, in.ID, version, in.Body.BaseVersion); err != nil {
			return nil, workflowError(err, "could not restore that version")
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		return out(workflowOut{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-toggle-workflow", Method: http.MethodPatch, Path: "/api/w/workflows/{id}",
		Summary: "Turn a workflow on or off without deleting it (MH-478)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}) (*body[workflowOut], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `UPDATE automation_workflows SET enabled = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID, in.Body.Enabled)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not change the workflow")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("no such workflow")
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		return out(workflowOut{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-workflow", Method: http.MethodDelete, Path: "/api/w/workflows/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Delete a workflow", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		// A schedule running this workflow would fail at its next due time with
		// nobody watching. Refused here, where someone is.
		var using string
		if err := db().QueryRowContext(ctx, `
			SELECT name FROM automation_schedules WHERE workspace_id = $1 AND workflow_id = $2 ORDER BY name LIMIT 1`,
			s.WorkspaceID, in.ID).Scan(&using); err == nil {
			return nil, huma.Error409Conflict("the schedule \"" + using + "\" runs this workflow; delete or change that schedule first")
		}
		res, err := db().ExecContext(ctx, `DELETE FROM automation_workflows WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the workflow")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("no such workflow")
		}
		// Runs it already produced stay: they are the record of what happened,
		// not part of the workflow. Its versions go with it (ON DELETE CASCADE).
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-run-workflow", Method: http.MethodPost, Path: "/api/w/workflows/{id}/run",
		DefaultStatus: http.StatusCreated,
		Summary:       "Run a saved workflow with values for its placeholders", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			AgentID string            `json:"agent_id"`
			Values  map[string]string `json:"values,omitempty"`
		}
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		runID, err := RunWorkflow(ctx, a, hub, s.WorkspaceID, s.UserID, in.ID, in.Body.AgentID, "manual", in.Body.Values)
		if err != nil {
			// Logged as well as answered: a 500 here used to say only "could not
			// start that run", which is no help at all when something in the
			// substitution or the storage is what failed.
			a.Log.Warn("workflow run failed", "workflow", in.ID, "workspace", s.WorkspaceID, "err", err)
			return nil, runError(err)
		}
		return out(struct {
			RunID string `json:"run_id"`
		}{runID}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-workflow", Method: http.MethodPost, Path: "/api/w/workflows/test",
		DefaultStatus: http.StatusCreated,
		Summary:       "Test-run the steps open in the editor, saved or not (MH-400)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			AgentID    string            `json:"agent_id"`
			WorkflowID string            `json:"workflow_id,omitempty" doc:"The workflow being edited, so the run links back to it. Empty for one not saved yet."`
			Steps      []Step            `json:"steps" maxItems:"200"`
			Params     []Param           `json:"params,omitempty" maxItems:"40"`
			Values     map[string]string `json:"values,omitempty"`
		}
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if err := ValidateWorkflow(WorkflowInput{Name: "test", Steps: in.Body.Steps, Params: in.Body.Params}); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		name := "Test run"
		if in.Body.WorkflowID != "" {
			w, err := getWorkflow(ctx, db(), s.WorkspaceID, in.Body.WorkflowID)
			if err != nil {
				return nil, err
			}
			name = w.Name + " (test)"
		}
		// The same Fill as a real run: the secrets are substituted into what
		// runs and redacted from what is stored, and the allow-list sees the
		// filled-in hosts. A test is a real run in a real browser.
		run, stored, err := Fill(cleanInput(WorkflowInput{Steps: in.Body.Steps}).Steps, in.Body.Params, in.Body.Values)
		if err != nil {
			return nil, runError(err)
		}
		runID, err := hub.Start(ctx, a, s.WorkspaceID, s.UserID, "test", in.Body.AgentID, run)
		if err != nil {
			return nil, runError(err)
		}
		recordRedacted(ctx, a, s.WorkspaceID, runID, stored, name, in.Body.WorkflowID)
		return out(struct {
			RunID string `json:"run_id"`
		}{runID}), nil
	})
}

// workflowError maps the editing errors to statuses.
func workflowError(err error, fallback string) error {
	switch {
	case errors.Is(err, ErrNoWorkflow):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrNoVersion):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrStaleVersion), errors.Is(err, ErrNameTaken), errors.Is(err, ErrSlugTaken):
		return huma.Error409Conflict(err.Error())
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

// RunWorkflow fills a workflow's placeholders and starts it. Shared by the
// REST route, MCP and schedules, so the substitution and the redaction happen
// in exactly one place.
func RunWorkflow(ctx context.Context, a *app.App, hub *Hub, workspaceID, userID, workflowID, agentID, trigger string, values map[string]string) (string, error) {
	return RunWorkflowFrom(ctx, a, hub, workspaceID, userID, workflowID, agentID, trigger, "", values)
}

// ErrWorkflowOff is a workflow switched off (MH-478): kept, with its history
// and schedules, and refusing to run until it is switched back on.
var ErrWorkflowOff = errors.New("this workflow is turned off")

// RunWorkflowFrom is RunWorkflow for a run a schedule started: the run keeps
// which schedule it was, so the schedule's row is settled by its own run and
// never by a neighbour's (fadymondy.com FM-95).
func RunWorkflowFrom(ctx context.Context, a *app.App, hub *Hub, workspaceID, userID, workflowID, agentID, trigger, scheduleID string, values map[string]string) (string, error) {
	// Scoped here, not by the caller: the scheduler calls this from a
	// background context with no workspace on it, and RLS then showed it no
	// workflow at all — every schedule of a saved workflow was "missed: no
	// such workflow" (MH-403).
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	w, err := FindWorkflow(sctx, a, workspaceID, workflowID)
	if err != nil {
		return "", err
	}
	workflowID = w.ID
	if !w.Enabled {
		return "", fmt.Errorf("%w: %s", ErrWorkflowOff, w.Name)
	}
	run, stored, err := Fill(w.Steps, w.Params, values)
	if err != nil {
		return "", err
	}

	runID, err := hub.Start(ctx, a, workspaceID, userID, trigger, agentID, run)
	if err != nil {
		return "", err
	}
	recordRedacted(sctx, a, workspaceID, runID, stored, w.Name, workflowID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET workflow_version = $3, schedule_id = $4 WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID, w.Version, scheduleID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_workflows SET run_count = run_count + 1, last_run_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, workflowID)
	return runID, nil
}

// recordRedacted replaces what a run stored with the REDACTED steps: a run is
// read back by a person and handed to a model, and a secret substituted into
// step args would be in both, for ever. The steps stored on each step row are
// redacted too — the run's own copy is not the only place a value lands.
func recordRedacted(ctx context.Context, a *app.App, workspaceID, runID string, stored []Step, name, workflowID string) {
	redacted, _ := json.Marshal(map[string]any{"steps": stored, "workflow": name})
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_runs SET params = $3::jsonb, workflow_id = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, runID, string(redacted), workflowID)
	for i, st := range stored {
		args, _ := json.Marshal(st.Args)
		if len(args) == 0 || string(args) == "null" {
			args = []byte("{}")
		}
		_, _ = a.SQLDB.ExecContext(ctx, `
			UPDATE automation_run_steps SET args = $4::jsonb, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, string(args))
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

const workflowColumns = `id, name, description, steps::text, params::text, run_count, version, created_at, updated_at, slug, enabled, last_run_at,
	hosts, icon, writes, preset`

func scanWorkflow(r rowScanner) (Workflow, error) {
	var w Workflow
	var steps, params string
	var last sql.NullTime
	var hosts pgtext.Array
	if err := r.Scan(&w.ID, &w.Name, &w.Description, &steps, &params, &w.RunCount, &w.Version, &w.CreatedAt, &w.UpdatedAt,
		&w.Slug, &w.Enabled, &last, &hosts, &w.Icon, &w.Writes, &w.Preset); err != nil {
		return w, err
	}
	w.Hosts = hosts
	if w.Hosts == nil {
		w.Hosts = []string{}
	}
	w.LastRunAt = timep(last)
	_ = json.Unmarshal([]byte(steps), &w.Steps)
	_ = json.Unmarshal([]byte(params), &w.Params)
	if w.Steps == nil {
		w.Steps = []Step{}
	}
	if w.Params == nil {
		w.Params = []Param{}
	}
	w.Placeholders = Placeholders(w.Steps)
	if w.Placeholders == nil {
		w.Placeholders = []string{}
	}
	return w, nil
}

func getWorkflow(ctx context.Context, db *sql.DB, workspaceID, id string) (Workflow, error) {
	row := db.QueryRowContext(ctx, `
		SELECT `+workflowColumns+`
		FROM automation_workflows WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	w, err := scanWorkflow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return w, huma.Error404NotFound("no such workflow")
	}
	if err != nil {
		return w, huma.Error500InternalServerError("could not read that workflow")
	}
	return w, nil
}

// FindWorkflow resolves an id or a slug — a tool call reads better with a slug,
// the page has the id (fadymondy.com's FindPlugin).
func FindWorkflow(ctx context.Context, a *app.App, workspaceID, ref string) (Workflow, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	row := a.SQLDB.QueryRowContext(sctx, `
		SELECT `+workflowColumns+`
		FROM automation_workflows WHERE workspace_id = $1 AND (id = $2 OR (slug <> '' AND slug = lower($2)))
		ORDER BY (id = $2) DESC LIMIT 1`, workspaceID, strings.TrimSpace(ref))
	w, err := scanWorkflow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return w, huma.Error404NotFound("no such workflow")
	}
	if err != nil {
		return w, huma.Error500InternalServerError("could not read that workflow")
	}
	return w, nil
}

func firstNonNil(p []Param) []Param {
	if p == nil {
		return []Param{}
	}
	return p
}
