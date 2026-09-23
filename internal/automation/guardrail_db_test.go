package automation_test

import (
	"context"
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
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The run's side of MH-422, against a real database and a real socket.

The extension decides that a run has left its allowed sites — only the browser
knows where its tab landed — and says so with code "left_allowed_sites". What
the SERVER must do with that is tested here, through the real dispatcher and
the real hub, with a fake browser on the other end of a real WebSocket:

  - every step carries the browser's CURRENT list, re-read per step, so the
    extension checks landings against the list as it is now
  - a left-the-sites refusal stops the run even on a continue_on_error step,
    records the browser's sentence as the run's failure reason, and skips the
    rest — no later step is ever sent
  - an ordinary refusal on a continue_on_error step still carries on
  - a goto whose host was removed from the list mid-run is refused by the
    server before it is sent
  - the kill switch and cancellation still stop a run, and a run stopped by
    the kill switch reads "cancelled" — not "failed: this browser is not
    connected", which is what the dropped socket makes the step in flight say
*/

type fakeBrowser struct {
	t    *testing.T
	ws   *websocket.Conn
	mu   sync.Mutex
	seen []map[string]any // run.step payloads, in order
	ends int              // run.end frames
}

// connectBrowser pairs a browser with a known token, serves the real hub, and
// completes the hello handshake. answer decides each step's reply.
func connectBrowser(t *testing.T, a *app.App, allowed string, answer func(n int, payload map[string]any) automation.Envelope) (hub *automation.Hub, wsID, agentID string, fb *fakeBrowser) {
	t.Helper()
	ctx := context.Background()

	token, _ := automation.NewToken()
	suffix := time.Now().UnixNano()
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by) VALUES ($1, $2, 'guardrail-test') RETURNING id`,
		"Guardrail Test", "guardrail-test-"+itoa(suffix)).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
		VALUES ($1, 'guardrail browser', $2, '', $3::text[]) RETURNING id`,
		wsID, automation.HashToken(token), allowed).Scan(&agentID); err != nil {
		t.Fatal(err)
	}

	hub = automation.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(hub.Handler(a))
	t.Cleanup(srv.Close)

	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	hello, _ := json.Marshal(map[string]any{"token": token, "extension_version": "0.1.2"})
	if err := ws.WriteJSON(automation.Envelope{ID: "hello", Type: "hello", Payload: hello}); err != nil {
		t.Fatal(err)
	}
	var ack automation.Envelope
	if err := ws.ReadJSON(&ack); err != nil || ack.Type != "hello.ok" {
		t.Fatalf("no hello.ok: %v %+v", err, ack)
	}

	fb = &fakeBrowser{t: t, ws: ws}
	go func() {
		for {
			var env automation.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			switch env.Type {
			case "run.end":
				fb.mu.Lock()
				fb.ends++
				fb.mu.Unlock()
			case "run.step":
				var p map[string]any
				_ = json.Unmarshal(env.Payload, &p)
				fb.mu.Lock()
				fb.seen = append(fb.seen, p)
				n := len(fb.seen)
				fb.mu.Unlock()
				reply := answer(n, p)
				reply.ID, reply.Type = env.ID, "run.step.result"
				_ = ws.WriteJSON(reply)
			}
		}
	}()

	for i := 0; i < 50 && !hub.Online(agentID); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !hub.Online(agentID) {
		t.Fatal("the fake browser never came online")
	}
	return hub, wsID, agentID, fb
}

func (fb *fakeBrowser) payloads() []map[string]any {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]map[string]any(nil), fb.seen...)
}

type finishedRun struct {
	status, err string
	steps       []struct{ status, err string }
}

func waitForRun(t *testing.T, a *app.App, wsID, runID string) finishedRun {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	var out finishedRun
	for i := 0; i < 200; i++ {
		if err := a.SQLDB.QueryRowContext(sctx, `
			SELECT status, error FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, runID).Scan(&out.status, &out.err); err != nil {
			t.Fatal(err)
		}
		if out.status != "queued" && out.status != "running" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT status, error FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`, wsID, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s struct{ status, err string }
		if err := rows.Scan(&s.status, &s.err); err != nil {
			t.Fatal(err)
		}
		out.steps = append(out.steps, s)
	}
	return out
}

func ok(payload string) automation.Envelope {
	return automation.Envelope{Payload: json.RawMessage(payload)}
}

func TestGuardrailLeavingTheAllowedSitesStopsTheRunEvenOnContinueOnError(t *testing.T) {
	a := testApp(t)
	const left = "step 3 went to evil.test, which isn't on this browser's allowed sites"
	hub, wsID, agentID, fb := connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		switch n {
		case 2:
			return automation.Envelope{Error: "nothing on the page matches #missing"} // ordinary, no code
		case 3:
			return automation.Envelope{Error: left, Code: automation.CodeLeftAllowedSites}
		}
		return ok(`{"summary":"fine"}`)
	})

	runID, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}},
		{Command: "click", Args: map[string]any{"selector": "#missing"}, ContinueOnError: true},
		{Command: "click", Args: map[string]any{"selector": "a.away"}, ContinueOnError: true},
		{Command: "extract", Args: map[string]any{"selector": "body"}, ContinueOnError: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, a, wsID, runID)

	if run.status != "failed" {
		t.Fatalf("status = %q, want failed", run.status)
	}
	// The browser's sentence IS the reason: it names the step and the host,
	// and it is not wrapped in a second "step 3:".
	if run.err != left {
		t.Errorf("run error = %q, want %q", run.err, left)
	}
	want := []string{"ok", "failed", "failed", "skipped"}
	for i, s := range run.steps {
		if s.status != want[i] {
			t.Errorf("step %d = %s, want %s (%s)", i+1, s.status, want[i], s.err)
		}
	}
	if run.steps[2].err != left {
		t.Errorf("step 3 error = %q", run.steps[2].err)
	}

	seen := fb.payloads()
	if len(seen) != 3 {
		t.Fatalf("the browser was sent %d steps; the one after leaving the sites must never be sent", len(seen))
	}
	for i, p := range seen {
		if got, _ := p["step"].(float64); int(got) != i+1 {
			t.Errorf("payload %d carries step %v, want %d", i+1, p["step"], i+1)
		}
		hosts, _ := p["allowed_hosts"].([]any)
		if len(hosts) != 1 || hosts[0] != "allowed.test" {
			t.Errorf("payload %d carries allowed_hosts %v", i+1, p["allowed_hosts"])
		}
	}
	// The browser is told the run is over, so it closes the tab.
	for i := 0; i < 50; i++ {
		fb.mu.Lock()
		ends := fb.ends
		fb.mu.Unlock()
		if ends > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("no run.end was sent after the run stopped")
}

