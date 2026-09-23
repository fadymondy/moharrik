package mcp

/*
OAuth 2.1 for the user MCP (MH-391).

claude.ai and ChatGPT connectors add a remote MCP server by URL and sign in
with OAuth; they cannot paste a bearer token. This file is the authorization
server they talk to, ported from fadymondy.com (FM-283) and implemented against
the MCP authorization specification:

  - protected-resource metadata (RFC 9728) at both well-known forms, and a
    WWW-Authenticate resource_metadata pointer on every 401 from /api/mcp;
  - authorization-server metadata (RFC 8414);
  - client registration by dynamic registration (RFC 7591) and by Client ID
    Metadata Document (what claude.ai offers; ChatGPT's also uses
    private_key_jwt — oauth_cimd.go, oauth_jwt.go);
  - authorization code + PKCE, S256 only, exact redirect-URI matching (https,
    or http on a loopback host per RFC 8252), the `iss` response parameter
    (RFC 9207) and `resource` binding (RFC 8707);
  - short-lived access tokens, rotating refresh tokens with reuse detection,
    and revocation (RFC 7009).

The multi-tenant difference from fadymondy.com: there, one owner approves.
Here any signed-in member approves on the consent page, and a grant is bound to
the ONE workspace they pick there and capped by their role in it — exactly
like an mcp_token (RoleMayHold at consent, Effective on every call). An access
token resolves to the same tenant.Scope a pasted token does, so the tools run
under the same authz and the same RLS, and cannot reach another workspace.
Membership is re-checked on every call and every refresh: a removed member's
connections die with the membership, a demoted one's scopes shrink.

Every secret — codes, access and refresh tokens, client secrets — is stored as
its sha256 (HashToken).
*/

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/tenant"
)

const (
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour
	oauthCodeTTL    = 2 * time.Minute
	// A grant approved on the consent page but never redeemed is swept after this.
	oauthPendingTTL = 10 * time.Minute

	accessPrefix  = "mho_at_"
	refreshPrefix = "mho_rt_"
	codePrefix    = "mho_ac_"
	clientPrefix  = "mhc_"
	secretPrefix  = "mhcs_"
)

/*
OAuthIssuer is this authorization server's issuer identifier: the public
origin, e.g. https://app.moharrik.com. OAUTH_ISSUER wins; APP_URL is the
fallback.

Configured, never taken from the request's Host header: the issuer is what a
client pins its registration to, and a value an attacker can steer is not an
identifier.
*/
func OAuthIssuer() string {
	for _, key := range []string{"OAUTH_ISSUER", "APP_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if i := strings.Index(v, " "); i > 0 {
				v = v[:i]
			}
			return strings.TrimRight(v, "/")
		}
	}
	return ""
}

// MCPResourceURL is the canonical URI of the user MCP server, which is also
// the audience every OAuth token is bound to.
func MCPResourceURL() string { return OAuthIssuer() + "/api/mcp" }

func protectedResourceMetadataURL() string {
	return OAuthIssuer() + "/.well-known/oauth-protected-resource/api/mcp"
}

type oauthServer struct {
	a   *app.App
	now func() time.Time
	// fetchCIMD fetches a Client ID Metadata Document. Replaced in tests; the
	// real one refuses anything but public https (oauth_cimd.go).
	fetchCIMD func(ctx context.Context, clientID string) ([]byte, error)
	// fetchJWKS fetches a private_key_jwt client's jwks_uri (same guard).
	fetchJWKS func(ctx context.Context, uri string) ([]byte, error)
	jwks      *jwksCache
	register  *ipLimiter
	tokenRate *ipLimiter
}

func newOAuthServer(a *app.App) *oauthServer {
	return &oauthServer{
		a:         a,
		now:       func() time.Time { return time.Now().UTC() },
		fetchCIMD: fetchClientMetadata,
		fetchJWKS: fetchClientMetadata,
		jwks:      newJWKSCache(),
		register:  newIPLimiter(20, time.Hour),
		tokenRate: newIPLimiter(120, time.Minute),
	}
}

// MountOAuth mounts the OAuth endpoints on the kernel router.
func MountOAuth(k *togo.Kernel, a *app.App) {
	newOAuthServer(a).mount(k.Router)
}

// OAuthPublicPaths are the endpoints anonymous callers reach by specification
// (the guard's public tier). The well-known documents are outside /api.
var OAuthPublicPaths = []string{"/api/oauth/register", "/api/oauth/token", "/api/oauth/revoke"}

// OAuthConsentPaths need a signed-in member but no workspace header: the member
// picks the workspace on the consent page (the guard's signed-in tier).
var OAuthConsentPaths = []string{"/api/oauth/authorize/request", "/api/oauth/authorize/decision"}

