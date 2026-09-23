package server

/*
OAuth for MCP (MH-391): conformance against the real stack.

A connector (claude.ai, ChatGPT) and a member are both played here, over HTTP,
exactly as web/e2e/oauth-mcp-check.mjs does against a running server: the
discovery documents and the 401 that points at them, dynamic registration and
its redirect-URI rules, PKCE (S256 only), exact redirect matching, single-use
codes, rotating refresh tokens with reuse detection, revocation, the audience
check — and the multi-tenant rules: a grant is bound to ONE workspace, capped
by the member's role there, narrowed when they are demoted and dead when they
leave, and can never reach another workspace.
*/

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/mcp"
)

type oauthClientT struct {
	t        *testing.T
	base     string
	id       string
	secret   string
	redirect string
	verifier string
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func postForm(t *testing.T, base, path string, form url.Values, basic ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		req.SetBasicAuth(basic[0], basic[1])
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

func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func register(t *testing.T, base string, body map[string]any) *oauthClientT {
	t.Helper()
	code, out := postJSON(t, base+"/api/oauth/register", body)
	if code != http.StatusCreated {
		t.Fatalf("register: %d %v", code, out)
	}
	c := &oauthClientT{t: t, base: base, id: out["client_id"].(string), redirect: body["redirect_uris"].([]string)[0],
		verifier: b64u([]byte("a-long-enough-code-verifier-for-pkce-" + rnd() + rnd() + rnd()))}
	c.secret, _ = out["client_secret"].(string)
	return c
}

func (c *oauthClientT) challenge() string {
	sum := sha256.Sum256([]byte(c.verifier))
	return b64u(sum[:])
}

func (c *oauthClientT) params(scope string) map[string]string {
	return map[string]string{
		"response_type": "code", "client_id": c.id, "redirect_uri": c.redirect,
		"code_challenge": c.challenge(), "code_challenge_method": "S256",
		"state": "st-" + rnd(), "scope": scope, "resource": c.base + "/api/mcp",
	}
}

func query(p map[string]string) string {
	v := url.Values{}
	for k, s := range p {
		v.Set(k, s)
	}
	return v.Encode()
}

// approve runs the consent decision as the member and returns the code.
func approve(t *testing.T, member *client, p map[string]string, workspace string, granted []string) string {
	t.Helper()
	body := map[string]any{"approve": true, "workspace": workspace, "granted_scopes": granted}
	for k, v := range p {
		body[k] = v
	}
	out := member.must("POST", "/api/oauth/authorize/decision", body, 200)
	u, _ := url.Parse(out["redirect_to"].(string))
	if u.Query().Get("state") != p["state"] || u.Query().Get("iss") == "" {
		t.Fatalf("the redirect lacks state or iss: %s", u)
	}
	return u.Query().Get("code")
}

func (c *oauthClientT) exchange(code string) (int, map[string]any) {
	return postForm(c.t, c.base, "/api/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.redirect},
		"client_id": {c.id}, "code_verifier": {c.verifier},
	})
}

func (c *oauthClientT) refresh(rt string, extra ...string) (int, map[string]any) {
	f := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {c.id}}
	for i := 0; i+1 < len(extra); i += 2 {
		f.Set(extra[i], extra[i+1])
	}
	return postForm(c.t, c.base, "/api/oauth/token", f)
}

