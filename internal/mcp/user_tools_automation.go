package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
AutomationTools: read the browsers and their runs (automation:read), and drive
one (automation:run).

A model gets exactly the same guardrails as a person, because it goes through
the same automation.Hub.Start: the kill switch, the browser's host allow-list,
one run at a time, and the run deadline. Nothing here re-implements any of
them, which is the point — a second copy of the allow-list check is a second
copy that can be wrong.

What a model deliberately CANNOT do here: pair a browser or lift the kill
switch; those have no tool. Changing a browser's allowed sites is
browser_set_allowed_sites (user_tools_browser.go), under automation:manage,
which only an admin's token can carry (MH-478).
*/
func AutomationTools() []Tool {
	return []Tool{
		{
			Name:        "list_browsers",
			Description: "The browsers paired with this workspace: id, name, platform, whether each is connected right now (online), the hosts it may visit, when it was last seen.",
			Scope:       "automation:read",
			InputSchema: schema(nil, nil),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT id, name, platform, allowed_hosts, last_seen_at, revoked_at
					FROM browser_agents WHERE workspace_id = $1 AND revoked_at IS NULL
					ORDER BY created_at DESC`, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				defer rows.Close()

				hub := automation.Default()
				items := []map[string]any{}
				for rows.Next() {
					var id, name, platform string
					var hosts pgtext.Array
					var lastSeen sql.NullTime
					var revoked sql.NullTime
					if err := rows.Scan(&id, &name, &platform, &hosts, &lastSeen, &revoked); err != nil {
						return "", err
					}
					online := hub != nil && hub.Online(id)
					var lastSeenAt any
					if lastSeen.Valid {
						lastSeenAt = lastSeen.Time
					}
					items = append(items, map[string]any{
						"id": id, "name": name, "platform": platform, "online": online,
						"allowed_hosts": []string(hosts), "last_seen_at": lastSeenAt,
					})
				}
				if err := rows.Err(); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(items), "items": items})
			},
		},
		{
			Name: "start_browser_run",
			Description: "Drive a paired browser through a sequence of steps. Each step is {command, args}. " +
				"A step that navigates is refused unless its host is on that browser's allow-list. " +
				"Returns run_id; read it back with get_browser_run.",
			Scope: "automation:run",
			InputSchema: schema([]string{"browser_id", "steps"}, map[string]any{
				"browser_id": map[string]any{"type": "string", "description": "From list_browsers"},
				"steps": map[string]any{
					"type":        "array",
					"description": `e.g. [{"command":"goto","args":{"url":"https://example.com"}},{"command":"extract","args":{"selector":"h1"}}]`,
					"items": map[string]any{
						"type":     "object",
						"required": []string{"command"},
						"properties": map[string]any{
							"command":           map[string]any{"type": "string"},
							"args":              map[string]any{"type": "object"},
							"continue_on_error": map[string]any{"type": "boolean"},
						},
					},
				},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					BrowserID string            `json:"browser_id"`
					Steps     []automation.Step `json:"steps"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				hub := automation.Default()
				if hub == nil {
					return "", errors.New("the browser registry is not running")
				}
				// trigger "mcp", and the token's user as the asker: "why did this
				// browser open my mail at 4am" has to be answerable.
				runID, err := hub.Start(ctx, a, s.WorkspaceID, s.UserID, "mcp", in.BrowserID, in.Steps)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "run_id": runID})
			},
		},
		{
			Name:        "get_browser_run",
			Description: "One run: its status, error, step_count, ok_count, notes filed before its first step, and every step " +
				"(position, command, status, summary, result as the browser sent it, error).",
			Scope:       "automation:read",
			InputSchema: schema([]string{"run_id"}, map[string]any{
				"run_id": map[string]any{"type": "string"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					RunID string `json:"run_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				var status, runErr string
				var stepCount, okCount int
				var notes pgtext.Array
				err := a.SQLDB.QueryRowContext(ctx, `
					SELECT status, error, step_count, ok_count, notes FROM automation_runs
					WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.RunID,
				).Scan(&status, &runErr, &stepCount, &okCount, &notes)
				if errors.Is(err, sql.ErrNoRows) {
					return "", errNotFound
				}
				if err != nil {
					return "", err
				}

				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT position, command, status, summary, result, error
					FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
					s.WorkspaceID, in.RunID)
				if err != nil {
					return "", err
				}
				defer rows.Close()
				steps := []map[string]any{}
				for rows.Next() {
					var pos int
					var command, st, summary, result, stepErr string
					if err := rows.Scan(&pos, &command, &st, &summary, &result, &stepErr); err != nil {
						return "", err
					}
					step := map[string]any{"position": pos, "command": command, "status": st, "summary": summary, "error": stepErr}
					// The payload as the browser sent it, not a re-encoding of it:
					// a JSON round trip through a Go map reorders keys and renders
					// numbers differently, which is what makes an answer
					// unreproducible — so it is carried as the exact stored text,
					// not decoded and re-marshaled (it may also be truncated, which
					// would make it invalid JSON on its own).
					if result != "" {
						step["result"] = result
					}
					steps = append(steps, step)
				}
				if err := rows.Err(); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{
					"run_id": in.RunID, "status": status, "error": runErr, "step_count": stepCount, "ok_count": okCount,
					"notes": []string(notes), "steps": steps,
				})
			},
		},
	}
}

