package trends

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

/*
The trends center's API (MH-385, MH-387): trending topics (stories), their
review workflow, the source catalog and the collection budget.

A "trending topic" here is a trend_clusters row — items from any number of
sources about one story. The older /api/w/topics are the workspace's keyword
watch lists; the page calls them keyword topics, and they feed relevance.
*/

// TrendTopicOut is one trending topic.
type TrendTopicOut struct {
	ID            string        `json:"id"`
	Day           string        `json:"day" doc:"UTC day of its latest item (YYYY-MM-DD)."`
	Title         string        `json:"title"`
	Summary       string        `json:"summary"`
	SummarySource string        `json:"summary_source" doc:"extractive (an item's own excerpt), extractive:headlines (the other outlets' headlines), or provider/model."`
	ClusterMethod string        `json:"cluster_method"`
	SourceCount   int           `json:"source_count"`
	ItemCount     int           `json:"item_count"`
	Outlets       []string      `json:"outlets"`
	SourceIDs     []string      `json:"source_ids"`
	Score         float64       `json:"score"`
	Reasons       []TrendReason `json:"reasons" doc:"Why it ranks where it does, largest part first. The score is their sum."`
	FirstSeenAt   time.Time     `json:"first_seen_at"`
	LastSeenAt    time.Time     `json:"last_seen_at"`
	State         string        `json:"state" enum:"open,saved,reviewed,dismissed"`
	ReviewedAt    *time.Time    `json:"reviewed_at"`
	DismissedAt   *time.Time    `json:"dismissed_at"`
	SavedAt       *time.Time    `json:"saved_at"`
	Items         []NewsOut     `json:"items" doc:"In the list: up to three, one per outlet first. In the detail: all of them."`
}

// States the list filters by.
const (
	StateOpen      = "open" // not reviewed, dismissed or saved: still to handle
	StateSaved     = "saved"
	StateReviewed  = "reviewed"
	StateDismissed = "dismissed"
	StateAll       = "all"
)

func stateOf(s TrendTopicOut) string {
	switch {
	case s.DismissedAt != nil:
		return StateDismissed
	case s.SavedAt != nil:
		return StateSaved
	case s.ReviewedAt != nil:
		return StateReviewed
	}
	return StateOpen
}

const storyColumns = `id, day, title, summary, summary_source, cluster_method, source_count, item_count, outlets, source_ids,
	score, reasons::text, first_seen_at, last_seen_at, reviewed_at, dismissed_at, saved_at`

func scanStory(row interface{ Scan(...any) error }) (TrendTopicOut, error) {
	var s TrendTopicOut
	var day time.Time
	var outlets, sources pgtext.Array
	var reasons string
	var reviewed, dismissed, saved sql.NullTime
	if err := row.Scan(&s.ID, &day, &s.Title, &s.Summary, &s.SummarySource, &s.ClusterMethod, &s.SourceCount, &s.ItemCount,
		&outlets, &sources, &s.Score, &reasons, &s.FirstSeenAt, &s.LastSeenAt, &reviewed, &dismissed, &saved); err != nil {
		return s, err
	}
	s.Day = day.UTC().Format("2006-01-02")
	s.Outlets, s.SourceIDs = []string(outlets), []string(sources)
	if s.Outlets == nil {
		s.Outlets = []string{}
	}
	if s.SourceIDs == nil {
		s.SourceIDs = []string{}
	}
	s.Reasons = []TrendReason{}
	_ = json.Unmarshal([]byte(reasons), &s.Reasons)
	for i := range s.Reasons {
		if s.Reasons[i].Detail == nil {
			s.Reasons[i].Detail = []string{}
		}
	}
	s.ReviewedAt, s.DismissedAt, s.SavedAt = timep(reviewed), timep(dismissed), timep(saved)
	s.State = stateOf(s)
	s.Items = []NewsOut{}
	return s, nil
}

type storyCursor struct {
	Day   string
	Score float64
	ID    string
}

func encodeStoryCursor(c storyCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.Day + "|" + strconv.FormatFloat(c.Score, 'g', -1, 64) + "|" + c.ID))
}

