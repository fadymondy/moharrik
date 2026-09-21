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
	CreatedAt    time.Time `json:"created_at"`
}

// RegisterWorkflows mounts the saved-workflow endpoints.
//
// Reading is automation:read; saving and running are automation:run — a
// workflow is a run someone else will press, so saving one is not a lighter
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
			SELECT id, name, description, steps::text, params::text, run_count, created_at
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
		OperationID: "w-create-workflow", Method: http.MethodPost, Path: "/api/w/workflows",
		DefaultStatus: http.StatusCreated,
		Summary:       "Save a sequence of steps to run again", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name        string  `json:"name" minLength:"1" maxLength:"80"`
			Description string  `json:"description,omitempty" maxLength:"400"`
			Steps       []Step  `json:"steps" maxItems:"200"`
			Params      []Param `json:"params,omitempty" maxItems:"40"`
		}
	}) (*body[struct {
		Workflow Workflow `json:"workflow"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		if len(in.Body.Steps) == 0 {
			return nil, huma.Error422UnprocessableEntity("a workflow needs at least one step")
		}
		// NOT checked against an allow-list here: the steps hold placeholders,
		// and a host that is only known once values are supplied cannot be
		// judged yet. The check happens when a run starts, on the filled-in
		// steps (workflows.go explains why that ordering matters).
		steps, err := json.Marshal(in.Body.Steps)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not store the steps")
		}
		params, err := json.Marshal(firstNonNil(in.Body.Params))
		if err != nil {
			return nil, huma.Error500InternalServerError("could not store the parameters")
		}

		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO automation_workflows (workspace_id, name, description, steps, params, created_by)
			VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6) RETURNING id`,
			s.WorkspaceID, strings.TrimSpace(in.Body.Name), strings.TrimSpace(in.Body.Description),
			string(steps), string(params), s.UserID).Scan(&id)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("a workflow with that name already exists")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the workflow")
		}
		got, err := getWorkflow(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Workflow Workflow `json:"workflow"`
		}{got}), nil
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
		res, err := db().ExecContext(ctx, `DELETE FROM automation_workflows WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the workflow")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("no such workflow")
		}
		// Runs it already produced stay: they are the record of what happened,
		// not part of the workflow.
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
}

// RunWorkflow fills a workflow's placeholders and starts it. Shared by the
// REST route, MCP and schedules, so the substitution and the redaction happen
// in exactly one place.
func RunWorkflow(ctx context.Context, a *app.App, hub *Hub, workspaceID, userID, workflowID, agentID, trigger string, values map[string]string) (string, error) {
	w, err := getWorkflow(ctx, a.SQLDB, workspaceID, workflowID)
	if err != nil {
		return "", err
	}
	run, stored, err := Fill(w.Steps, w.Params, values)
	if err != nil {
		return "", err
	}

	runID, err := hub.Start(ctx, a, workspaceID, userID, trigger, agentID, run)
	if err != nil {
		return "", err
	}

	// The run keeps the REDACTED steps: a run is read back by a person and
	// handed to a model, and a secret substituted into step args would be in
	// both, for ever.
	redacted, _ := json.Marshal(map[string]any{"steps": stored, "workflow": w.Name})
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_runs SET params = $3::jsonb, workflow_id = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, runID, string(redacted), workflowID)
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE automation_workflows SET run_count = run_count + 1, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, workflowID)

	// The steps stored ON EACH STEP ROW are redacted too: the run's own copy is
	// not the only place a value lands.
	for i, st := range stored {
		args, _ := json.Marshal(st.Args)
		if len(args) == 0 {
			args = []byte("{}")
		}
		_, _ = a.SQLDB.ExecContext(ctx, `
			UPDATE automation_run_steps SET args = $4::jsonb, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, string(args))
	}
	return runID, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflow(r rowScanner) (Workflow, error) {
	var w Workflow
	var steps, params string
	if err := r.Scan(&w.ID, &w.Name, &w.Description, &steps, &params, &w.RunCount, &w.CreatedAt); err != nil {
		return w, err
	}
	_ = json.Unmarshal([]byte(steps), &w.Steps)
	_ = json.Unmarshal([]byte(params), &w.Params)
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
		SELECT id, name, description, steps::text, params::text, run_count, created_at
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

func firstNonNil(p []Param) []Param {
	if p == nil {
		return []Param{}
	}
	return p
}
