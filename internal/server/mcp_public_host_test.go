package server

/*
MH-516: mcp.moharrik.com — the dedicated MCP host, against the real stack.

mcp.MountPublicHost wires its middleware once, at Boot(): whether it exists
at all is decided by MCP_PUBLIC_URL's value AT THAT MOMENT, unlike
OAUTH_ISSUER (bootAudit sets that AFTER Boot(), which works because it is
read lazily per request). So this file gets its own boot helper that sets
MCP_PUBLIC_URL before calling Boot().

The dedicated host is a virtual host on the SAME httptest listener: requests
set req.Host instead of dialing anywhere new — exactly how a client
distinguishes app.moharrik.com from mcp.moharrik.com behind one reverse proxy.
*/

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/testdb"
)

const (
	testMCPHost      = "mcp.example.test"
	testMCPPublicURL = "https://" + testMCPHost
)

// bootPublicHostMCP is bootAudit (audit_test.go) plus MCP_PUBLIC_URL, set
// before Boot() so the dedicated host's router is actually mounted.
func bootPublicHostMCP(t *testing.T) (*app.App, string) {
	t.Helper()
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "public-host-test-secret-0123456789abcdef")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	t.Setenv("SNOOZE_WAKER", "off")
	t.Setenv("EVOLUTION_API_URL", "http://127.0.0.1:9")
	t.Setenv("APP_URL", "http://127.0.0.1")
	t.Setenv("MCP_PUBLIC_URL", testMCPPublicURL)
	a := Boot()
	t.Cleanup(a.Kernel.Close)
	srv := newTestServer(t, a)
	t.Setenv("APP_URL", srv)
	return a, srv
}

// getHost is a GET with an optional Host override, decoded as JSON.
func getHost(t *testing.T, base, host, path string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// rpcAt is mcpWith (oauth_test.go) generalised over the path and Host: mcpWith
// is always /api/mcp on the default host, this hits whichever the test needs.
func rpcAt(t *testing.T, base, host, path, token, tool string) (int, string, bool, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]any{}}})
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &out)
	text := ""
	if len(out.Result.Content) > 0 {
		text = out.Result.Content[0].Text
	}
	return res.StatusCode, text, out.Result.IsError, res.Header.Get("WWW-Authenticate")
}

func TestMCPPublicHostRouting(t *testing.T) {
	_, base := bootPublicHostMCP(t)

	alice := newClient(t, base)
	aliceEmail := "ph-alice-" + rnd() + "@mail.example"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "PH A " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID
	pasted := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "pasted", "scopes": []string{"members:read"}}, 201)["plaintext"].(string)

	// "/" on the dedicated host is the same transport as /api/mcp: a pasted
	// token (no audience restriction) works exactly as it does there.
	if code, text, isErr, _ := rpcAt(t, base, testMCPHost, "/", pasted, "whoami"); code != 200 || isErr || !strings.Contains(text, wsID) {
		t.Errorf("root path on the dedicated host: %d %v %q", code, isErr, text)
	}
	// GET / is a 405, same as /api/mcp today — proof it reached the transport
	// (which has no SSE stream and nothing to answer GET with) and not a
	// blanket 404.
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.Host = testMCPHost
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET / on the dedicated host: %d, want 405", res.StatusCode)
	}

	// Everything else on this host is a 404 — not the legacy MCP path, not
	// REST, not GraphQL, not the workspace tier's 401, not the SPA.
	for _, path := range []string{"/api/mcp", "/api/health", "/graphql", "/en/workspaces", "/api/w/contacts", "/anything"} {
		if code, _ := getHost(t, base, testMCPHost, path); code != http.StatusNotFound {
			t.Errorf("%s on the dedicated host: %d, want 404", path, code)
		}
	}
	// The same paths on the app's own host are untouched.
	if code, _ := getHost(t, base, "", "/api/health"); code != http.StatusOK {
		t.Errorf("/api/health on the app host: %d, want 200", code)
	}
	// The legacy path still works, unaffected, on the app's own host.
	if code, text, isErr, _ := rpcAt(t, base, "", "/api/mcp", pasted, "whoami"); code != 200 || isErr || !strings.Contains(text, wsID) {
		t.Errorf("legacy /api/mcp on the app host: %d %v %q", code, isErr, text)
	}
}

