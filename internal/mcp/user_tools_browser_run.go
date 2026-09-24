package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Driving a browser from a model, fadymondy.com's way (MH-478 phase 4): the rest
of its browser_* tools — browser_agents, browser_extract, browser_run,
browser_result, browser_cancel — with the same names, arguments and answers,
inside the token's workspace.

Three things from fadymondy.com's browser_tools.go carry over unchanged:

  ASYNCHRONOUS BY DEFAULT. A tool starts the run, waits a short window in case
  the work is quick, and answers with the data or with the run's key for
  browser_result. No MCP client's own timeout is anywhere near.

  "NO BROWSER" IS AN ANSWER. A closed laptop is the normal state of a
  browser, so these say so in a sentence a model can pass on, not an error.

  NO NAME, NO GUESS. With no browser named, a run goes to the only connected
  one; with several, the model is asked to name one — picking whichever sorted
  first would open a page on somebody's machine at random.

And one thing is Moharrik's: none of these can widen where a browser may go.
A raw run is checked against the browser's allowed sites before anything is
queued (and every step again, in the browser — MH-422); a refused host is
named, with the tool that changes the list (browser_set_allowed_sites, an
admin's). Only a SAVED workflow's run adds its own sites (MH-517).
*/

// How long a tool waits before answering with a key instead of the data.
var browserWaitWindow = 25 * time.Second

// An extract's data is quoted back up to this size; past it the model gets
// the start and where the rest is.
const modelPayloadLimit = 24 << 10

func BrowserRunTools() []Tool {
	agentProp := map[string]any{"type": "string", "description": "Browser name or id. Omit to use the only connected one."}
	return []Tool{
		{
			Name: "browser_agents",
			Description: "List the browsers paired with this workspace: name, whether each is connected right now, " +
				"what it runs on, when it was last seen, and the sites it may be driven on. These are REAL browsers, " +
				"signed into their owners' own accounts.",
			Scope:       "automation:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT id, name, platform, extension_version, allowed_hosts, last_seen_at, revoked_at
					FROM browser_agents WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 100`, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				defer rows.Close()
				hub := automation.Default()
				var access map[string]automation.SiteAccess
				if hub != nil {
					access = hub.AccessIn(s.WorkspaceID)
				}
				var b strings.Builder
				n := 0
				for rows.Next() {
					var id, name, platform, version string
					var hosts pgtext.Array
					var seen, revoked sql.NullTime
					if err := rows.Scan(&id, &name, &platform, &version, &hosts, &seen, &revoked); err != nil {
						return "", err
					}
					state := "offline"
					if hub != nil && hub.Online(id) {
						state = "online"
					}
					if revoked.Valid {
						state = "revoked"
					}
					fmt.Fprintf(&b, "- %s (%s) — %s", name, id, state)
					if platform != "" {
						fmt.Fprintf(&b, ", %s", platform)
					}
					if version != "" {
						fmt.Fprintf(&b, ", extension %s", version)
					}
					if seen.Valid {
						fmt.Fprintf(&b, ", last seen %s", seen.Time.UTC().Format(time.RFC3339))
					}
					b.WriteString("\n")
					if len(hosts) > 0 {
						fmt.Fprintf(&b, "    may be driven on: %s\n", strings.Join(hosts, ", "))
					} else {
						b.WriteString("    may be driven on: nothing yet — an admin adds sites (browser_set_allowed_sites, or the Automation page)\n")
					}
					if acc, ok := access[id]; ok && acc.Mode == "sites" && len(acc.Pending) > 0 {
						fmt.Fprintf(&b, "    waiting for an OK in the browser for: %s\n", strings.Join(acc.Pending, ", "))
					}
					n++
				}
				if n == 0 {
					return "No browsers are paired yet. Install the extension and connect it from its popup; an admin approves it on the site.", nil
				}
				return fmt.Sprintf("%d paired browser(s):\n\n%s", n, b.String()), nil
			},
		},
		{
			Name: "browser_extract",
			Description: "Open a URL in one of the workspace's browsers and pull data out of the page, using the " +
				"session that browser is already signed into. Give `fields` as a map of name -> CSS selector, and " +
				"`list` when the page has repeating rows (each field is then read inside each row). Returns the " +
				"extracted data, or a run key to poll if the page is slow. The URL's site must be on the browser's " +
				"allowed sites. It drives a real browser, visible on its owner's screen while it runs.",
			Scope: "automation:run",
			InputSchema: schema([]string{"url", "fields"}, map[string]any{
				"agent":    agentProp,
				"url":      map[string]any{"type": "string"},
				"list":     map[string]any{"type": "string", "description": "CSS selector for repeating rows, e.g. 'table tbody tr'"},
				"fields":   map[string]any{"type": "object", "description": "name -> CSS selector. Append '|@href' to read an attribute, '|html' for markup."},
				"wait_for": map[string]any{"type": "string", "description": "Selector to wait for before extracting."},
				"limit":    map[string]any{"type": "integer"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Agent   string            `json:"agent"`
					URL     string            `json:"url"`
					List    string            `json:"list"`
					Fields  map[string]string `json:"fields"`
					WaitFor string            `json:"wait_for"`
					Limit   int               `json:"limit"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", err
				}
				if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
					return "url must be an http(s) address.", nil
				}
				if len(in.Fields) == 0 {
					return "fields must name at least one selector.", nil
				}
				b, why, err := resolveOnline(ctx, a, s.WorkspaceID, in.Agent)
				if err != nil || b == nil {
					return why, err
				}
				steps := []automation.Step{{Command: "navigate", Args: map[string]any{"url": in.URL}}}
				if in.WaitFor != "" {
					steps = append(steps, automation.Step{Command: "wait_for", Args: map[string]any{"selector": in.WaitFor}})
				}
				fields := map[string]any{}
				for k, v := range in.Fields {
					fields[k] = v
				}
				extract := map[string]any{"fields": fields}
				if in.List != "" {
					extract["list"] = in.List
				}
				if in.Limit > 0 {
					extract["limit"] = in.Limit
				}
				steps = append(steps, automation.Step{Command: "extract", Args: extract})
				return startAndReport(ctx, a, s, b, steps)
			},
		},
		{
			Name: "browser_run",
			Description: "Run an explicit sequence of browser commands on a workspace browser. Commands: navigate, " +
				"wait_for, click, type, press, scroll, select, upload, extract, screenshot, eval_readonly. Each step " +
				"is {\"command\": ..., \"args\": {...}}. Prefer browser_extract for plain 'open this and read that'; " +
				"use this when the data needs interaction first (filter, paginate). Every site it goes to must be on " +
				"the browser's allowed sites.",
			Scope: "automation:run",
			InputSchema: schema([]string{"steps"}, map[string]any{
				"agent": agentProp,
				"steps": map[string]any{"type": "array", "description": "[{command, args, continue_on_error, timeout_seconds}]", "items": map[string]any{"type": "object"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Agent string            `json:"agent"`
					Steps []automation.Step `json:"steps"`
				}
				if err := json.Unmarshal(raw, &in); err != nil || len(in.Steps) == 0 {
					return "steps must be a list of {command, args}.", nil
				}
				for i, st := range in.Steps {
					if strings.TrimSpace(st.Command) == "" {
						return fmt.Sprintf("step %d has no command.", i+1), nil
					}
				}
				b, why, err := resolveOnline(ctx, a, s.WorkspaceID, in.Agent)
				if err != nil || b == nil {
					return why, err
				}
				return startAndReport(ctx, a, s, b, in.Steps)
			},
		},
		{
			Name: "browser_result",
			Description: "Fetch a browser run by key: its status, each step with timing, the reason it stopped if " +
				"it failed, and the extracted data when it finished.",
			Scope:       "automation:read",
			InputSchema: schema([]string{"run"}, map[string]any{"run": map[string]any{"type": "string"}}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Run string `json:"run"`
				}
				_ = json.Unmarshal(raw, &in)
				return describeRun(ctx, a, s.WorkspaceID, in.Run)
			},
		},
		{
			Name:        "browser_cancel",
			Description: "Stop a browser run. It stops after the step currently in flight.",
			Scope:       "automation:run",
			InputSchema: schema([]string{"run"}, map[string]any{"run": map[string]any{"type": "string"}}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Run string `json:"run"`
				}
				_ = json.Unmarshal(raw, &in)
				var status string
				err := a.SQLDB.QueryRowContext(ctx, `SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`,
					s.WorkspaceID, strings.TrimSpace(in.Run)).Scan(&status)
				if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(in.Run) == "" {
					return "No run with that key.", nil
				}
				if err != nil {
					return "", err
				}
				stopped, err := automation.CancelRun(ctx, a, s.WorkspaceID, in.Run)
				if err != nil {
					return "", err
				}
				if !stopped {
					return fmt.Sprintf("Run %s had already finished (%s); nothing to stop.", in.Run, status), nil
				}
				return fmt.Sprintf("Run %s is cancelled; it stops after the step in flight.", in.Run), nil
			},
		},
	}
}

