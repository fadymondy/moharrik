package server

/*
TestBrowserRunTools — MH-478 phase 4, through the real stack: fadymondy.com's
browser_agents, browser_extract, browser_run, browser_result and
browser_cancel, answered by a stand-in browser on a real socket.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run BrowserRunTools -v
*/

import (
	"encoding/json"
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

	if text := call("browser_agents", map[string]any{}); !strings.Contains(text, "Shop Chrome ("+shopID+") — online") || !strings.Contains(text, "may be driven on: shop.test") {
		t.Fatalf("browser_agents: %s", text)
	}

	// Open and read, with the data quoted back in the same call.
	text := call("browser_extract", map[string]any{"url": "https://shop.test/beans", "fields": map[string]string{"name": "h1"}, "list": "li"})
	if !strings.Contains(text, "— ok (2/2 steps ok)") || !strings.Contains(text, "Extracted data") || !strings.Contains(text, "Beans, 1 kg") {
		t.Fatalf("browser_extract: %s", text)
	}
	// A site the browser does not allow: named, with the way to allow it, and nothing runs.
	var before int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1`, wsID).Scan(&before)
	text = call("browser_extract", map[string]any{"url": "https://evil.test/", "fields": map[string]string{"x": "h1"}})
	var after int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1`, wsID).Scan(&after)
	if !strings.Contains(text, "evil.test") || !strings.Contains(text, "Nothing was run") || !strings.Contains(text, "browser_set_allowed_sites") || after != before {
		t.Fatalf("browser_extract off the list: %s (%d → %d runs)", text, before, after)
	}
	if text := call("browser_extract", map[string]any{"url": "file:///etc/passwd", "fields": map[string]string{"x": "h1"}}); !strings.Contains(text, "http(s)") {
		t.Fatalf("a file URL: %s", text)
	}

	// An explicit sequence, and its result read back by key.
	text = call("browser_run", map[string]any{"steps": []map[string]any{
		{"command": "navigate", "args": map[string]any{"url": "https://shop.test/"}},
		{"command": "click", "args": map[string]any{"selector": "#more"}},
		{"command": "extract", "args": map[string]any{"selector": "h1"}},
	}})
	if !strings.Contains(text, "(3/3 steps ok)") {
		t.Fatalf("browser_run: %s", text)
	}
	runKey := strings.Fields(strings.TrimPrefix(text, "Run "))[0]
	if text := call("browser_result", map[string]any{"run": runKey}); !strings.HasPrefix(text, "Run "+runKey+" — ok") || !strings.Contains(text, "3. extract — ok") {
		t.Fatalf("browser_result: %s", text)
	}
	if text := call("browser_result", map[string]any{"run": "00000000-0000-0000-0000-000000000000"}); !strings.Contains(text, "No run with that key") {
		t.Fatalf("browser_result, unknown key: %s", text)
	}

	// Cancelling: a run still going stops; a finished one says so.
	var liveRun string
	if err := a.SysDB.QueryRow(`INSERT INTO automation_runs (workspace_id, agent_id, status, trigger) VALUES ($1, $2, 'running', 'mcp') RETURNING id`, wsID, shopID).Scan(&liveRun); err != nil {
		t.Fatal(err)
	}
	if text := call("browser_cancel", map[string]any{"run": liveRun}); !strings.Contains(text, "is cancelled") {
		t.Fatalf("browser_cancel: %s", text)
	}
	var st string
	_ = a.SysDB.QueryRow(`SELECT status FROM automation_runs WHERE id = $1`, liveRun).Scan(&st)
	if st != "cancelled" {
		t.Fatalf("the run is %s", st)
	}
	if text := call("browser_cancel", map[string]any{"run": runKey}); !strings.Contains(text, "already finished") {
		t.Fatalf("browser_cancel, finished: %s", text)
	}

	// No name and two connected browsers: the model is asked, not guessed for.
	connect("Laptop Chrome", "{shop.test}")
	if text := call("browser_extract", map[string]any{"url": "https://shop.test/", "fields": map[string]string{"x": "h1"}}); !strings.Contains(text, "More than one browser is connected") {
		t.Fatalf("two browsers, no name: %s", text)
	}
	if text := call("browser_extract", map[string]any{"agent": "laptop chrome", "url": "https://shop.test/", "fields": map[string]string{"x": "h1"}}); !strings.Contains(text, "(2/2 steps ok)") {
		t.Fatalf("named: %s", text)
	}
}
