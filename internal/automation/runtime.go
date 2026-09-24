package automation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The browser runtime's server half (MH-478), after fadymondy.com's FM-267.

The extension now drives through Chrome's debugger, gives every run its own
tab in a "Moharrik" window, and can bind a run to a KEPT site tab. This file
is what the server tells it about that, and what the server does for it:

	AT HELLO   how many runs may hold a tab at once (tabs_per_browser), which
	           kept tabs exist (persistent_tabs) and the automation window's
	           size — all configuration, never hard-coded in the extension.

	RUNS.SYNC  on connect and every minute, the list of this browser's runs
	           that are really in flight. The extension gives back any tab it
	           still holds for a run that is not on the list: a run.end lost to
	           a restart would otherwise keep a tab busy for ever.

	PUSHES     agent.update when a browser's name or allowed sites change on
	           the site, so the popup and the Sites page stay true without a
	           reconnect. (Every step still carries the list as it is at that
	           moment — MH-422 — so this is display, not enforcement.)

	SCREENSHOTS a screenshot step's PNG is written to storage and taken out of
	           the step's result, which is text kept verbatim for a person and
	           a model to read — not the place for a megabyte of base64.
*/

// Capabilities an extension may advertise at hello.
const (
	CapabilityMultiTab       = "multi_tab"
	CapabilityPersistentTabs = "persistent_tabs"
	CapabilityCDP            = "cdp"
)

// TabsPerBrowser is how many runs a multi-tab browser may hold tabs for.
func TabsPerBrowser() int { return envInt("AUTOMATION_TABS_PER_BROWSER", 3) }

// PersistentTab is one kept site tab a run can be bound to by key.
type PersistentTab struct {
	Key           string `json:"key"`
	Match         string `json:"match"`
	MaxAgeMinutes int    `json:"max_age_minutes"`
}

// PersistentTabs is AUTOMATION_PERSISTENT_TABS (a JSON array), or none. A
// workspace names the tab on a step ("persistent": "gmail"); the list only
// says how fresh each kept page has to be before a run reads it as it is.
func PersistentTabs() []PersistentTab {
	out := []PersistentTab{}
	if raw := strings.TrimSpace(os.Getenv("AUTOMATION_PERSISTENT_TABS")); raw != "" {
		var list []PersistentTab
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, p := range list {
				p.Key = strings.TrimSpace(p.Key)
				if p.Key == "" {
					continue
				}
				if p.MaxAgeMinutes <= 0 {
					p.MaxAgeMinutes = 30
				}
				out = append(out, p)
			}
		}
	}
	return out
}

// WindowSize is the automation window's size. Sites lay out for the window
// they get, and a workflow recorded on a desktop layout breaks on a tablet one.
func WindowSize() (int, int) {
	w, h := 1440, 900
	if raw := strings.TrimSpace(os.Getenv("AUTOMATION_WINDOW_SIZE")); raw != "" {
		var x, y int
		if _, err := fmt.Sscanf(strings.ToLower(raw), "%dx%d", &x, &y); err == nil && x >= 800 && y >= 600 {
			w, h = x, y
		}
	}
	return w, h
}

func envInt(name string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(os.Getenv(name)), "%d", &n); err == nil && n > 0 {
		return n
	}
	return fallback
}

// helloExtras is what a connecting browser is told about the runtime.
func helloExtras() map[string]any {
	w, h := WindowSize()
	return map[string]any{
		"tabs_per_browser":  TabsPerBrowser(),
		"persistent_tabs":   PersistentTabs(),
		"automation_window": map[string]int{"width": w, "height": h},
	}
}

// HasCapability reports whether a connected browser advertised a capability.
func (h *Hub) HasCapability(agentID, capability string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.conns[agentID]
	if c == nil {
		return false
	}
	for _, have := range c.agent.Capabilities {
		if have == capability {
			return true
		}
	}
	return false
}

// Push sends a frame that expects no answer. Best effort: a browser that is
// not connected learns the same thing at its next hello.
func (h *Hub) Push(workspaceID, agentID string, env Envelope) bool {
	h.mu.RLock()
	c := h.conns[agentID]
	h.mu.RUnlock()
	if c == nil || c.agent.WorkspaceID != workspaceID {
		return false
	}
	frame, err := json.Marshal(env)
	if err != nil {
		return false
	}
	return c.trySend(frame)
}

