package realtime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// channel is the one Postgres NOTIFY channel every process LISTENs on for
// cross-process workspace events.
//
// One channel, not one per workspace. Scoping does not move here — it stays
// exactly where it always was, in Hub.deliverLocal / Event.For, which is run
// again on the receiving side for every subscriber exactly as it is for a
// local Publish. A process hearing every workspace's envelope over NOTIFY is
// no different, authorization-wise, from the single in-process Hub that
// already held every workspace's live subscribers before this file existed —
// the bus is a transport underneath the hub, not a new place to decide who
// may see what.
const channel = "moharrik_realtime"

// notifyLimit keeps every payload comfortably under Postgres's 8000-byte
// NOTIFY limit, with headroom for the JSON envelope around the event and for
// multi-byte UTF-8 (Arabic content) growing under \u escaping.
const notifyLimit = 7500

// envelope is what actually goes out over NOTIFY: the event, which workspace
// it is for, and which process raised it (so that process's own Bus can
// recognise and skip its own notification — Publish already delivered it
// locally before the NOTIFY round trip even started).
//
// Permission travels as its own field, not as part of Event's JSON: Event's
// Permission has `json:"-"` on purpose, because that struct is also what
// Hub sends down an SSE connection to a browser, and a viewer's audience
// permission is never something the browser itself should see. The bus's
// envelope is an internal, server-to-server message, not that browser wire —
// deliverLocal on the receiving side needs Permission to decide the audience
// exactly as it would for a local Publish, so leaving it off here would
// silently fail every delivery closed (empty Permission = nobody, by
// Event.For's own rule).
type envelope struct {
	Origin     string `json:"o"`
	WS         string `json:"w"`
	Event      Event  `json:"e"`
	Permission string `json:"p"`
}

// Bus is the cross-process half of Hub: it publishes a Hub's local events to
// every other process's Hub over Postgres LISTEN/NOTIFY, and (via Listen)
// feeds this process's Hub whatever the others published.
//
// Nil is a fully supported, and the DEFAULT, state: Hub.Publish checks for an
// attached Bus and does nothing extra when there isn't one, so an all-in-one
// deployment (today's shape, and local dev) never opens a LISTEN connection
// or issues a NOTIFY — one in-process Hub already delivers correctly there,
// and forcing every event through Postgres would only add latency and a new
// way to fail for zero benefit. The bus exists for the moment the background
// loops move to a separate Hosbah worker process (WORKERS=external): from
// then on an event raised by, say, the automation scheduler (now running in
// the worker, with no browsers connected to it) still needs to reach the tabs
// connected to a web replica's Hub, and this is how.
type Bus struct {
	dsn    string
	log    *slog.Logger
	origin string
	notify func(ctx context.Context, payload string) error // set by NewBus; swappable in tests
}

// NewBus builds a Bus against dsn (the same DATABASE_URL the rest of the app
// uses — LISTEN needs no elevated role, and no RLS: a NOTIFY payload never
// touches a tenant table). notifyDB is the pool Publish sends NOTIFY through
// (a.SysDB is the natural choice: already open, no RLS ceremony needed for
// `SELECT pg_notify(...)`). Listen opens its OWN dedicated connection — a
// pooled *sql.DB connection cannot LISTEN, because the pool is free to hand
// that connection to an unrelated query, or close it, at any time.
func NewBus(dsn string, notifyDB *sql.DB, log *slog.Logger) *Bus {
	origin := randomOrigin()
	return &Bus{
		dsn:    dsn,
		log:    log,
		origin: origin,
		notify: func(ctx context.Context, payload string) error {
			_, err := notifyDB.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
			return err
		},
	}
}

