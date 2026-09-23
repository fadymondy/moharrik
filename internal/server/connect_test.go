package server

/*
TestConnectByApproval — connecting a browser by approval (MH-478), through the
real stack: the guard's public and signed-in tiers, the approval checks, the
one-time token collection, and the socket accepting the token that came out
of it.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run Connect -v
*/

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/testdb"
)

type connectStart struct {
	Code       string `json:"code"`
	PollToken  string `json:"poll_token"`
	BrowserKey string `json:"browser_key"`
	VerifyPath string `json:"verify_path"`
}

func startConnect(t *testing.T, c *client, fingerprint, name string) connectStart {
	t.Helper()
	code, b := c.do("POST", "/api/agents/connect/start", map[string]any{
		"name": name, "fingerprint": fingerprint, "platform": "Windows x86-64",
		"browser_version": "Chrome 140", "extension_version": "0.3.0", "locale": "ar",
	})
	if code != http.StatusCreated {
		t.Fatalf("connect/start: %d %s", code, b)
	}
	var s connectStart
	_ = json.Unmarshal(b, &s)
	return s
}

func poll(t *testing.T, c *client, pollToken string) map[string]any {
	t.Helper()
	return c.must("POST", "/api/agents/connect/poll", map[string]any{"poll_token": pollToken}, 200)
}

