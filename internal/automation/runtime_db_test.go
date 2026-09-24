package automation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	// The storage plugin, as in the server: a screenshot needs somewhere to go.
	_ "github.com/fadymondy/moharrik/internal/plugins"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The runtime's server half (MH-478), against a real database and a real socket:

  - hello.ok tells a browser how the runtime is to behave: tabs per browser,
    kept tabs, the automation window's size
  - a browser that says it gives every run its own tab ("multi_tab") is told,
    at connect, which of its runs are really in flight (runs.sync)
  - a name or allowed-sites change is pushed to a connected browser
  - a screenshot step's PNG goes to storage, out of the step's text, and the
    retention sweep deletes it with the run
  - a kept-tab key travels with every step of a run
*/

type runtimeBrowser struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	frames []automation.Envelope
	answer func(env automation.Envelope) *automation.Envelope
}

func (rb *runtimeBrowser) seen(kind string) []automation.Envelope {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	var out []automation.Envelope
	for _, f := range rb.frames {
		if f.Type == kind {
			out = append(out, f)
		}
	}
	return out
}

func connectRuntime(t *testing.T, a *app.App, allowed string, capabilities []string, answer func(env automation.Envelope) *automation.Envelope) (*automation.Hub, string, string, *runtimeBrowser, map[string]any) {
	t.Helper()
	ctx := context.Background()
	token, _ := automation.NewToken()
	var wsID, agentID string
	suffix := time.Now().UnixNano()
	if err := a.SysDB.QueryRowContext(ctx, `INSERT INTO workspaces (name, slug, created_by) VALUES ('Runtime', $1, 'rt') RETURNING id`,
		"runtime-"+itoa(suffix)).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
		VALUES ($1, 'runtime browser', $2, '', $3::text[]) RETURNING id`, wsID, automation.HashToken(token), allowed).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	hub := automation.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(hub.Handler(a))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	hello, _ := json.Marshal(map[string]any{"token": token, "extension_version": "0.4.0", "capabilities": capabilities, "tab_capacity": 3})
	if err := ws.WriteJSON(automation.Envelope{ID: "hello", Type: "hello", Payload: hello}); err != nil {
		t.Fatal(err)
	}
	var ack automation.Envelope
	if err := ws.ReadJSON(&ack); err != nil || ack.Type != "hello.ok" {
		t.Fatalf("no hello.ok: %v %+v", err, ack)
	}
	var ackBody map[string]any
	_ = json.Unmarshal(ack.Payload, &ackBody)

	rb := &runtimeBrowser{ws: ws, answer: answer}
	go func() {
		for {
			var env automation.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			rb.mu.Lock()
			rb.frames = append(rb.frames, env)
			rb.mu.Unlock()
			if env.ID == "" {
				continue
			}
			reply := &automation.Envelope{Payload: json.RawMessage(`{"ok":true}`)}
			if rb.answer != nil {
				if r := rb.answer(env); r != nil {
					reply = r
				}
			}
			reply.ID, reply.Type = env.ID, env.Type+".result"
			_ = ws.WriteJSON(reply)
		}
	}()
	for i := 0; i < 50 && !hub.Online(agentID); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return hub, wsID, agentID, rb, ackBody
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRuntimeHelloTellsTheBrowserHowToRun(t *testing.T) {
	a := testApp(t)
	t.Setenv("AUTOMATION_TABS_PER_BROWSER", "2")
	t.Setenv("AUTOMATION_WINDOW_SIZE", "1600x1000")
	t.Setenv("AUTOMATION_PERSISTENT_TABS", `[{"key":"gmail","match":"mail.google.com"}]`)
	_, _, _, _, ack := connectRuntime(t, a, "{example.com}", nil, nil)
	if ack["tabs_per_browser"] != float64(2) {
		t.Fatalf("tabs_per_browser: %v", ack)
	}
	if w := ack["automation_window"].(map[string]any); w["width"] != float64(1600) || w["height"] != float64(1000) {
		t.Fatalf("automation_window: %v", w)
	}
	kept := ack["persistent_tabs"].([]any)
	if len(kept) != 1 || kept[0].(map[string]any)["key"] != "gmail" || kept[0].(map[string]any)["max_age_minutes"] != float64(30) {
		t.Fatalf("persistent_tabs: %v", kept)
	}
	if ack["workspace_name"] != "Runtime" {
		t.Fatalf("workspace_name: %v", ack)
	}
}

func TestRuntimeRunsSyncOnlyForAMultiTabBrowser(t *testing.T) {
	a := testApp(t)
	_, _, _, old, _ := connectRuntime(t, a, "{example.com}", nil, nil)
	_, wsID, agentID, multi, _ := connectRuntime(t, a, "{example.com}", []string{"multi_tab", "cdp"}, nil)
	eventually(t, "runs.sync", func() bool { return len(multi.seen("runs.sync")) > 0 })
	var body struct {
		Active []string `json:"active"`
	}
	_ = json.Unmarshal(multi.seen("runs.sync")[0].Payload, &body)
	if body.Active == nil || len(body.Active) != 0 {
		t.Fatalf("a browser with no runs in flight is told an empty list, not nothing: %s", multi.seen("runs.sync")[0].Payload)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(old.seen("runs.sync")); n != 0 {
		t.Fatalf("an old extension was sent %d runs.sync frames it does not understand", n)
	}
	// A run in flight is on the list: a browser that reconnects mid-run is told
	// to keep that run's tab.
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	var runID string
	if err := a.SQLDB.QueryRowContext(sctx, `INSERT INTO automation_runs (workspace_id, agent_id, status) VALUES ($1, $2, 'running') RETURNING id`, wsID, agentID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	_, _, _, again, _ := reconnectRuntime(t, a, wsID, agentID)
	eventually(t, "runs.sync after reconnect", func() bool { return len(again.seen("runs.sync")) > 0 })
	if p := string(again.seen("runs.sync")[0].Payload); !strings.Contains(p, runID) {
		t.Fatalf("the run in flight is not on the list: %s", p)
	}
}

// reconnectRuntime opens a second socket for an existing browser with a fresh token.
func reconnectRuntime(t *testing.T, a *app.App, wsID, agentID string) (*automation.Hub, string, string, *runtimeBrowser, map[string]any) {
	t.Helper()
	token, _ := automation.NewToken()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE browser_agents SET token_hash = $3 WHERE workspace_id = $1 AND id = $2`, wsID, agentID, automation.HashToken(token)); err != nil {
		t.Fatal(err)
	}
	hub := automation.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(hub.Handler(a))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	hello, _ := json.Marshal(map[string]any{"token": token, "capabilities": []string{"multi_tab"}})
	_ = ws.WriteJSON(automation.Envelope{ID: "hello", Type: "hello", Payload: hello})
	var ack automation.Envelope
	if err := ws.ReadJSON(&ack); err != nil || ack.Type != "hello.ok" {
		t.Fatalf("no hello.ok: %v %+v", err, ack)
	}
	rb := &runtimeBrowser{ws: ws}
	go func() {
		for {
			var env automation.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			rb.mu.Lock()
			rb.frames = append(rb.frames, env)
			rb.mu.Unlock()
			if env.ID != "" {
				_ = ws.WriteJSON(automation.Envelope{ID: env.ID, Type: env.Type + ".result", Payload: json.RawMessage(`{"ok":true}`)})
			}
		}
	}()
	return hub, wsID, agentID, rb, nil
}