func randomOrigin() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// publish sends ev out over NOTIFY, best-effort and never blocking the
// caller (Hub.Publish, which may be running inside an HTTP request handler):
// the round trip to Postgres happens on its own goroutine, with a bounded
// timeout, exactly like the local delivery it follows never blocking on a
// slow subscriber.
func (b *Bus) publish(workspaceID string, ev Event) {
	env := envelope{Origin: b.origin, WS: workspaceID, Event: ev, Permission: ev.Permission}
	payload, err := json.Marshal(env)
	if err != nil {
		return
	}
	if len(payload) > notifyLimit {
		// The event is a hint to refetch, not the data itself (see Hub.Publish) —
		// so when the envelope does not fit, the fix is to shed the two
		// variable-length, human-text fields (a customer's message content;
		// nothing else on Event grows unbounded) rather than drop the event, or
		// round-trip to the database for a smaller representation the receiving
		// side would have to know how to re-expand per event Kind. A subscriber
		// that gets a trimmed hint still revalidates through the normal,
		// RLS-scoped endpoint exactly as it would for any event.
		trimmed := env
		trimmed.Event.Sender, trimmed.Event.Snippet = "", ""
		payload, err = json.Marshal(trimmed)
		if err != nil || len(payload) > notifyLimit {
			if b.log != nil {
				b.log.Warn("realtime bus: event too large for NOTIFY even after trimming; dropped", "workspace", workspaceID, "kind", ev.Kind)
			}
			return
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.notify(ctx, string(payload)); err != nil && b.log != nil {
			b.log.Warn("realtime bus: NOTIFY failed", "err", err)
		}
	}()
}

// Listen opens a dedicated LISTEN connection and feeds hub.deliverLocal with
// whatever another process's Bus publishes, for the life of ctx.
//
// The first connection attempt happens SYNCHRONOUSLY, before Listen returns.
// Postgres never queues a NOTIFY for a session that has not LISTENed yet, so
// an async-only dial would leave a window, right after boot, where a Publish
// from this same process (or a fast peer) could fire before the LISTEN was
// actually registered and be lost forever — not "delayed", gone, because
// nothing replays it. server.go attaches the bus and starts Listen before any
// route takes traffic specifically to make that window as small as possible;
// making the first attempt synchronous closes it outright for the common
// case (the database is already reachable at boot). If that first attempt
// fails (the database is briefly unreachable), Listen still returns rather
// than blocking boot — the reconnect loop below keeps trying in the
// background, logged but not fatal: a web replica that cannot yet hear the
// worker still serves everything else, and the next SSE reconnect (or page
// load) catches a tab up on whatever it missed, the same trade a slow
// subscriber's dropped event always has (see Hub.Publish's doc comment).
func (b *Bus) Listen(ctx context.Context, hub *Hub) {
	conn, err := b.dial(ctx)
	if err != nil && b.log != nil {
		b.log.Warn("realtime bus: initial LISTEN failed, retrying in the background", "err", err)
	}
	go func() {
		backoff := time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			if conn == nil {
				conn, err = b.dial(ctx)
			}
			if conn != nil {
				err = b.serve(ctx, conn, hub)
				conn = nil
			}
			if err != nil && ctx.Err() == nil && b.log != nil {
				b.log.Warn("realtime bus: LISTEN connection lost, reconnecting", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
}

// dial opens one connection and issues LISTEN on it. A pooled *sql.DB
// connection cannot be used for this: the pool is free to hand that
// connection to an unrelated query, or close it, at any time.
func (b *Bus) dial(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, b.dsn)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}

// serve holds one LISTEN connection open, delivering notifications to hub,
// until it fails or ctx ends. It always closes conn before returning.
func (b *Bus) serve(ctx context.Context, conn *pgx.Conn, hub *Hub) error {
	defer conn.Close(context.Background())
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var env envelope
		if err := json.Unmarshal([]byte(n.Payload), &env); err != nil {
			continue // not ours, or malformed — never fatal to the listener
		}
		if env.Origin == b.origin {
			continue // this process's own Publish already delivered it locally
		}
		env.Event.Permission = env.Permission
		hub.deliverLocal(env.WS, env.Event)
	}
}
