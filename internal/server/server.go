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

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/analytics"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/circlexo"
	"github.com/fadymondy/moharrik/internal/home"
	"github.com/fadymondy/moharrik/internal/opportunities"
	"github.com/fadymondy/moharrik/internal/researchflow"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/contactcontext"
	"github.com/fadymondy/moharrik/internal/conversions"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/googlecontacts"
	graphgen "github.com/fadymondy/moharrik/internal/graph/gen"
	"github.com/fadymondy/moharrik/internal/graph/resolvers"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/imports"
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
	"github.com/fadymondy/moharrik/internal/rules"
	"github.com/fadymondy/moharrik/internal/searchconsole"
	"github.com/fadymondy/moharrik/internal/telemetry"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
	"github.com/fadymondy/moharrik/internal/vcard"
	"github.com/fadymondy/moharrik/internal/whatsapp"
	"github.com/fadymondy/moharrik/internal/workspaces"
	"github.com/fadymondy/moharrik/internal/youtube"
)

// externalWorkers reports whether a separate Hosbah worker process (moharrik-api
// worker; see cmd/api/main.go and BootWorker below) runs this deploy's
// background loops, so THIS process — the web one, taking public traffic —
// must not also run them. WORKERS=external is set by deploy/start.sh only
// when Hosbah's [processes] config actually gives the app a worker process;
// unset (the default) keeps every loop running in-process here, exactly as
// before WORKERS existed — backward compatible for local dev and every
// already-deployed instance that has no worker process configured.
func externalWorkers() bool { return os.Getenv("WORKERS") == "external" }

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

	// The cross-process realtime bus (internal/realtime/bus.go): only needed
	// once an event can be raised somewhere other than this process — i.e.
	// once a separate worker exists. An all-in-one deploy (the default) keeps
	// the Hub in-process-only, exactly as before the bus existed: no LISTEN
	// connection, no NOTIFY round trip, nothing for local dev to depend on
	// that a bare `docker compose up -d` Postgres didn't already need to run.
	if externalWorkers() {
		attachRealtimeBus(ctx, k, a)
	}

	// The research engine: search, reader and model, resolved from the
	// environment once. Each tier is independently optional, and a run says
	// which ones it had rather than failing opaquely (MH-227). Built here,
	// ahead of the guard, so mcp.MountPublicHost below can hand its tool
	// registry to the dedicated MCP host's transport (MH-516).
	researchEngine := research.NewEngine(k.Log)
	// Discovery rides on research: settled discovery runs become prospects,
	// and scheduled discovery runs on the research clock (MH-397).
	prospects.Init(researchEngine)

	installErrors()
	installProblemReports(k)          // outermost: sees what the guard and handlers answer
	k.UseMiddleware(telemetry.Middleware) // inside it: a panic is traced as an exception, then reported
	installInputGuard(k)              // text Postgres cannot store is a 400 before any query sees it
	installClientIP(k)                // the visitor's address, from the TRUSTED proxy chain only
	k.UseMiddleware(audit.Middleware) // who/where for the audit log (MH-392), after the address
	installChatCORS(k)                // before the guard: a preflight carries no session
	// The dedicated MCP host (MH-516), if MCP_PUBLIC_URL names one: ahead of
	// the guard so a path that is not its transport or metadata is a plain
	// 404 on that host, never a workspace-tier 401 or the SPA. A no-op when
	// MCP_PUBLIC_URL is unset.
	mcp.MountPublicHost(k, a, researchEngine)
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

	// Sign in with CircleXO (off unless CIRCLEXO_ISSUER is set).
	installCircleXO(k, a)

	// Last, so every registered route wins: unknown paths go to Next when the API is the
	// public process (WEB_ORIGIN; see webproxy.go).
	installWebProxy(k)

	// The WhatsApp webhook, and the inbox's own "file this message" handed to
	// it: neither package imports the other, so the wiring happens here. The
	// callbacks (wireChannelCallbacks) are shared with BootWorker, because the
	// Discord/Telegram/Slack GATEWAY loops call the same package-level filer a
	// webhook handler does; the webhook ROUTES below stay web-only — nothing
	// but a public HTTP endpoint mounts them.
	whatsapp.RegisterHook(k.Router, a)
	channels.RegisterTelegramHook(k.Router, a)
	channels.RegisterSlackHook(k.Router, a)
	channels.RegisterDiscordHook(k.Router, a)
	wireChannelCallbacks(a)

	// Every loop below is safe to run in a separate Hosbah worker process
	// (see BootWorker) and, when WORKERS=external says one exists, MUST NOT
	// also run here — a second copy ticking in the web process would be
	// exactly the double-run a worker process was meant to replace, not just
	// wasted work. startMovableLoops is the one place both boot paths call,
	// so the set of "what moves" cannot drift between them.
	if !externalWorkers() {
		startMovableLoops(a, agentHub, researchEngine, k.Log)
	}

	// These two stay in the web process ALWAYS, never gated by WORKERS: both
	// ultimately dispatch to automation.Default()'s live WebSocket registry —
	// agentHub.StartScheduler picks a connected browser to run a schedule's
	// steps on, and a "run automation workflow" rule action
	// (internal/rules/actions.go) goes through the same Hub. That registry
	// only has connections where /api/agents/ws is actually mounted, which is
	// here. Running these in the worker instead would not crash — pickAgents
	// already handles "no browser is connected" as the ordinary case — it
	// would just always report zero candidates and silently stop dispatching
	// automation runs, which is worse than not moving them. See the report in
	// AGENTS task history for the full reasoning.
	if os.Getenv("SCHEDULER") != "off" {
		agentHub.StartScheduler(a)
	}
	rules.Start(a)

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
	crm.RegisterAvatars(api, a)
	crm.RegisterProviderPhotos(api, a) // MH-602: WhatsApp-cached photos, signed like an uploaded avatar
	notifications.Register(api, a)
	inbox.Register(api, a)
	inbox.RegisterMailbox(api, a)
	inbox.RegisterAttachments(api, a)
	channels.RegisterTelegram(api, a)
	channels.RegisterSlack(api, a)
	channels.RegisterDiscord(api, a)
	channels.RegisterWorkflowChannels(api, a) // browser-workflow channels (MH-564)
	channels.RegisterAggregate(api, a)        // the One Channels page (MH-571)
	whatsapp.Register(api, a)
	whatsapp.RegisterNotify(api, a)                      // members' WhatsApp notifications (MH-411)
	imports.RegisterProvider(imports.WhatsAppProvider{}) // MH-562: import WhatsApp chats/groups
	imports.RegisterProvider(imports.EmailProvider{})    // MH-562: import a mailbox's senders
	imports.RegisterProvider(imports.SlackProvider{})    // MH-562: import Slack channels/DMs
	imports.RegisterProvider(imports.DiscordProvider{})  // MH-562: import Discord guild channels
	imports.RegisterProvider(imports.TelegramProvider{}) // MH-562: no history API — honestly not-ready
	imports.RegisterProvider(imports.WorkflowProvider{}) // MH-562: no history source — honestly not-ready
	imports.Register(api, a)                             // Contacts → "Import from a connection"
	vcard.Register(api, a)                               // Contacts → import/export .vcf (MH-584)
	var ccAI *ai.Client
	if a != nil {
		ccAI = contactcontext.ResolveAIClient(a.Log) // nil is a fully supported state (MH-586): every ask/report path works without one
	}
	contactcontext.Register(api, a, ccAI) // Contacts → WhatsApp export/notes/ask/report (MH-586)
	trends.Register(api, a)
	trends.RegisterDigest(api, a) // the trends digest (MH-389)
	automation.Register(api, a, automation.Default())
	automation.RegisterConnect(api, a, automation.Default()) // connect a browser by approval (MH-478)
	automation.RegisterSchedules(api, a, automation.Default())
	automation.RegisterWorkflows(api, a, automation.Default())
	automation.RegisterPresets(api, a, automation.Default())
	automation.RegisterExtension(api)
	automation.RegisterPick(api, a, automation.Default())
	automation.RegisterCanvas(api, a) // the workflow canvas: layouts and executions (MH-480)
	rules.Register(api, a)            // automation rules: event → conditions → actions (MH-427)
	research.Register(api, a, researchEngine)
	prospects.Register(api, a, researchEngine) // prospect discovery (MH-397)
	mcp.RegisterTokenRoutes(api, a)
	mcp.RegisterGrantRoutes(api, a) // connected apps over OAuth (MH-391)
	google.Register(api, a)
	googlecontacts.Register(api, a) // Google Contacts import/sync REST (MH-585)
	alerts.Register(api, a)
	overview.Register(api, a)      // the workspace front page (MH-462)
	home.Register(api, a)          // Home (MH-1062)
	opportunities.Register(api, a) // Opportunity signals (MH-1065)
	researchflow.Register(api, a)  // Research funnel and stages (MH-1066)
	circlexo.RegisterAPI(api, a)    // the workspace's CircleXO link + entitlements
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
