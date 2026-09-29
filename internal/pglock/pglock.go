// Package pglock is a cluster-wide mutual-exclusion lock for the background
// loops in internal/server, built on Postgres advisory locks.
//
// Every "one thing at a time" ticker (schedulers, digests, notifiers,
// collectors) was written assuming exactly one process — Moharrik's
// historical shape, one container running the API and every loop as a
// goroutine. Once loops can run in a worker process that Hosbah scales to
// more than one replica, each tick must still happen once cluster-wide, not
// once per replica: a Postgres advisory lock, held by whichever replica gets
// there first, is that mutual exclusion, without a new table or a new
// dependency (every deploy already has one Postgres).
//
// pg_advisory_lock takes a single bigint key. hashtext(name) turns any string
// name into one, the same trick internal/automation/capacity.go already uses
// per-workspace (pg_advisory_xact_lock(hashtext($1))) — this package is the
// same idea, generalised, session-scoped (not transaction-scoped) so a lock
// can be held for a single tick's work OR for a long-lived goroutine (the
// Discord gateway's one session per connection, held for as long as that
// session runs).
package pglock

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TryLock attempts to acquire the named advisory lock without blocking, on a
// connection taken out of db's pool and held for as long as the lock is held
// (an advisory lock is session-scoped: returning the connection to the pool
// would let some unrelated query on the same connection release it early, or
// worse, have it silently vanish on a pooled connection reused by someone
// else).
//
// ok reports whether the lock was acquired. When ok is false, name is held by
// another process (another replica's tick, or another connection of ours) and
// the caller should skip this tick's work rather than duplicate it; TryLock
// has already returned the borrowed connection to the pool in that case, and
// unlock is nil.
//
// When ok is true, the caller must call unlock exactly once, whether the
// work it guards succeeds or fails, to release the lock and return the
// connection to the pool.
func TryLock(ctx context.Context, db *sql.DB, name string) (unlock func(), ok bool, err error) {
	if db == nil {
		return nil, false, errors.New("pglock: nil db")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&ok); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !ok {
		_ = conn.Close()
		return nil, false, nil
	}
	var once bool
	unlock = func() {
		if once {
			return
		}
		once = true
		// A best-effort unlock: if the connection is already gone (process
		// exiting, network blip) Postgres releases every advisory lock the
		// session held anyway, when the connection closes.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(uctx, `SELECT pg_advisory_unlock(hashtext($1))`, name)
		_ = conn.Close()
	}
	return unlock, true, nil
}

// WithLock runs fn only if name's advisory lock is free, and reports whether
// it ran. It is the shape every tick-based loop in this codebase wants: "if
// nobody else is doing this right now, do it; otherwise skip this tick" — a
// single call instead of the acquire/defer-unlock boilerplate at every call
// site.
func WithLock(ctx context.Context, db *sql.DB, name string, fn func()) (ran bool, err error) {
	unlock, ok, err := TryLock(ctx, db, name)
	if err != nil || !ok {
		return false, err
	}
	defer unlock()
	fn()
	return true, nil
}
