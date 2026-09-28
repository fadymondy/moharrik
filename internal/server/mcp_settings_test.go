package server

/*
TestMCPSettingsTools covers MH-680: the workspace settings a token can be
granted — the workspace itself, members and invitations, the pipeline, the
mail services and the audit retention — driven over /api/mcp on the real
stack. A token without the scope is refused naming it, and a token keeps no
more than its member's role: demote the member and their token loses the
tools with them. The Google tools are left out: disconnect revokes at Google.
*/

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMCPSettingsTools(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice
	agent, _ := mint(t, alice, "settings agent", allUserScopes())

	// ---- the workspace -------------------------------------------------------
	w := mcpJSON(t, agent, "update_workspace", map[string]any{"name": "  Renamed by agent  "})["workspace"].(map[string]any)
	if w["name"] != "Renamed by agent" {
		t.Fatalf("update_workspace: %v", w)
	}
	mcpRefused(t, agent, "update_workspace", map[string]any{"name": "   "})
	png := []byte("\x89PNG\r\n\x1a\n-not-really-a-png-but-typed-as-one")
	w = mcpJSON(t, agent, "set_workspace_picture", map[string]any{
		"data": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "content_type": "image/png",
	})["workspace"].(map[string]any)
	avatar, _ := w["avatar_url"].(string)
	if !strings.HasPrefix(avatar, "http") {
		t.Fatalf("set_workspace_picture: avatar_url should be absolute, got %q", avatar)
	}
	mcpRefused(t, agent, "set_workspace_picture", map[string]any{"data": base64.StdEncoding.EncodeToString(png), "content_type": "image/gif"})
	w = mcpJSON(t, agent, "remove_workspace_picture", map[string]any{})["workspace"].(map[string]any)
	if w["avatar_url"] == avatar {
		t.Fatalf("remove_workspace_picture kept the picture: %v", w)
	}

	// ---- invitations ---------------------------------------------------------
	bobEmail := "settings-bob-" + rnd() + "@example.test"
	inv := mcpJSON(t, agent, "invite_member", map[string]any{"email": bobEmail, "role": "admin"})
	accept, _ := inv["accept_url"].(string)
	if !strings.HasPrefix(accept, "http") || !strings.Contains(accept, "/invite/") {
		t.Fatalf("invite_member: accept_url should be an absolute /invite/ link, got %q", accept)
	}
	mcpRefused(t, agent, "invite_member", map[string]any{"email": "not-an-email", "role": "member"})
	mcpRefused(t, agent, "invite_member", map[string]any{"email": "x-" + rnd() + "@example.test", "role": "owner"})
	spare := mcpJSON(t, agent, "invite_member", map[string]any{"email": "spare-" + rnd() + "@example.test", "role": "client"})
	spareID := spare["invitation"].(map[string]any)["id"].(string)
	if n := mcpJSON(t, agent, "list_invitations", map[string]any{})["count"].(float64); n != 2 {
		t.Fatalf("list_invitations: want 2 pending, got %v", n)
	}
	mcpJSON(t, agent, "revoke_invitation", map[string]any{"id": spareID})
	mcpRefused(t, agent, "revoke_invitation", map[string]any{"id": spareID})
	mcpRefused(t, agent, "revoke_invitation", map[string]any{"id": "not-a-uuid"})

	// Bob accepts through the link the tool returned.
	bob := newClient(t, sf.srv.URL)
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-7"}, 201)
	bob.must("POST", "/api/invitations/accept", map[string]string{"token": accept[strings.LastIndex(accept, "/")+1:]}, 200)
	bob.ws = sf.ws
	mcpRefused(t, agent, "invite_member", map[string]any{"email": bobEmail, "role": "member"}) // already a member

	// ---- a token is never more than its member -------------------------------
	bobAgent, _ := mint(t, bob, "bob's agent", []string{"members:manage", "workspace:update"})
	mcpJSON(t, bobAgent, "list_invitations", map[string]any{})
	members := mcpJSON(t, agent, "set_member_role", map[string]any{"member": bobEmail, "role": "member"})["members"].([]any)
	if roleOf(members, bobEmail) != "member" {
		t.Fatalf("set_member_role: bob should be a member now: %v", members)
	}
	mcpRefused(t, bobAgent, "invite_member", map[string]any{"email": "y-" + rnd() + "@example.test", "role": "member"})
	mcpRefused(t, bobAgent, "update_workspace", map[string]any{"name": "Bob's now"})

	// ---- removing members ----------------------------------------------------
	var aliceEmail string
	for _, m := range members {
		if mm := m.(map[string]any); mm["role"] == "owner" {
			aliceEmail = mm["email"].(string)
		}
	}
	if txt := mcpRefused(t, agent, "remove_member", map[string]any{"member": aliceEmail}); !strings.Contains(txt, "own member") {
		t.Fatalf("remove_member on the token's own member: %s", txt)
	}
	members = mcpJSON(t, agent, "remove_member", map[string]any{"member": bobEmail})["members"].([]any)
	if roleOf(members, bobEmail) != "" {
		t.Fatalf("remove_member left bob in: %v", members)
	}

	// ---- the pipeline --------------------------------------------------------
	stages := mcpJSON(t, agent, "set_pipeline_stages", map[string]any{"stages": []any{
		map[string]any{"name": "Talking", "kind": "open"},
		map[string]any{"name": "Proposal", "kind": "open"},
		map[string]any{"name": "Signed", "kind": "won"},
		map[string]any{"name": "Gone", "kind": "lost"},
	}})["stages"].([]any)
	if len(stages) != 4 || stages[0].(map[string]any)["name"] != "Talking" {
		t.Fatalf("set_pipeline_stages: %v", stages)
	}
	first := stages[0].(map[string]any)["id"].(string)
	stages = mcpJSON(t, agent, "set_pipeline_stages", map[string]any{"stages": []any{
		map[string]any{"id": first, "name": "Discovery", "kind": "open"},
		map[string]any{"name": "Signed", "kind": "won"},
	}})["stages"].([]any)
	if len(stages) != 2 || stages[0].(map[string]any)["id"] != first || stages[0].(map[string]any)["name"] != "Discovery" {
		t.Fatalf("set_pipeline_stages rename/remove: %v", stages)
	}
	mcpRefused(t, agent, "set_pipeline_stages", map[string]any{"stages": []any{map[string]any{"name": "Won only", "kind": "won"}}})

	// ---- mail services -------------------------------------------------------
	svc := mcpJSON(t, agent, "create_smtp_service", map[string]any{
		"name": "Primary", "host": "smtp.example.test", "port": 587, "username": "mailer",
		"password": "s3cret-never-returned", "from_email": "hello@example.test",
	})["service"].(map[string]any)
	svcID := svc["id"].(string)
	if svc["has_password"] != true || svc["is_default"] != true {
		t.Fatalf("create_smtp_service: %v", svc)
	}
	second := mcpJSON(t, agent, "create_smtp_service", map[string]any{
		"name": "Bulk", "host": "bulk.example.test", "port": 465, "from_email": "news@example.test", "tls_mode": "tls",
	})["service"].(map[string]any)["id"].(string)
	svc = mcpJSON(t, agent, "update_smtp_service", map[string]any{
		"id": svcID, "name": "Primary (renamed)", "host": "smtp.example.test", "port": 2525, "from_email": "hello@example.test",
	})["service"].(map[string]any)
	if svc["has_password"] != true || svc["port"].(float64) != 2525 {
		t.Fatalf("update_smtp_service without a password should keep it: %v", svc)
	}
	list := mcpJSON(t, agent, "set_default_smtp_service", map[string]any{"id": second, "campaign": true})
	if raw := mustJSON(list); strings.Contains(raw, "s3cret") {
		t.Fatal("an SMTP password came back from a tool")
	}
	for _, s := range list["services"].([]any) {
		if sm := s.(map[string]any); sm["id"] == second && sm["is_campaign_default"] != true {
			t.Fatalf("set_default_smtp_service campaign: %v", sm)
		}
	}
	mcpJSON(t, agent, "delete_smtp_service", map[string]any{"id": second})
	if n := mcpJSON(t, agent, "smtp_services", map[string]any{})["count"].(float64); n != 1 {
		t.Fatalf("smtp_services after delete: %v", n)
	}

	// ---- audit retention -----------------------------------------------------
	if d := mcpJSON(t, agent, "set_audit_retention", map[string]any{"days": 90})["retention_days"].(float64); d != 90 {
		t.Fatalf("set_audit_retention: %v", d)
	}
	mcpRefused(t, agent, "set_audit_retention", map[string]any{"days": 10})

	// ---- scopes: a token without one is refused, naming it --------------------
	narrow, _ := mint(t, alice, "narrow", []string{"members:read"})
	for tool, scope := range map[string]string{
		"update_workspace":        "workspace:update",
		"invite_member":           "members:manage",
		"set_pipeline_stages":     "crm:manage",
		"smtp_services":           "mail:settings",
		"set_audit_retention":     "audit:manage",
		"list_google_connections": "integrations:manage",
	} {
		if txt := mcpRefused(t, narrow, tool, map[string]any{}); !strings.Contains(txt, scope) {
			t.Errorf("%s refused without naming %s: %s", tool, scope, txt)
		}
	}
}

func roleOf(members []any, email string) string {
	for _, m := range members {
		if mm := m.(map[string]any); strings.EqualFold(mm["email"].(string), email) {
			return mm["role"].(string)
		}
	}
	return ""
}
