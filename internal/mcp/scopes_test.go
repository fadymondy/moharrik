package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/research"
)

// TestEveryToolDeclaresAScope is the CI guard for FM-22: a tool without a
// scope — or with a scope outside the closed list — fails the build.
func TestEveryToolDeclaresAScope(t *testing.T) {
	check := func(server string, tools []Tool, known func(string) bool) {
		seen := map[string]bool{}
		for _, tool := range tools {
			if seen[tool.Name] {
				t.Errorf("%s: duplicate tool name %q", server, tool.Name)
			}
			seen[tool.Name] = true
			if strings.TrimSpace(tool.Scope) == "" {
				t.Errorf("%s: tool %q declares no scope — every tool must (default-deny)", server, tool.Name)
				continue
			}
			if !known(tool.Scope) {
				t.Errorf("%s: tool %q declares unknown scope %q", server, tool.Name, tool.Scope)
			}
			if tool.Run == nil {
				t.Errorf("%s: tool %q has no Run", server, tool.Name)
			}
			// A tool the model cannot read the purpose of is a tool it will
			// misuse or never pick. Short ones are almost always a name
			// restated, so this asks for a sentence.
			if d := strings.TrimSpace(tool.Description); len(d) < 20 {
				t.Errorf("%s: tool %q has no real description (%q)", server, tool.Name, d)
			}
			if tool.InputSchema == nil || tool.InputSchema["type"] != "object" {
				t.Errorf("%s: tool %q has no object input schema", server, tool.Name)
			}
			// "properties" must encode as a JSON object, never null: one null
			// makes strict clients (Claude Code) reject the whole tools/list.
			if b, _ := json.Marshal(tool.InputSchema["properties"]); len(b) == 0 || b[0] != '{' {
				t.Errorf("%s: tool %q: inputSchema.properties encodes as %s, not an object", server, tool.Name, b)
			}
		}
	}
	check("user", UserTools(&research.Engine{}), func(s string) bool { _, ok := userScope(s); return ok })
	check("admin", AdminTools(), adminScope)

	for _, tool := range UserTools(&research.Engine{}) {
		if adminScope(tool.Scope) {
			t.Errorf("user tool %q uses an admin scope", tool.Name)
		}
	}
	for _, tool := range AdminTools() {
		if _, ok := userScope(tool.Scope); ok {
			t.Errorf("admin tool %q uses a user scope", tool.Name)
		}
	}
}

// TestEveryOfferedScopeHasATool: a scope on the token page that no tool
// declares grants nothing, which is how trends:read sat offered and empty
// until MH-386. Every user scope must now be used by at least one tool.
func TestEveryOfferedScopeHasATool(t *testing.T) {
	used := map[string]bool{}
	for _, tool := range UserTools(&research.Engine{}) {
		used[tool.Scope] = true
	}
	// convert_lead also demands contacts:write in-band (user_tools_leads.go),
	// which the loop above cannot see; contacts:write has tools of its own.
	for _, s := range UserScopes {
		if !used[s.Scope] {
			t.Errorf("scope %q is offered on the token page but no tool declares it", s.Scope)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("scope %q has no description", s.Scope)
		}
	}
}

func call(t *testing.T, s *server, scopes []string, tool string) map[string]any {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": tool, "arguments": map[string]any{}})
	res := s.dispatch(context.Background(), &app.App{}, scopes, rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params,
	})
	if res.Error != nil {
		t.Fatalf("expected an in-band result, got RPC error %+v", res.Error)
	}
	out, _ := res.Result.(map[string]any)
	return out
}

// TestDispatchDeniesUnscopedTool: even if a scope-less tool slipped past the
// test above, dispatch refuses it rather than allowing it (the FM-22 bug was
// the opposite).
func TestDispatchDeniesUnscopedTool(t *testing.T) {
	ran := false
	s := &server{tools: []Tool{{Name: "sneaky", Run: func(context.Context, *app.App, json.RawMessage) (string, error) {
		ran = true
		return "ran", nil
	}}}}
	out := call(t, s, []string{"members:read", ""}, "sneaky")
	if ran || out["isError"] != true {
		t.Fatalf("a tool with no scope must be denied in-band, got %+v (ran=%v)", out, ran)
	}
}

func TestOutOfScopeFailsInBand(t *testing.T) {
	s := &server{tools: []Tool{{Name: "list_contacts", Scope: "contacts:read",
		Run: func(context.Context, *app.App, json.RawMessage) (string, error) { return "ok", nil }}}}
	out := call(t, s, []string{"members:read"}, "list_contacts")
	if out["isError"] != true {
		t.Fatalf("out-of-scope call must be isError, got %+v", out)
	}
	if out := call(t, s, []string{"contacts:read"}, "list_contacts"); out["isError"] == true {
		t.Fatalf("in-scope call failed: %+v", out)
	}
}

func TestEffectiveNarrowsToRole(t *testing.T) {
	got := Effective("member", []string{"members:read", "campaigns:write", "automation:run", "nonsense"})
	if len(got) != 1 || got[0] != "members:read" {
		t.Fatalf("a member's token must keep only member scopes, got %v", got)
	}
	if len(Effective("owner", []string{"campaigns:write"})) != 1 {
		t.Fatal("an owner may hold campaigns:write")
	}
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	s := &server{name: "t"}
	for requested, want := range map[string]string{"2025-06-18": "2025-06-18", "1999-01-01": protocolVersion, "": protocolVersion} {
		params, _ := json.Marshal(map[string]any{"protocolVersion": requested})
		res := s.dispatch(context.Background(), &app.App{}, nil, rpcRequest{ID: json.RawMessage(`1`), Method: "initialize", Params: params})
		got := res.Result.(map[string]any)["protocolVersion"]
		if got != want {
			t.Errorf("initialize(%q) = %v, want %v", requested, got, want)
		}
	}
}
