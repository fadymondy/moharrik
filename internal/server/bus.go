package server

import (
	"context"
	"os"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
)

// attachRealtimeBus wires a.Events to Postgres LISTEN/NOTIFY (see
// internal/realtime/bus.go) and starts listening: called by Boot when a
// separate worker process exists (WORKERS=external), so an event the worker
// raises still reaches a browser tab connected to this (the web) process's
// Hub. BootWorker calls the publish-only half itself (attachRealtimeBusPublishOnly)
// — a worker has no browsers connected to its own local Hub, so it has
// nothing to listen for.
func attachRealtimeBus(ctx context.Context, k *togo.Kernel, a *app.App) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || a.SysDB == nil {
		k.Log.Warn("WORKERS=external but DATABASE_URL is unset: the realtime bus cannot start, so events raised elsewhere will not reach this process's browsers")
		return
	}
	bus := realtime.NewBus(dsn, a.SysDB, k.Log)
	a.Events.AttachBus(bus)
	bus.Listen(ctx, a.Events)
}

// attachRealtimeBusPublishOnly wires a.Events to publish over NOTIFY without
// listening — the worker process's half: every event it raises (trends,
// automation runs it triggers via a job, a lead, …) needs to reach the web
// process's browsers, but nothing ever subscribes to the worker's own Hub, so
// a LISTEN connection there would do nothing but hold a socket open.
func attachRealtimeBusPublishOnly(k *togo.Kernel, a *app.App) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || a.SysDB == nil {
		k.Log.Warn("the realtime bus cannot start (DATABASE_URL unset): events raised in the worker will not reach any web process's browsers")
		return
	}
	bus := realtime.NewBus(dsn, a.SysDB, k.Log)
	a.Events.AttachBus(bus)
}