// PushAgentUpdate tells a connected browser its current name, allowed sites
// and workspace profile (MH-478): whatever the extension's popup and its
// Application autofill panel read from storage.
func PushAgentUpdate(ctx context.Context, a *app.App, hub *Hub, workspaceID, agentID string) {
	if hub == nil {
		return
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var name string
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT name FROM browser_agents WHERE workspace_id = $1 AND id = $2`, workspaceID, agentID).Scan(&name); err != nil {
		return
	}
	hosts, err := allowedHosts(sctx, a, workspaceID, agentID)
	if err != nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"name": name, "allowed_hosts": hosts, "profile": profileOrEmpty(sctx, a, workspaceID)})
	hub.Push(workspaceID, agentID, Envelope{Type: "agent.update", Payload: payload})
}

// PushProfileUpdate tells every browser of the workspace connected right now
// its new profile — the workspace profile is shared by all of them, so saving
// it once (Settings → Application profile) reaches every open popup without
// anyone reconnecting.
func PushProfileUpdate(ctx context.Context, a *app.App, hub *Hub, workspaceID string) {
	if hub == nil {
		return
	}
	for agentID := range hub.OnlineIn(workspaceID) {
		PushAgentUpdate(ctx, a, hub, workspaceID, agentID)
	}
}

// syncLoop tells a multi-tab browser which of its runs are really in flight,
// at connect and then every minute, for as long as the socket lives.
func (h *Hub) syncLoop(a *app.App, c *conn, done <-chan struct{}) {
	if !h.HasCapability(c.agent.AgentID, CapabilityMultiTab) {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		h.syncRuns(a, c.agent)
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

func (h *Hub) syncRuns(a *app.App, agent Connected) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sctx := tenant.WithWorkspace(ctx, agent.WorkspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id FROM automation_runs WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('queued', 'running')`,
		agent.WorkspaceID, agent.AgentID)
	if err != nil {
		return
	}
	active := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			active = append(active, id)
		}
	}
	rows.Close()
	payload, _ := json.Marshal(map[string]any{"active": active})
	if _, err := h.Call(ctx, agent.WorkspaceID, agent.AgentID, Envelope{Type: "runs.sync", Payload: payload}, 15*time.Second); err != nil {
		h.log.Debug("runs.sync", "agent", agent.AgentID, "err", err)
	}
}

// ── screenshots ──────────────────────────────────────────────────────────

// screenshotKey is where a step's PNG lives in storage, under the workspace
// and the run, so the retention sweep and a workspace's deletion find it.
func screenshotKey(workspaceID, runID string, position int) string {
	return path.Join("automation", workspaceID, runID, fmt.Sprintf("step-%03d.png", position))
}

// takeScreenshot pulls a PNG out of a step's reply and stores it. It returns
// the reply without the image, and where the image went ("" when the reply
// carried none). A reply that claims an image and cannot be stored is an
// error: a step that says it took a screenshot must have one.
func takeScreenshot(a *app.App, workspaceID, runID string, position int, reply []byte) ([]byte, string, error) {
	if len(reply) == 0 || !strings.Contains(string(reply), `"base64"`) {
		return reply, "", nil
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(reply, &body) != nil {
		return reply, "", nil
	}
	var b64 string
	if json.Unmarshal(body["base64"], &b64) != nil || b64 == "" {
		return reply, "", nil
	}
	png, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return reply, "", fmt.Errorf("the screenshot could not be read")
	}
	if a.Storage == nil {
		return reply, "", fmt.Errorf("screenshots cannot be kept: no storage is configured")
	}
	key := screenshotKey(workspaceID, runID, position)
	if err := a.Storage.Put(key, png); err != nil {
		return reply, "", fmt.Errorf("the screenshot could not be kept: %w", err)
	}
	delete(body, "base64")
	body["screenshot"] = json.RawMessage(`true`)
	body["png_bytes"] = json.RawMessage(fmt.Sprintf("%d", len(png)))
	out, err := json.Marshal(body)
	if err != nil {
		return reply, key, nil
	}
	return out, key, nil
}

// minCommandGap spaces a run's steps out, so no workflow can hammer a site
// faster than a person could click (fadymondy.com's guardrail of the same name).
const minCommandGap = 150 * time.Millisecond

