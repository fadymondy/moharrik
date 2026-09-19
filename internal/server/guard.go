package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The request guard: one default-deny middleware around the whole router,
including routes plugins mount (auth, auth-oauth).

Tiers, decided by path before any handler runs:

	blocked    — plugin routes Moharrik does not offer; answered 404
	public     — anonymous; enumerated below
	signed-in  — any session; enumerated below (account-level, no workspace)
	workspace  — EVERYTHING else under /api and /graphql: session + membership
	             of X-Workspace-ID (404 otherwise) + authz for generated CRUD

The workspace tier is the default, so a newly generated resource is closed
behind membership AND authz.Generated (itself default-deny) until someone opens
it deliberately. That is fadymondy.com's FM-63/FM-43 lesson applied from day one.
*/

type tier int

const (
	tierWorkspace tier = iota
	tierPublic
	tierSignedIn
	tierBlocked
)

func classify(r *http.Request) tier {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") && path != "/graphql" {
		return tierPublic // pages, docs, assets
	}

	for _, p := range []string{
		// auth-dev hands an admin session to an empty body (fadymondy.com FM-42).
		// The plugin is not installed; this stays as a tripwire.
		"/api/auth/dev/",
		// The auth plugin's personal access tokens: a second token system whose
		// "abilities" nothing here would check. MCP tokens are the one token system.
		"/api/auth/tokens",
		// OTP sign-in: no delivery channel is wired in P0.
		"/api/auth/otp",
		// Magic links — the auth-mfa vulnerability class (fadymondy.com FM-81).
		"/api/auth/magic",
	} {
		if strings.HasPrefix(path, p) {
			return tierBlocked
		}
	}

	switch path {
	case "/api/health",
		// Both MCP transports authenticate with their own bearer tokens. Exact
		// matches: a prefix "/api/mcp" would also match /api/mcp_tokens.
		"/api/mcp",
		"/api/admin/mcp",
		// Double opt-in and unsubscribe links from an email: the random token in
		// the body is the authorization (internal/marketing).
		"/api/mail/confirm",
		"/api/mail/unsubscribe":
		return tierPublic
	case "/api/invitations/accept":
		return tierSignedIn
	}
	if strings.HasPrefix(path, "/api/auth/") {
		return tierPublic // the auth plugin guards its own routes
	}
	if strings.HasPrefix(path, "/api/me/") {
		return tierSignedIn
	}
	// The invite preview: the unguessable token in the URL is the authorization.
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/invitations/") &&
		!strings.Contains(strings.TrimPrefix(path, "/api/invitations/"), "/") {
		return tierPublic
	}
	return tierWorkspace
}

func installGuard(k *togo.Kernel, a *app.App) {
	svc, ok := auth.FromKernel(k)
	if !ok {
		k.Log.Error("auth service unavailable — refusing to start with an unguarded API")
		panic("guard: auth service not registered")
	}

	k.UseMiddleware(func(next http.Handler) http.Handler {
		signedIn := svc.Middleware(csrf(next))
		workspace := svc.Middleware(csrf(tenant.Require(a.SysDB)(generatedAuthz(next))))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch classify(r) {
			case tierBlocked:
				writeError(w, http.StatusNotFound, "not found")
			case tierPublic:
				next.ServeHTTP(w, r)
			case tierSignedIn:
				signedIn.ServeHTTP(w, r)
			default:
				workspace.ServeHTTP(w, r)
			}
		})
	})
}

// csrf applies the auth plugin's double-submit rule to Moharrik's own routes:
// a cookie-authenticated unsafe request must echo the togo_csrf cookie in
// X-CSRF-Token. Bearer requests carry no ambient credential and are exempt.
func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("togo_csrf")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || header == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// generatedAuthz runs after membership is established. Hand-written /api/w/*
// handlers check authz.Can themselves; everything else in the workspace tier
// is generated CRUD (or GraphQL) and is checked against authz.Generated.
func generatedAuthz(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := tenant.From(r.Context())
		path := r.URL.Path

		if path == "/graphql" {
			if !authz.Can(s.Role, authz.GraphQL) {
				writeError(w, http.StatusForbidden, "GraphQL needs the admin role in this workspace")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/api/w/") {
			next.ServeHTTP(w, r)
			return
		}

		table := strings.SplitN(strings.TrimPrefix(path, "/api/"), "/", 2)[0]
		if _, known := authz.Generated[table]; !known {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if !authz.CanTable(s.Role, table, r.Method) {
			writeError(w, http.StatusForbidden, "your role cannot do this through the generic API")
			return
		}
		next.ServeHTTP(w, r)
	})
}