func mcpWith(t *testing.T, base, token, tool string) (int, string, bool, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": map[string]any{}}})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/mcp", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
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

func TestOAuthConformance(t *testing.T) {
	a, base := bootAudit(t)
	t.Setenv("OAUTH_ISSUER", base)

	// ---- discovery ---------------------------------------------------------
	code, _, _, www := mcpWith(t, base, "", "whoami")
	if code != http.StatusUnauthorized || !strings.Contains(www, `resource_metadata="`+base+`/.well-known/oauth-protected-resource/api/mcp"`) {
		t.Fatalf("401 from /api/mcp must point at the resource metadata: %d %q", code, www)
	}
	if _, _, _, www := mcpWith(t, base, "mho_at_bogus", "whoami"); !strings.Contains(www, `error="invalid_token"`) {
		t.Errorf("a presented, bad token gets error=invalid_token: %q", www)
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/api/mcp"} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		var prm map[string]any
		_ = json.NewDecoder(res.Body).Decode(&prm)
		res.Body.Close()
		if prm["resource"] != base+"/api/mcp" || prm["authorization_servers"].([]any)[0] != base {
			t.Errorf("%s: %v", p, prm)
		}
	}
	res, _ := http.Get(base + "/.well-known/oauth-authorization-server")
	var asm map[string]any
	_ = json.NewDecoder(res.Body).Decode(&asm)
	res.Body.Close()
	if asm["issuer"] != base || asm["authorization_endpoint"] != base+"/oauth/authorize" || asm["token_endpoint"] != base+"/api/oauth/token" ||
		asm["registration_endpoint"] != base+"/api/oauth/register" || asm["revocation_endpoint"] != base+"/api/oauth/revoke" {
		t.Errorf("server metadata endpoints: %v", asm)
	}
	if m := asm["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Errorf("PKCE methods must be S256 only: %v", m)
	}

	// ---- dynamic registration: redirect URI rules -------------------------------
	for _, bad := range []string{"http://app.example/cb", "https://app.example/cb#frag", "https://user:pw@app.example/cb", "javascript:alert(1)", "/relative"} {
		if code, _ := postJSON(t, base+"/api/oauth/register", map[string]any{"client_name": "x", "redirect_uris": []string{bad}}); code != 400 {
			t.Errorf("DCR accepted redirect %q (%d)", bad, code)
		}
	}
	for _, good := range []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1:6274/cb", "http://localhost:33418/cb", "http://[::1]:9/cb"} {
		if code, out := postJSON(t, base+"/api/oauth/register", map[string]any{"client_name": "x", "redirect_uris": []string{good}}); code != 201 {
			t.Errorf("DCR refused redirect %q: %d %v", good, code, out)
		}
	}
	if code, _ := postJSON(t, base+"/api/oauth/register", map[string]any{"client_name": "x", "redirect_uris": []string{"https://a.example/cb"}, "grant_types": []string{"client_credentials"}}); code != 400 {
		t.Error("DCR accepted an unsupported grant type")
	}
	cl := register(t, base, map[string]any{"client_name": "Conformance", "redirect_uris": []string{"https://client.example/cb"}})
	if !strings.HasPrefix(cl.id, "mhc_") || cl.secret != "" {
		t.Fatalf("public client: %q secret=%q", cl.id, cl.secret)
	}

	// ---- two members, three workspaces ----------------------------------------
	alice := newClient(t, base)
	aliceEmail := "oa-alice-" + rnd() + "@mail.example"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	wsA := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "OA A " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	wsB := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "OA B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsB
	markerB := "OnlyInB" + rnd()
	alice.must("POST", "/api/w/contacts", map[string]any{"email": strings.ToLower(markerB) + "@mail.example", "name": markerB}, 201)
	alice.ws = ""
	// Carol owns C; alice is a plain member there.
	carol := newClient(t, base)
	carol.must("POST", "/api/auth/register", map[string]string{"email": "oa-carol-" + rnd() + "@mail.example", "password": "correct-horse-2"}, 201)
	wsC := carol.must("POST", "/api/me/workspaces", map[string]string{"name": "OA C " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	var aliceID string
	_ = a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, aliceEmail).Scan(&aliceID)
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'member', 'active')`, wsC, aliceID); err != nil {
		t.Fatal(err)
	}

	// ---- the authorization request ------------------------------------------------
	p := cl.params("members:read contacts:read contacts:write audit:read")
	check := func(name string, mut map[string]string, wantStatus int, wantRedirect bool) {
		t.Helper()
		q := map[string]string{}
		for k, v := range p {
			q[k] = v
		}
		for k, v := range mut {
			q[k] = v
		}
		code, b := alice.do("GET", "/api/oauth/authorize/request?"+query(q), nil)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		_, redirects := out["redirect_to"]
		if code != wantStatus || redirects != wantRedirect {
			t.Errorf("%s: got %d redirect=%v, want %d redirect=%v: %s", name, code, redirects, wantStatus, wantRedirect, b)
		}
	}
	check("plain PKCE", map[string]string{"code_challenge_method": "plain"}, 400, true)
	check("no PKCE", map[string]string{"code_challenge": ""}, 400, true)
	check("not a S256 digest", map[string]string{"code_challenge": "short"}, 400, true)
	check("redirect not an exact match", map[string]string{"redirect_uri": "https://client.example/cb/"}, 400, false)
	check("redirect of another host", map[string]string{"redirect_uri": "https://evil.example/cb"}, 400, false)
	check("unknown client", map[string]string{"client_id": "mhc_nope"}, 400, false)
	check("unknown scope", map[string]string{"scope": "members:read admin:everything"}, 400, true)
	check("another resource", map[string]string{"resource": "https://other.example/api/mcp"}, 400, true)
	check("response_type token", map[string]string{"response_type": "token"}, 400, true)
	if code, _ := newClient(t, base).do("GET", "/api/oauth/authorize/request?"+query(p), nil); code != http.StatusUnauthorized {
		t.Errorf("the consent request needs a session: %d", code)
	}

	consent := alice.must("GET", "/api/oauth/authorize/request?"+query(p), nil, 200)
	byID := map[string][]any{}
	for _, w := range consent["workspaces"].([]any) {
		m := w.(map[string]any)
		byID[m["id"].(string)] = m["allowed_scopes"].([]any)
	}
	if len(byID) != 3 || len(byID[wsA]) != 4 || len(byID[wsC]) != 3 {
		t.Errorf("the consent request caps scopes by role per workspace: %v", byID)
	}

	// A member cannot grant what their role does not hold; a CSRF-less POST is refused.
	body := map[string]any{"approve": true, "workspace": wsC, "granted_scopes": []string{"audit:read"}}
	for k, v := range p {
		body[k] = v
	}
	if code, _ := alice.do("POST", "/api/oauth/authorize/decision", body); code != http.StatusForbidden {
		t.Errorf("a member granting audit:read in C: %d, want 403", code)
	}
	body["workspace"] = "not-mine"
	body["granted_scopes"] = []string{"members:read"}
	if code, _ := alice.do("POST", "/api/oauth/authorize/decision", body); code != http.StatusNotFound {
		t.Errorf("approving into a workspace one is not in: %d, want 404", code)
	}
	body["workspace"] = wsA
	body["granted_scopes"] = []string{"inbox:read"}
	if code, _ := alice.do("POST", "/api/oauth/authorize/decision", body); code != http.StatusBadRequest {
		t.Errorf("granting a scope that was not requested: %d, want 400", code)
	}
	noCSRF := newClient(t, base)
	noCSRF.http.Jar = alice.http.Jar
	noCSRF.csrf = "wrong"
	if code, _ := noCSRF.do("POST", "/api/oauth/authorize/decision", body); code != http.StatusForbidden {
		t.Errorf("a decision without the CSRF token: %d, want 403", code)
	}

	// ---- code → tokens --------------------------------------------------------------
	granted := []string{"members:read", "contacts:read", "contacts:write", "audit:read"}
	c1 := approve(t, alice, p, wsA, granted)
	if code, out := postForm(t, base, "/api/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {c1}, "redirect_uri": {"https://client.example/other"}, "client_id": {cl.id}, "code_verifier": {cl.verifier}}); code != 400 || out["error"] != "invalid_grant" {
		t.Errorf("a different redirect_uri at the token endpoint: %d %v", code, out)
	}
	c2 := approve(t, alice, p, wsA, granted)
	other := register(t, base, map[string]any{"client_name": "Other", "redirect_uris": []string{"https://client.example/cb"}})
	if code, out := postForm(t, base, "/api/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {c2}, "redirect_uri": {cl.redirect}, "client_id": {other.id}, "code_verifier": {cl.verifier}}); code != 400 || out["error"] != "invalid_grant" {
		t.Errorf("a code redeemed by another client: %d %v", code, out)
	}
	c3 := approve(t, alice, p, wsA, granted)
	code, tok := cl.exchange(c3)
	if code != 200 || tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 {
		t.Fatalf("token: %d %v", code, tok)
	}
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	// Replaying the code: refused, and the connection it made is revoked.
	if code, out := cl.exchange(c3); code != 400 || out["error"] != "invalid_grant" {
		t.Errorf("a replayed code: %d %v", code, out)
	}
	if code, _, _, _ := mcpWith(t, base, access, "whoami"); code != http.StatusUnauthorized {
		t.Errorf("a replayed code must revoke what the first redemption issued: %d", code)
	}

	c4 := approve(t, alice, p, wsA, granted)
	_, tok = cl.exchange(c4)
	access, refresh = tok["access_token"].(string), tok["refresh_token"].(string)
	var stored int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE token_hash IN ($1, $2)`, mcp.HashToken(access), mcp.HashToken(refresh)).Scan(&stored)
	var plainStored int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE token_hash IN ($1, $2)`, access, refresh).Scan(&plainStored)
	if stored != 2 || plainStored != 0 {
		t.Errorf("tokens must be stored hashed: hashed=%d plain=%d", stored, plainStored)
	}

	// ---- the token acts in A, and only A ------------------------------------------------
	_, who, isErr, _ := mcpWith(t, base, access, "whoami")
	if isErr || !strings.Contains(who, wsA) || strings.Contains(who, wsB) || !strings.Contains(who, "role: owner") {
		t.Errorf("whoami: %q", who)
	}
	// X-Workspace-ID cannot move it.
	req, _ := http.NewRequest(http.MethodPost, base+"/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_contacts","arguments":{"q":"`+markerB+`"}}}`))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("X-Workspace-ID", wsB)
	r2, _ := http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if strings.Contains(string(raw), markerB) {
		t.Error("an OAuth token bound to A read B's contact")
	}
	// The token is not a session: the REST API does not take it.
	restReq, _ := http.NewRequest(http.MethodGet, base+"/api/w/contacts", nil)
	restReq.Header.Set("Authorization", "Bearer "+access)
	restReq.Header.Set("X-Workspace-ID", wsA)
	if r3, _ := http.DefaultClient.Do(restReq); r3.StatusCode == 200 {
		t.Error("an MCP OAuth token opened the REST API")
	}

	// ---- refresh: rotation, no widening, reuse revokes -----------------------------------
	code, r1 := cl.refresh(refresh)
	if code != 200 || r1["refresh_token"] == refresh {
		t.Fatalf("refresh: %d %v", code, r1)
	}
	if code, out := other.refresh(r1["refresh_token"].(string)); code != 400 || out["error"] != "invalid_grant" {
		t.Errorf("another client's refresh: %d %v", code, out)
	}
	// A refresh token presented by another client has leaked: the connection is revoked.
	if code, _, _, _ := mcpWith(t, base, r1["access_token"].(string), "whoami"); code != http.StatusUnauthorized {
		t.Errorf("reuse of a refresh token must revoke the connection: %d", code)
	}

	c5 := approve(t, alice, p, wsA, granted)
	_, tok = cl.exchange(c5)
	code, r1 = cl.refresh(tok["refresh_token"].(string), "scope", "inbox:read")
	if code != 400 || r1["error"] != "invalid_scope" {
		t.Errorf("a widening refresh: %d %v", code, r1)
	}
	c6 := approve(t, alice, p, wsA, granted)
	_, tok = cl.exchange(c6)
	code, r1 = cl.refresh(tok["refresh_token"].(string), "scope", "members:read")
	if code != 200 || r1["scope"] != "members:read" {
		t.Errorf("a narrowing refresh: %d %v", code, r1)
	}

	// ---- the member's role caps the grant, now ----------------------------------------------
	pc := cl.params("members:read contacts:read")
	c7 := approve(t, alice, pc, wsC, []string{"members:read", "contacts:read"})
	_, tokC := cl.exchange(c7)
	accessC := tokC["access_token"].(string)
	if _, who, _, _ := mcpWith(t, base, accessC, "whoami"); !strings.Contains(who, wsC) || !strings.Contains(who, "contacts:read") {
		t.Errorf("the grant in C: %q", who)
	}
	_, _ = a.SysDB.Exec(`UPDATE workspace_members SET role = 'client' WHERE workspace_id = $1 AND user_id = $2`, wsC, aliceID)
	if _, text, isErr, _ := mcpWith(t, base, accessC, "whoami"); !isErr || !strings.Contains(text, "members:read") {
		t.Errorf("a demoted member's grant must shrink with them: %v %q", isErr, text)
	}
	_, _ = a.SysDB.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, wsC, aliceID)
	if code, _, _, _ := mcpWith(t, base, accessC, "whoami"); code != http.StatusUnauthorized {
		t.Errorf("a removed member's connection must die with the membership: %d", code)
	}
	if code, out := cl.refresh(tokC["refresh_token"].(string)); code != 400 || out["error"] != "invalid_grant" {
		t.Errorf("…and cannot be refreshed: %d %v", code, out)
	}

	// ---- audience ---------------------------------------------------------------------------
	c8 := approve(t, alice, p, wsA, granted)
	_, tok8 := cl.exchange(c8)
	_, _ = a.SysDB.Exec(`UPDATE oauth_grants SET resource = 'https://elsewhere.example/api/mcp' WHERE id = (SELECT grant_id FROM oauth_tokens WHERE token_hash = $1)`, mcp.HashToken(tok8["access_token"].(string)))
	if code, _, _, _ := mcpWith(t, base, tok8["access_token"].(string), "whoami"); code != http.StatusUnauthorized {
		t.Errorf("a token for another audience: %d", code)
	}

	// ---- revocation: RFC 7009 and Settings --------------------------------------------------
	c9 := approve(t, alice, p, wsA, granted)
	_, tok9 := cl.exchange(c9)
	if code, _ := postForm(t, base, "/api/oauth/revoke", url.Values{"token": {tok9["access_token"].(string)}, "client_id": {cl.id}}); code != 200 {
		t.Errorf("revoke an access token: %d", code)
	}
	if code, _, _, _ := mcpWith(t, base, tok9["access_token"].(string), "whoami"); code != http.StatusUnauthorized {
		t.Error("a revoked access token still works")
	}
	if code, _ := postForm(t, base, "/api/oauth/revoke", url.Values{"token": {"mho_rt_unknown"}, "client_id": {cl.id}}); code != 200 {
		t.Error("revoking an unknown token must answer 200 and reveal nothing")
	}
	c10 := approve(t, alice, p, wsA, granted)
	_, tok10 := cl.exchange(c10)
	alice.ws = wsA
	list := alice.must("GET", "/api/w/oauth_grants", nil, 200)
	var live string
	for _, g := range list["grants"].([]any) {
		m := g.(map[string]any)
		if m["revoked_at"] == nil && m["client_name"] == "Conformance" {
			live = m["id"].(string) // newest first: the connection tok10 belongs to
			break
		}
	}
	if live == "" || list["server_url"] != base+"/api/mcp" {
		t.Fatalf("Settings lists the live connection and the server URL: %v", list)
	}
	alice.ws = wsB
	if code, _ := alice.do("DELETE", "/api/w/oauth_grants/"+live, nil); code != http.StatusNotFound {
		t.Errorf("revoking A's grant from B: %d, want 404", code)
	}
	alice.ws = wsA
	alice.must("DELETE", "/api/w/oauth_grants/"+live, nil, 204)
	if code, _, _, _ := mcpWith(t, base, tok10["access_token"].(string), "whoami"); code != http.StatusUnauthorized {
		t.Error("revoking in Settings must cut the connector off")
	}
	if code, _ := cl.refresh(tok10["refresh_token"].(string)); code != 400 {
		t.Error("…and its refresh token")
	}

	// ---- a confidential client (client_secret_basic) -----------------------------------------
	conf := register(t, base, map[string]any{"client_name": "Confidential", "redirect_uris": []string{"https://conf.example/cb"}, "token_endpoint_auth_method": "client_secret_basic"})
	if !strings.HasPrefix(conf.secret, "mhcs_") {
		t.Fatalf("a confidential client gets a secret: %q", conf.secret)
	}
	cc := approve(t, alice, conf.params("members:read"), wsA, []string{"members:read"})
	f := url.Values{"grant_type": {"authorization_code"}, "code": {cc}, "redirect_uri": {conf.redirect}, "code_verifier": {conf.verifier}}
	if code, _ := postForm(t, base, "/api/oauth/token", f, conf.id, "wrong-secret"); code != http.StatusUnauthorized {
		t.Errorf("a wrong client secret: %d", code)
	}
	cc = approve(t, alice, conf.params("members:read"), wsA, []string{"members:read"})
	f.Set("code", cc)
	if code, out := postForm(t, base, "/api/oauth/token", f, conf.id, conf.secret); code != 200 {
		t.Errorf("client_secret_basic: %d %v", code, out)
	}

	// ---- Deny --------------------------------------------------------------------------------
	deny := map[string]any{"approve": false}
	for k, v := range p {
		deny[k] = v
	}
	out := alice.must("POST", "/api/oauth/authorize/decision", deny, 200)
	if u, _ := url.Parse(out["redirect_to"].(string)); u.Query().Get("error") != "access_denied" {
		t.Errorf("Deny: %v", out)
	}

	// ---- pasted tokens still work -------------------------------------------------------------
	alice.ws = wsA
	pasted := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "pasted", "scopes": []string{"members:read"}}, 201)["plaintext"].(string)
	if code, text, isErr, _ := mcpWith(t, base, pasted, "whoami"); code != 200 || isErr || !strings.Contains(text, wsA) {
		t.Errorf("a pasted mhr_ token: %d %v %q", code, isErr, text)
	}
}
