package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/circlexo"
	"github.com/fadymondy/moharrik/internal/presence"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The request guard: one default-deny middleware around the whole router,
including routes plugins mount (auth, auth-oauth).

Tiers, decided by path before any handler runs:

	blocked    — plugin routes Moharrik does not offer; answered 404
	public     — anonymous; enumerated below
	signed-in  — any session; enumerated below (account-level, no workspace)
	admin      — /api/admin/* (except the admin MCP): session + platform admin,
	             404 otherwise, never 401 (MH-1184)
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
	tierAdmin
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
		// The MCP tool reference (MH-428): public docs, the same names and
		// schemas tools/list hands any token, and no workspace data.
		"/api/mcp/docs",
		// The permission explainer (MH-1069): the same registry, summarised per scope.
		"/api/mcp/permissions",
		// Double opt-in and unsubscribe links from an email: the random token in
		// the body is the authorization (internal/marketing).
		"/api/mail/confirm",
		"/api/mail/unsubscribe",
		// CircleXO: the public config the login page reads, and the hub's signed
		// server-to-server calls (authenticated by their signature, internal/circlexo).
		// The /auth/circlexo and /sso/circlexo routes are outside /api: public already.
		circlexo.ConfigPath, circlexo.EventsPath, circlexo.ProvisionPath, circlexo.DeprovisionPath:
		return tierPublic
	case "/api/invitations/accept":
		return tierSignedIn
	// OAuth for MCP (MH-391). Registration, the token endpoint and revocation
	// are anonymous by specification: a client authenticates to them itself
	// (PKCE, its secret or its signed assertion), and nothing they hand out
	// exists until a member consented. The consent page's two calls need a
	// session but no X-Workspace-ID — the member picks the workspace there,
	// and the handler checks their membership and role in the one they picked.
	case "/api/oauth/register", "/api/oauth/token", "/api/oauth/revoke":
		return tierPublic
	case "/api/oauth/authorize/request", "/api/oauth/authorize/decision":
		return tierSignedIn
	// Google's redirect back after a workspace connects an account (MH-439).
	// No X-Workspace-ID: the workspace comes from the single-use state row.
	// But not public: the callback must come back to the SAME signed-in user
	// who started the connect, so it needs the session this tier checks. The
	// session cookie is SameSite=Lax, so Google's top-level redirect carries it.
	case "/api/google/oauth/callback":
		return tierSignedIn
	// The browser extension's build info and zip (MH-399). The same file for
	// every workspace, fetched by a plain link that cannot carry
	// X-Workspace-ID — and nothing in it is secret: a browser is useless until
	// an admin of a workspace approves its pairing.
	case "/api/automation/extension", "/api/automation/extension/download":
		return tierSignedIn
	// How the extension updates itself (MH-483): Chrome's updater reads the
	// update manifest and fetches the signed package with no session, and the
	// extension asks which version is current before it has connected. The
	// same files for everyone, nothing secret in them.
	case "/api/automation/extension/updates.xml", "/api/automation/extension/moharrik.crx", "/api/automation/extension/latest":
		return tierPublic
	}
	// A webhook delivery: no session, no X-Workspace-ID — the id in the path
	// plus its HMAC signature over the raw body are the authorization
	// (internal/notifications). Same shape as the mail links above, just a
	// prefix instead of two exact paths because the source id varies.
	if strings.HasPrefix(path, "/api/hooks/") {
		return tierPublic
	}
	// A visitor writing to (or polling) an embedded chat widget: no session, no
	// X-Workspace-ID — the widget key in the path plus the Origin allow-list are
	// the authorization (internal/inbox).
	if strings.HasPrefix(path, "/api/chat/") {
		return tierPublic
	}
	// Downloading an email attachment (MH-556): no session, no X-Workspace-ID —
	// the attachment id plus its signed, time-limited exp/sig query params ARE
	// the authorization (internal/inbox/attachment.go), the same shape as
	// /api/hooks/ and /api/chat/ above. It has to sit outside the workspace
	// tier: the sandboxed email iframe that loads it as <img src> carries no
	// cookies or headers of its own.
	if strings.HasPrefix(path, "/api/attachments/") {
		return tierPublic
	}
	// Downloading an uploaded contact avatar (MH-602): same shape as
	// /api/attachments/ just above — the id plus its signed exp/sig query
	// params are the authorization, needed because a plain <img src> in the
	// inbox or contact page carries no cookies or headers of its own either.
	if strings.HasPrefix(path, "/api/contact-avatars/") {
		return tierPublic
	}
	// Downloading a WhatsApp-cached provider photo (MH-602 defect 2): the same
	// signed-URL shape as /api/contact-avatars/ just above, and for the same
	// reason — a bare <img src> in the inbox list/detail carries no cookies or
	// headers, so the ws/exp/sig query params (crm.verifyProviderPhotoURL) are
	// the whole authorization. Forgetting this line was caught by the manual
	// browser check: the handler's own signature check never even ran, because
	// the workspace tier's session requirement rejected the request first.
	if strings.HasPrefix(path, "/api/provider-photos/") {
		return tierPublic
	}
	// A visitor typing their address into an embedded subscribe form: no
	// session, no X-Workspace-ID — the form key in the path plus the Origin
	// allow-list are the authorization (internal/marketing/subscribe_routes.go),
	// exactly as for the chat widget above. Without this the endpoint sits in
	// the workspace tier and no website can reach it at all.
	if strings.HasPrefix(path, "/api/subscribe/") {
		return tierPublic
	}
	// A visitor sending an inquiry from an embedded contact form (MH-390): the
	// same shape as /api/subscribe/ above — the form key plus the Origin
	// allow-list are the authorization (marketing.ResolvePublicForm), and the
	// lead is filed inside the workspace the key resolves to.
	if strings.HasPrefix(path, "/api/inquiry/") {
		return tierPublic
	}
	// The browser socket and the two extension halves of connecting by
	// approval (MH-478): no session and no X-Workspace-ID, because the
	// extension has neither yet. The socket authenticates with a token in its
	// FIRST FRAME (a browser WebSocket cannot set headers); connect/start can
	// only create a REQUEST that does nothing until a member approves it on the
	// site; connect/poll hands out nothing without the poll token that only the
	// requesting extension holds. Same shape as /api/hooks/ and /api/chat/.
	if path == "/api/agents/ws" || path == "/api/agents/connect/start" || path == "/api/agents/connect/poll" {
		return tierPublic
	}
	// The member's half of connecting — seeing the request, approving it into a
	// workspace, refusing it — needs a session but no X-Workspace-ID: the
	// member CHOOSES the workspace on that page, and the handler checks their
	// membership and role in the one they chose (automation/connect.go).
	if strings.HasPrefix(path, "/api/agents/connect/") {
		return tierSignedIn
	}
	// A WhatsApp delivery from the Evolution server: no session and no
	// X-Workspace-ID. The unguessable id in the path plus the token it sends
	// back in a header are the authorization (internal/whatsapp/routes.go) —
	// the same shape as /api/hooks/ and /api/chat/.
	if strings.HasPrefix(path, "/api/whatsapp/hook/") {
		return tierPublic
	}
	// Telegram, Slack and Discord deliveries (MH-563): the same shape as the
	// WhatsApp hook above — no session, no X-Workspace-ID. Each resolves its
	// own workspace from the unguessable webhook_id in the path, and its own
	// signature/token check is the rest of the authorization: Telegram's
	// secret_token header, Slack's request signature, Discord's Ed25519
	// signature over the interaction body.
	if strings.HasPrefix(path, "/api/telegram/hook/") ||
		strings.HasPrefix(path, "/api/slack/hook/") ||
		strings.HasPrefix(path, "/api/discord/hook/") {
		return tierPublic
	}
	// The superadmin panel (MH-1184). /api/admin/mcp is matched exactly above
	// and keeps its own token auth.
	if strings.HasPrefix(path, "/api/admin/") {
		return tierAdmin
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
		signedIn := svc.Middleware(hubSessionEnded(accountGate(a, csrf(next))))
		workspace := svc.Middleware(hubSessionEnded(accountGate(a, csrf(tenant.Require(a.SysDB)(stampPresence(generatedAuthz(a, next)))))))
		admin := notFoundOn401(svc.Middleware(hubSessionEnded(accountGate(a, requirePlatformAdmin(a, csrf(next))))))
		public := refuseSuspendedLogin(a, next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch classify(r) {
			case tierBlocked:
				writeError(w, http.StatusNotFound, "not found")
			case tierPublic:
				public.ServeHTTP(w, r)
			case tierSignedIn:
				signedIn.ServeHTTP(w, r)
			case tierAdmin:
				admin.ServeHTTP(w, r)
			default:
				workspace.ServeHTTP(w, r)
			}
		})
	})
}

// stampPresence records that someone who can answer the inbox is using this
// workspace, which is what the chat widget's online/away dot reads (MH-382,
// internal/presence). It runs after tenant.Require, so the workspace is one
// the caller is a member of. A role that cannot reply is not "the team being
// around" as far as a visitor is concerned, and does not stamp.
func stampPresence(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s, ok := tenant.From(r.Context()); ok && authz.Can(s.Role, authz.InboxWrite) {
			presence.Touch(s.WorkspaceID)
		}
		next.ServeHTTP(w, r)
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
//
// A write it refuses is recorded in the audit log (MH-392): a member trying to
// change what their role does not allow is exactly what the log is for, and the
// refusal never reaches the handlers that would otherwise record it.
func generatedAuthz(a *app.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, _ := tenant.From(r.Context())
		path := r.URL.Path
		refused := func(status int, msg string) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				// The table, not the raw path: the path may carry an id from
				// anywhere, and the log records only what is this workspace's.
				table := strings.SplitN(strings.TrimPrefix(path, "/api/"), "/", 2)[0]
				audit.RecordRequest(r.Context(), a, audit.Fallback{Operation: r.Method + " /api/" + table, Status: status, EntityType: table})
			}
			writeError(w, status, msg)
		}

		if path == "/graphql" {
			if !authz.Can(s.Role, authz.GraphQL) {
				refused(http.StatusForbidden, "GraphQL needs the admin role in this workspace")
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
			refused(http.StatusNotFound, "not found")
			return
		}
		if !authz.CanTable(s.Role, table, r.Method) {
			refused(http.StatusForbidden, "your role cannot do this through the generic API")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hubSessionEnded refuses a session that was born from a CircleXO sign-in the hub has since
// revoked (session.revoked webhook). Inert when the integration is off. It sits after the auth
// middleware, so the identity is known.
func hubSessionEnded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := circlexo.Current(); s != nil {
			if id, ok := auth.IdentityFrom(r.Context()); ok && id != nil && circlexo.SessionEnded(r.Context(), s, id.ID, r) {
				writeError(w, http.StatusUnauthorized, "your session was ended — sign in again")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