func (s *oauthServer) mount(r chi.Router) {
	r.Get("/.well-known/oauth-protected-resource", s.protectedResource)
	r.Get("/.well-known/oauth-protected-resource/api/mcp", s.protectedResource)
	r.Get("/.well-known/oauth-authorization-server", s.serverMetadata)
	// Some clients ask for the server metadata relative to the resource path.
	r.Get("/.well-known/oauth-authorization-server/api/mcp", s.serverMetadata)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/api/mcp",
		"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/api/mcp",
		"/api/oauth/register", "/api/oauth/token", "/api/oauth/revoke"} {
		r.Options(p, s.preflight)
	}
	r.Post("/api/oauth/register", s.registerClient)
	r.Post("/api/oauth/token", s.token)
	r.Post("/api/oauth/revoke", s.revoke)

	// The consent page's two calls: a signed-in member (the guard checks the
	// session and, for the POST, the CSRF token).
	r.Get("/api/oauth/authorize/request", s.authorizeRequest)
	r.Post("/api/oauth/authorize/decision", s.authorizeDecision)
}

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

func scopeNames() []string {
	names := make([]string, 0, len(UserScopes))
	for _, sc := range UserScopes {
		names = append(names, sc.Scope)
	}
	return names
}

func (s *oauthServer) protectedResource(w http.ResponseWriter, _ *http.Request) {
	allowAnyOrigin(w)
	writeJSONNoStore(w, http.StatusOK, map[string]any{
		"resource":                 MCPResourceURL(),
		"authorization_servers":    []string{OAuthIssuer()},
		"scopes_supported":         scopeNames(),
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Moharrik",
		"resource_documentation":   OAuthIssuer() + "/en/workspaces",
	})
}

func (s *oauthServer) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	allowAnyOrigin(w)
	iss := OAuthIssuer()
	methods := []string{"none", "client_secret_post", "client_secret_basic", "private_key_jwt"}
	writeJSONNoStore(w, http.StatusOK, map[string]any{
		"issuer": iss,
		// A web page, not an API route: the member signs in and consents there.
		"authorization_endpoint":                                iss + "/oauth/authorize",
		"token_endpoint":                                        iss + "/api/oauth/token",
		"registration_endpoint":                                 iss + "/api/oauth/register",
		"revocation_endpoint":                                   iss + "/api/oauth/revoke",
		"response_types_supported":                              []string{"code"},
		"response_modes_supported":                              []string{"query"},
		"grant_types_supported":                                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":                      []string{"S256"},
		"token_endpoint_auth_methods_supported":                 methods,
		"token_endpoint_auth_signing_alg_values_supported":      assertionAlgs,
		"revocation_endpoint_auth_methods_supported":            methods,
		"revocation_endpoint_auth_signing_alg_values_supported": assertionAlgs,
		"scopes_supported":                                      scopeNames(),
		"client_id_metadata_document_supported":                 true,
		"authorization_response_iss_parameter_supported":        true,
	})
}

// ---------------------------------------------------------------------------
// Clients
// ---------------------------------------------------------------------------

type oauthClient struct {
	ID           string
	Name         string
	URI          string
	RedirectURIs []string
	AuthMethod   string
	SecretHash   string
	JWKSURI      string
	JWKS         string
	// A metadata document client (client_id is an https URL).
	CIMD bool
}

var errUnknownClient = errors.New("unknown client")

// loadClient resolves a client_id. A URL-shaped id is a metadata document and
// is fetched fresh when `fetch` is true (the consent step); otherwise the copy
// stored at consent is used (the token step).
func (s *oauthServer) loadClient(ctx context.Context, clientID string, fetch bool) (*oauthClient, error) {
	if clientID == "" || len(clientID) > 2000 {
		return nil, errUnknownClient
	}
	if isCIMDClientID(clientID) && fetch {
		c, err := s.resolveCIMD(ctx, clientID)
		if err != nil {
			return nil, err
		}
		redirects, _ := json.Marshal(c.RedirectURIs)
		_, err = s.a.SysDB.ExecContext(ctx, `
			INSERT INTO oauth_clients (id, client_name, client_uri, redirect_uris, token_endpoint_auth_method, jwks_uri, jwks)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET client_name = EXCLUDED.client_name,
				client_uri = EXCLUDED.client_uri, redirect_uris = EXCLUDED.redirect_uris,
				token_endpoint_auth_method = EXCLUDED.token_endpoint_auth_method,
				jwks_uri = EXCLUDED.jwks_uri, jwks = EXCLUDED.jwks, updated_at = now()`,
			c.ID, c.Name, c.URI, string(redirects), c.AuthMethod, c.JWKSURI, c.JWKS)
		if err != nil {
			return nil, err
		}
		return c, nil
	}

	var c oauthClient
	var redirects string
	err := s.a.SysDB.QueryRowContext(ctx, `
		SELECT id, client_name, client_uri, redirect_uris, token_endpoint_auth_method, secret_hash, jwks_uri, jwks
		FROM oauth_clients WHERE id = $1`, clientID).
		Scan(&c.ID, &c.Name, &c.URI, &redirects, &c.AuthMethod, &c.SecretHash, &c.JWKSURI, &c.JWKS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errUnknownClient
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(redirects), &c.RedirectURIs)
	c.CIMD = isCIMDClientID(c.ID)
	return &c, nil
}

