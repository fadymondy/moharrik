package server

/*
The superadmin panel (MH-1184), against the real stack: who reaches /api/admin/*, suspending an
account (sign-in refused, every call 403, MCP tokens dead), ending its sessions, suspending a
workspace, granting platform admin, the self-lockout refusals, and that every change lands in
admin_audit.
*/

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/platform"
)

func TestPlatformAdmin(t *testing.T) {
	a, base := bootAudit(t)

	root, bob := newClient(t, base), newClient(t, base)
	rootEmail, bobEmail := "root-"+rnd()+"@example.test", "padm-bob-"+rnd()+"@example.test"
	root.must("POST", "/api/auth/register", map[string]string{"email": rootEmail, "password": "correct-horse-9"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-9"}, 201)
	var rootID, bobID string
	_ = a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, rootEmail).Scan(&rootID)
	_ = a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, bobEmail).Scan(&bobID)
	ws := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Padm " + rnd()}, 201)["workspace"].(map[string]any)
	wsID, slug := ws["id"].(string), ws["slug"].(string)
	bob.ws = wsID
	bobTok := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "padm", "scopes": []string{"members:read"}}, 201)
	bobMCP := newClient(t, base)
	bobMCP.bearer = bobTok["plaintext"].(string)
	mcpOK := func(want bool, when string) {
		t.Helper()
		code, _, isErr := mcpCall(bobMCP, "/api/mcp", "whoami", nil)
		if ok := code == 200 && !isErr; ok != want {
			t.Errorf("%s: bob's MCP token works=%v, want %v (status %d)", when, ok, want, code)
		}
	}
	mcpOK(true, "before anything")

	// ---- nobody but a platform admin reaches the panel ------------------------
	anon := newClient(t, base)
	anon.must("GET", "/api/admin/stats", nil, 404)
	root.must("GET", "/api/admin/stats", nil, 404)
	if got := root.must("GET", "/api/me/platform", nil, 200); got["is_admin"] != false {
		t.Fatalf("me/platform before the grant: %v", got)
	}
	if _, err := a.SysDB.Exec(`UPDATE users SET roles = 'admin' WHERE id = $1`, rootID); err != nil {
		t.Fatal(err)
	}
	platform.Invalidate(rootID)
	if got := root.must("GET", "/api/me/platform", nil, 200); got["is_admin"] != true {
		t.Fatalf("me/platform after the grant: %v", got)
	}
	stats := root.must("GET", "/api/admin/stats", nil, 200)
	if u := stats["users"].(map[string]any); u["total"].(float64) < 2 || u["admins"].(float64) < 1 {
		t.Errorf("stats users: %v", u)
	}
	if s := stats["signups"].([]any); len(s) != 30 {
		t.Errorf("signups series has %d days, want 30", len(s))
	}
	bob.must("GET", "/api/admin/users", nil, 404)

	code, b := root.do("GET", "/api/admin/users?q="+bobEmail, nil)
	var users []map[string]any
	_ = json.Unmarshal(b, &users)
	if code != 200 || len(users) != 1 || users[0]["id"] != bobID || len(users[0]["workspaces"].([]any)) != 1 {
		t.Fatalf("user search: %d %s", code, b)
	}
	if d := root.must("GET", "/api/admin/workspaces/"+slug, nil, 200); d["owner_email"] != bobEmail || len(d["member_list"].([]any)) != 1 {
		t.Fatalf("workspace detail: %v", d)
	}
	root.must("GET", "/api/admin/permissions", nil, 200)

	// ---- self-lockout ---------------------------------------------------------
	root.must("POST", "/api/admin/users/"+rootID+"/suspend", map[string]string{}, 409)
	root.must("PUT", "/api/admin/users/"+rootID+"/admin", map[string]bool{"admin": false}, 409)

	// ---- suspending an account -----------------------------------------------
	root.must("POST", "/api/admin/users/"+bobEmail+"/suspend", map[string]string{"reason": "testing"}, 200)
	if code, b := bob.do("GET", "/api/me/workspaces", nil); code != 403 || !strings.Contains(string(b), "account_suspended") || !strings.Contains(string(b), "testing") {
		t.Errorf("a suspended account's call: %d %s", code, b)
	}
	if code, _ := bob.do("GET", "/api/w/members", nil); code != 403 {
		t.Errorf("a suspended account's workspace call: %d", code)
	}
	mcpOK(false, "account suspended")
	again := newClient(t, base)
	if code, b := again.do("POST", "/api/auth/login", map[string]string{"email": bobEmail, "password": "correct-horse-9"}); code != 403 || !strings.Contains(string(b), "account_suspended") {
		t.Errorf("a suspended account's sign-in: %d %s", code, b)
	}
	root.must("POST", "/api/admin/users/"+bobID+"/reactivate", nil, 200)
	bob.must("GET", "/api/me/workspaces", nil, 200)
	mcpOK(true, "account reactivated")

	// ---- ending sessions ----------------------------------------------------------
	time.Sleep(1100 * time.Millisecond) // a session's iat has whole-second resolution
	root.must("POST", "/api/admin/users/"+bobID+"/sign-out", nil, 200)
	if code, b := bob.do("GET", "/api/me/workspaces", nil); code != 401 || !strings.Contains(string(b), "session_revoked") {
		t.Errorf("an ended session: %d %s", code, b)
	}
	fresh := newClient(t, base)
	fresh.must("POST", "/api/auth/login", map[string]string{"email": bobEmail, "password": "correct-horse-9"}, 200)
	fresh.must("GET", "/api/me/workspaces", nil, 200)
	fresh.ws = wsID

	// ---- suspending a workspace ----------------------------------------------------
	root.must("POST", "/api/admin/workspaces/"+slug+"/suspend", map[string]string{"reason": "unpaid"}, 200)
	if code, b := fresh.do("GET", "/api/w/members", nil); code != 403 || !strings.Contains(string(b), "suspended") {
		t.Errorf("a suspended workspace: %d %s", code, b)
	}
	mcpOK(false, "workspace suspended")
	fresh.must("GET", "/api/me/workspaces", nil, 200) // the account itself is fine
	root.must("POST", "/api/admin/workspaces/"+slug+"/reactivate", nil, 200)
	fresh.must("GET", "/api/w/members", nil, 200)
	mcpOK(true, "workspace reactivated")

	// ---- members ---------------------------------------------------------------------
	root.must("POST", "/api/admin/workspaces/"+slug+"/members", map[string]string{"user": rootEmail, "role": "member"}, 200)
	root.must("POST", "/api/admin/workspaces/"+slug+"/members", map[string]string{"user": rootEmail, "role": "member"}, 409)
	root.must("PUT", "/api/admin/workspaces/"+slug+"/members/"+bobID, map[string]string{"role": "member"}, 409) // last owner
	root.must("DELETE", "/api/admin/workspaces/"+slug+"/members/"+rootID, nil, 200)

	// ---- platform admin grant ----------------------------------------------------------
	root.must("PUT", "/api/admin/users/"+bobID+"/admin", map[string]bool{"admin": true}, 200)
	if got := fresh.must("GET", "/api/me/platform", nil, 200); got["is_admin"] != true {
		t.Errorf("granted admin not visible: %v", got)
	}
	root.must("PUT", "/api/admin/users/"+bobID+"/admin", map[string]bool{"admin": false}, 200)
	fresh.must("GET", "/api/admin/stats", nil, 404)

	// ---- deleting needs the slug -------------------------------------------------------
	root.must("DELETE", "/api/admin/workspaces/"+wsID+"?confirm=nope", nil, 422)

	// ---- the audit trail ----------------------------------------------------------------
	code, b = root.do("GET", "/api/admin/audit?target="+bobID, nil)
	var entries []map[string]any
	_ = json.Unmarshal(b, &entries)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e["action"].(string)] = true
		if e["actor_id"] != rootID {
			t.Errorf("audit entry by %v, want root", e["actor_id"])
		}
	}
	for _, want := range []string{"user.suspend", "user.reactivate", "user.sign_out", "user.grant_admin", "user.revoke_admin"} {
		if !seen[want] {
			t.Errorf("audit is missing %s: %d %s", want, code, b)
		}
	}
	code, b = root.do("GET", "/api/admin/audit?target="+wsID, nil)
	for _, want := range []string{"workspace.suspend", "workspace.reactivate", "member.add", "member.remove"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("workspace audit is missing %s: %d %s", want, code, b)
		}
	}
	_ = http.StatusOK
}