func TestGuardrailEveryStepCarriesTheListAsItIsNow(t *testing.T) {
	a := testApp(t)
	var wsID, agentID string
	var hub *automation.Hub
	var fb *fakeBrowser
	hub, wsID, agentID, fb = connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
		if n == 1 {
			// Someone edits the list while the run is going: allowed.test is
			// taken off, other.test put on.
			sctx := tenant.WithWorkspace(context.Background(), wsID)
			if _, err := a.SQLDB.ExecContext(sctx, `
				UPDATE browser_agents SET allowed_hosts = '{other.test}' WHERE workspace_id = $1 AND id = $2`, wsID, agentID); err != nil {
				t.Error(err)
			}
		}
		return ok(`{"summary":"fine"}`)
	})

	runID, err := hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}},
		{Command: "click", Args: map[string]any{"selector": "#next"}},
		{Command: "goto", Args: map[string]any{"url": "https://allowed.test/again"}, ContinueOnError: true},
		{Command: "extract", Args: map[string]any{"selector": "body"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := waitForRun(t, a, wsID, runID)
	seen := fb.payloads()

	if len(seen) != 2 {
		t.Fatalf("sent %d steps; the goto to a host no longer on the list must not be sent", len(seen))
	}
	if hosts, _ := seen[1]["allowed_hosts"].([]any); len(hosts) != 1 || hosts[0] != "other.test" {
		t.Errorf("step 2 was sent the list %v; it must be the list as edited, [other.test]", seen[1]["allowed_hosts"])
	}
	if run.status != "failed" || !strings.Contains(run.err, "step 3") || !strings.Contains(run.err, "allowed.test") {
		t.Errorf("run = %q %q", run.status, run.err)
	}
	if got := []string{run.steps[2].status, run.steps[3].status}; got[0] != "failed" || got[1] != "skipped" {
		t.Errorf("steps 3 and 4 = %v", got)
	}
}

// The kill switch and cancellation still reach a running run: both are read
// between steps, before the allow-list, and a step after either is never sent.
func TestGuardrailKillSwitchAndCancelStillStopARun(t *testing.T) {
	a := testApp(t)

	for _, how := range []string{"kill switch", "cancel"} {
		t.Run(how, func(t *testing.T) {
			var wsID, runID string
			var hub *automation.Hub
			var agentID string
			started := make(chan struct{})
			var fb *fakeBrowser
			hub, wsID, agentID, fb = connectBrowser(t, a, "{allowed.test}", func(n int, p map[string]any) automation.Envelope {
				if n == 1 {
					<-started
					ctx := context.Background()
					if how == "kill switch" {
						if err := automation.Pause(ctx, a, wsID, "", "test"); err != nil {
							t.Error(err)
						}
						automation.CancelWorkspaceRuns(ctx, a, wsID, "automation was stopped")
						// As the route does: the browser's socket is dropped, so
						// this very step comes back "not connected".
						hub.DisconnectWorkspace(wsID, "automation is stopped")
					} else {
						sctx := tenant.WithWorkspace(ctx, wsID)
						if _, err := a.SQLDB.ExecContext(sctx, `
							UPDATE automation_runs SET status = 'cancelled', error = 'cancelled by test' WHERE workspace_id = $1 AND id = $2`, wsID, runID); err != nil {
							t.Error(err)
						}
					}
				}
				return ok(`{"summary":"fine"}`)
			})
			var err error
			runID, err = hub.Start(context.Background(), a, wsID, "", "manual", agentID, []automation.Step{
				{Command: "goto", Args: map[string]any{"url": "https://allowed.test/"}},
				{Command: "click", Args: map[string]any{"selector": "#next"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			close(started)
			run := waitForRun(t, a, wsID, runID)
			// The stop marks the row cancelled at once; the dispatcher finishes
			// the run a moment later, when the step in flight comes back. What
			// matters is the row AFTER that, so give it the moment.
			time.Sleep(500 * time.Millisecond)
			run = waitForRun(t, a, wsID, runID)
			// Cancelled, and saying why — not "failed: this browser is not
			// connected", which is what the dropped socket made the step say.
			if run.status != "cancelled" || strings.Contains(run.err, "not connected") {
				t.Errorf("run = %q (%s), want cancelled with the stop's own reason", run.status, run.err)
			}
			if got := len(fb.payloads()); got != 1 {
				t.Errorf("sent %d steps; nothing after the stop may be sent", got)
			}
		})
	}
}
