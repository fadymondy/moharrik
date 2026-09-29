package audit

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pglock"
)

// Sweep deletes every row older than its workspace's retention window, and
// every row of a workspace that no longer exists. On the owner pool: the app
// role cannot delete from audit_log at all.
func Sweep(ctx context.Context, a *app.App, now time.Time) (int64, error) {
	res, err := a.SysDB.ExecContext(ctx, `
		DELETE FROM audit_log l
		WHERE l.occurred_at < $1::timestamptz - make_interval(days => coalesce(
		          (SELECT s.retention_days FROM audit_settings s WHERE s.workspace_id = l.workspace_id), $2))
		   OR NOT EXISTS (SELECT 1 FROM workspaces w WHERE w.id = l.workspace_id)`,
		now, DefaultRetentionDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StartRetention runs Sweep every six hours. Off wherever the scheduler is
// (SCHEDULER=off: the tests, the OpenAPI export), like automation's retention.
//
// Deleting the same already-gone rows twice is a no-op, so two replicas
// running this at once was never a correctness bug — but it is still wasted
// work and log noise, so each tick takes a cluster-wide advisory lock
// (internal/pglock) and simply skips when another replica already holds it.
func StartRetention(a *app.App, log *slog.Logger) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if os.Getenv("SCHEDULER") == "off" || a == nil || a.SysDB == nil {
		return cancel
	}
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			ran, err := pglock.WithLock(ctx, a.SysDB, "moharrik:audit:retention", func() {
				n, err := Sweep(ctx, a, time.Now().UTC())
				if err != nil {
					log.Error("audit retention sweep failed", "err", err)
				} else if n > 0 {
					log.Info("audit retention swept", "rows", n)
				}
			})
			if err != nil {
				log.Error("audit retention: could not acquire the cluster lock", "err", err)
			}
			_ = ran
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return cancel
}