// resolvePersistent decides the kept site tab a run is bound to, if any: a
// step's own "persistent" argument first, then the first navigation's URL
// matched against PersistentTabs(). "" gives the run a tab of its own.
func resolvePersistent(steps []Step) string {
	for _, s := range steps {
		if k, _ := s.Args["persistent"].(string); strings.TrimSpace(k) != "" {
			return strings.TrimSpace(k)
		}
	}
	list := PersistentTabs()
	if len(list) == 0 {
		return ""
	}
	for _, s := range steps {
		host, navigates := StepHost(s.Command, s.Args)
		if !navigates {
			continue
		}
		raw, _ := s.Args["url"].(string)
		p := "/"
		if i := strings.Index(raw, host); i >= 0 {
			rest := raw[i+len(host):]
			if j := strings.IndexAny(rest, "/"); j >= 0 {
				p = rest[j:]
			}
		}
		for _, pt := range list {
			mhost, mpath, _ := strings.Cut(strings.ToLower(strings.TrimSpace(pt.Match)), "/")
			if mhost == "" || host != mhost {
				continue
			}
			if mpath == "" || strings.HasPrefix(strings.TrimSuffix(p, "/")+"/", "/"+strings.TrimSuffix(mpath, "/")+"/") {
				return pt.Key
			}
		}
		return ""
	}
	return ""
}

// notifyRun tells the workspace's open tabs that a run moved, so a run page
// that is open follows it step by step.
func notifyRun(a *app.App, workspaceID string) {
	if a.Events != nil {
		a.Events.Publish(workspaceID, realtime.Event{Kind: "run", Permission: authz.AutomationRead})
	}
}

