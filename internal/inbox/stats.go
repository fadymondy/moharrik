package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
Inbox stats (MH-383): per channel, over a window, straight from SQL.

  - conversations: started in the window.
  - first response: from the customer's first message to the team's first
    reply after it, for the conversations above that have one. Median and
    mean; a thread nobody has answered yet is counted in `awaiting`, not
    averaged in as zero.
  - resolution: of the conversations above, how many are closed now, and the
    median time from start to close. Threads closed before closed_at existed
    count as resolved but have no time, so they are left out of the median
    rather than guessed.

A window with no conversations answers zero counts and null times; the UI says
"no data yet" rather than drawing zeros that look like a measurement.
*/

type ChannelStats struct {
	Channel                    string   `json:"channel" doc:"chat, email, whatsapp, or all."`
	Conversations              int64    `json:"conversations" doc:"Started in the window."`
	Answered                   int64    `json:"answered" doc:"Of those, how many the team has replied to."`
	Awaiting                   int64    `json:"awaiting" doc:"Of those, how many are still waiting for a first reply."`
	FirstResponseMedianSeconds *float64 `json:"first_response_median_seconds" doc:"Null when nothing was answered."`
	FirstResponseMeanSeconds   *float64 `json:"first_response_mean_seconds"`
	Resolved                   int64    `json:"resolved" doc:"Of those, how many are closed."`
	ResolutionRate             *float64 `json:"resolution_rate" doc:"resolved / conversations, 0..1; null when there were none."`
	ResolutionMedianSeconds    *float64 `json:"resolution_median_seconds" doc:"Start to close, over the resolved ones whose close time is known."`
}

type InboxStats struct {
	Days     int            `json:"days"`
	Since    time.Time      `json:"since"`
	Channels []ChannelStats `json:"channels" doc:"Always chat, email and whatsapp, in that order."`
	Total    ChannelStats   `json:"total"`
	// Right now, whatever the window.
	OpenNow    int64 `json:"open_now"`
	SnoozedNow int64 `json:"snoozed_now"`
	UnreadNow  int64 `json:"unread_now"`
}

const statsSQL = `
	WITH conv AS (
		SELECT c.id, c.channel, c.status, c.created_at, c.closed_at,
		       (SELECT min(m.created_at) FROM messages m
		         WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id AND m.direction = 'in') AS first_in
		FROM conversations c
		WHERE c.workspace_id = $1 AND c.created_at >= $2
	), timed AS (
		SELECT conv.*,
		       (SELECT min(m.created_at) FROM messages m
		         WHERE m.workspace_id = $1 AND m.conversation_id = conv.id AND m.direction = 'out'
		           AND conv.first_in IS NOT NULL AND m.created_at >= conv.first_in) AS first_out
		FROM conv
	)
	SELECT coalesce(channel, 'all'),
	       count(*),
	       count(first_out),
	       count(*) FILTER (WHERE first_in IS NOT NULL AND first_out IS NULL),
	       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM first_out - first_in)) FILTER (WHERE first_out IS NOT NULL),
	       avg(extract(epoch FROM first_out - first_in)) FILTER (WHERE first_out IS NOT NULL),
	       count(*) FILTER (WHERE status = 'closed'),
	       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM closed_at - created_at)) FILTER (WHERE status = 'closed' AND closed_at IS NOT NULL)
	FROM timed
	GROUP BY ROLLUP (channel)`

// Stats computes the inbox stats for a workspace over the last `days` days.
// db is the app pool and ctx carries the workspace.
func Stats(ctx context.Context, db *sql.DB, wsID string, days int, now time.Time) (*InboxStats, error) {
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	res := &InboxStats{Days: days, Since: since, Total: ChannelStats{Channel: "all"}}
	by := map[string]ChannelStats{}
	rows, err := db.QueryContext(ctx, statsSQL, wsID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ChannelStats
		var frMed, frMean, resMed sql.NullFloat64
		if err := rows.Scan(&s.Channel, &s.Conversations, &s.Answered, &s.Awaiting, &frMed, &frMean, &s.Resolved, &resMed); err != nil {
			return nil, err
		}
		s.FirstResponseMedianSeconds = floatp(frMed)
		s.FirstResponseMeanSeconds = floatp(frMean)
		s.ResolutionMedianSeconds = floatp(resMed)
		if s.Conversations > 0 {
			r := float64(s.Resolved) / float64(s.Conversations)
			s.ResolutionRate = &r
		}
		by[s.Channel] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, ch := range []string{"chat", "email", "whatsapp"} {
		s, ok := by[ch]
		if !ok {
			s = ChannelStats{Channel: ch}
		}
		res.Channels = append(res.Channels, s)
	}
	if s, ok := by["all"]; ok {
		res.Total = s
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE status = 'open'), count(*) FILTER (WHERE status = 'snoozed'), count(*) FILTER (WHERE unread)
		FROM conversations WHERE workspace_id = $1`, wsID).Scan(&res.OpenNow, &res.SnoozedNow, &res.UnreadNow); err != nil {
		return nil, err
	}
	return res, nil
}

func floatp(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func registerStats(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-inbox-stats", Method: http.MethodGet, Path: "/api/w/inbox/stats",
		Summary: "Conversations per channel, first-response time and resolution over a window", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Days int `query:"days" minimum:"1" maximum:"365" doc:"The window, in days (default 30)."`
	}) (*body[InboxStats], error) {
		st, err := InboxStatsFor(ctx, a, in.Days)
		if err != nil {
			return nil, err
		}
		return out(*st), nil
	})
}