func (c *oauthClient) allowsRedirect(uri string) bool {
	for _, r := range c.RedirectURIs {
		// Exact string comparison — no normalisation, no prefix, no wildcard.
		if subtle.ConstantTimeCompare([]byte(r), []byte(uri)) == 1 {
			return true
		}
	}
	return false
}

/*
validRedirectURI is the registration-time rule: https, or http on a loopback
host (RFC 8252 section 7.3 — native apps listening on 127.0.0.1 or [::1], any
port). Absolute, no fragment, no credentials.
*/
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > 2000 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Fragment != "" || strings.Contains(raw, "#") || u.User != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return isLoopbackHost(u.Hostname())
	}
	return false
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && isLoopbackHost(u.Hostname())
}

func (s *oauthServer) registerClient(w http.ResponseWriter, r *http.Request) {
	allowAnyOrigin(w)
	if !s.register.allow(clientIPOf(r), s.now()) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many registrations from this address")
		return
	}
	var in struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		ClientURI               string   `json:"client_uri"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		Scope                   string   `json:"scope"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON object")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "between 1 and 10 redirect_uris are required")
		return
	}
	for _, u := range in.RedirectURIs {
		if !validRedirectURI(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, or http on a loopback host, with no fragment")
			return
		}
	}
	method := in.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	if method != "none" && method != "client_secret_post" && method != "client_secret_basic" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant_type "+g)
			return
		}
	}
	for _, rt := range in.ResponseTypes {
		if rt != "code" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported response_type "+rt)
			return
		}
	}
	if in.ClientURI != "" {
		if u, err := url.Parse(in.ClientURI); err != nil || u.Scheme != "https" || len(in.ClientURI) > 500 {
			in.ClientURI = ""
		}
	}

	id := clientPrefix + randomToken(18)
	secret, secretHash := "", ""
	if method != "none" {
		secret = secretPrefix + randomToken(32)
		secretHash = HashToken(secret)
	}
	name := oauthClip(strings.TrimSpace(in.ClientName), 120)
	redirects, _ := json.Marshal(in.RedirectURIs)
	if _, err := s.a.SysDB.ExecContext(r.Context(), `
		INSERT INTO oauth_clients (id, client_name, client_uri, redirect_uris, token_endpoint_auth_method, secret_hash, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, name, in.ClientURI, string(redirects), method, secretHash, oauthClip(clientIPOf(r), 64)); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}

	out := map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        s.now().Unix(),
		"client_name":                name,
		"redirect_uris":              in.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	}
	if in.ClientURI != "" {
		out["client_uri"] = in.ClientURI
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	writeJSONNoStore(w, http.StatusCreated, out)
}

// ---------------------------------------------------------------------------
// Authorization (consent)
// ---------------------------------------------------------------------------

type authzParams struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	State               string `json:"state"`
	Scope               string `json:"scope"`
	Resource            string `json:"resource"`
}

// authzProblem is a validation failure. Redirectable ones are sent back to the
// client; the others are shown on the page, because the redirect URI itself is
// not trusted yet.
type authzProblem struct {
	status       int
	code         string
	desc         string
	redirectable bool
}

var challengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierRE = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// validateScopes splits known from unknown, deduplicated, in UserScopes order.
func validateScopes(requested []string) ([]string, string) {
	want := map[string]bool{}
	for _, sc := range requested {
		if _, ok := userScope(sc); !ok {
			return nil, sc
		}
		want[sc] = true
	}
	out := []string{}
	for _, d := range UserScopes {
		if want[d.Scope] {
			out = append(out, d.Scope)
		}
	}
	return out, ""
}

func subsetOf(sub, of []string) bool {
	for _, s := range sub {
		if !hasScope(of, s) {
			return false
		}
	}
	return true
}

func intersectScopes(a, b []string) []string {
	out := []string{}
	for _, s := range a {
		if hasScope(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func (s *oauthServer) validateAuthz(ctx context.Context, p authzParams, fetch bool) (*oauthClient, []string, *authzProblem) {
	client, err := s.loadClient(ctx, p.ClientID, fetch)
	if err != nil {
		desc := "unknown client"
		if !errors.Is(err, errUnknownClient) {
			desc = "the client's metadata could not be used: " + err.Error()
		}
		return nil, nil, &authzProblem{http.StatusBadRequest, "invalid_client", desc, false}
	}
	if p.RedirectURI == "" || !client.allowsRedirect(p.RedirectURI) {
		return nil, nil, &authzProblem{http.StatusBadRequest, "invalid_request", "redirect_uri does not exactly match a registered URI", false}
	}
	// From here on the redirect URI is trusted, so errors go back to the client.
	if p.ResponseType != "code" {
		return client, nil, &authzProblem{http.StatusBadRequest, "unsupported_response_type", "only response_type=code is supported", true}
	}
	if p.CodeChallenge == "" {
		return client, nil, &authzProblem{http.StatusBadRequest, "invalid_request", "PKCE is required: send code_challenge with code_challenge_method=S256", true}
	}
	if p.CodeChallengeMethod != "S256" {
		return client, nil, &authzProblem{http.StatusBadRequest, "invalid_request", "code_challenge_method must be S256", true}
	}
	if !challengeRE.MatchString(p.CodeChallenge) {
		return client, nil, &authzProblem{http.StatusBadRequest, "invalid_request", "code_challenge is not a base64url SHA-256 digest", true}
	}
	if p.Resource != "" && p.Resource != MCPResourceURL() {
		return client, nil, &authzProblem{http.StatusBadRequest, "invalid_target", "this server only issues tokens for " + MCPResourceURL(), true}
	}
	requested := strings.Fields(p.Scope)
	if len(requested) == 0 {
		requested = scopeNames()
	}
	scopes, unknown := validateScopes(requested)
	if unknown != "" {
		return client, nil, &authzProblem{http.StatusBadRequest, "invalid_scope", "unknown scope " + unknown, true}
	}
	return client, scopes, nil
}

func paramsFromQuery(q url.Values) authzParams {
	return authzParams{
		ResponseType:        q.Get("response_type"),
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		State:               q.Get("state"),
		Scope:               q.Get("scope"),
		Resource:            q.Get("resource"),
	}
}

func redirectWith(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *oauthServer) errorRedirect(p authzParams, code, desc string) string {
	return redirectWith(p.RedirectURI, map[string]string{
		"error": code, "error_description": desc, "state": p.State, "iss": OAuthIssuer(),
	})
}

func signedInUser(w http.ResponseWriter, r *http.Request) (*auth.Identity, bool) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok || id == nil || id.ID == "" {
		writeJSONNoStore(w, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return nil, false
	}
	return id, true
}

// ConsentScope is one requested scope as the consent page shows it.
type ConsentScope struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
	MinRole     string `json:"min_role"`
}

// ConsentWorkspace is one of the member's workspaces and which of the
// requested scopes their role there lets them grant.
type ConsentWorkspace struct {
	ID      string   `json:"id"`
	Slug    string   `json:"slug"`
	Name    string   `json:"name"`
	Role    string   `json:"role"`
	Allowed []string `json:"allowed_scopes"`
}

func (s *oauthServer) memberWorkspaces(ctx context.Context, userID string, requested []string) ([]ConsentWorkspace, error) {
	rows, err := s.a.SysDB.QueryContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 AND m.status = 'active'
		ORDER BY lower(w.name), w.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConsentWorkspace{}
	for rows.Next() {
		var c ConsentWorkspace
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Role); err != nil {
			return nil, err
		}
		c.Allowed = []string{}
		for _, sc := range requested {
			if RoleMayHold(c.Role, sc) {
				c.Allowed = append(c.Allowed, sc)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// authorizeRequest validates an authorization request for the consent page and
// says what the member could grant, workspace by workspace.
func (s *oauthServer) authorizeRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := signedInUser(w, r)
	if !ok {
		return
	}
	p := paramsFromQuery(r.URL.Query())
	client, scopes, problem := s.validateAuthz(r.Context(), p, true)
	if problem != nil {
		body := map[string]any{"error": problem.code, "error_description": problem.desc}
		if problem.redirectable {
			body["redirect_to"] = s.errorRedirect(p, problem.code, problem.desc)
		}
		writeJSONNoStore(w, problem.status, body)
		return
	}
	wss, err := s.memberWorkspaces(r.Context(), id.ID, scopes)
	if err != nil {
		writeJSONNoStore(w, http.StatusInternalServerError, map[string]string{"error": "could not list your workspaces"})
		return
	}
	redirect, _ := url.Parse(p.RedirectURI)
	offered := []ConsentScope{}
	for _, sc := range scopes {
		d, _ := userScope(sc)
		offered = append(offered, ConsentScope{Scope: d.Scope, Description: d.Description, MinRole: string(d.MinRole)})
	}
	writeJSONNoStore(w, http.StatusOK, map[string]any{
		"client": map[string]any{
			"id":                client.ID,
			"name":              client.Name,
			"uri":               client.URI,
			"metadata_document": client.CIMD,
		},
		"redirect_uri":       p.RedirectURI,
		"redirect_host":      redirect.Host,
		"loopback_redirect":  isLoopbackRedirect(p.RedirectURI),
		"requested_scopes":   offered,
		"workspaces":         wss,
		"resource":           MCPResourceURL(),
		"access_ttl_seconds": int(oauthAccessTTL.Seconds()),
	})
}

func (s *oauthServer) authorizeDecision(w http.ResponseWriter, r *http.Request) {
	id, ok := signedInUser(w, r)
	if !ok {
		return
	}
	var in struct {
		authzParams
		Approve   bool     `json:"approve"`
		Workspace string   `json:"workspace"`
		Granted   []string `json:"granted_scopes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in) != nil {
		writeJSONNoStore(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	p := in.authzParams
	ctx := r.Context()
	// Re-validated in full: the page is not trusted to have shown what it was sent.
	client, requested, problem := s.validateAuthz(ctx, p, false)
	if problem != nil {
		body := map[string]any{"error": problem.code, "error_description": problem.desc}
		if problem.redirectable {
			body["redirect_to"] = s.errorRedirect(p, problem.code, problem.desc)
		}
		writeJSONNoStore(w, problem.status, body)
		return
	}
	if !in.Approve {
		writeJSONNoStore(w, http.StatusOK, map[string]string{
			"redirect_to": s.errorRedirect(p, "access_denied", "the member declined the request"),
		})
		return
	}
	// The workspace: one the member is in, as they are in it today.
	scope, err := tenant.Resolve(ctx, s.a.SysDB, id.ID, in.Workspace)
	if err != nil {
		writeJSONNoStore(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if !authz.Can(scope.Role, authz.TokensManageOwn) {
		writeJSONNoStore(w, http.StatusForbidden, map[string]string{"error": "your role cannot connect apps to this workspace"})
		return
	}
	granted, unknown := validateScopes(in.Granted)
	if unknown != "" || len(granted) == 0 || !subsetOf(granted, requested) {
		// Consent may narrow, never widen.
		writeJSONNoStore(w, http.StatusBadRequest, map[string]string{"error": "choose at least one of the requested scopes"})
		return
	}
	for _, sc := range granted {
		if !RoleMayHold(scope.Role, sc) {
			writeJSONNoStore(w, http.StatusForbidden, map[string]string{"error": "your role cannot grant " + sc})
			return
		}
	}

	// The grant is the workspace's: written through the app pool under the
	// member's scope, so RLS checks the workspace and the audit log records
	// the member connecting the app (MH-392).
	scope.Via = "session"
	tenant.RequestFrom(ctx).SetOperation("oauth-approve")
	wctx := tenant.With(ctx, scope)
	var grantID string
	if err := s.a.SQLDB.QueryRowContext(wctx, `
		INSERT INTO oauth_grants (workspace_id, user_id, client_id, client_name, client_uri, scopes, resource)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		scope.WorkspaceID, id.ID, client.ID, client.Name, client.URI, strings.Join(granted, " "), MCPResourceURL()).Scan(&grantID); err != nil {
		writeJSONNoStore(w, http.StatusInternalServerError, map[string]string{"error": "could not record the connection"})
		return
	}
	code := codePrefix + randomToken(32)
	if _, err := s.a.SysDB.ExecContext(ctx, `
		INSERT INTO oauth_codes (code_hash, grant_id, client_id, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		HashToken(code), grantID, client.ID, p.RedirectURI, p.CodeChallenge, s.now().Add(oauthCodeTTL)); err != nil {
		writeJSONNoStore(w, http.StatusInternalServerError, map[string]string{"error": "could not issue a code"})
		return
	}
	s.sweep(ctx)
	writeJSONNoStore(w, http.StatusOK, map[string]any{
		"redirect_to": redirectWith(p.RedirectURI, map[string]string{"code": code, "state": p.State, "iss": OAuthIssuer()}),
		"workspace":   map[string]string{"id": scope.WorkspaceID, "slug": scope.WorkspaceSlug, "name": scope.WorkspaceName},
	})
}

// sweep removes what can no longer be used: expired codes, grants approved but
// never redeemed, spent assertions, expired tokens. Best effort.
func (s *oauthServer) sweep(ctx context.Context) {
	now := s.now()
	c := context.WithoutCancel(ctx)
	_, _ = s.a.SysDB.ExecContext(c, `DELETE FROM oauth_codes WHERE expires_at < $1`, now.Add(-time.Hour))
	_, _ = s.a.SysDB.ExecContext(c, `DELETE FROM oauth_grants WHERE activated_at IS NULL AND created_at < $1`, now.Add(-oauthPendingTTL))
	_, _ = s.a.SysDB.ExecContext(c, `DELETE FROM oauth_tokens WHERE expires_at < $1`, now.Add(-24*time.Hour))
	_, _ = s.a.SysDB.ExecContext(c, `DELETE FROM oauth_client_assertions WHERE expires_at < $1`, now)
}

// ---------------------------------------------------------------------------
// Token endpoint
// ---------------------------------------------------------------------------

// authenticateClient applies the client's registered token endpoint auth.
func (s *oauthServer) authenticateClient(r *http.Request) (*oauthClient, bool) {
	clientID := r.PostForm.Get("client_id")
	secret := r.PostForm.Get("client_secret")
	basic := false
	if u, p, ok := r.BasicAuth(); ok {
		if uu, err := url.QueryUnescape(u); err == nil {
			u = uu
		}
		if pp, err := url.QueryUnescape(p); err == nil {
			p = pp
		}
		if clientID != "" && clientID != u {
			return nil, false
		}
		clientID, secret, basic = u, p, true
	}
	assertionType := r.PostForm.Get("client_assertion_type")
	assertion := r.PostForm.Get("client_assertion")
	if assertion != "" && clientID == "" && !basic {
		// RFC 7523 lets client_id be omitted; the assertion's sub names the
		// client, verified below against the client it names.
		clientID = assertionSubject(assertion)
	}
	client, err := s.loadClient(r.Context(), clientID, false)
	if err != nil {
		return nil, false
	}
	switch client.AuthMethod {
	case "none":
		return client, true
	case "private_key_jwt":
		if basic || secret != "" || assertionType != clientAssertionType || assertion == "" {
			return nil, false
		}
		if err := s.verifyClientAssertion(r.Context(), client, assertion); err != nil {
			if s.a != nil && s.a.Log != nil {
				s.a.Log.Warn("oauth client assertion refused", "client_id", client.ID, "reason", err.Error())
			}
			return nil, false
		}
		return client, true
	case "client_secret_post", "client_secret_basic":
		if client.AuthMethod == "client_secret_basic" && !basic {
			return nil, false
		}
		if client.AuthMethod == "client_secret_post" && basic {
			return nil, false
		}
		if secret == "" || subtle.ConstantTimeCompare([]byte(HashToken(secret)), []byte(client.SecretHash)) != 1 {
			return nil, false
		}
		return client, true
	}
	return nil, false
}

func (s *oauthServer) token(w http.ResponseWriter, r *http.Request) {
	allowAnyOrigin(w)
	if !s.tokenRate.allow(clientIPOf(r), s.now()) {
		oauthError(w, http.StatusTooManyRequests, "slow_down", "too many requests")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	client, ok := s.authenticateClient(r)
	if !ok {
		s.logRefusedClient(r)
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	if res := r.PostForm.Get("resource"); res != "" && res != MCPResourceURL() {
		oauthError(w, http.StatusBadRequest, "invalid_target", "this server only issues tokens for "+MCPResourceURL())
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.redeemCode(w, r, client)
	case "refresh_token":
		s.redeemRefresh(w, r, client)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

type grantRow struct {
	id, workspaceID, userID, clientID, scopes, resource string
	activated, revoked                                  *time.Time
}

func (s *oauthServer) grant(ctx context.Context, id string) (*grantRow, error) {
	var g grantRow
	err := s.a.SysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, client_id, scopes, resource, activated_at, revoked_at
		FROM oauth_grants WHERE id = $1`, id).
		Scan(&g.id, &g.workspaceID, &g.userID, &g.clientID, &g.scopes, &g.resource, &g.activated, &g.revoked)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *oauthServer) revokeGrantByID(ctx context.Context, id string) {
	c := context.WithoutCancel(ctx)
	_, _ = s.a.SysDB.ExecContext(c, `UPDATE oauth_grants SET revoked_at = now(), updated_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	_, _ = s.a.SysDB.ExecContext(c, `DELETE FROM oauth_tokens WHERE grant_id = $1`, id)
}

func (s *oauthServer) redeemCode(w http.ResponseWriter, r *http.Request, client *oauthClient) {
	ctx := r.Context()
	code := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	redirectURI := r.PostForm.Get("redirect_uri")
	if code == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}
	if verifier == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code_verifier is required (PKCE)")
		return
	}

	// Consumed atomically before anything else is checked: a code is spent by
	// its first presentation, right or wrong.
	var grantID, clientID, storedRedirect, challenge string
	var expires time.Time
	err := s.a.SysDB.QueryRowContext(ctx, `
		UPDATE oauth_codes SET used_at = $2
		WHERE code_hash = $1 AND used_at IS NULL
		RETURNING grant_id, client_id, redirect_uri, code_challenge, expires_at`,
		HashToken(code), s.now()).Scan(&grantID, &clientID, &storedRedirect, &challenge, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		// A replayed code: whatever the first redemption produced is now
		// suspect, so it goes (RFC 6749 section 4.1.2).
		var spent string
		if s.a.SysDB.QueryRowContext(ctx, `SELECT grant_id FROM oauth_codes WHERE code_hash = $1`, HashToken(code)).Scan(&spent) == nil {
			s.revokeGrantByID(ctx, spent)
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is invalid or has already been used")
		return
	}
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not redeem the code")
		return
	}
	switch {
	case !s.now().Before(expires):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code has expired")
		return
	case clientID != client.ID:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another client")
		return
	case redirectURI != storedRedirect:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	case !verifierRE.MatchString(verifier) || !pkceMatches(verifier, challenge):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match the code_challenge")
		return
	}
	g, err := s.grant(ctx, grantID)
	if err != nil || g.revoked != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the connection no longer exists")
		return
	}
	scopes, ok := s.stillAllowed(ctx, g)
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the approving member is no longer in that workspace")
		return
	}
	if _, err := s.a.SysDB.ExecContext(ctx, `UPDATE oauth_grants SET activated_at = $2, updated_at = now() WHERE id = $1`, grantID, s.now()); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not activate the connection")
		return
	}
	s.issueTokens(w, r, grantID, scopes)
}

// stillAllowed re-reads the member's standing in the grant's workspace: the
// session that consented may be long gone; what matters is whether the member
// could grant these scopes today. Returns the grant's scopes narrowed by the
// current role.
func (s *oauthServer) stillAllowed(ctx context.Context, g *grantRow) ([]string, bool) {
	scope, err := tenant.Resolve(ctx, s.a.SysDB, g.userID, g.workspaceID)
	if err != nil {
		return nil, false
	}
	return Effective(scope.Role, strings.Fields(g.scopes)), true
}

func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func (s *oauthServer) redeemRefresh(w http.ResponseWriter, r *http.Request, client *oauthClient) {
	ctx := r.Context()
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	var grantID, tokenScopes string
	var expires time.Time
	err := s.a.SysDB.QueryRowContext(ctx, `
		UPDATE oauth_tokens SET used_at = $2
		WHERE token_hash = $1 AND kind = 'refresh' AND used_at IS NULL
		RETURNING grant_id, scopes, expires_at`, HashToken(refresh), s.now()).
		Scan(&grantID, &tokenScopes, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		// Rotation means a refresh token is good once. Seeing a spent one again
		// means two parties hold it, so the whole connection is revoked.
		var spent string
		if s.a.SysDB.QueryRowContext(ctx, `SELECT grant_id FROM oauth_tokens WHERE token_hash = $1 AND kind = 'refresh'`,
			HashToken(refresh)).Scan(&spent) == nil {
			s.revokeGrantByID(ctx, spent)
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is invalid or has already been used")
		return
	}
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not redeem the refresh token")
		return
	}
	g, err := s.grant(ctx, grantID)
	switch {
	case err != nil:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the connection no longer exists")
		return
	case g.revoked != nil:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the connection has been revoked")
		return
	case g.clientID != client.ID:
		// Another client holding this client's refresh token means it leaked:
		// the connection goes, as for reuse.
		s.revokeGrantByID(ctx, g.id)
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token was issued to another client")
		return
	case !s.now().Before(expires):
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token has expired")
		return
	}
	allowed, ok := s.stillAllowed(ctx, g)
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the approving member is no longer in that workspace")
		return
	}
	scopes := intersectScopes(strings.Fields(tokenScopes), allowed)
	if requested := strings.Fields(r.PostForm.Get("scope")); len(requested) > 0 {
		if !subsetOf(requested, scopes) {
			oauthError(w, http.StatusBadRequest, "invalid_scope", "a refresh cannot widen the approved scopes")
			return
		}
		scopes = requested
	}
	// The spent refresh token's access tokens stay good until they expire (an
	// hour); the new pair replaces them.
	s.issueTokens(w, r, grantID, scopes)
}

func (s *oauthServer) issueTokens(w http.ResponseWriter, r *http.Request, grantID string, scopes []string) {
	access := accessPrefix + randomToken(32)
	refresh := refreshPrefix + randomToken(32)
	now := s.now()
	joined := strings.Join(scopes, " ")
	_, err := s.a.SysDB.ExecContext(r.Context(), `
		INSERT INTO oauth_tokens (token_hash, grant_id, kind, scopes, expires_at) VALUES
		($1, $3, 'access', $4, $5), ($2, $3, 'refresh', $4, $6)`,
		HashToken(access), HashToken(refresh), grantID, joined,
		now.Add(oauthAccessTTL), now.Add(oauthRefreshTTL))
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue tokens")
		return
	}
	writeJSONNoStore(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         joined,
	})
}