func TestMCPPublicHostMetadata(t *testing.T) {
	_, base := bootPublicHostMCP(t)

	// The app host's metadata is unchanged: still the legacy resource.
	if code, prm := getHost(t, base, "", "/.well-known/oauth-protected-resource"); code != 200 ||
		prm["resource"] != base+"/api/mcp" || prm["authorization_servers"].([]any)[0] != base {
		t.Errorf("app host metadata: %d %v", code, prm)
	}

	// The dedicated host's metadata names ITSELF as the resource, the same
	// authorization server, and answers both the unsuffixed and
	// trailing-slash forms a client may try for a root-path resource.
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/"} {
		code, prm := getHost(t, base, testMCPHost, p)
		if code != 200 || prm["resource"] != testMCPPublicURL || prm["authorization_servers"].([]any)[0] != base {
			t.Errorf("dedicated host metadata %s: %d %v", p, code, prm)
		}
	}

	// A 401 from the dedicated host points at ITS OWN metadata, never the
	// legacy one — otherwise a client would discover the wrong resource and
	// mint a token that could never work here.
	if _, _, _, www := rpcAt(t, base, testMCPHost, "/", "", "whoami"); !strings.Contains(www, `resource_metadata="`+testMCPPublicURL+`/.well-known/oauth-protected-resource"`) {
		t.Errorf("401 on the dedicated host must point at its own metadata: %q", www)
	}
	// ...and a 401 from the app host still points at the legacy one.
	if _, _, _, www := rpcAt(t, base, "", "/api/mcp", "", "whoami"); !strings.Contains(www, `resource_metadata="`+base+`/.well-known/oauth-protected-resource/api/mcp"`) {
		t.Errorf("401 on the app host must still point at the legacy metadata: %q", www)
	}
}

func TestMCPPublicHostOAuth(t *testing.T) {
	a, base := bootPublicHostMCP(t)

	alice := newClient(t, base)
	aliceEmail := "ph-oa-alice-" + rnd() + "@mail.example"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "PH OA " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)

	cl := register(t, base, map[string]any{"client_name": "Dedicated host", "redirect_uris": []string{"https://client.example/cb"}})

	// ---- the full flow against the new resource ----------------------------
	p := cl.params("members:read")
	p["resource"] = testMCPPublicURL
	consent := alice.must("GET", "/api/oauth/authorize/request?"+query(p), nil, 200)
	if consent["resource"] != testMCPPublicURL {
		t.Errorf("consent must show the resource the client asked for: %v", consent)
	}
	code := approve(t, alice, p, wsID, []string{"members:read"})
	_, tok := cl.exchange(code)
	access, _ := tok["access_token"].(string), tok["refresh_token"].(string)
	if access == "" {
		t.Fatalf("token exchange: %v", tok)
	}

	// The token works at the dedicated host's root...
	if code, text, isErr, _ := rpcAt(t, base, testMCPHost, "/", access, "whoami"); code != 200 || isErr || !strings.Contains(text, wsID) {
		t.Errorf("a token minted for the dedicated host, used there: %d %v %q", code, isErr, text)
	}
	// ...but is refused at the legacy path: two audiences, never one, even
	// though both reach the same tools.
	if code, _, _, _ := rpcAt(t, base, "", "/api/mcp", access, "whoami"); code != http.StatusUnauthorized {
		t.Errorf("a token minted for the dedicated host must be refused at the legacy path: %d", code)
	}

	// ---- and the other direction --------------------------------------------
	legacyP := cl.params("members:read") // defaults to base+"/api/mcp"
	legacyCode := approve(t, alice, legacyP, wsID, []string{"members:read"})
	_, legacyTok := cl.exchange(legacyCode)
	legacyAccess, _ := legacyTok["access_token"].(string)
	if code, _, _, _ := rpcAt(t, base, testMCPHost, "/", legacyAccess, "whoami"); code != http.StatusUnauthorized {
		t.Errorf("a token minted for the legacy resource must be refused at the dedicated host: %d", code)
	}
	if code, text, isErr, _ := rpcAt(t, base, "", "/api/mcp", legacyAccess, "whoami"); code != 200 || isErr || !strings.Contains(text, wsID) {
		t.Errorf("the legacy token still works at the legacy path: %d %v %q", code, isErr, text)
	}

	// ---- wrong-resource refusal ----------------------------------------------
	badP := cl.params("members:read")
	badP["resource"] = "https://not-accepted.example/api/mcp"
	if code, out := alice.do("GET", "/api/oauth/authorize/request?"+query(badP), nil); code != http.StatusBadRequest {
		t.Errorf("an unaccepted resource at authorize/request: %d %s", code, out)
	}
	if code, out := postForm(t, base, "/api/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {"whatever"}, "redirect_uri": {cl.redirect},
		"client_id": {cl.id}, "code_verifier": {cl.verifier}, "resource": {"https://not-accepted.example/api/mcp"},
	}); code != http.StatusBadRequest || out["error"] != "invalid_target" {
		t.Errorf("an unaccepted resource at the token endpoint: %d %v", code, out)
	}

	// ---- Settings shows the dedicated host, and connects new tokens to it ---
	alice.ws = wsID
	list := alice.must("GET", "/api/w/oauth_grants", nil, 200)
	if list["server_url"] != testMCPPublicURL {
		t.Errorf("Settings must show the dedicated host once MCP_PUBLIC_URL names one: %v", list)
	}

	_ = a
}
