package inbox

import (
	"context"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pglock"
)

// StartMailCollector polls every enabled mailbox on a ticker.
//
// Same shape and same caveat as the trends collector: without a lock, a
// multi-replica deploy would have every replica polling the same mailboxes.
// That is not a disaster here — IMAP hands each message to whoever marks it
// \Seen first, and the unique index on (workspace_id, external_id) makes a
// double file a no-op — but it is wasted work, so each tick takes a
// cluster-wide advisory lock (internal/pglock) and skips when another
// replica already holds it.
//
// MAIL_COLLECTOR=off disables it, which is what the tests and the OpenAPI
// export want: neither should open a connection to anyone's mail server.
func StartMailCollector(a *app.App, interval time.Duration, limit int) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// A first pass shortly after boot, then on the interval: a workspace
		// that just configured a mailbox should not wait a full cycle.
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:inbox:collector", func() {
				polled, filed := CollectDue(ctx, a, limit)
				if polled > 0 {
					a.Log.Info("mail collected", "mailboxes", polled, "messages", filed)
				}
			})
			if err != nil {
				a.Log.Error("mail collector: could not acquire the cluster lock", "err", err)
			}
			timer.Reset(interval)
		}
	}()
	return cancel
}

// CollectDue polls up to limit enabled mailboxes, oldest poll first, and
// returns how many were polled and how many messages were filed.
//
// The queue is read on the OWNER pool because "which workspaces have mail to
// collect" is by definition a cross-tenant question. Everything each poll then
// does runs on the app pool inside that one workspace (see Poll and File).
func CollectDue(ctx context.Context, a *app.App, limit int) (int, int) {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id FROM mailboxes
		WHERE enabled AND imap_host <> '' AND password_sealed <> ''
		ORDER BY last_polled_at NULLS FIRST LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("mail collector: could not read the queue", "err", err)
		return 0, 0
	}
	defer rows.Close()

	type due struct{ id, workspaceID string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.workspaceID); err != nil {
			continue
		}
		list = append(list, d)
	}

	polled, filed := 0, 0
	for _, d := range list {
		if ctx.Err() != nil {
			break
		}
		mb, err := loadMailboxByID(ctx, a, d.workspaceID, d.id)
		if err != nil {
			// A password that cannot be unsealed (AUTH_SECRET changed) is worth
			// recording on the row rather than logging into the void.
			recordPoll(ctx, a, d.workspaceID, d.id, 0, err)
			continue
		}
		n, perr := Poll(ctx, a, *mb)
		recordPoll(ctx, a, d.workspaceID, d.id, n, perr)
		polled++
		filed += n
	}
	return polled, filed
}