// revoke implements RFC 7009. It always answers 200 for a well-formed request,
// whether or not the token existed, so it reveals nothing. Revoking a refresh
// token ends the whole connection; revoking an access token ends that token.
func (s *oauthServer) revoke(w http.ResponseWriter, r *http.Request) {
	allowAnyOrigin(w)
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "form body required")
		return
	}
	client, ok := s.authenticateClient(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	token := r.PostForm.Get("token")
	if token == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	ctx := r.Context()
	var kind, grantID string
	err := s.a.SysDB.QueryRowContext(ctx, `
		SELECT t.kind, t.grant_id FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
		WHERE t.token_hash = $1 AND g.client_id = $2`, HashToken(token), client.ID).Scan(&kind, &grantID)
	if err == nil {
		if kind == "refresh" {
			s.revokeGrantByID(ctx, grantID)
		} else {
			_, _ = s.a.SysDB.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE token_hash = $1`, HashToken(token))
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Resource-server side: /api/mcp
// ---------------------------------------------------------------------------

/*
authOAuth resolves an OAuth access token presented to /api/mcp to the same
tenant.Scope a pasted token gets: the grant's ONE workspace, its member, their
CURRENT role there, and the approved scopes narrowed by that role. Re-checked
on every call: the token's expiry, the grant's revocation, its audience, and
the membership.
*/
func authOAuth(r *http.Request, a *app.App, token string) (context.Context, []string, bool) {
	if a == nil || a.SysDB == nil || !strings.HasPrefix(token, accessPrefix) {
		return nil, nil, false
	}
	ctx := r.Context()
	var grantID, workspaceID, userID, grantScopes, tokenScopes, resource, clientName, clientID string
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT g.id, g.workspace_id, g.user_id, g.scopes, t.scopes, g.resource, g.client_name, g.client_id
		FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
		WHERE t.token_hash = $1 AND t.kind = 'access' AND t.expires_at > $2
		  AND g.revoked_at IS NULL AND g.activated_at IS NOT NULL`,
		HashToken(token), time.Now().UTC()).
		Scan(&grantID, &workspaceID, &userID, &grantScopes, &tokenScopes, &resource, &clientName, &clientID)
	if err != nil {
		return nil, nil, false
	}
	// Audience: a token is only good for the resource it was issued for.
	if resource != MCPResourceURL() {
		return nil, nil, false
	}
	s, err := tenant.Resolve(ctx, a.SysDB, userID, workspaceID)
	if err != nil {
		return nil, nil, false
	}
	name := clientName
	if name == "" {
		name = clientID
	}
	s.Via = "oauth"
	s.TokenID, s.TokenName = grantID, name
	s.Scopes = Effective(s.Role, intersectScopes(strings.Fields(tokenScopes), strings.Fields(grantScopes)))
	_, _ = a.SysDB.ExecContext(ctx, `UPDATE oauth_grants SET last_used_at = now() WHERE id = $1`, grantID)
	return tenant.With(ctx, s), s.Scopes, true
}

// authUserOrOAuth is the user MCP's authenticator: a pasted mhr_ token, or an
// OAuth access token. Existing token access is unchanged.
func authUserOrOAuth(r *http.Request, a *app.App, token string) (context.Context, []string, bool) {
	if strings.HasPrefix(token, accessPrefix) {
		return authOAuth(r, a, token)
	}
	return authUser(r, a, token)
}

// bearerChallenge is the WWW-Authenticate value for a 401 from /api/mcp: it
// points the client at the protected-resource metadata (RFC 9728 section 5.1),
// which is how claude.ai and ChatGPT discover that they can sign in.
func bearerChallenge(presented bool) string {
	v := `Bearer realm="moharrik-mcp", resource_metadata="` + protectedResourceMetadataURL() + `"`
	if presented {
		v += `, error="invalid_token", error_description="the access token is invalid, expired or revoked"`
	}
	return v
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func oauthClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func clientIPOf(r *http.Request) string {
	if ip := clientip.From(r.Context()); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func writeJSONNoStore(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSONNoStore(w, status, map[string]string{"error": code, "error_description": desc})
}

// allowAnyOrigin: the public OAuth endpoints use no cookies, so a wildcard
// origin grants a browser-based client nothing a server could not already do.
func allowAnyOrigin(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func (s *oauthServer) preflight(w http.ResponseWriter, _ *http.Request) {
	allowAnyOrigin(w)
	w.WriteHeader(http.StatusNoContent)
}

// ipLimiter is a fixed-window counter per address, in memory.
type ipLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	seen   map[string]*ipWindow
}

type ipWindow struct {
	start time.Time
	n     int
}

func newIPLimiter(max int, window time.Duration) *ipLimiter {
	return &ipLimiter{max: max, window: window, seen: map[string]*ipWindow{}}
}

func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) > 10000 {
		for k, v := range l.seen {
			if now.Sub(v.start) > l.window {
				delete(l.seen, k)
			}
		}
	}
	w := l.seen[ip]
	if w == nil || now.Sub(w.start) > l.window {
		l.seen[ip] = &ipWindow{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.max
}

// logRefusedClient records the shape of a token request whose client could not
// be authenticated: which fields were present, never their values.
func (s *oauthServer) logRefusedClient(r *http.Request) {
	if s.a == nil || s.a.Log == nil {
		return
	}
	basicUser, _, hasBasic := r.BasicAuth()
	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.a.Log.Warn("oauth client authentication refused",
		"client_id", r.PostForm.Get("client_id"),
		"has_basic", hasBasic,
		"basic_user", basicUser,
		"has_secret", r.PostForm.Get("client_secret") != "",
		"assertion_type", r.PostForm.Get("client_assertion_type"),
		"has_assertion", r.PostForm.Get("client_assertion") != "",
		"grant_type", r.PostForm.Get("grant_type"),
		"form_keys", strings.Join(keys, ","))
}