// WorkflowTools: a model runs a PREPARED sequence rather than composing raw
// steps. That is the safer shape — the steps were written and saved by a
// person, and the model only supplies values — and it is why start_workflow
// exists alongside start_browser_run rather than replacing it.
func WorkflowTools() []Tool {
	return []Tool{
		{
			Name:        "list_workflows",
			Description: "Saved workflows: id, name, description, run count, and the values each one asks for (name, required, secret).",
			Scope:       "automation:read",
			InputSchema: schema(nil, nil),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT id, name, description, params::text, run_count
					FROM automation_workflows WHERE workspace_id = $1 ORDER BY created_at DESC`, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				defer rows.Close()

				items := []map[string]any{}
				for rows.Next() {
					var id, name, desc, params string
					var runs int
					if err := rows.Scan(&id, &name, &desc, &params, &runs); err != nil {
						return "", err
					}
					var ps []automation.Param
					_ = json.Unmarshal([]byte(params), &ps)
					items = append(items, map[string]any{
						"id": id, "name": name, "description": desc, "run_count": runs, "params": ps,
					})
				}
				if err := rows.Err(); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(items), "items": items})
			},
		},
		{
			Name: "start_workflow",
			Description: "Run a saved workflow on a paired browser, supplying values for its placeholders. " +
				"A workflow that publishes, sends or submits needs confirm: true — without it, returns needs_confirm: true and runs nothing.",
			Confirm: "A workflow that publishes, sends or submits only runs when the call carries confirm: true; without it nothing runs.",
			Scope: "automation:run",
			InputSchema: schema([]string{"workflow_id", "browser_id"}, map[string]any{
				"workflow_id": map[string]any{"type": "string", "description": "From list_workflows"},
				"browser_id":  map[string]any{"type": "string", "description": "From list_browsers"},
				"values":      map[string]any{"type": "object", "description": "Values for the workflow's placeholders"},
				"confirm":     map[string]any{"type": "boolean", "description": "required for a workflow that publishes"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					WorkflowID string            `json:"workflow_id"`
					BrowserID  string            `json:"browser_id"`
					Values     map[string]string `json:"values"`
					Confirm    bool              `json:"confirm"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				w, err := automation.FindWorkflow(ctx, a, s.WorkspaceID, in.WorkflowID)
				if err != nil {
					return "", err
				}
				if w.Writes && !in.Confirm {
					return jsonResult(map[string]any{
						"ok": false, "needs_confirm": true, "workflow": w.Name, "hosts": w.Hosts,
					})
				}
				hub := automation.Default()
				if hub == nil {
					return "", errors.New("the browser registry is not running")
				}
				runID, err := automation.RunWorkflow(ctx, a, hub, s.WorkspaceID, s.UserID, w.ID, in.BrowserID, "mcp", in.Values)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "run_id": runID})
			},
		},
	}
}
