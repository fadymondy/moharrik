package server

/*
TestBrowserRunTools — MH-478 phase 4, through the real stack: fadymondy.com's
browser_agents, browser_extract, browser_run, browser_result and
browser_cancel, answered by a stand-in browser on a real socket.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run BrowserRunTools -v
*/

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestBrowserRunTools(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "browser-tools-secret-browser-tools-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "tools-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Tools " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	// A stand-in browser, connected over the real socket: extract answers
	// with a row, everything else with ok.
	connect := func(name, hosts string) string {
		token, _ := automation.NewToken()
		var id string
		if err := a.SysDB.QueryRow(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
			VALUES ($1, $2, $3, '', $4::text[]) RETURNING id`, wsID, name, automation.HashToken(token), hosts).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/agents/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close() })
		hello, _ := json.Marshal(map[string]any{"token": token, "extension_version": "0.5.0"})
		if err := ws.WriteJSON(automation.Envelope{ID: "hello", Type: "hello", Payload: hello}); err != nil {
			t.Fatal(err)
		}
		var ack automation.Envelope
		if err := ws.ReadJSON(&ack); err != nil || ack.Type != "hello.ok" {
			t.Fatalf("no hello.ok: %v %+v", err, ack)
		}
		go func() {
			for {
				var env automation.Envelope
				if ws.ReadJSON(&env) != nil {
					return
				}
				if env.Type != "run.step" || env.ID == "" {
					continue
				}
				var p struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(env.Payload, &p)
				payload := `{"ok":true}`
				if p.Command == "extract" {
					payload = `{"rows":[{"name":"Beans, 1 kg","price":"420"}]}`
				}
				_ = ws.WriteJSON(automation.Envelope{ID: env.ID, Type: "run.step.result", Payload: json.RawMessage(payload)})
			}
		}()
		for i := 0; i < 100 && !automation.Default().Online(id); i++ {
			time.Sleep(20 * time.Millisecond)
		}
		return id
	}
	shopID := connect("Shop Chrome", "{shop.test}")

	tk := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "tools", "scopes": []string{"automation:read", "automation:run"}}, 201)
	agent := newClient(t, srv.URL)
	agent.bearer = tk["plaintext"].(string)
	call := func(tool string, args map[string]any) string {
		t.Helper()
		code, text, _ := mcpCall(agent, "/api/mcp", tool, args)
		if code != 200 {
			t.Fatalf("%s: %d", tool, code)
		}
		return text
	}

	decode := func(t *testing.T, text string) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("not JSON: %s: %v", text, err)
		}
		return v
	}

	agents := decode(t, call("browser_agents", map[string]any{}))
	items, _ := agents["items"].([]any)
	var found bool
	for _, it := range items {
		m := it.(map[string]any)
		if m["id"] == shopID {
			found = true
			if m["status"] != "online" {
				t.Fatalf("browser_agents: %v", m)
			}
			hosts, _ := m["allowed_hosts"].([]any)
			if len(hosts) != 1 || hosts[0] != "shop.test" {
				t.Fatalf("browser_agents allowed_hosts: %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("browser_agents: shop browser not found in %v", agents)
	}

	// Open and read, with the data quoted back in the same call.
	text := decode(t, call("browser_extract", map[string]any{"url": "https://shop.test/beans", "fields": map[string]string{"name": "h1"}, "list": "li"}))
	if text["status"] != "ok" || text["ok_count"] != float64(2) || text["step_count"] != float64(2) {
		t.Fatalf("browser_extract: %v", text)
	}
	if data, _ := text["data"].(string); !strings.Contains(data, "Beans, 1 kg") {
		t.Fatalf("browser_extract data: %v", text)
	}
	// A site the browser does not allow: named, with the way to allow it, and nothing runs.
	var before int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1`, wsID).Scan(&before)
	notAllowed := decode(t, call("browser_extract", map[string]any{"url": "https://evil.test/", "fields": map[string]string{"x": "h1"}}))
	var after int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1`, wsID).Scan(&after)
	reason, _ := notAllowed["reason"].(string)
	msg, _ := notAllowed["message"].(string)
	if notAllowed["ok"] != false || !strings.Contains(reason, "evil.test") || !strings.Contains(msg, "browser_set_allowed_sites") || after != before {
		t.Fatalf("browser_extract off the list: %v (%d → %d runs)", notAllowed, before, after)
	}
	if fileURL := decode(t, call("browser_extract", map[string]any{"url": "file:///etc/passwd", "fields": map[string]string{"x": "h1"}})); !strings.Contains(fmt.Sprint(fileURL["reason"]), "http(s)") {
		t.Fatalf("a file URL: %v", fileURL)
	}

	// An explicit sequence, and its result read back by key.
	run := decode(t, call("browser_run", map[string]any{"steps": []map[string]any{
		{"command": "navigate", "args": map[string]any{"url": "https://shop.test/"}},
		{"command": "click", "args": map[string]any{"selector": "#more"}},
		{"command": "extract", "args": map[string]any{"selector": "h1"}},
	}}))
	if run["status"] != "ok" || run["ok_count"] != float64(3) || run["step_count"] != float64(3) {
		t.Fatalf("browser_run: %v", run)
	}
	runKey, _ := run["run_id"].(string)
	if runKey == "" {
		t.Fatalf("browser_run: no run_id in %v", run)
	}
	result := decode(t, call("browser_result", map[string]any{"run": runKey}))
	if result["run_id"] != runKey || result["status"] != "ok" {
		t.Fatalf("browser_result: %v", result)
	}
	steps, _ := result["steps"].([]any)
	if len(steps) != 3 || steps[2].(map[string]any)["command"] != "extract" || steps[2].(map[string]any)["status"] != "ok" {
		t.Fatalf("browser_result steps: %v", result)
	}
	unknown := decode(t, call("browser_result", map[string]any{"run": "00000000-0000-0000-0000-000000000000"}))
	if unknown["ok"] != false || !strings.Contains(fmt.Sprint(unknown["reason"]), "No run with that key") {
		t.Fatalf("browser_result, unknown key: %v", unknown)
	}

	// Cancelling: a run still going stops; a finished one says so.
	var liveRun string
	if err := a.SysDB.QueryRow(`INSERT INTO automation_runs (workspace_id, agent_id, status, trigger) VALUES ($1, $2, 'running', 'mcp') RETURNING id`, wsID, shopID).Scan(&liveRun); err != nil {
		t.Fatal(err)
	}
	cancelled := decode(t, call("browser_cancel", map[string]any{"run": liveRun}))
	if cancelled["ok"] != true || cancelled["cancelled"] != true {
		t.Fatalf("browser_cancel: %v", cancelled)
	}
	var st string
	_ = a.SysDB.QueryRow(`SELECT status FROM automation_runs WHERE id = $1`, liveRun).Scan(&st)
	if st != "cancelled" {
		t.Fatalf("the run is %s", st)
	}
	alreadyFinished := decode(t, call("browser_cancel", map[string]any{"run": runKey}))
	if alreadyFinished["ok"] != false || !strings.Contains(fmt.Sprint(alreadyFinished["reason"]), "already finished") {
		t.Fatalf("browser_cancel, finished: %v", alreadyFinished)
	}

	// No name and two connected browsers: the model is asked, not guessed for.
	connect("Laptop Chrome", "{shop.test}")
	twoBrowsers := decode(t, call("browser_extract", map[string]any{"url": "https://shop.test/", "fields": map[string]string{"x": "h1"}}))
	if twoBrowsers["ok"] != false || !strings.Contains(fmt.Sprint(twoBrowsers["reason"]), "More than one browser is connected") {
		t.Fatalf("two browsers, no name: %v", twoBrowsers)
	}
	named := decode(t, call("browser_extract", map[string]any{"agent": "laptop chrome", "url": "https://shop.test/", "fields": map[string]string{"x": "h1"}}))
	if named["status"] != "ok" || named["ok_count"] != float64(2) || named["step_count"] != float64(2) {
		t.Fatalf("named: %v", named)
	}
}