// MountRunArtifacts serves a step's screenshot (MH-478). A plain chi route in
// the guard's WORKSPACE tier — membership of X-Workspace-ID is checked before
// this runs — because it answers with bytes, not a JSON body. The page fetches
// it with the header and shows it as a blob; a plain <img src> cannot send one.
//
// The key comes from a row this workspace owns, read under RLS, and is still
// required to sit under this workspace's own prefix before storage is asked:
// "it came from our own table" is the assumption that stops being true the day
// something else writes to that table.
func MountRunArtifacts(router chi.Router, a *app.App) {
	// Everything the run extracted, as JSON or as a spreadsheet (MH-478).
	router.Get("/api/w/runs/{id}/data", func(w http.ResponseWriter, r *http.Request) {
		s, ok := tenant.From(r.Context())
		if !ok || !authz.Can(s.Role, authz.AutomationRead) {
			writePlain(w, http.StatusNotFound, "not found")
			return
		}
		data, err := ReadRunData(r.Context(), a, s.WorkspaceID, chi.URLParam(r, "id"))
		if errors.Is(err, sql.ErrNoRows) {
			writePlain(w, http.StatusNotFound, "no such run")
			return
		}
		if err != nil {
			writePlain(w, http.StatusInternalServerError, "could not read the run's data")
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		name := "run-" + chi.URLParam(r, "id")
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
			// A byte-order mark, so a spreadsheet opens Arabic text as UTF-8.
			_, _ = w.Write([]byte("ï»¿"))
			_, _ = w.Write(data.CSV())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("download") != "" {
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		}
		_ = json.NewEncoder(w).Encode(data)
	})

	router.Get("/api/w/runs/{id}/steps/{position}/screenshot", func(w http.ResponseWriter, r *http.Request) {
		s, ok := tenant.From(r.Context())
		if !ok || !authz.Can(s.Role, authz.AutomationRead) {
			writePlain(w, http.StatusNotFound, "not found")
			return
		}
		pos, err := strconv.Atoi(chi.URLParam(r, "position"))
		if err != nil || pos < 0 {
			writePlain(w, http.StatusNotFound, "no screenshot for that step")
			return
		}
		var key string
		err = a.SQLDB.QueryRowContext(r.Context(), `
			SELECT screenshot_path FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			s.WorkspaceID, chi.URLParam(r, "id"), pos).Scan(&key)
		if err != nil || key == "" || !strings.HasPrefix(key, "automation/"+s.WorkspaceID+"/") || strings.Contains(key, "..") {
			writePlain(w, http.StatusNotFound, "no screenshot for that step")
			return
		}
		if a.Storage == nil {
			writePlain(w, http.StatusNotFound, "no screenshot for that step")
			return
		}
		png, err := a.Storage.Get(key)
		if err != nil {
			writePlain(w, http.StatusNotFound, "that screenshot is gone (retention, probably)")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		// The content of someone's signed-in session: never in a shared cache.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(png)))
		_, _ = w.Write(png)
	})
}

// ── the data a run extracted (MH-478; fadymondy.com FM-281) ──────────────

// RunDataStep is one extracting step's payload, as the browser sent it.
type RunDataStep struct {
	Position int             `json:"position"`
	Command  string          `json:"command"`
	Payload  json.RawMessage `json:"payload"`
}

// RunData is every extraction of a run — per step, and merged: the rows of
// every per-row extract in step order, the fields of every field extract (a
// later step wins a clash), and every single value.
type RunData struct {
	RunID  string                     `json:"run_id"`
	Steps  []RunDataStep              `json:"steps"`
	Rows   []map[string]any           `json:"rows"`
	Fields map[string]json.RawMessage `json:"fields"`
	Values []json.RawMessage          `json:"values"`
}

// ReadRunData collects a run's extracted data. The run must be this
// workspace's; an unknown id is sql.ErrNoRows.
func ReadRunData(ctx context.Context, a *app.App, workspaceID, runID string) (*RunData, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var exists bool
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT true FROM automation_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT position, command, result FROM automation_run_steps
		WHERE workspace_id = $1 AND run_id = $2 AND status = 'ok' AND command IN ('extract', 'read', 'eval_readonly') AND result <> ''
		ORDER BY position`, workspaceID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &RunData{RunID: runID, Steps: []RunDataStep{}, Rows: []map[string]any{}, Fields: map[string]json.RawMessage{}, Values: []json.RawMessage{}}
	for rows.Next() {
		var st RunDataStep
		var result string
		if err := rows.Scan(&st.Position, &st.Command, &result); err != nil {
			return nil, err
		}
		if !json.Valid([]byte(result)) {
			continue
		}
		st.Payload = json.RawMessage(result)
		out.Steps = append(out.Steps, st)
		var body struct {
			Rows   []map[string]any           `json:"rows"`
			Fields map[string]json.RawMessage `json:"fields"`
			Values []json.RawMessage          `json:"values"`
			Value  json.RawMessage            `json:"value"`
		}
		if json.Unmarshal(st.Payload, &body) != nil {
			continue
		}
		out.Rows = append(out.Rows, body.Rows...)
		for k, v := range body.Fields {
			out.Fields[k] = v
		}
		out.Values = append(out.Values, body.Values...)
		if len(body.Value) > 0 && len(body.Rows) == 0 && len(body.Fields) == 0 {
			out.Values = append(out.Values, body.Value)
		}
	}
	return out, rows.Err()
}

// CSV renders the data as a spreadsheet: the rows when there are any (one
// column per field, in the order fields first appear), else the fields as
// name,value, else the values one per line.
func (d *RunData) CSV() []byte {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	cell := func(v any) string {
		switch x := v.(type) {
		case nil:
			return ""
		case string:
			return x
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	}
	raw := func(r json.RawMessage) string {
		var v any
		if json.Unmarshal(r, &v) != nil {
			return string(r)
		}
		return cell(v)
	}
	switch {
	case len(d.Rows) > 0:
		var cols []string
		seen := map[string]bool{}
		for _, row := range d.Rows {
			keys := make([]string, 0, len(row))
			for k := range row {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, k)
				}
			}
		}
		_ = w.Write(cols)
		for _, row := range d.Rows {
			line := make([]string, len(cols))
			for i, c := range cols {
				line[i] = cell(row[c])
			}
			_ = w.Write(line)
		}
	case len(d.Fields) > 0:
		_ = w.Write([]string{"field", "value"})
		keys := make([]string, 0, len(d.Fields))
		for k := range d.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_ = w.Write([]string{k, raw(d.Fields[k])})
		}
	default:
		_ = w.Write([]string{"value"})
		for _, v := range d.Values {
			_ = w.Write([]string{raw(v)})
		}
	}
	w.Flush()
	return []byte(buf.String())
}
