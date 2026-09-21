// Package app is the application container (Laravel-style): shared services
// wired once and handed to controllers, resolvers, actions, and seeders.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/togo-framework/togo"

	// SQLite is the kernel's built-in default driver (no plugin). Postgres/MySQL/
	// MongoDB drivers ship as PLUGINS — `togo new --db <stack>` blank-imports the
	// matching db-* plugin into internal/plugins, so the raw driver never lives in
	// this app's go.mod or app.go (microkernel: drivers are plugins).
	_ "modernc.org/sqlite"

	db "github.com/fadymondy/moharrik/internal/db/gen"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// App holds the shared services handed to controllers, resolvers, actions, seeders.
// Service fields are nil unless the matching feature/provider plugin is installed.
type App struct {
	Kernel *togo.Kernel
	DB     *db.Queries // sqlc typed queries, on the RLS-enforced app pool

	// SQLDB is the APP pool: a non-owner, non-superuser, non-BYPASSRLS role
	// (APP_DATABASE_URL) whose every statement is scoped to the workspace in the
	// request context (internal/tenant/rlsdb.go). The ORM — and therefore every
	// generated REST handler and GraphQL resolver — uses this handle, which is
	// what makes unfiltered generated CRUD safe to mount.
	SQLDB *sql.DB

	// SysDB is the OWNER pool (DATABASE_URL, the kernel's own handle). It bypasses
	// RLS. Only tenant resolution, auth, token lookup, workspace creation and the
	// platform admin MCP may use it, and each of those queries carries its own
	// explicit user/workspace predicate.
	SysDB *sql.DB

	Dialect  togo.Dialect // driver-specific SQL dialect (from DB_DRIVER)
	Hooks    *togo.Hooks
	Log      *slog.Logger
	Cache    togo.Cache
	Queue    togo.Queue
	Storage  togo.Storage
	Realtime togo.Broker

	// Events is Moharrik's own stream: per-workspace, so a subscriber can only
	// receive what was published for the workspace it is in. Kernel.Realtime
	// (above) is the framework's global broker and is deliberately not used for
	// tenant data — see internal/realtime.
	Events *realtime.Hub
}

// New builds the container from the kernel, opening both pools if configured.
func New(ctx context.Context, k *togo.Kernel) *App {
	a := &App{
		Kernel:   k,
		Hooks:    k.Hooks,
		Log:      k.Log,
		Dialect:  k.Dialect(),
		Cache:    k.Cache,
		Queue:    k.Queue,
		Storage:  k.Storage,
		Realtime: k.Realtime,
		Events:   realtime.NewHub(),
	}
	if sysDB, err := k.SQL(ctx); err == nil {
		a.SysDB = sysDB
		// Bounded too: an unbounded pool against a shared Postgres is the wrong
		// default whatever it is for.
		tenant.Tune(sysDB)
	} else {
		k.Log.Warn("database unavailable", "err", err)
	}

	// Never fall back to the owner pool for the app pool: that would silently
	// switch RLS off for every generated route. No APP_DATABASE_URL => no app
	// pool, and server.Boot refuses to start.
	if url := os.Getenv("APP_DATABASE_URL"); url != "" {
		appDB, err := tenant.OpenRLS(url)
		if err != nil {
			k.Log.Error("app database pool", "err", err)
		} else {
			a.SQLDB = appDB
			a.DB = db.New(appDB)
		}
	}
	return a
}

// T translates a key in the configured locale (backend trans()).
func (a *App) T(key string) string { return a.Kernel.T(key) }

// Emit fires kernel hooks for in-process listeners.
//
// The scaffold also broadcast every payload over the global SSE stream
// (/events) — full rows, to every subscriber, regardless of workspace. For a
// multi-tenant app that is a cross-tenant leak (a generated create of an
// mcp_token broadcast its token_hash), so the broadcast is gone until realtime
// gets per-user/per-workspace channels in P2.
func (a *App) Emit(ctx context.Context, event string, payload any) {
	_ = a.Hooks.Fire(ctx, event, payload)
	_ = json.Marshal // kept for the P2 per-channel broadcast
}
