package inbox

import (
	"context"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pglock"
	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
Snooze (MH-383).

A snoozed conversation carries the time it wakes (conversations.snoozed_until).
Waking is: status back to open, unread again, the time cleared — so the thread
comes back to the top of the open list and the bell counts it, the way it would
if the customer had written.

Two things wake it, and both are needed:
  - wakeDue, at the top of the inbox list and a thread read, inside the
    request's workspace. It makes what a member sees exact to the second,
    whether or not the waker has run.
  - StartSnoozeWaker, a ticker on the owner pool, so a thread wakes (and every
    open tab's list and bell move) even when nobody happens to reload.

And a customer writing always wakes a snoozed thread (reopenOnInbound).
*/

const wakeSQL = `
	UPDATE conversations SET status = 'open', snoozed_until = NULL, unread = true, updated_at = now()
	WHERE status = 'snoozed' AND snoozed_until IS NOT NULL AND snoozed_until <= now()`

// wakeDue wakes this workspace's conversations whose snooze has run out.
// ctx must carry the workspace (the app pool's RLS). Best effort: a failure
// leaves them snoozed a little longer, which the waker then fixes.
func wakeDue(ctx context.Context, a *app.App, wsID string) {
	if a == nil || a.SQLDB == nil {
		return
	}
	rows, err := a.SQLDB.QueryContext(ctx, wakeSQL+` AND workspace_id = $1 RETURNING id`, wsID)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		changed(ctx, a, wsID, realtime.Event{Kind: "conversation", ConversationID: id})
	}
}

// WakeAllDue wakes every workspace's due conversations and returns how many.
// On the owner pool: "whose snooze has run out" is a cross-tenant question,
// the same as the mail collector's queue.
func WakeAllDue(ctx context.Context, a *app.App) (int, error) {
	rows, err := a.SysDB.QueryContext(ctx, wakeSQL+` RETURNING workspace_id, id`)
	if err != nil {
		return 0, err
	}
	type woke struct{ ws, id string }
	var list []woke
	for rows.Next() {
		var w woke
		if err := rows.Scan(&w.ws, &w.id); err != nil {
			rows.Close()
			return len(list), err
		}
		list = append(list, w)
	}
	rows.Close()
	for _, w := range list {
		changed(ctx, a, w.ws, realtime.Event{Kind: "conversation", ConversationID: w.id})
	}
	return len(list), rows.Err()
}

// StartSnoozeWaker wakes due conversations on a ticker. A second replica
// racing the same tick would wake the same rows twice, which is harmless
// on its own (the UPDATE only matches what is still snoozed, via
// RETURNING) — but it still takes the cluster-wide lock so only one
// replica does the work and fires the realtime event per tick.
// SNOOZE_WAKER=off turns it off (server.go).
func StartSnoozeWaker(a *app.App, interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:inbox:snooze", func() {
				if n, err := WakeAllDue(ctx, a); err != nil {
					a.Log.Warn("could not wake snoozed conversations", "err", err)
				} else if n > 0 {
					a.Log.Info("snoozed conversations woke", "count", n)
				}
			})
			if err != nil {
				a.Log.Error("snooze waker: could not acquire the cluster lock", "err", err)
			}
		}
	}()
	return cancel
}
