// Package server wires the togo kernel with both interfaces (Huma REST/OpenAPI
// and gqlgen GraphQL) and returns a ready App. It lives in its own package so
// both cmd/api (serve) and feature tests can boot the exact same stack.
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/analytics"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/conversions"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/google"
	graphgen "github.com/fadymondy/moharrik/internal/graph/gen"
	"github.com/fadymondy/moharrik/internal/graph/resolvers"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/leads"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/mcp"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/overview"
	_ "github.com/fadymondy/moharrik/internal/plugins" // blank-imports installed plugins (togo install)
	"github.com/fadymondy/moharrik/internal/prospects"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/reports"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/rest"
	"github.com/fadymondy/moharrik/internal/searchconsole"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
	"github.com/fadymondy/moharrik/internal/whatsapp"
	"github.com/fadymondy/moharrik/internal/workspaces"
	"github.com/fadymondy/moharrik/internal/youtube"
)

// Boot builds the kernel + app and mounts REST, GraphQL, MCP and health on the
// kernel router. It does not start listening — the caller calls Kernel.Serve.
func Boot() *app.App {
	k := togo.New()
	ctx := context.Background()
	a := app.New(ctx, k)

	// Fail closed: without the RLS-enforced app pool, generated CRUD would run
	// on the owner pool with no tenant isolation at all.
	if a.SysDB == nil || a.SQLDB == nil {
		panic("moharrik: DATABASE_URL (owner) and APP_DATABASE_URL (app role) are both required — see README")
	}
	if err := tenant.VerifyAppRole(ctx, a.SQLDB); err != nil {
		panic(err)
	}

	installErrors()
	installProblemReports(k)          // outermost: sees what the guard and handlers answer
	installInputGuard(k)              // text Postgres cannot store is a 400 before any query sees it
	installClientIP(k)                // the visitor's address, from the TRUSTED proxy chain only
	k.UseMiddleware(audit.Middleware) // who/where for the audit log (MH-392), after the address
	installChatCORS(k)                // before the guard: a preflight carries no session
	installGuard(k, a)

	// The registry of connected browsers, created BEFORE the routes: the
	// automation handlers are handed it at registration time, so a hub made
	// afterwards is a nil pointer in every one of them. (It was, once — the
	// isolation sweep caught it.)
	//
	// The socket itself is mounted on the router rather than through huma: a
	// WebSocket upgrade is not a request/response body. It sits in the guard's
	// PUBLIC tier because the extension has no session and cannot set headers —
	// the token in its first frame is the authentication (internal/automation/ws.go).
	agentHub := automation.NewHub(k.Log)
	automation.SetDefault(agentHub)
	k.Router.Get("/api/agents/ws", agentHub.Handler(a))
	// The extension zip (MH-399): a file, not a JSON body, so a plain route.
	automation.MountExtensionDownload(k.Router, a)
	automation.MountExtensionUpdates(k.Router, a)
	// A step's screenshot (MH-478): bytes, so a plain route, in the workspace tier.
	automation.MountRunArtifacts(k.Router, a)

	// The research engine: search, reader and model, resolved from the
	// environment once. Each tier is independently optional, and a run says
	// which ones it had rather than failing opaquely (MH-227).
	researchEngine := research.NewEngine(k.Log)
	// Discovery rides on research: settled discovery runs become prospects,
	// and scheduled discovery runs on the research clock (MH-397).
	prospects.Init(researchEngine)
	// The server jobs a schedule can run (MH-478).
	registerJobs(a, researchEngine)

	// A fake-Google override that production ignores is still worth a line in
	// the log: whoever set it expected it to do something (MH-439).
	if google.FakeRefused() {
		k.Log.Warn("GOOGLE_API_BASE is set but ignored: it is honoured only on a local, non-production APP_URL")
	}

	api := humachi.New(k.Router, apiConfig())
	// Before any route: huma applies middleware when an operation is registered.
	audit.UseHuma(api, a)
	registerREST(api, a, researchEngine)

	mcp.Mount(k, a, researchEngine)

	// Last, so every registered route wins: unknown paths go to Next when the API is the
	// public process (WEB_ORIGIN; see webproxy.go).
	installWebProxy(k)

	// The trends collector: one in-process ticker looking for sources that are
	// due. When each source is due is its own interval (6, 12 or 24 hours; MH-387).
	// TRENDS_COLLECTOR=off turns it off, which is what the tests and the OpenAPI
	// export want — neither should reach out to the internet.
	if os.Getenv("TRENDS_COLLECTOR") != "off" {
		trends.StartCollector(a, 5*time.Minute, 25)
	}

	// The WhatsApp webhook, and the inbox's own "file this message" handed to
	// it: neither package imports the other, so the wiring happens here.
	whatsapp.RegisterHook(k.Router, a)
	whatsapp.SetFiler(func(ctx context.Context, workspaceID, phone, name, text, externalID string) error {
		// "STOP" is the ONLY way out of a WhatsApp broadcast — there is no
		// unsubscribe header the way email has one — so it is honoured before
		// anything else happens to the message (MH-320).
		//
		// The message is still filed afterwards: the owner should see the
		// customer asking to stop in the thread, not just find a flag changed.
		if stopped, err := marketing.HandleStopWord(ctx, a, workspaceID, phone, text); err != nil {
			a.Log.Warn("could not record a WhatsApp opt-out", "workspace", workspaceID, "err", err)
		} else if stopped {
			var locale string
			_ = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, workspaceID), `
				SELECT coalesce(locale, 'en') FROM contacts
				WHERE workspace_id = $1 AND regexp_replace(coalesce(phone, ''), '[^0-9]', '', 'g') = $2
				LIMIT 1`, workspaceID, marketing.OnlyDigits(phone)).Scan(&locale)
			// Confirmed once, in their language. Silence after a STOP reads as
			// having been ignored.
			if _, err := whatsapp.Send(ctx, a, workspaceID, phone, marketing.StopConfirmation(locale)); err != nil {
				a.Log.Warn("could not confirm a WhatsApp opt-out", "workspace", workspaceID, "err", err)
			}
		}
		return inbox.FileWhatsApp(ctx, a, workspaceID, phone, name, text, externalID)
	})

	// Members' WhatsApp notifications (MH-411): one in-process ticker that
	// reads new events and sends each member's batch when their rules allow.
	// WHATSAPP_NOTIFY=off turns it off.
	whatsapp.StartNotifier(a, 30*time.Second)

	// The automation scheduler: one in-process clock firing the schedules that
	// are due. SCHEDULER=off turns it off for the tests and the OpenAPI export.
	// In-process, like the other two tickers — a second replica would need a
	// lock, and the note is here rather than assumed.
	if os.Getenv("SCHEDULER") != "off" {
		agentHub.StartScheduler(a)
	}

	// The research clock (MH-388): scheduled research, spending each
	// workspace's daily budget. RESEARCH_SCHEDULER=off turns it off, and so
	// does SCHEDULER=off — the tests and the OpenAPI export must not search
	// the web.
	if os.Getenv("RESEARCH_SCHEDULER") != "off" && os.Getenv("SCHEDULER") != "off" {
		researchEngine.StartScheduler(a, 2*time.Minute)
	}

	// Run retention: finished runs are deleted past each workspace's window
	// (MH-234). It reads SCHEDULER itself, so it is off wherever the scheduler
	// is — a sweeper that deletes rows under a test is exactly the thing not
	// to leave running.
	automation.StartRetention(a, k.Log)
	// Audit log retention (MH-392): each workspace's window, 365 days by default.
	audit.StartRetention(a, k.Log)

	// The inbox's mail collector: one in-process ticker polling the mailboxes
	// that are due. MAIL_COLLECTOR=off turns it off — the tests and the OpenAPI
	// export must not open a connection to anyone's mail server.
	if os.Getenv("MAIL_COLLECTOR") != "off" {
		inbox.StartMailCollector(a, 2*time.Minute, 20)
	}

	// The Google data collector (MH-440..447): discovery, then the enabled
	// properties, sites and channels that are due. GOOGLE_COLLECTOR=off turns
	// it off — the tests and the OpenAPI export must not call Google.
	analytics.RegisterSync()     // GA4's nightly snapshot (MH-444)
	searchconsole.RegisterSync() // Search Console's daily import (MH-445)
	youtube.RegisterSync()       // YouTube sync + quota ledger (MH-441)
	if os.Getenv("GOOGLE_COLLECTOR") != "off" {
		google.StartCollector(a, 5*time.Minute, 20)
	}

	// The notification center's collected sources (MH-384): Bluesky, YouTube
	// and RSS, each on its own interval. NOTIFY_COLLECTOR=off turns it off —
	// the tests and the OpenAPI export must not call anybody's servers.
	if os.Getenv("NOTIFY_COLLECTOR") != "off" {
		notifications.StartCollector(a, time.Minute, 20)
	}

	// Snoozed conversations wake when their time comes (MH-383), so every open
	// tab's list and bell move without anyone reloading. SNOOZE_WAKER=off
	// turns it off; the inbox list wakes due threads on read either way.
	// GA4 conversions (MH-449): drains the outbox the lead and deal triggers fill,
	// a few seconds after each moment. GA4_SENDER=off turns it off; the tests
	// drain it themselves, against a fake Google.
	conversions.StartSender(a, 5*time.Second)

	if os.Getenv("SNOOZE_WAKER") != "off" {
		inbox.StartSnoozeWaker(a, 30*time.Second)
	}

	gql := handler.NewDefaultServer(graphgen.NewExecutableSchema(graphgen.Config{
		Resolvers: &resolvers.Resolver{App: a},
	}))
	k.Router.Handle(k.Config.GraphQLPath, gql)
	k.Router.Handle("/graphql/play", playground.Handler("Moharrik GraphQL", k.Config.GraphQLPath))

	k.Router.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"moharrik","togo":%q}`, togoVersion())
	})

	// The workspace's own event stream. Mounted on the router rather than through
	// huma because SSE is a long-lived response, not a request/response body —
	// but it sits under /api/w/, so it goes through the same guard tier as every
	// other workspace route and inherits session + membership from it.
	// The member's user id and role ride along: every event names who may see
	// it (realtime.Event.For), so a client-role member's stream stays empty.
	k.Router.Get("/api/w/events", realtime.Handler(a.Events, func(r *http.Request) (string, realtime.Viewer) {
		s, ok := tenant.From(r.Context())
		if !ok {
			return "", realtime.Viewer{}
		}
		return s.WorkspaceID, realtime.Viewer{UserID: s.UserID, Role: s.Role}
	}))

	// A chat visitor's own stream (MH-382): one conversation, authorized by the
	// widget key, the Origin allow-list and the visitor token, exactly like the
	// thread GET. SSE for the same reason as above; /api/chat/ is a public
	// prefix in the guard and gets the chat CORS headers.
	k.Router.Get("/api/chat/{key}/events", inbox.VisitorEvents(a))

	// Deliberately NOT mounted from the scaffold:
	//   /events               — one global SSE stream; would broadcast every
	//                           tenant's rows to every subscriber (see app.Emit).
	//   /api/_meta/resources  — the dashboard plugin's schema dump; no dashboard.
	return a
}

func registerREST(api huma.API, a *app.App, researchEngine *research.Engine) {
	rest.RegisterRoutes(api, a) // generated CRUD — behind the guard's workspace tier
	workspaces.Register(api, a)
	marketing.Register(api, a)
	marketing.RegisterSubscribeForms(api, a)
	marketing.RegisterWhatsAppConsent(api, a)
	leads.Register(api, a) // the public inquiry endpoint + the Leads API (MH-390)
	crm.Register(api, a)
	notifications.Register(api, a)
	inbox.Register(api, a)
	inbox.RegisterMailbox(api, a)
	whatsapp.Register(api, a)
	whatsapp.RegisterNotify(api, a) // members' WhatsApp notifications (MH-411)
	trends.Register(api, a)
	automation.Register(api, a, automation.Default())
	automation.RegisterConnect(api, a, automation.Default()) // connect a browser by approval (MH-478)
	automation.RegisterSchedules(api, a, automation.Default())
	automation.RegisterWorkflows(api, a, automation.Default())
	automation.RegisterPresets(api, a, automation.Default())
	automation.RegisterExtension(api)
	automation.RegisterPick(api, a, automation.Default())
	automation.RegisterCanvas(api, a) // the workflow canvas: layouts and executions (MH-480)
	research.Register(api, a, researchEngine)
	prospects.Register(api, a, researchEngine) // prospect discovery (MH-397)
	mcp.RegisterTokenRoutes(api, a)
	mcp.RegisterGrantRoutes(api, a) // connected apps over OAuth (MH-391)
	google.Register(api, a)
	alerts.Register(api, a)
	overview.Register(api, a)      // the workspace front page (MH-462)
	reports.Register(api, a)       // CRM reporting (MH-394)
	icp.Register(api, a)           // ideal-customer profile + lead score (MH-395)
	analytics.Register(api, a)     // GA4 (MH-444)
	conversions.Register(api, a)   // GA4 conversions over the Measurement Protocol (MH-449)
	searchconsole.Register(api, a) // Search Console performance (MH-445)
	youtube.Register(api, a)       // YouTube channel, videos, comments (MH-441)
	audit.Register(api, a)         // the audit log (MH-392)
}

/*
installClientIP puts the request's real client address on its context, for the
public chat limiter (internal/inbox/ratelimit.go). Which forwarding header is
believed, and from whom, is TRUSTED_PROXIES / CLIENT_IP_HEADER — the reasoning
and the production values are in internal/clientip.

A malformed TRUSTED_PROXIES stops the boot: silently falling back would change
who the limiter thinks it is talking to.
*/
func installClientIP(k *togo.Kernel) {
	r, configured, err := clientip.FromEnv()
	if err != nil {
		panic("moharrik: " + err.Error())
	}
	if !configured && os.Getenv("APP_ENV") == "production" {
		// Behind NPM every visitor would then share NPM's address, so the
		// per-address chat limit becomes one bucket for everybody. Fails
		// closed (tighter, not looser), but it is not the intended limit.
		k.Log.Warn("TRUSTED_PROXIES is unset: only loopback proxies are believed, so behind a reverse proxy every visitor shares one address for rate limiting (see internal/clientip)")
	}
	k.UseMiddleware(clientip.Middleware(r))
}

// togoVersion reports the togo framework version this app is built against.
func togoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/togo-framework/togo" {
				return d.Version
			}
		}
	}
	return "dev"
}

// OpenAPI returns the generated OpenAPI 3.1 document (used by `togo generate`).
func OpenAPI() ([]byte, error) {
	return openAPISpec().YAML()
}

// openAPISpec is the document OpenAPI exports, as an object (the tests walk it).
func openAPISpec() *huma.OpenAPI {
	router := chi.NewMux()
	installErrors()
	api := humachi.New(router, apiConfig())
	audit.UseHuma(api, nil)
	// A zero engine, not a configured one: the export must not read the
	// environment or reach a search service. Every tier reads as absent, which
	// changes no route — only the capability flags a live server reports.
	registerREST(api, nil, &research.Engine{})
	return api.OpenAPI()
}
