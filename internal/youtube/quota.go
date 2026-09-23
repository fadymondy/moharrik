/*
Package youtube reads a workspace's YouTube channels (MH-441, MH-442): the
channel, every upload, top comments, new comments into the notification
center, and YouTube Analytics. Ported from fadymondy.com's internal/youtube
(one owner there, per workspace and per enabled channel here).
*/
package youtube

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
)

/*
The YouTube Data API quota, two levels (MH-441).

Google gives the whole OAuth project one daily budget (10,000 units by
default), shared by every workspace. So each Data API call reserves its units
BEFORE it is made (Google charges failed calls too) against two caps:

	YOUTUBE_QUOTA_PER_DAY         the project's cap, all workspaces together
	                              (default 9,000: headroom under Google's 10,000)
	YOUTUBE_QUOTA_PER_WORKSPACE   one workspace's share (default 2,000), so one
	                              busy channel cannot starve every other tenant

A refusal stops the sync with google.ErrQuota rather than calling unmetered.
The day is Pacific, as Google's is. The Analytics and Reporting APIs have their
own quotas and are not charged here.

The ledger is cross-tenant by definition (the global cap sums every workspace),
so it lives on the owner pool, one row per (day, workspace), every statement
naming its workspace, serialised by a transaction-scoped advisory lock.
*/

// CostList is one Data API list call (channels, playlistItems, videos, commentThreads).
const CostList = 1

const quotaLockKey = 4_410_441

var pacific = func() *time.Location {
	if l, err := time.LoadLocation("America/Los_Angeles"); err == nil {
		return l
	}
	return time.UTC
}()

func envInt(k string, def int64) int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(k)), 10, 64); err == nil && v > 0 {
		return v
	}
	return def
}

// GlobalCap is the project's daily cap.
func GlobalCap() int64 { return envInt("YOUTUBE_QUOTA_PER_DAY", 9000) }

// WorkspaceCap is one workspace's daily share.
func WorkspaceCap() int64 { return envInt("YOUTUBE_QUOTA_PER_WORKSPACE", 2000) }

func quotaDay(now time.Time) string { return now.In(pacific).Format("2006-01-02") }

// QuotaError is a refused reservation: which cap, and how much is left.
type QuotaError struct {
	Scope     string // workspace | global
	Used, Cap int64
}

func (e *QuotaError) Error() string {
	if e.Scope == "workspace" {
		return fmt.Sprintf("this workspace has used its YouTube quota for today (%d of %d units); it resets at midnight Pacific time", e.Used, e.Cap)
	}
	return fmt.Sprintf("Moharrik's shared YouTube quota for today is used up (%d of %d units); it resets at midnight Pacific time", e.Used, e.Cap)
}

func (e *QuotaError) Is(target error) bool { return target == google.ErrQuota }

// Reserve spends units of today's budget for a workspace, or refuses.
func Reserve(ctx context.Context, a *app.App, workspaceID string, units int64) error {
	if a == nil || a.SysDB == nil {
		return errors.New("youtube: no database")
	}
	day := quotaDay(time.Now())
	tx, err := a.SysDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, quotaLockKey); err != nil {
		return err
	}
	var global, mine int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(units), 0), coalesce(sum(units) FILTER (WHERE workspace_id = $2), 0)
		FROM youtube_quota WHERE day = $1`, day, workspaceID).Scan(&global, &mine); err != nil {
		return err
	}
	if cap := WorkspaceCap(); mine+units > cap {
		return &QuotaError{Scope: "workspace", Used: mine, Cap: cap}
	}
	if cap := GlobalCap(); global+units > cap {
		return &QuotaError{Scope: "global", Used: global, Cap: cap}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO youtube_quota (day, workspace_id, units, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (day, workspace_id) DO UPDATE SET units = youtube_quota.units + EXCLUDED.units, updated_at = now()`,
		day, workspaceID, units); err != nil {
		return err
	}
	return tx.Commit()
}

// YTQuota is today's use, for the page.
type YTQuota struct {
	Day            string `json:"day" doc:"The Pacific day the budget is for."`
	WorkspaceUsed  int64  `json:"workspace_used"`
	WorkspaceLimit int64  `json:"workspace_limit"`
	// The whole project's use, so a workspace can see why it was refused
	// even with its own share left. No other workspace is named.
	GlobalUsed  int64 `json:"global_used"`
	GlobalLimit int64 `json:"global_limit"`
}

// QuotaFor reads today's use for a workspace.
func QuotaFor(ctx context.Context, db *sql.DB, workspaceID string) (YTQuota, error) {
	q := YTQuota{Day: quotaDay(time.Now()), WorkspaceLimit: WorkspaceCap(), GlobalLimit: GlobalCap()}
	err := db.QueryRowContext(ctx, `SELECT coalesce(sum(units), 0), coalesce(sum(units) FILTER (WHERE workspace_id = $2), 0)
		FROM youtube_quota WHERE day = $1`, q.Day, workspaceID).Scan(&q.GlobalUsed, &q.WorkspaceUsed)
	return q, err
}
