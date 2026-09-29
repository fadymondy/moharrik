package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/prospects"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
BootWorker and RunWorker are the "moharrik-api worker" process (cmd/api's
worker subcommand): every background loop startMovableLoops names, running
with NO public HTTP listener at all — no REST, no GraphQL, no webhooks, no
/api/agents/ws. Hosbah starts it as its own process (cloudy.toml's
[processes] worker, alongside web) and can then scale, restart and resource-
limit it independently of the process taking public traffic.

The two loops that need a live browser WebSocket connection — the automation
scheduler and rules dispatch's "run workflow" action — stay in Boot (the web
process) and are deliberately NOT started here; see the comment beside them
in server.go. Everything else Boot starts through startMovableLoops is
started here too, unconditionally (each still honours its own SCHEDULER=off /
*_COLLECTOR=off / *_SENDER=off / *_WAKER=off switch, exactly as it does in the
web process, so the test suite and the OpenAPI export — which never call this
path — are unaffected either way).
*/

// BootWorker builds the same App Boot does (same DB pools, same fail-closed
// checks) but mounts no HTTP routes and starts only the background loops.
func BootWorker() *app.App {
	k := togo.New()
	ctx := context.Background()
	a := app.New(ctx, k)

	if a.SysDB == nil || a.SQLDB == nil {
		panic("moharrik: DATABASE_URL (owner) and APP_DATABASE_URL (app role) are both required — see README")
	}
	if err := tenant.VerifyAppRole(ctx, a.SQLDB); err != nil {
		panic(err)
	}

	// Always attached: this process's every event needs to reach whichever
	// web process replica has the browser tab that cares. Publish-only — see
	// attachRealtimeBusPublishOnly's comment for why the worker never LISTENs.
	attachRealtimeBusPublishOnly(k, a)

	researchEngine := research.NewEngine(k.Log)
	prospects.Init(researchEngine)

	// automation.SetDefault: rule actions built while running IN THE WORKER
	// (e.g. rules.Start, if it is ever moved here) would otherwise reach a nil
	// default Hub. This Hub has no HTTP route mounted anywhere, so it never
	// gains a browser connection — pickAgents-style dispatch from it always
	// reports "nothing connected", the same safe, already-handled case as a
	// closed laptop. It exists only so any code path that unconditionally
	// calls automation.Default() does not panic.
	agentHub := automation.NewHub(k.Log)
	automation.SetDefault(agentHub)

	wireChannelCallbacks(a)
	startMovableLoops(a, agentHub, researchEngine, k.Log)

	return a
}

// RunWorker boots the worker and blocks until SIGTERM/SIGINT, the same
// lifecycle deploy/start.sh already gives the web process (see stop() there).
// A tiny loopback-only HTTP listener answers /health for as long as the
// process is alive — not required by Hosbah's healthcheck (cloudy.toml's
// [healthcheck] is unchanged, and still points at the web process only: see
// the top of cloudy.toml), but harmless, loopback-only, and useful if a human
// or a future Hosbah convention wants to probe the worker directly rather
// than relying on process liveness alone.
func RunWorker() error {
	a := BootWorker()
	defer a.Kernel.Close()

	healthAddr := os.Getenv("WORKER_HEALTH_ADDR")
	if healthAddr == "" {
		healthAddr = "127.0.0.1:8091"
	}
	ln, err := net.Listen("tcp", healthAddr)
	if err != nil {
		a.Log.Warn("worker: could not open the loopback health listener; continuing without it", "addr", healthAddr, "err", err)
	} else {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"ok","service":"moharrik-worker"}`)
		})
		srv := &http.Server{Handler: mux}
		go func() { _ = srv.Serve(ln) }()
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		}()
	}

	a.Log.Info("moharrik-worker running (no public listener; background loops only)")

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-sigCtx.Done()
	a.Log.Info("moharrik-worker stopping")
	return nil
}
