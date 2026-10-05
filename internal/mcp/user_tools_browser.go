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
fadymondy.com's browser tools, in a workspace (MH-478, phase 3a): the same
names, arguments and answers as its sites_tools.go, plugin_tools.go and
plugin_save_tool.go, so an agent written against one works against the other.

  browser_allowed_sites      a browser's allowed sites                automation:read
  browser_set_allowed_sites  replace / add / remove, pushed live      automation:manage (admin)
  browser_plugins            the workspace's workflows and presets    automation:read
  browser_plugin_run         run one by slug, with its inputs         automation:run
  browser_plugin_save        create or update one by slug             automation:run

Everything is inside the token's workspace: a browser or workflow of another
workspace is "not found", never "forbidden". Only browser_set_allowed_sites
can widen where a browser may go, and it is an admin's scope. Saving a
workflow records the sites it names but allows none of them: a run of it is
checked against the browser's list like every other run.
*/
func BrowserTools() []Tool {
	agentProp := map[string]any{"type": "string", "description": "browser name or id; omitted = the only paired one"}
	hostList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return []Tool{
		{
			Name: "browser_allowed_sites",
			Description: "Show the sites a paired browser may drive (its allowlist). Subdomains are included: " +
				"google.com covers news.google.com. An empty list means the browser drives nothing.",
			Scope:       "automation:read",
			InputSchema: schema(nil, map[string]any{"agent": agentProp}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Agent string `json:"agent"`
				}
				_ = json.Unmarshal(args, &in)
				b, why, err := findBrowser(ctx, a, s.WorkspaceID, in.Agent)
				if err != nil {
					return "", err
				}
				if b == nil {
					return noMatch(why)
				}
				return jsonResult(map[string]any{"id": b.id, "name": b.name, "hosts": b.hosts, "count": len(b.hosts)})
			},
		},
		{
			Name: "browser_set_allowed_sites",
			Description: "Change the sites a paired browser may drive. Use add and/or remove to edit the list, or " +
				"replace to set it exactly. Hosts only (example.com), not URLs or paths. Pushed to the browser at " +
				"once and recorded in the audit log.",
			Scope: "automation:manage",
			InputSchema: schema(nil, map[string]any{
				"agent":   agentProp,
				"add":     hostList,
				"remove":  hostList,
				"replace": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "the whole new list; overrides add/remove"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Agent   string    `json:"agent"`
					Add     []string  `json:"add"`
					Remove  []string  `json:"remove"`
					Replace *[]string `json:"replace"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				b, why, err := findBrowser(ctx, a, s.WorkspaceID, in.Agent)
				if err != nil {
					return "", err
				}
				if b == nil {
					return noMatch(why)
				}
				var rejected []string
				for _, list := range [][]string{in.Add, in.Remove} {
					rejected = append(rejected, notHosts(list)...)
				}
				ch := automation.HostChange{Add: in.Add, Remove: in.Remove}
				if in.Replace != nil {
					ch = automation.HostChange{Replace: append([]string{}, *in.Replace...)}
					rejected = notHosts(*in.Replace)
				}
				next, added, err := automation.ChangeAllowedHosts(ctx, a, automation.Default(), s.WorkspaceID, b.id, ch)
				if err != nil {
					return "", err
				}
				removed := minus(b.hosts, next)
				return jsonResult(map[string]any{
					"ok": true, "id": b.id, "name": b.name, "changed": len(added) > 0 || len(removed) > 0,
					"hosts": next, "count": len(next), "added": added, "removed": removed, "rejected": rejected,
				})
			},
		},
		{
			Name: "browser_plugins",
			Description: "List the workspace's saved browser workflows (plugins), including ones added from presets " +
				"and recorded in the extension: slug, what each does, the sites it touches, the inputs it asks for, " +
				"and whether it publishes. Presets not added yet are listed after them. Run one with browser_plugin_run.",
			Scope:       "automation:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT slug, name, description, hosts, params::text, steps::text, version, enabled, writes, preset
					FROM automation_workflows WHERE workspace_id = $1 ORDER BY updated_at DESC LIMIT 200`, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				defer rows.Close()
				items := []map[string]any{}
				added := map[string]bool{}
				for rows.Next() {
					var slug, name, desc, params, steps, preset string
					var hosts pgtext.Array
					var version int
					var enabled, writes bool
					if err := rows.Scan(&slug, &name, &desc, &hosts, &params, &steps, &version, &enabled, &writes, &preset); err != nil {
						return "", err
					}
					added[preset] = true
					var ps []automation.Param
					_ = json.Unmarshal([]byte(params), &ps)
					var st []automation.Step
					_ = json.Unmarshal([]byte(steps), &st)
					items = append(items, map[string]any{
						"slug": slug, "name": name, "description": desc, "hosts": []string(hosts), "params": ps,
						"step_count": len(st), "version": version, "enabled": enabled, "writes": writes,
					})
				}
				if err := rows.Err(); err != nil {
					return "", err
				}
				_, presets := automation.Presets()
				notAdded := []map[string]any{}
				for i := range presets {
					if !added[presets[i].Slug] {
						notAdded = append(notAdded, map[string]any{
							"slug": presets[i].Slug, "summary": automation.PresetSummary(&presets[i]),
						})
					}
				}
				return jsonResult(map[string]any{"count": len(items), "items": items, "not_added": notAdded})
			},
		},
		{
			Name: "browser_plugin_run",
			Description: "Run a saved workflow by slug (or id) on a paired browser, with values for its inputs. " +
				"A workflow that publishes, sends or submits needs confirm: true — run its -dryrun twin first where " +
				"there is one. Waits about 25 seconds and answers with the run's steps and extracted data; a run still going " +
				"by then answers with its id, to poll with browser_result.",
			Confirm: "A workflow that publishes, sends or submits only runs when the call carries confirm: true; without it nothing runs.",
			Scope: "automation:run",
			InputSchema: schema([]string{"plugin"}, map[string]any{
				"plugin":  map[string]any{"type": "string", "description": "slug or id"},
				"agent":   agentProp,
				"inputs":  map[string]any{"type": "object", "description": "input name -> value"},
				"confirm": map[string]any{"type": "boolean", "description": "required for a workflow that publishes"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Plugin  string         `json:"plugin"`
					Agent   string         `json:"agent"`
					Inputs  map[string]any `json:"inputs"`
					Confirm bool           `json:"confirm"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				w, err := automation.FindWorkflow(ctx, a, s.WorkspaceID, in.Plugin)
				if err != nil {
					return noMatch("No workflow with that slug. List them with browser_plugins.")
				}
				if !w.Enabled {
					return jsonResult(map[string]any{"ok": false, "reason": fmt.Sprintf("%s is turned off.", w.Name)})
				}
				if w.Writes && !in.Confirm {
					return jsonResult(map[string]any{
						"ok": false, "needs_confirm": true, "workflow": w.Name, "hosts": w.Hosts,
					})
				}
				b, why, err := resolveOnline(ctx, a, s.WorkspaceID, in.Agent)
				if err != nil {
					return "", err
				}
				if b == nil {
					return noMatch(why)
				}
				values := map[string]string{}
				for k, v := range in.Inputs {
					values[k] = fmt.Sprint(v)
				}
				hub := automation.Default()
				if hub == nil {
					return "", errors.New("the browser registry is not running")
				}
				runID, err := automation.RunWorkflow(ctx, a, hub, s.WorkspaceID, s.UserID, w.ID, b.id, "mcp", values)
				if err != nil {
					// Answers a model can act on, not failures of the tool:
					// a value missing, a site only an admin can allow.
					var missing *automation.MissingParam
					if errors.As(err, &missing) || errors.Is(err, automation.ErrSitesNeedAdmin) {
						return jsonResult(map[string]any{"ok": false, "reason": err.Error()})
					}
					if errors.Is(err, automation.ErrHostNotAllowed) {
						return jsonResult(map[string]any{
							"ok": false, "reason": err.Error(),
							"message": "An admin can allow the site with browser_set_allowed_sites, or on the Automation page.",
						})
					}
					return "", err
				}
				// As fadymondy.com's does: wait a short window, then the data or the key.
				return waitAndDescribe(ctx, a, s.WorkspaceID, b.name, runID)
			},
		},
		{
			Name: "browser_plugin_save",
			Description: "Create or update a saved browser workflow (plugin) by slug. Steps use the same commands as " +
				"start_browser_run; {{name}} placeholders are filled from inputs when it runs. Changing it keeps the " +
				"previous definition as a version. hosts records the sites it goes to; it does NOT allow them on any " +
				"browser (browser_set_allowed_sites does, for an admin).",
			Scope: "automation:run",
			InputSchema: schema([]string{"slug", "name", "steps"}, map[string]any{
				"slug":        map[string]any{"type": "string", "description": "lowercase-with-dashes; the identity"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string", "description": "what it does, its inputs, and the date its selectors were verified"},
				"icon":        map[string]any{"type": "string", "description": "lucide icon name"},
				"hosts":       hostList,
				"inputs": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"},
						"required": map[string]any{"type": "boolean"}, "default": map[string]any{"type": "string"},
						"help": map[string]any{"type": "string"}, "secret": map[string]any{"type": "boolean"},
					},
				}},
				"steps": map[string]any{"type": "array", "items": map[string]any{
					"type":     "object",
					"required": []string{"command"},
					"properties": map[string]any{
						"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "object"},
						"continue_on_error": map[string]any{"type": "boolean"},
						"timeout_seconds":   map[string]any{"type": "integer"},
					},
				}},
				"writes":  map[string]any{"type": "boolean", "description": "it publishes, sends or submits; runs then need confirm"},
				"enabled": map[string]any{"type": "boolean"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Slug        string             `json:"slug"`
					Name        string             `json:"name"`
					Description string             `json:"description"`
					Icon        string             `json:"icon"`
					Hosts       []string           `json:"hosts"`
					Inputs      []automation.Param `json:"inputs"`
					Steps       []automation.Step  `json:"steps"`
					Writes      *bool              `json:"writes"`
					Enabled     *bool              `json:"enabled"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", err
				}
				res, err := automation.SavePlugin(ctx, a, s.WorkspaceID, s.UserID, automation.PluginSave{
					Slug: in.Slug, Name: in.Name, Description: in.Description, Icon: in.Icon, Hosts: in.Hosts,
					Params: in.Inputs, Steps: in.Steps, Writes: in.Writes, Enabled: in.Enabled,
				})
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
	}
}

type browserRef struct {
	id, name string
	hosts    []string
}

// findBrowser resolves a browser of the workspace by id or name, never a
// revoked one. An empty ref is the only paired browser. The second value is
// the sentence to answer with when there is no single match.
func findBrowser(ctx context.Context, a *app.App, workspaceID, ref string) (*browserRef, string, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id, name, allowed_hosts FROM browser_agents
		WHERE workspace_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 100`, workspaceID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var live []browserRef
	for rows.Next() {
		var b browserRef
		var hosts pgtext.Array
		if err := rows.Scan(&b.id, &b.name, &hosts); err != nil {
			return nil, "", err
		}
		b.hosts = []string(hosts)
		if b.hosts == nil {
			b.hosts = []string{}
		}
		live = append(live, b)
	}
	if len(live) == 0 {
		return nil, "No browser is paired with this workspace.", nil
	}
	list := func(bs []browserRef) string {
		parts := make([]string, 0, len(bs))
		for _, b := range bs {
			parts = append(parts, fmt.Sprintf("%s (%s)", b.name, b.id))
		}
		return strings.Join(parts, ", ")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if len(live) == 1 {
			return &live[0], "", nil
		}
		return nil, "Several browsers are paired; name one: " + list(live), nil
	}
	var matches []browserRef
	for i := range live {
		if live[i].id == ref {
			return &live[i], "", nil
		}
		if strings.EqualFold(live[i].name, ref) {
			matches = append(matches, live[i])
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], "", nil
	case 0:
		return nil, fmt.Sprintf("No paired browser called %q. Paired: %s", ref, list(live)), nil
	default:
		return nil, fmt.Sprintf("Several browsers are called %q; use an id: %s", ref, list(matches)), nil
	}
}

func notHosts(values []string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" && automation.NormaliseHost(v) == "" {
			out = append(out, v)
		}
	}
	return out
}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, h := range b {
		in[h] = true
	}
	var out []string
	for _, h := range a {
		if !in[h] {
			out = append(out, h)
		}
	}
	return out
}

var _ = sql.ErrNoRows