// resolveOnline turns what the model said into a CONNECTED browser, or
// explains why not.
func resolveOnline(ctx context.Context, a *app.App, workspaceID, ref string) (*browserRef, string, error) {
	hub := automation.Default()
	online := func(id string) bool { return hub != nil && hub.Online(id) }
	if strings.TrimSpace(ref) != "" {
		b, why, err := findBrowser(ctx, a, workspaceID, ref)
		if err != nil || b == nil {
			return nil, why, err
		}
		if !online(b.id) {
			return nil, fmt.Sprintf("The browser %q is paired but not connected right now. It needs to be open with the extension running; ask its owner to open it, then try again.", b.name), nil
		}
		return b, "", nil
	}
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT id, name, allowed_hosts FROM browser_agents WHERE workspace_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 100`, workspaceID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var all, connected []browserRef
	for rows.Next() {
		var b browserRef
		var hosts pgtext.Array
		if err := rows.Scan(&b.id, &b.name, &hosts); err != nil {
			return nil, "", err
		}
		b.hosts = []string(hosts)
		all = append(all, b)
		if online(b.id) {
			connected = append(connected, b)
		}
	}
	names := func(bs []browserRef) string {
		out := make([]string, 0, len(bs))
		for _, b := range bs {
			out = append(out, b.name)
		}
		return strings.Join(out, ", ")
	}
	switch {
	case len(all) == 0:
		return nil, "No browser is paired with this workspace. Install the extension and connect it from its popup.", nil
	case len(connected) == 0:
		return nil, "No browser of this workspace is connected right now. It needs to be open with the extension running.", nil
	case len(connected) == 1:
		return &connected[0], "", nil
	default:
		return nil, "More than one browser is connected — name the one to use: " + names(connected), nil
	}
}

// startAndReport starts a raw run, waits a short window, and reports whatever
// state it reached. A host the browser does not allow is refused with the way
// to allow it: a raw run never widens the list.
func startAndReport(ctx context.Context, a *app.App, s *tenant.Scope, b *browserRef, steps []automation.Step) (string, error) {
	hub := automation.Default()
	if hub == nil {
		return "", errors.New("the browser registry is not running")
	}
	runID, err := hub.Start(ctx, a, s.WorkspaceID, s.UserID, "mcp", b.id, steps)
	if err != nil {
		if errors.Is(err, automation.ErrHostNotAllowed) {
			return fmt.Sprintf("%s. Nothing was run. An admin can allow the site with browser_set_allowed_sites, or on the Automation page.", err.Error()), nil
		}
		return "", err
	}
	return waitAndDescribe(ctx, a, s.WorkspaceID, b.name, runID)
}

// waitAndDescribe waits up to browserWaitWindow for a run to finish, then
// describes it.
func waitAndDescribe(ctx context.Context, a *app.App, workspaceID, browserName, runID string) (string, error) {
	deadline := time.Now().Add(browserWaitWindow)
	for time.Now().Before(deadline) {
		var status string
		if err := a.SQLDB.QueryRowContext(ctx, `SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).Scan(&status); err == nil &&
			status != "queued" && status != "running" {
			return describeRun(ctx, a, workspaceID, runID)
		}
		select {
		case <-ctx.Done():
			return fmt.Sprintf("Run %s is still going; poll it with browser_result.", runID), nil
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Sprintf("Run %s on %s is still going after %s. Poll it with browser_result — it keeps running in the browser.",
		runID, browserName, browserWaitWindow), nil
}