func TestRuntimeAgentUpdateIsPushed(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, rb, _ := connectRuntime(t, a, "{example.com}", []string{"multi_tab"}, nil)
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE browser_agents SET allowed_hosts = '{example.com,mail.google.com}', name = 'Renamed' WHERE workspace_id = $1 AND id = $2`, wsID, agentID); err != nil {
		t.Fatal(err)
	}
	automation.PushAgentUpdate(context.Background(), a, hub, wsID, agentID)
	eventually(t, "agent.update", func() bool { return len(rb.seen("agent.update")) > 0 })
	var body struct {
		Name  string   `json:"name"`
		Hosts []string `json:"allowed_hosts"`
	}
	_ = json.Unmarshal(rb.seen("agent.update")[0].Payload, &body)
	if body.Name != "Renamed" || strings.Join(body.Hosts, ",") != "example.com,mail.google.com" {
		t.Fatalf("agent.update: %+v", body)
	}
	// Another workspace's id never reaches this browser.
	if hub.Push("some-other-workspace", agentID, automation.Envelope{Type: "agent.update"}) {
		t.Fatal("a push named with another workspace reached the browser")
	}
}

func TestRuntimeScreenshotGoesToStorageAndRetentionTakesItAway(t *testing.T) {
	a := testApp(t)
	png := []byte("\x89PNG fake image bytes")
	hub, wsID, agentID, rb, _ := connectRuntime(t, a, "{example.com}", []string{"multi_tab", "cdp"}, func(env automation.Envelope) *automation.Envelope {
		if env.Type != "run.step" {
			return nil
		}
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(env.Payload, &p)
		if p.Command == "screenshot" {
			body, _ := json.Marshal(map[string]any{"ok": true, "command": "screenshot", "format": "png", "base64": base64.StdEncoding.EncodeToString(png), "summary": "1 kB screenshot"})
			return &automation.Envelope{Payload: body}
		}
		return &automation.Envelope{Payload: json.RawMessage(`{"ok":true,"summary":"fine"}`)}
	})
	runID, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://example.com/"}},
		{Command: "screenshot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := waitForRun(t, a, wsID, runID); r.status != "ok" {
		t.Fatalf("run: %+v", r)
	}
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	var result, key string
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT result, screenshot_path FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 AND position = 1`, wsID, runID).Scan(&result, &key); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "base64") || !strings.Contains(result, `"screenshot":true`) {
		t.Fatalf("the image stayed in the step's text: %s", result)
	}
	if !strings.HasPrefix(key, "automation/"+wsID+"/"+runID+"/") {
		t.Fatalf("screenshot key %q", key)
	}
	stored, err := a.Storage.Get(key)
	if err != nil || string(stored) != string(png) {
		t.Fatalf("stored screenshot: %v %q", err, stored)
	}
	// run.end was pushed, so the browser gives the tab back.
	eventually(t, "run.end", func() bool { return len(rb.seen("run.end")) > 0 })

	// Past the retention window, the run and its picture go together.
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE automation_runs SET finished_at = now() - interval '40 days' WHERE workspace_id = $1 AND id = $2`, wsID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.SweepRuns(context.Background(), a, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Storage.Get(key); err == nil {
		t.Fatal("the screenshot outlived its run")
	}
}

func TestRuntimeKeptTabTravelsWithEveryStep(t *testing.T) {
	a := testApp(t)
	t.Setenv("AUTOMATION_PERSISTENT_TABS", `[{"key":"gmail","match":"mail.google.com"}]`)
	hub, wsID, agentID, rb, _ := connectRuntime(t, a, "{google.com,example.com}", []string{"multi_tab", "persistent_tabs"}, nil)
	runID, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://mail.google.com/mail/u/0/"}},
		{Command: "extract", Args: map[string]any{"selector": "title"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, a, wsID, runID)
	steps := rb.seen("run.step")
	if len(steps) != 2 {
		t.Fatalf("%d steps sent", len(steps))
	}
	for _, s := range steps {
		var p map[string]any
		_ = json.Unmarshal(s.Payload, &p)
		if p["tab"] != "gmail" {
			t.Fatalf("a step went without its kept tab: %s", s.Payload)
		}
	}
	// A step's own "persistent" argument names it too, and a run with neither
	// gets a tab of its own.
	runID, _ = hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://example.com/"}},
	})
	waitForRun(t, a, wsID, runID)
	last := rb.seen("run.step")
	var p map[string]any
	_ = json.Unmarshal(last[len(last)-1].Payload, &p)
	if _, has := p["tab"]; has {
		t.Fatalf("a run with no kept tab was bound to one: %s", last[len(last)-1].Payload)
	}
}

// MH-483: a browser says whether it holds every site or waits for an OK per
// site, and the workspace (only that workspace) can read it while it is
// connected. A mode the server does not know is ignored.
func TestRuntimeBrowserReportsItsSiteAccess(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, rb, _ := connectRuntime(t, a, "{example.com,news.google.com}", []string{"multi_tab"}, nil)
	send := func(payload string) {
		rb.mu.Lock()
		defer rb.mu.Unlock()
		if err := rb.ws.WriteJSON(automation.Envelope{Type: "agent.state", Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := hub.AccessIn(wsID)[agentID]; ok {
		t.Fatal("known before the browser said anything")
	}
	send(`{"site_access":"sites","pending_hosts":["News.Google.com","https://example.com/x"]}`)
	eventually(t, "site access", func() bool { return hub.AccessIn(wsID)[agentID].Mode == "sites" })
	if got := hub.AccessIn(wsID)[agentID].Pending; len(got) != 2 || got[0] != "news.google.com" || got[1] != "example.com" {
		t.Fatalf("pending: %v", got)
	}
	if len(hub.AccessIn("another-workspace")) != 0 {
		t.Fatal("another workspace sees this browser's access")
	}
	send(`{"site_access":"everything","pending_hosts":[]}`)
	send(`{"site_access":"all","pending_hosts":["example.com"]}`)
	eventually(t, "all sites", func() bool { return hub.AccessIn(wsID)[agentID].Mode == "all" })
	if got := hub.AccessIn(wsID)[agentID].Pending; len(got) != 0 {
		t.Fatalf("every site granted, yet waiting: %v", got)
	}
}
