package mcp

/*
MH-516: mcp.moharrik.com — the same user MCP server, reachable at its own
root path with no /api/mcp suffix, so a connector's "add server by URL" step
is just the domain.

MCP_PUBLIC_URL names that host. Unset (the default), MountPublicHost does
nothing: MCPResourceURL (APP_URL + /api/mcp) stays the only audience and
/api/mcp the only entry point — exactly as before this card, and exactly what
local dev and every existing environment gets until Hosbah sets it.

Set, a second http.Handler answers "/" on that host and its own
protected-resource metadata is served there too. The two entry points share
one tool registry and one scope model, but deliberately NOT one OAuth
audience — see authOAuthForResource's doc comment in oauth.go for why treating
them as interchangeable would be the unsafe choice here.
*/

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/togo-framework/ratelimit"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/research"
)

// MountPublicHost installs the MCP_PUBLIC_URL host's router. It MUST be
// wired ahead of the request guard — server.Boot() calls it before
// installGuard — so that on this host, any path that is not the MCP
// transport or its protected-resource metadata is a plain 404 rather than
// whatever the guard's tier for that path would otherwise answer (a 401 from
// the workspace tier, the proxied SPA at "/", and so on). Everything below
// runs entirely outside chi's router: nothing is registered at path "/" on
// k.Router itself, so the app host's own "/" (the SPA) is untouched.
func MountPublicHost(k *togo.Kernel, a *app.App, researchEngine *research.Engine) {
	publicURL := MCPPublicURL()
	if publicURL == "" {
		return
	}
	host := hostOf(publicURL)
	if host == "" {
		k.Log.Warn("MCP_PUBLIC_URL is set but names no host — the dedicated MCP host is not mounted", "value", publicURL)
		return
	}
	if hostMatches(host, hostOf(OAuthIssuer())) {
		// Same host as the app itself: nothing to route BY, and mounting this
		// would turn "/" on the whole site into the MCP transport and 404 the
		// rest. Misconfiguration, not a second host — refuse quietly rather
		// than take the site down.
		k.Log.Warn("MCP_PUBLIC_URL has the same host as APP_URL/OAUTH_ISSUER — the dedicated MCP host is not mounted", "value", publicURL)
		return
	}

	publicUser := &server{
		name:      "moharrik-mcp",
		tools:     UserTools(researchEngine),
		auth:      authUserOrOAuthPublicHost,
		challenge: publicHostBearerChallenge,
	}
	wrap := func(h http.Handler) http.Handler { return h }
	if rl, ok := ratelimit.FromKernel(k); ok {
		wrap = rl.Middleware(ratelimit.Rate("mcp-public-host", 120, time.Minute), func(r *http.Request) string {
			// Per token (hashed), falling back to the client address — same
			// key shape as /api/mcp's limiter (server.go), just its own bucket.
			if t := bearer(r); t != "" {
				return "mcp:" + HashToken(t)[:16]
			}
			return "mcp-ip:" + r.RemoteAddr
		})
	}
	transport := wrap(publicUser.handler(a))

	k.UseMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hostMatches(r.Host, host) {
				next.ServeHTTP(w, r)
				return
			}
			switch r.URL.Path {
			case "/":
				// POST is the JSON-RPC transport; the handler itself answers
				// 405 for GET/DELETE, exactly as /api/mcp does today — this
				// server has no SSE stream and no session to terminate.
				transport.ServeHTTP(w, r)
			case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/":
				if r.Method == http.MethodOptions {
					allowAnyOrigin(w)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				writePublicHostProtectedResource(w, r)
			default:
				// Everything else on this host is a 404 — never the workspace
				// tier's 401, never the SPA, never REST or GraphQL.
				writeJSONNoStore(w, http.StatusNotFound, map[string]string{"error": "not found"})
			}
		})
	})
	k.Log.Info("mcp: dedicated host mounted", "host", host, "resource", publicURL)
}

// hostOf is the host[:port] of a URL, lower-cased; "" if raw does not parse
// or names no host.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// hostWithoutPort strips a trailing ":port" so a request that arrives with
// one (the local *.localhost proof, e.g. mcp.localhost:8519) still matches a
// configured value that has none, and vice versa.
func hostWithoutPort(h string) string {
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h, "]") {
		return h[:i]
	}
	return h
}

func hostMatches(reqHost, want string) bool {
	reqHost, want = strings.ToLower(reqHost), strings.ToLower(want)
	return reqHost == want || hostWithoutPort(reqHost) == hostWithoutPort(want)
}

// publicHostBearerChallenge is the WWW-Authenticate value on a 401 from the
// dedicated host's root path: it must point AT THAT HOST's metadata, never
// the legacy one (bearerChallenge, oauth.go), or an OAuth client would
// discover the wrong resource and mint a token that can never work here.
func publicHostBearerChallenge(presented bool) string {
	v := `Bearer realm="moharrik-mcp", resource_metadata="` + MCPPublicURL() + `/.well-known/oauth-protected-resource"`
	if presented {
		v += `, error="invalid_token", error_description="the access token is invalid, expired or revoked"`
	}
	return v
}

func writePublicHostProtectedResource(w http.ResponseWriter, _ *http.Request) {
	allowAnyOrigin(w)
	writeJSONNoStore(w, http.StatusOK, protectedResourceBody(MCPPublicURL()))
}

// authOAuthPublicHost/authUserOrOAuthPublicHost mirror authOAuth /
// authUserOrOAuth (oauth.go) but demand the dedicated host's own audience.
// A pasted mhr_ token (authUser) carries no resource restriction and is
// unaffected either way — the same token works at both hosts.
func authOAuthPublicHost(r *http.Request, a *app.App, token string) (context.Context, []string, bool) {
	return authOAuthForResource(r, a, token, MCPPublicURL())
}

func authUserOrOAuthPublicHost(r *http.Request, a *app.App, token string) (context.Context, []string, bool) {
	if strings.HasPrefix(token, accessPrefix) {
		return authOAuthPublicHost(r, a, token)
	}
	return authUser(r, a, token)
}
