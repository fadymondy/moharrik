package inbox

import (
	"context"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

// StartMailCollector polls every enabled mailbox on a ticker.
//
// Same shape and same caveat as the trends collector: it is IN-PROCESS, so a
// multi-replica deploy would have every replica polling the same mailboxes.
// That is not a disaster here — IMAP hands each message to whoever marks it
// \Seen first, and the unique index on (workspace_id, external_id) makes a
// double file a no-op — but it is wasted work, and a shared lock (or one
// replica elected to run it) is the answer when there is a second replica.
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
			polled, filed := CollectDue(ctx, a, limit)
			if polled > 0 {
				a.Log.Info("mail collected", "mailboxes", polled, "messages", filed)
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
		SELECT workspace_id FROM mailboxes
		WHERE enabled AND imap_host <> '' AND password_sealed <> ''
		ORDER BY last_polled_at NULLS FIRST LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("mail collector: could not read the queue", "err", err)
		return 0, 0
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}

	polled, filed := 0, 0
	for _, workspaceID := range ids {
		if ctx.Err() != nil {
			break
		}
		mb, err := LoadMailbox(ctx, a, workspaceID)
		if err != nil {
			// A password that cannot be unsealed (AUTH_SECRET changed) is worth
			// recording on the row rather than logging into the void.
			recordPoll(ctx, a, workspaceID, 0, err)
			continue
		}
		n, perr := Poll(ctx, a, *mb)
		recordPoll(ctx, a, workspaceID, n, perr)
		polled++
		filed += n
	}
	return polled, filed
}