func decodeStoryCursor(s string) (storyCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return storyCursor{}, false
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 || parts[2] == "" {
		return storyCursor{}, false
	}
	if _, err := time.Parse("2006-01-02", parts[0]); err != nil {
		return storyCursor{}, false
	}
	score, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return storyCursor{}, false
	}
	return storyCursor{parts[0], score, parts[2]}, true
}

// TrendCounts are the list's state tabs.
type TrendCounts struct {
	Open      int64 `json:"open"`
	Saved     int64 `json:"saved"`
	Reviewed  int64 `json:"reviewed"`
	Dismissed int64 `json:"dismissed"`
	All       int64 `json:"all"`
}

// TrendDay is one day with stories on it.
type TrendDay struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// TrendTopicPage is one page of the list.
type TrendTopicPage struct {
	Topics     []TrendTopicOut `json:"topics"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Counts     TrendCounts     `json:"counts" doc:"Per state, with the day/source/search filters applied."`
	Days       []TrendDay      `json:"days" doc:"The last 30 days that have stories in this state, newest first."`
}

// stateClause is the WHERE fragment for a state; "" for all.
func stateClause(state string) (string, bool) {
	switch state {
	case "", StateOpen:
		return "reviewed_at IS NULL AND dismissed_at IS NULL AND saved_at IS NULL", true
	case StateSaved:
		return "saved_at IS NOT NULL", true
	case StateReviewed:
		return "reviewed_at IS NOT NULL AND dismissed_at IS NULL", true
	case StateDismissed:
		return "dismissed_at IS NOT NULL", true
	case StateAll:
		return "", true
	}
	return "", false
}

// attachItems puts the stories' items on them: at most perStory each (0 = all),
// one per outlet first so the three links in the list are three voices.
func attachItems(ctx context.Context, db *sql.DB, wsID string, stories []TrendTopicOut, perStory int) error {
	if len(stories) == 0 {
		return nil
	}
	ids := make([]string, len(stories))
	index := map[string]int{}
	for i, s := range stories {
		ids[i] = s.ID
		index[s.ID] = i
	}
	list, err := queryNews(ctx, db, `SELECT id, source_id, source_name, outlet, provider, title, url, summary, author, topic_ids, cluster_id,
		       rank, points, comments, published_at, saved_at
		FROM news_items WHERE workspace_id = $1 AND cluster_id = ANY ($2::text[])
		ORDER BY published_at DESC, id DESC`, wsID, pgtext.Literal(ids))
	if err != nil {
		return err
	}
	grouped := map[string][]NewsOut{}
	for _, n := range list {
		if n.ClusterID != nil {
			grouped[*n.ClusterID] = append(grouped[*n.ClusterID], n)
		}
	}
	for id, items := range grouped {
		i := index[id]
		if perStory <= 0 {
			stories[i].Items = items
			continue
		}
		var firsts, rest []NewsOut
		seen := map[string]bool{}
		for _, n := range items {
			if k := strings.ToLower(n.Outlet); !seen[k] {
				seen[k] = true
				firsts = append(firsts, n)
			} else {
				rest = append(rest, n)
			}
		}
		picked := append(firsts, rest...)
		if len(picked) > perStory {
			picked = picked[:perStory]
		}
		stories[i].Items = picked
	}
	return nil
}

func getStory(ctx context.Context, db *sql.DB, wsID, id string) (TrendTopicOut, error) {
	return scanStory(db.QueryRowContext(ctx, `SELECT `+storyColumns+` FROM trend_clusters WHERE workspace_id = $1 AND id = $2`, wsID, id))
}

// TrendOutlet is one outlet in a story, with how many of its items it carried.
type TrendOutlet struct {
	Outlet string `json:"outlet"`
	Count  int    `json:"count"`
}

// TrendSettings is the workspace's collection budget and what it is spending.
type TrendSettings struct {
	FetchBudgetPerDay int    `json:"fetch_budget_per_day"`
	FetchesToday      int    `json:"fetches_today"`
	BudgetDay         string `json:"budget_day" doc:"The UTC day fetches_today counts."`
	// ScheduledPerDay is what the enabled sources' intervals ask for: a source
	// every 6 hours is 4 fetches a day.
	ScheduledPerDay int `json:"scheduled_per_day"`
	MaxBudget       int `json:"max_budget"`
}

func getSettings(ctx context.Context, db *sql.DB, wsID string) (TrendSettings, error) {
	st := TrendSettings{FetchBudgetPerDay: DefaultBudget, MaxBudget: MaxBudget, BudgetDay: time.Now().UTC().Format("2006-01-02")}
	var day time.Time
	err := db.QueryRowContext(ctx, `SELECT fetch_budget_per_day, budget_day,
		CASE WHEN budget_day = (now() AT TIME ZONE 'UTC')::date THEN fetches_today ELSE 0 END
		FROM trend_settings WHERE workspace_id = $1`, wsID).Scan(&st.FetchBudgetPerDay, &day, &st.FetchesToday)
	if err != nil && !notFound(err) {
		return st, err
	}
	err = db.QueryRowContext(ctx, `SELECT COALESCE(sum(1440 / interval_minutes), 0) FROM feed_sources WHERE workspace_id = $1 AND enabled`,
		wsID).Scan(&st.ScheduledPerDay)
	return st, err
}

// registerStories mounts the trending topics, the catalog and the budget.
func registerStories(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-trend-topics", Method: http.MethodGet, Path: "/api/w/trends",
		Summary: "Trending topics: stories clustered across sources, newest day first and best first, a page at a time",
		Tags:    tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		State  string `query:"state" enum:"open,saved,reviewed,dismissed,all" doc:"Default open: not yet reviewed, dismissed or saved."`
		Day    string `query:"day" doc:"YYYY-MM-DD (UTC)."`
		Source string `query:"source" doc:"A source id: stories it carried."`
		Q      string `query:"q" doc:"Title or summary contains."`
		Cursor string `query:"cursor" doc:"next_cursor from the previous page."`
		Limit  int    `query:"limit" minimum:"1" maximum:"100" doc:"Default 20."`
	}) (*body[TrendTopicPage], error) {
		page, err := ListTrendTopics(ctx, a, TopicFilter{State: in.State, Day: in.Day, SourceID: in.Source, Q: in.Q, Cursor: in.Cursor, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-trend-topic", Method: http.MethodGet, Path: "/api/w/trends/{id}",
		Summary: "One trending topic with every linked item and outlet", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[TrendTopicDetail], error) {
		d, err := GetTrendTopic(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-trend-topic", Method: http.MethodPatch, Path: "/api/w/trends/{id}",
		Summary: "Review, dismiss or save a trending topic (or undo any of them)", Tags: tags,
		Description: "Saving takes a topic out of dismissed, and dismissing takes it out of saved. Reviewed is " +
			"independent: a saved topic can also be reviewed.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body TopicReview
	}) (*body[struct {
		Topic TrendTopicOut `json:"topic"`
	}], error) {
		t, err := ReviewTrendTopic(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Topic TrendTopicOut `json:"topic"`
		}{*t}), nil
	})

	// ---- the catalog --------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-feed-catalog", Method: http.MethodGet, Path: "/api/w/feed_catalog",
		Summary: "Sources a workspace can add in one click, and which it already has", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Entries []CatalogOut `json:"entries"`
	}], error) {
		list, err := ListCatalog(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Entries []CatalogOut `json:"entries"`
		}{list}), nil
	})

	// ---- the budget ---------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-trend-settings", Method: http.MethodGet, Path: "/api/w/trend_settings",
		Summary: "The workspace's daily collection budget and what it has spent today", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Settings TrendSettings `json:"settings"`
	}], error) {
		st, err := GetBudget(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Settings TrendSettings `json:"settings"`
		}{*st}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-trend-settings", Method: http.MethodPatch, Path: "/api/w/trend_settings",
		Summary: "Change the daily collection budget", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			FetchBudgetPerDay int `json:"fetch_budget_per_day" minimum:"1" maximum:"500" doc:"Fetches a day across every source."`
		}
	}) (*body[struct {
		Settings TrendSettings `json:"settings"`
	}], error) {
		st, err := SetBudget(ctx, a, in.Body.FetchBudgetPerDay)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Settings TrendSettings `json:"settings"`
		}{*st}), nil
	})
}