// describeRun is what the model reads: the outcome first, then the data.
func describeRun(ctx context.Context, a *app.App, workspaceID, runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "Give the run key returned when the run started.", nil
	}
	var status, runErr string
	var stepCount, okCount int
	var notes pgtext.Array
	err := a.SQLDB.QueryRowContext(ctx, `SELECT status, error, step_count, ok_count, notes FROM automation_runs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID).Scan(&status, &runErr, &stepCount, &okCount, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return "No run with that key. It may have aged out — runs are kept for the workspace's retention period.", nil
	}
	if err != nil {
		return "", err
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT position, command, status, summary, result, error, duration_ms FROM automation_run_steps
		WHERE workspace_id = $1 AND run_id = $2 ORDER BY position LIMIT 200`, workspaceID, runID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type stepRow struct {
		pos                                  int
		command, status, summary, res, error string
		ms                                   int
	}
	var steps []stepRow
	for rows.Next() {
		var r stepRow
		if err := rows.Scan(&r.pos, &r.command, &r.status, &r.summary, &r.res, &r.error, &r.ms); err != nil {
			return "", err
		}
		steps = append(steps, r)
	}
	// ok_count is stamped when a run finishes: one still going is counted
	// from its steps, or the header would contradict the list under it.
	if status == "queued" || status == "running" {
		okCount = 0
		for _, st := range steps {
			if st.status == "ok" {
				okCount++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run %s — %s (%d/%d steps ok)\n", runID, status, okCount, stepCount)
	for _, n := range notes {
		fmt.Fprintf(&b, "Note: %s\n", n)
	}
	if runErr != "" {
		fmt.Fprintf(&b, "Stopped: %s\n", runErr)
	}
	if len(steps) > 0 {
		b.WriteString("\nSteps:\n")
		for _, st := range steps {
			detail := st.summary
			if st.error != "" {
				detail = st.error
			}
			fmt.Fprintf(&b, "  %d. %s — %s (%dms) %s\n", st.pos+1, st.command, st.status, st.ms, detail)
		}
	}
	if status == "queued" || status == "running" {
		b.WriteString("\nStill going — poll again in a few seconds.\n")
		return b.String(), nil
	}
	// The data: every extract step's result, quoted as the browser sent it.
	var data strings.Builder
	for _, st := range steps {
		if (st.command == "extract" || st.command == "read") && st.status == "ok" && st.res != "" {
			if data.Len() > 0 {
				data.WriteString("\n")
			}
			fmt.Fprintf(&data, "step %d: %s", st.pos+1, st.res)
		}
	}
	if data.Len() == 0 {
		return b.String(), nil
	}
	if data.Len() > modelPayloadLimit {
		fmt.Fprintf(&b, "\nThe extracted data is %d kB — too large to quote in full. All of it is at /api/w/runs/%s/data (CSV with ?format=csv); the first part is:\n\n%s…\n",
			data.Len()/1024, runID, data.String()[:modelPayloadLimit])
		return b.String(), nil
	}
	fmt.Fprintf(&b, "\nExtracted data (exactly as the browser sent it):\n\n%s\n", data.String())
	return b.String(), nil
}