// hello dials the browser socket with a token and returns the first frame the
// server answers with, or the close error.
func hello(t *testing.T, srvURL, token string) (automation.Envelope, error) {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srvURL, "http")+"/api/agents/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	payload, _ := json.Marshal(map[string]any{"token": token, "extension_version": "0.3.0"})
	if err := ws.WriteJSON(automation.Envelope{ID: "hello", Type: "hello", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var env automation.Envelope
	err = ws.ReadJSON(&env)
	return env, err
}

func TestConnectByApproval(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "connect-test-secret-connect-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()
	ctx := context.Background()

	alice, bob, carol, anon := newClient(t, srv.URL), newClient(t, srv.URL), newClient(t, srv.URL), newClient(t, srv.URL)
	carolEmail := "carol-" + rnd() + "@example.test"
	alice.must("POST", "/api/auth/register", map[string]string{"email": "alice-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "bob-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	carol.must("POST", "/api/auth/register", map[string]string{"email": carolEmail, "password": "correct-horse-3"}, 201)
	wsA := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Connect A " + rnd()}, 201)["workspace"].(map[string]any)
	wsB := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Connect B " + rnd()}, 201)["workspace"].(map[string]any)
	aID, aName, bID := wsA["id"].(string), wsA["name"].(string), wsB["id"].(string)
	// carol is a plain member of A: she may see automation, not connect a browser.
	var carolID string
	if err := a.SysDB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, carolEmail).Scan(&carolID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, aID, carolID); err != nil {
		t.Fatal(err)
	}

	// ---- the extension asks, anonymously ---------------------------------
	fp := "fp-" + rnd()
	s := startConnect(t, anon, fp, "Windows browser")
	if len(automation.NormaliseConnectCode(s.Code)) != 8 || len(s.PollToken) < 16 || len(s.BrowserKey) < 16 {
		t.Fatalf("start handed back %+v", s)
	}
	if s.VerifyPath != "/ar/automation/connect/"+s.Code+"#k="+s.BrowserKey {
		t.Fatalf("verify path %q", s.VerifyPath)
	}
	// Nothing about the request is stored in the clear.
	var clear int
	_ = a.SysDB.QueryRowContext(ctx, `SELECT count(*) FROM agent_connects WHERE code_hash = $1 OR poll_hash = $2 OR key_hash = $3`,
		s.Code, s.PollToken, s.BrowserKey).Scan(&clear)
	if clear != 0 {
		t.Fatal("a connect secret is stored in the clear")
	}
	if p := poll(t, anon, s.PollToken); p["status"] != "pending" || p["token"] != nil {
		t.Fatalf("poll before approval: %v", p)
	}
	if code, _ := anon.do("POST", "/api/agents/connect/poll", map[string]any{"poll_token": "moh_poll_nobody-has-this-one"}); code != 404 {
		t.Fatalf("an unknown poll token: %d", code)
	}

	// ---- the page: signed in only, and each member sees their own ------
	if code, _ := anon.do("GET", "/api/agents/connect/"+s.Code, nil); code != 401 {
		t.Fatalf("anonymous GET of a connect request: %d", code)
	}
	// Without the page's key: that the request exists, nothing about the browser.
	if v := alice.must("GET", "/api/agents/connect/"+s.Code, nil, 200); v["key_ok"] != false || v["requested_name"] != "" || v["ip"] != "" {
		t.Fatalf("a view without the key described the browser: %v", v)
	}
	alice.connectKey = s.BrowserKey
	view := alice.must("GET", "/api/agents/connect/"+strings.ToLower(s.Code), nil, 200)
	alice.connectKey = ""
	if view["status"] != "pending" || view["requested_name"] != "Windows browser" || view["code"] != s.Code || view["key_ok"] != true {
		t.Fatalf("alice's view: %v", view)
	}
	wss := view["workspaces"].([]any)
	if len(wss) != 1 || wss[0].(map[string]any)["id"] != aID || wss[0].(map[string]any)["can_approve"] != true {
		t.Fatalf("alice's workspaces: %v", wss)
	}
	if _, b := bob.do("GET", "/api/agents/connect/"+s.Code, nil); strings.Contains(string(b), aID) {
		t.Fatalf("bob's view names A: %s", b)
	}
	if cw := carol.must("GET", "/api/agents/connect/"+s.Code, nil, 200)["workspaces"].([]any); len(cw) != 1 || cw[0].(map[string]any)["can_approve"] != false {
		t.Fatalf("carol may not approve into A: %v", cw)
	}

	// ---- who may approve, and with what ---------------------------------
	approve := func(c *client, code, key, ws, name string, hosts []string) (int, []byte) {
		return c.do("POST", "/api/agents/connect/approve", map[string]any{
			"code": code, "key": key, "workspace": ws, "name": name, "allowed_hosts": hosts,
		})
	}
	if code, b := approve(alice, s.Code, "", aID, "Work laptop", nil); code != 403 {
		t.Fatalf("approving without the browser key: %d %s", code, b)
	}
	if code, b := approve(alice, s.Code, "not-the-key-at-all", aID, "Work laptop", nil); code != 403 {
		t.Fatalf("approving with the wrong key: %d %s", code, b)
	}
	if code, b := approve(bob, s.Code, s.BrowserKey, aID, "Bob's now", []string{"bob.test"}); code != 404 {
		t.Fatalf("bob approving into A: %d %s", code, b)
	}
	if code, b := approve(carol, s.Code, s.BrowserKey, aID, "Carol's", nil); code != 403 {
		t.Fatalf("a plain member approving: %d %s", code, b)
	}
	if code, _ := anon.do("POST", "/api/agents/connect/approve", map[string]any{"code": s.Code, "key": s.BrowserKey, "workspace": aID}); code != 401 {
		t.Fatalf("anonymous approve: %d", code)
	}
	// None of that decided anything.
	if p := poll(t, anon, s.PollToken); p["status"] != "pending" {
		t.Fatalf("refused approvals changed the request: %v", p)
	}

	code, b := approve(alice, s.Code, s.BrowserKey, aID, "Work laptop", []string{"https://Example.com/x", "mail.google.com"})
	if code != 200 {
		t.Fatalf("alice approving: %d %s", code, b)
	}
	var approved struct {
		Browser struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			AllowedHosts []string `json:"allowed_hosts"`
		} `json:"browser"`
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	_ = json.Unmarshal(b, &approved)
	if approved.Browser.Name != "Work laptop" || approved.Workspace.ID != aID ||
		strings.Join(approved.Browser.AllowedHosts, ",") != "example.com,mail.google.com" {
		t.Fatalf("approved: %s", b)
	}
	if code, _ := approve(alice, s.Code, s.BrowserKey, aID, "Again", nil); code != 409 {
		t.Fatalf("approving twice: %d", code)
	}

	// ---- the extension collects its token exactly once -------------------
	got := poll(t, anon, s.PollToken)
	token, _ := got["token"].(string)
	if got["status"] != "approved" || !strings.HasPrefix(token, automation.TokenPrefix) ||
		got["workspace_name"] != aName || got["agent_id"] != approved.Browser.ID || got["name"] != "Work laptop" {
		t.Fatalf("the first poll after approval: %v", got)
	}
	if again := poll(t, anon, s.PollToken); again["status"] != "claimed" || again["token"] != nil {
		t.Fatalf("a second poll handed out the token again: %v", again)
	}
	var once string
	_ = a.SysDB.QueryRowContext(ctx, `SELECT token_once FROM agent_connects WHERE poll_hash = encode(sha256($1::bytea), 'hex')`, s.PollToken).Scan(&once)
	if once != "" {
		t.Fatal("the collected token is still in the row")
	}

	// The browser belongs to A and nowhere else.
	alice.ws = aID
	if list := alice.must("GET", "/api/w/browsers", nil, 200)["browsers"].([]any); len(list) != 1 {
		t.Fatalf("A's browsers: %v", list)
	}
	bob.ws = bID
	if _, b := bob.do("GET", "/api/w/browsers", nil); strings.Contains(string(b), approved.Browser.ID) {
		t.Fatalf("B's browsers include A's: %s", b)
	}
	// Only the member who decided it is told where it went.
	if v := alice.must("GET", "/api/agents/connect/"+s.Code, nil, 200); v["workspace"] == nil || v["status"] != "claimed" {
		t.Fatalf("alice's view after approving: %v", v)
	}
	if _, b := bob.do("GET", "/api/agents/connect/"+s.Code, nil); strings.Contains(string(b), aID) || strings.Contains(string(b), aName) {
		t.Fatalf("bob learned where the browser went: %s", b)
	}

	// ---- the token opens the socket, and says which workspace -----------
	ack, err := hello(t, srv.URL, token)
	if err != nil || ack.Type != "hello.ok" {
		t.Fatalf("hello with the new token: %v %+v", err, ack)
	}
	var who map[string]any
	_ = json.Unmarshal(ack.Payload, &who)
	if who["workspace_name"] != aName || who["agent_id"] != approved.Browser.ID {
		t.Fatalf("hello.ok: %v", who)
	}

	// ---- the same browser again is the same browser, re-keyed ------------
	s2 := startConnect(t, anon, fp, "Windows browser")
	code, b = approve(alice, s2.Code, s2.BrowserKey, aSlug(t, alice, aID), "Work laptop", []string{"example.com"})
	if code != 200 || !strings.Contains(string(b), `"replaced":true`) || !strings.Contains(string(b), approved.Browser.ID) {
		t.Fatalf("reconnecting the same browser: %d %s", code, b)
	}
	token2, _ := poll(t, anon, s2.PollToken)["token"].(string)
	if token2 == "" || token2 == token {
		t.Fatal("reconnecting did not mint a new token")
	}
	if _, err := hello(t, srv.URL, token); err == nil {
		t.Fatal("the replaced token still opens the socket")
	}
	if ack, err := hello(t, srv.URL, token2); err != nil || ack.Type != "hello.ok" {
		t.Fatalf("the new token: %v %+v", err, ack)
	}

	// ---- a name already used is refused, and the request survives --------
	s3 := startConnect(t, anon, "fp-"+rnd(), "Another")
	if code, b := approve(alice, s3.Code, s3.BrowserKey, aID, "work LAPTOP", nil); code != 409 {
		t.Fatalf("a duplicate name: %d %s", code, b)
	}
	if code, b := approve(alice, s3.Code, s3.BrowserKey, aID, "Home desktop", nil); code != 200 {
		t.Fatalf("approving under another name after the refusal: %d %s", code, b)
	}

	// ---- deny --------------------------------------------------------------
	s4 := startConnect(t, anon, "fp-"+rnd(), "Unknown")
	if code, _ := bob.do("POST", "/api/agents/connect/deny", map[string]any{"code": s4.Code, "key": "wrong-key-wrong-key"}); code != 403 {
		t.Fatalf("denying without the key: %d", code)
	}
	bob.must("POST", "/api/agents/connect/deny", map[string]any{"code": s4.Code, "key": s4.BrowserKey}, 200)
	if p := poll(t, anon, s4.PollToken); p["status"] != "denied" || p["token"] != nil {
		t.Fatalf("poll after deny: %v", p)
	}
	if code, _ := approve(alice, s4.Code, s4.BrowserKey, aID, "Late", nil); code != 409 {
		t.Fatalf("approving a denied request: %d", code)
	}

	// ---- expiry ------------------------------------------------------------
	s5 := startConnect(t, anon, "fp-"+rnd(), "Slow")
	if _, err := a.SysDB.ExecContext(ctx, `UPDATE agent_connects SET expires_at = now() - interval '1 minute' WHERE poll_hash = encode(sha256($1::bytea), 'hex')`, s5.PollToken); err != nil {
		t.Fatal(err)
	}
	if p := poll(t, anon, s5.PollToken); p["status"] != "expired" {
		t.Fatalf("poll after expiry: %v", p)
	}
	if code, _ := approve(alice, s5.Code, s5.BrowserKey, aID, "Slow", nil); code != 410 {
		t.Fatalf("approving an expired request: %d", code)
	}
	if v := alice.must("GET", "/api/agents/connect/"+s5.Code, nil, 200); v["status"] != "expired" {
		t.Fatalf("an expired request reads: %v", v)
	}

	// ---- revoke drops it for good -------------------------------------------
	alice.must("DELETE", "/api/w/browsers/"+approved.Browser.ID, nil, 204)
	if _, err := hello(t, srv.URL, token2); err == nil {
		t.Fatal("a revoked browser's token still opens the socket")
	}
}

func aSlug(t *testing.T, c *client, id string) string {
	t.Helper()
	for _, w := range c.must("GET", "/api/me/workspaces", nil, 200)["workspaces"].([]any) {
		if m := w.(map[string]any); m["id"] == id {
			return m["slug"].(string)
		}
	}
	t.Fatal("no slug")
	return ""
}
