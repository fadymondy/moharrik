package google

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The Google data collector (MH-440..447), in the inbox collector's shape: the
OWNER pool picks what is due across every workspace (by definition a
cross-tenant question), and each piece of work then runs on the app pool
inside that one workspace (tenant.WithWorkspace).

Each kind registers what a sync of one resource does (internal/analytics,
internal/searchconsole, internal/youtube); this file only schedules, claims,
records and backs off:

  - one sync per resource at a time (sync_started_at, 30-minute lease);
  - success: last_synced_at = now, last_error cleared, next run per the kind;
  - failure: last_error = Describe(err), failures+1, and the next run backs
    off — longer when Google rate limited (and backoff_until is set, which the
    on-demand reads honour), 12h at most; a connection needing reauth is
    not retried until it is reconnected (the queue skips it).

Discovery runs here too: connections never discovered, or not for a day.

GOOGLE_COLLECTOR=off disables the ticker (tests, OpenAPI export).
*/

// Syncer syncs one resource. ctx carries the workspace.
type Syncer func(ctx context.Context, a *app.App, r Resource) error

// Schedule says when a kind's next successful sync is due.
type Schedule func(now time.Time) time.Time

type registration struct {
	sync Syncer
	next Schedule
}

var (
	regMu    sync.RWMutex
	registry = map[Kind]registration{}
)

// RegisterSyncer installs what a kind's sync does and when it next runs.
func RegisterSyncer(k Kind, s Syncer, next Schedule) {
	regMu.Lock()
	registry[k] = registration{sync: s, next: next}
	regMu.Unlock()
}

func registered(k Kind) (registration, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := registry[k]
	return r, ok
}

// Nightly is a schedule: the next 02:00 UTC at least an hour away.
func Nightly(now time.Time) time.Time {
	n := now.UTC()
	next := time.Date(n.Year(), n.Month(), n.Day(), 2, 0, 0, 0, time.UTC)
	for next.Before(n.Add(time.Hour)) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// Every is a fixed-interval schedule.
func Every(d time.Duration) Schedule { return func(now time.Time) time.Time { return now.Add(d) } }

// ErrBusy: a sync of that resource is already running.
var ErrBusy = errors.New("a sync of this is already running")

// RunSync syncs one resource now, in the calling goroutine, and records the
// outcome on its row. It returns the sync's own error (already recorded).
func RunSync(ctx context.Context, a *app.App, workspaceID, id string) error {
	if err := ready(a); err != nil {
		return err
	}
	wctx := tenant.WithWorkspace(ctx, workspaceID)
	var claimed string
	err := a.SQLDB.QueryRowContext(wctx, `
		UPDATE google_resources SET sync_started_at = now()
		WHERE workspace_id = $1 AND id = $2 AND enabled
		  AND (sync_started_at IS NULL OR sync_started_at < now() - interval '30 minutes')
		RETURNING id`, workspaceID, id).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBusy
	}
	if err != nil {
		return err
	}
	r, err := GetResource(ctx, a, workspaceID, id)
	if err != nil {
		return err
	}
	reg, ok := registered(r.Kind)
	if !ok {
		// Nothing reads this kind on this server (yet): release the claim and
		// leave the row as it was rather than record a failure nobody caused.
		_, err = a.SQLDB.ExecContext(wctx, `UPDATE google_resources SET sync_started_at = NULL WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
		return err
	}
	serr := safeSync(wctx, a, reg.sync, *r)
	now := time.Now()
	if serr == nil {
		next := now.Add(24 * time.Hour)
		if reg.next != nil {
			next = reg.next(now)
		}
		_, err = a.SQLDB.ExecContext(wctx, `
			UPDATE google_resources SET sync_started_at = NULL, last_synced_at = now(), last_error = '',
				failures = 0, backoff_until = NULL, next_sync_at = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, id, next)
		return err
	}
	var failures int
	_ = a.SQLDB.QueryRowContext(wctx, `SELECT failures FROM google_resources WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id).Scan(&failures)
	wait, backoff := Backoff(serr, failures+1)
	var until *time.Time
	if backoff {
		t := now.Add(wait)
		until = &t
	}
	_, _ = a.SQLDB.ExecContext(wctx, `
		UPDATE google_resources SET sync_started_at = NULL, last_error = $3, failures = failures + 1,
			next_sync_at = $4, backoff_until = COALESCE($5, backoff_until), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, Describe(serr), now.Add(wait), until)
	logWarn(a, "google sync failed: "+string(r.Kind), serr)
	return serr
}

// safeSync keeps a panicking syncer from taking the collector down with it.
func safeSync(ctx context.Context, a *app.App, s Syncer, r Resource) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errors.New("the sync crashed; it is logged and will be retried")
			if a != nil && a.Log != nil {
				a.Log.Error("google sync panicked", "kind", r.Kind, "panic", p)
			}
		}
	}()
	return s(ctx, a, r)
}

