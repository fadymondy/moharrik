package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

What a model deliberately CANNOT do here: pair a browser, change its
allow-list, or lift the kill switch. Those are authz.AutomationManage (admin)
and have no tool at all, so no token carries them.
*/
func AutomationTools() []Tool {
	return []Tool{
		{
			Name:        "list_browsers",
			Description: "The browsers paired with this workspace, whether each is connected right now, and where it may be driven.",
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
				var b strings.Builder
				n := 0
				for rows.Next() {
					var id, name, platform string
					var hosts pgtext.Array
					var lastSeen sql.NullTime
					var revoked sql.NullTime
					if err := rows.Scan(&id, &name, &platform, &hosts, &lastSeen, &revoked); err != nil {
						return "", err
					}
					status := "offline"
					if hub != nil && hub.Online(id) {
						status = "online"
					}
					where := "nothing allowed yet"
					if len(hosts) > 0 {
						where = strings.Join(hosts, ", ")
					}
					fmt.Fprintf(&b, "%s  [%s]  %s  may visit: %s\n", id, status, name, where)
					n++
				}
				if n == 0 {
					return "No browser is paired with this workspace yet. Pairing one is done by an admin on the Automation page.", nil
				}
				return b.String(), nil
			},
		},
		{
			Name: "start_browser_run",
			Description: "Drive a paired browser through a sequence of steps. Each step is {command, args}. " +
				"A step that navigates is refused unless its host is on that browser's allow-list. " +
				"Returns the run id; read it back with get_browser_run.",
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
				return "Run " + runID + " started. Read it with get_browser_run.", nil
			},
		},
		{
			Name:        "get_browser_run",
			Description: "One run: its status and every step, with what each step returned.",
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
				err := a.SQLDB.QueryRowContext(ctx, `
					SELECT status, error, step_count, ok_count FROM automation_runs
					WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.RunID,
				).Scan(&status, &runErr, &stepCount, &okCount)
				if errors.Is(err, sql.ErrNoRows) {
					return "", errNotFound
				}
				if err != nil {
					return "", err
				}

				var b strings.Builder
				fmt.Fprintf(&b, "run %s: %s (%d/%d steps ok)\n", in.RunID, status, okCount, stepCount)
				if runErr != "" {
					fmt.Fprintf(&b, "error: %s\n", runErr)
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT position, command, status, summary, result, error
					FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
					s.WorkspaceID, in.RunID)
				if err != nil {
					return "", err
				}
				defer rows.Close()
				for rows.Next() {
					var pos int
					var command, st, summary, result, stepErr string
					if err := rows.Scan(&pos, &command, &st, &summary, &result, &stepErr); err != nil {
						return "", err
					}
					fmt.Fprintf(&b, "\n%d. %s — %s\n", pos+1, command, st)
					if stepErr != "" {
						fmt.Fprintf(&b, "   error: %s\n", stepErr)
					}
					// The payload as the browser sent it, not a re-encoding of it:
					// a JSON round trip reorders keys and renders numbers
					// differently, which is what makes an answer unreproducible.
					if result != "" {
						fmt.Fprintf(&b, "   result: %s\n", result)
					} else if summary != "" {
						fmt.Fprintf(&b, "   %s\n", summary)
					}
				}
				return b.String(), nil
			},
		},
	}
}
