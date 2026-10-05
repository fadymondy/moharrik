package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// WorkflowRunTools: the workflow overview and "run again" (MH-1067). Both are
// the REST routes' own functions, so a model sees the same figures and meets
// the same refusals as the page.
func WorkflowRunTools() []Tool {
	return []Tool{
		{
			Name: "workflow_overview",
			Description: "One workflow's record from its real runs over the last window_days (30) days: status (enabled or paused, as stored), " +
				"run counts by outcome, success_rate (succeeded of finished = succeeded + failed; percent is null with no finished run), " +
				"the last run (status, trigger, version, duration, error, items), items.total (rows its extract steps stored; null when nothing was extracted), " +
				"its schedules, and a per-step record (ran, ok, failed, last status) for the current version.",
			Scope: "automation:read",
			InputSchema: schema([]string{"workflow_id"}, map[string]any{
				"workflow_id": prop("string", "From list_workflows (an id or a slug)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, _ := decodeArgs(raw)["workflow_id"].(string)
				o, err := automation.ReadWorkflowOverview(ctx, a.SQLDB, id)
				if err != nil {
					return "", err
				}
				return jsonResult(o)
			},
		},
		{
			Name: "rerun_workflow_run",
			Description: "Run a finished run again, exactly as it ran: the steps it stored are repeated, so the workflow version it ran is the one that runs, " +
				"even if the workflow was edited since. Records the new run as a re-run of the original (rerun_of). " +
				"A run that used a secret value cannot be repeated, because the value is never stored. A workflow that publishes, sends or submits " +
				"needs confirm: true — without it, returns needs_confirm: true and runs nothing.",
			Scope: "automation:run",
			InputSchema: schema([]string{"run_id"}, map[string]any{
				"run_id":     prop("string", "The run to repeat, from get_browser_run"),
				"browser_id": prop("string", "Which browser to run on; defaults to the one the original ran on"),
				"confirm":    prop("boolean", "Required for a workflow that publishes, sends or submits"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					RunID     string `json:"run_id"`
					BrowserID string `json:"browser_id"`
					Confirm   bool   `json:"confirm"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", err
				}
				hub := automation.Default()
				if hub == nil {
					return "", errors.New("the browser registry is not running")
				}
				id, err := automation.RerunRun(ctx, a, hub, s.WorkspaceID, s.UserID, in.RunID, in.BrowserID, in.Confirm)
				var nc *automation.NeedsConfirm
				if errors.As(err, &nc) {
					return jsonResult(map[string]any{"ok": false, "needs_confirm": true, "workflow": nc.Workflow, "hosts": nc.Hosts})
				}
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "run_id": id, "rerun_of": in.RunID})
			},
		},
	}
}