// Backoff is how long to wait after the n-th consecutive failure, and whether
// Google asked us to slow down (so on-demand reads should wait too).
func Backoff(err error, n int) (time.Duration, bool) {
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	step := 30 * time.Minute
	limited := RateLimited(err) || errors.Is(err, ErrQuota)
	switch {
	case errors.Is(err, ErrReauth) || errors.Is(err, ErrScope) || errors.Is(err, ErrNotConnected):
		// Nothing changes until someone reconnects; reconnecting makes it due.
		return 24 * time.Hour, false
	case limited:
		step = 15 * time.Minute
	}
	d := step << (n - 1)
	if d > 12*time.Hour {
		d = 12 * time.Hour
	}
	return d, limited
}

// ErrQuota is a refusal from Moharrik's own quota ledger (YouTube).
var ErrQuota = errors.New("the YouTube quota for today is used up")

// ---- in the background -------------------------------------------------------------

var (
	bgMu sync.Mutex
	// bgWG lets tests wait for background work to settle.
	bgWG sync.WaitGroup
)

// SyncSoon starts a sync of one resource in the background, detached from
// the request that asked for it. A sync already running is left alone.
func SyncSoon(a *app.App, workspaceID, id string) {
	if a == nil || os.Getenv("GOOGLE_SYNC_INLINE") == "off" {
		return
	}
	bgMu.Lock()
	bgWG.Add(1)
	bgMu.Unlock()
	go func() {
		defer bgWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		_ = RunSync(ctx, a, workspaceID, id)
	}()
}

// DiscoverSoon runs discovery for one connection in the background: what a
// successful connect does, so the account's data appears without a step.
func DiscoverSoon(a *app.App, workspaceID, connectionID string) {
	if a == nil || os.Getenv("GOOGLE_SYNC_INLINE") == "off" {
		return
	}
	bgMu.Lock()
	bgWG.Add(1)
	bgMu.Unlock()
	go func() {
		defer bgWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = Discover(ctx, a, workspaceID, connectionID)
	}()
}

// WaitBackground blocks until background syncs and discoveries finish (tests).
func WaitBackground() { bgWG.Wait() }

// ---- the collector ------------------------------------------------------------------

// StartCollector runs discovery and due syncs on a ticker.
func StartCollector(a *app.App, interval time.Duration, limit int) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			d, s := CollectDue(ctx, a, limit)
			if d+s > 0 {
				a.Log.Info("google collected", "discovered", d, "synced", s)
			}
			timer.Reset(interval)
		}
	}()
	return cancel
}

// CollectDue discovers the connections that are due, then syncs up to limit
// due resources. It returns how many of each it ran.
func CollectDue(ctx context.Context, a *app.App, limit int) (discovered, synced int) {
	if ready(a) != nil || !LoadConfig().Ready() {
		return 0, 0
	}
	type pair struct{ ws, id string }
	var conns []pair
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT c.workspace_id, c.id FROM google_connections c
		WHERE c.status = ''
		  AND NOT EXISTS (SELECT 1 FROM google_discoveries d
		                  WHERE d.workspace_id = c.workspace_id AND d.connection_id = c.id
		                    AND d.discovered_at >= greatest(c.updated_at, now() - interval '24 hours'))
		ORDER BY c.updated_at LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("google collector: could not read the discovery queue", "err", err)
		return 0, 0
	}
	for rows.Next() {
		var p pair
		if rows.Scan(&p.ws, &p.id) == nil {
			conns = append(conns, p)
		}
	}
	rows.Close()
	for _, p := range conns {
		if ctx.Err() != nil {
			return
		}
		_ = Discover(ctx, a, p.ws, p.id)
		discovered++
	}

	var due []pair
	rows, err = a.SysDB.QueryContext(ctx, `
		SELECT r.workspace_id, r.id FROM google_resources r
		JOIN google_connections c ON c.workspace_id = r.workspace_id AND c.id = r.connection_id
		WHERE r.enabled AND c.status = '' AND r.next_sync_at <= now()
		  AND (r.sync_started_at IS NULL OR r.sync_started_at < now() - interval '30 minutes')
		ORDER BY r.next_sync_at LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("google collector: could not read the sync queue", "err", err)
		return
	}
	for rows.Next() {
		var p pair
		if rows.Scan(&p.ws, &p.id) == nil {
			due = append(due, p)
		}
	}
	rows.Close()
	for _, p := range due {
		if ctx.Err() != nil {
			return
		}
		if err := RunSync(ctx, a, p.ws, p.id); !errors.Is(err, ErrBusy) {
			synced++
		}
	}
	return
}
