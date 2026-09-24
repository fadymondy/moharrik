// Package overview is the workspace's front page (MH-462): one aggregate read
// of what is happening across leads, the pipeline, the inbox, notifications,
// marketing, trends and research, plus what is still to be set up.
//
// Every figure is computed in SQL, on the app (RLS) pool, with workspace_id in
// every predicate — the same two locks every other workspace handler holds.
// Nothing is estimated or filled in: a section the caller's role cannot read is
// null (not zero), and money is only ever summed in the workspace's currency,
// with the deals in any other currency counted separately rather than
// converted.
//
// The whole read runs in one REPEATABLE READ, READ ONLY transaction, so the
// numbers on the page come from a single snapshot and agree with each other.
//
// Build takes a *sql.DB, a workspace id, a role and a clock rather than a
// request, so the REST route and the tests share it.
package overview

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

// Latest is how many rows each "latest" list carries.
const Latest = 5

// Overview is the whole front page. A nil section is one the caller's role
// may not read; the UI leaves it out rather than showing it empty.
type Overview struct {
	Currency string `json:"currency" doc:"The workspace's currency; every money figure here is in it."`
	Timezone string `json:"timezone" doc:"The workspace's time zone; 'today' and 'this month' are in it."`

	Today         Today              `json:"today"`
	Pipeline      *Pipeline          `json:"pipeline" doc:"Null without contacts:read."`
	Leads         []LeadItem         `json:"leads" doc:"The newest leads. Null without leads:read."`
	Conversations []ConversationItem `json:"conversations" doc:"Unread first, then most recent. Null without inbox:read."`
	Notifications []NotificationItem `json:"notifications" doc:"The newest notifications. Null without notifications:read."`
	Marketing     *Marketing         `json:"marketing" doc:"Null without campaigns:read."`
	Trends        *Trends            `json:"trends" doc:"Null without trends:read."`
	Setup         []SetupItem        `json:"setup" doc:"Only the items the caller's role can see."`
	Traffic       *Traffic           `json:"traffic" doc:"Null without analytics:read, or when no GA4 property or Search Console site is enabled (MH-444/445)."`
	YouTube       []YouTubeChannel   `json:"youtube" doc:"Each enabled YouTube channel. Null without analytics:read or with none enabled (MH-441/442)."`
	// LeadsDaily/ConversationsDaily (MH-567): the Overview's own charts. Zero-
	// filled every day in the 28-day window — a lead or a conversation is this
	// workspace's own data, so a day with none really is 0, never a dash.
	LeadsDaily         []CountDay        `json:"leads_daily" doc:"Leads created per day, the last 28 days ending today, zero-filled. Null without leads:read."`
	ConversationsDaily []ConversationDay `json:"conversations_daily" doc:"Conversations started per day by channel, the last 28 days ending today, zero-filled. Null without inbox:read."`
}

// CountDay is one day of a zero-filled count series.
type CountDay struct {
	Date  string `json:"date" doc:"YYYY-MM-DD, workspace time zone."`
	Count int64  `json:"count"`
}

// ConversationDay is one day of conversations started, split by channel.
type ConversationDay struct {
	Date     string `json:"date" doc:"YYYY-MM-DD, workspace time zone."`
	Chat     int64  `json:"chat"`
	Email    int64  `json:"email"`
	WhatsApp int64  `json:"whatsapp"`
}

// YouTubeChannel is one enabled channel on the front page: its counters from
// the last sync, and its views over the last 28 days from YouTube Analytics
// when they have been fetched. Nothing fetched is null, never 0.
type YouTubeChannel struct {
	ResourceID  string     `json:"resource_id"`
	Title       string     `json:"title"`
	Subscribers *int64     `json:"subscribers" doc:"Null until synced, or when the channel hides it."`
	Videos      *int64     `json:"videos"`
	Views28     *int64     `json:"views_28" doc:"YouTube Analytics views over the last 28 days; null until fetched."`
	Views28To   *string    `json:"views_28_to" doc:"The last day of those 28 (YYYY-MM-DD, Pacific)."`
	SyncedAt    *time.Time `json:"synced_at"`
	LastError   string     `json:"last_error"`
}

// Traffic is the last 28 days of each enabled GA4 property and Search
// Console site, from the stored snapshots (the collector refreshes them
// nightly; opening the Analytics page refreshes them hourly). A figure that
// has never been fetched is null, never 0.
type Traffic struct {
	Properties []TrafficProperty `json:"properties"`
	Sites      []TrafficSite     `json:"sites"`
	// Summary (MH-567) is the one-card rollup that replaced a row per
	// property: totals across every source, each with its change vs the
	// previous 28 days where that comparison is known, a daily trend and the
	// top properties by users. Never null when Traffic itself is not null.
	Summary *TrafficSummary `json:"summary"`
}

// TrafficSummary is the cross-source analytics summary (MH-567): one set of
// totals for the last 28 days, each with its % change vs the 28 days before
// that when the history to compare against actually exists. A total that has
// never been fetched is null; a delta that cannot be computed (no prior
// history, or a source with nothing enabled) is null too — never a fake 0%.
type TrafficSummary struct {
	PeriodStart string `json:"period_start" doc:"YYYY-MM-DD, workspace time zone."`
	PeriodEnd   string `json:"period_end" doc:"YYYY-MM-DD, workspace time zone."`

	GA4           *GA4Summary           `json:"ga4" doc:"Null with no enabled GA4 property."`
	SearchConsole *SearchConsoleSummary `json:"search_console" doc:"Null with no enabled Search Console site."`
	YouTube       *YouTubeSummary       `json:"youtube" doc:"Null with no enabled YouTube channel."`

	// Daily is the traffic-over-time chart: GA4 users/sessions and Search
	// Console clicks, summed across every enabled source, one point per day.
	// A source with nothing fetched for a day is null that day, not 0.
	Daily []TrafficDay `json:"daily"`

	// TopProperties: up to 5 GA4 properties by users, replacing the old
	// one-row-per-property list. Links to /analytics.
	TopProperties []TopProperty `json:"top_properties"`
}

// GA4Summary is GA4's part of the rollup: totals from the stored 28-day
// snapshot (ga4_reports, GA4's own dedup'd totals) summed across every
// enabled property, plus whatever the snapshot's payload carries beyond
// users/sessions/views. The % change is computed separately, from the daily
// history (ga4_daily) on a like-for-like basis (sum of days vs sum of days),
// which is why the two can disagree very slightly with the headline total.
type GA4Summary struct {
	PropertiesCount int `json:"properties_count"`

	Users    *int64   `json:"users"`
	UsersPct *float64 `json:"users_delta_pct" doc:"% change vs the previous 28 days; null when that period's daily history is not (yet) stored."`

	Sessions    *int64   `json:"sessions"`
	SessionsPct *float64 `json:"sessions_delta_pct"`

	Views    *int64   `json:"views"`
	ViewsPct *float64 `json:"views_delta_pct"`

	EngagementRate    *float64 `json:"engagement_rate" doc:"engaged sessions / sessions, 0..1, summed across properties."`
	AvgSessionSeconds *float64 `json:"avg_session_seconds" doc:"Engagement time per session, summed across properties."`
	KeyEvents         *int64   `json:"key_events"`
}

// SearchConsoleSummary is Search Console's part of the rollup, summed across
// every enabled site (gsc_search_totals_daily, which already carries
// anonymised queries — the site's real totals).
type SearchConsoleSummary struct {
	SitesCount int `json:"sites_count"`

	Clicks    *int64   `json:"clicks"`
	ClicksPct *float64 `json:"clicks_delta_pct"`

	Impressions    *int64   `json:"impressions"`
	ImpressionsPct *float64 `json:"impressions_delta_pct"`

	// CTR and AvgPosition are impression-weighted, the same rule the
	// Search Console page itself uses when it sums more than one row.
	CTR         *float64 `json:"ctr"`
	AvgPosition *float64 `json:"avg_position"`
}

// YouTubeSummary is YouTube's part of the rollup. Subscribers and video
// counts are the channels' current counters, not a 28-day window, so there
// is no previous period to compare against — never a delta here.
type YouTubeSummary struct {
	ChannelsCount int    `json:"channels_count"`
	Views28       *int64 `json:"views_28"`
	Subscribers   *int64 `json:"subscribers" doc:"Null when every enabled channel hides its subscriber count."`
}

// TopProperty is one row of the top-properties breakdown.
type TopProperty struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind" enum:"ga4_property"`
	Users      *int64 `json:"users"`
}

// TrafficDay is one day of the traffic-over-time chart.
type TrafficDay struct {
	Date     string `json:"date" doc:"YYYY-MM-DD, workspace time zone."`
	Users    *int64 `json:"users" doc:"GA4 activeUsers, summed across enabled properties. Null: no property had data fetched for this day."`
	Sessions *int64 `json:"sessions"`
	Clicks   *int64 `json:"clicks" doc:"Search Console clicks, summed across enabled sites. Null: not imported for this day."`
}

// TrafficSite is one Search Console site's clicks over the 28 days ending on
// its newest day with data (Search Console lags two to three days).
type TrafficSite struct {
	ResourceID  string  `json:"resource_id"`
	Name        string  `json:"name"`
	Clicks      *int64  `json:"clicks" doc:"Null until the first import."`
	Impressions *int64  `json:"impressions"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	LastError   string  `json:"last_error"`
}

// TrafficProperty is one GA4 property's 28-day totals.
type TrafficProperty struct {
	ResourceID string     `json:"resource_id"`
	Name       string     `json:"name"`
	Users      *int64     `json:"users" doc:"GA4 activeUsers over the 28 days; null until the first fetch."`
	Sessions   *int64     `json:"sessions"`
	StartDate  *string    `json:"start_date"`
	EndDate    *string    `json:"end_date"`
	FetchedAt  *time.Time `json:"fetched_at"`
	LastError  string     `json:"last_error"`
}

// Today is the stat strip. Each field is null when the role cannot read it.
type Today struct {
	Leads               *LeadCounts   `json:"leads" doc:"Leads per status, whole workspace. Null without leads:read."`
	LeadsReceivedToday  *int64        `json:"leads_received_today" doc:"Leads that arrived since midnight in the workspace's time zone."`
	UnreadConversations *int64        `json:"unread_conversations" doc:"Null without inbox:read."`
	UnreadNotifications *int64        `json:"unread_notifications" doc:"Null without notifications:read."`
	Research            *ResearchLive `json:"research" doc:"Null without research:read."`
}

// LeadCounts is how many leads are in each status. Every status is present.
type LeadCounts map[string]int64

// ResearchLive says whether a research run is going right now.
type ResearchLive struct {
	Live   int64        `json:"live" doc:"Runs queued or running."`
	Latest *ResearchRef `json:"latest" doc:"The newest live run, or null when none is going."`
}

// ResearchRef points at one run.
type ResearchRef struct {
	ID     string `json:"id"`
	Topic  string `json:"topic"`
	Status string `json:"status" enum:"queued,running"`
}

// Pipeline is the open value per stage and what closed this month.
type Pipeline struct {
	Stages []StageValue `json:"stages" doc:"Open stages in board order."`
	// Open deals in a currency other than the workspace's are counted in the
	// stage's deal_count but NOT in its value: summing money across currencies
	// would be a number that means nothing. The deals board draws the same line.
	OpenDeals          int64      `json:"open_deals"`
	OpenValueMinor     int64      `json:"open_value_minor"`
	OtherCurrencyDeals int64      `json:"other_currency_deals" doc:"Open deals left out of the values because they are in another currency."`
	WonThisMonth       WonMonth   `json:"won_this_month"`
	NextCloses         []DealItem `json:"next_closes" doc:"Open deals expected to close today or later, soonest first."`
}

// StageValue is one open stage's column total.
type StageValue struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DealCount  int64  `json:"deal_count"`
	ValueMinor int64  `json:"value_minor" doc:"Minor units, workspace currency only."`
}

// WonMonth is the calendar month so far, in the workspace's time zone.
type WonMonth struct {
	Since              time.Time `json:"since" doc:"The start of this calendar month in the workspace's time zone."`
	Count              int64     `json:"count"`
	ValueMinor         int64     `json:"value_minor" doc:"Workspace currency only."`
	OtherCurrencyDeals int64     `json:"other_currency_deals"`
}

// DealItem is one upcoming close.
type DealItem struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	ValueMinor    int64   `json:"value_minor"`
	Currency      string  `json:"currency"`
	StageName     string  `json:"stage_name"`
	CompanyName   *string `json:"company_name"`
	ExpectedClose string  `json:"expected_close" doc:"YYYY-MM-DD."`
}

// LeadItem is one row of the latest leads.
type LeadItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	Company     string          `json:"company"`
	Status      string          `json:"status" enum:"new,contacted,qualified,converted,spam"`
	Origin      string          `json:"origin"`
	Attribution LeadAttribution `json:"attribution"`
	CreatedAt   time.Time       `json:"created_at"`
}

// LeadAttribution is the part of a lead's attribution the list shows: the
// same inputs the Leads page turns into "where it came from".
type LeadAttribution struct {
	UtmSource string `json:"utm_source"`
	UtmMedium string `json:"utm_medium"`
	Referrer  string `json:"referrer"`
	Gclid     string `json:"gclid"`
}

// ConversationItem is one row of the inbox list.
type ConversationItem struct {
	ID            string    `json:"id"`
	Channel       string    `json:"channel" enum:"chat,email,whatsapp"`
	Status        string    `json:"status" enum:"open,snoozed,closed"`
	CustomerName  string    `json:"customer_name"`
	CustomerEmail string    `json:"customer_email"`
	CustomerPhone string    `json:"customer_phone"`
	Subject       string    `json:"subject"`
	Unread        bool      `json:"unread"`
	Preview       string    `json:"preview" doc:"The last message, trimmed to 140 characters."`
	LastMessageAt time.Time `json:"last_message_at"`
}

// NotificationItem is one row of the notification list.
type NotificationItem struct {
	ID         string    `json:"id"`
	Severity   string    `json:"severity" enum:"info,warning,error"`
	Title      string    `json:"title"`
	SourceName string    `json:"source_name"`
	URL        string    `json:"url"`
	Read       bool      `json:"read"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Marketing is the lists' audience and the campaigns still to send.
type Marketing struct {
	Lists      []ListCount `json:"lists" doc:"Null without contacts:read."`
	DraftCount int64       `json:"draft_count"`
	Drafts     []DraftItem `json:"drafts" doc:"The newest drafts, up to three."`
}

// ListCount is one list's subscribed and pending consent.
type ListCount struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Subscribed int64  `json:"subscribed"`
	Pending    int64  `json:"pending"`
}

// DraftItem is one unsent campaign.
type DraftItem struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Channel   string    `json:"channel" enum:"email,whatsapp"`
	ListName  string    `json:"list_name"`
	CreatedAt time.Time `json:"created_at"`
}

// Trends is the newest items that matched one of the workspace's topics.
type Trends struct {
	Topics  int64       `json:"topics" doc:"Topics the workspace has, enabled or not."`
	Matched int64       `json:"matched" doc:"Collected items that matched at least one topic."`
	Items   []TrendItem `json:"items"`
}

// TrendItem is one matched item.
type TrendItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	SourceName  string    `json:"source_name"`
	Topics      []string  `json:"topics" doc:"The names of the topics it matched."`
	PublishedAt time.Time `json:"published_at"`
}

// Setup keys, in the order the checklist lists them.
const (
	SetupInquiryForm = "inquiry_form"
	SetupSMTP        = "smtp"
	SetupChatWidget  = "chat_widget"
	SetupWhatsApp    = "whatsapp"
	SetupGoogle      = "google"
	SetupBrowser     = "browser"
)

// SetupItem is one thing the workspace can connect.
type SetupItem struct {
	Key  string `json:"key" enum:"inquiry_form,smtp,chat_widget,whatsapp,google,browser"`
	Done bool   `json:"done"`
	// Available is false when THIS server cannot do it at all (no WhatsApp
	// server, no Google OAuth client configured): the checklist says so rather
	// than linking to a page whose button cannot work.
	Available bool `json:"available"`
}

// Env is what the deployment can do, resolved by the caller (the route reads
// the environment; tests pass what they want).
type Env struct {
	WhatsAppAvailable bool
	GoogleAvailable   bool
}

// queryer is what Build needs: a *sql.Tx in practice.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Build reads the whole overview for one workspace as seen by one role. ctx
// must carry the workspace (tenant.WithWorkspace) for the RLS pool.
func Build(ctx context.Context, db *sql.DB, wsID, role string, env Env, now time.Time) (*Overview, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	o := &Overview{}
	if err := tx.QueryRowContext(ctx, `SELECT currency, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&o.Currency, &o.Timezone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil {
		// An unknown zone name would otherwise fail the whole page. UTC is
		// what the workspace defaults to, and the response says which it used.
		loc, o.Timezone = time.UTC, "UTC"
	}
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)

	can := func(action string) bool { return authz.Can(role, action) }
	b := builder{ctx: ctx, q: tx, ws: wsID}

	// The charts' own 28-day window: today and the 27 days before it, in the
	// workspace's own time zone — the same "today" the stat strip uses.
	chartEnd := midnight
	chartStart := midnight.AddDate(0, 0, -27)

	if can(authz.LeadsRead) {
		o.Today.Leads, o.Today.LeadsReceivedToday = b.leadCounts(midnight)
		o.Leads = b.latestLeads()
		o.LeadsDaily = b.leadsDaily(o.Timezone, chartStart, chartEnd)
	}
	if can(authz.InboxRead) {
		o.Today.UnreadConversations = b.count(`SELECT count(*) FROM conversations WHERE workspace_id = $1 AND unread`)
		o.Conversations = b.latestConversations()
		o.ConversationsDaily = b.conversationsDaily(o.Timezone, chartStart, chartEnd)
	}
	if can(authz.NotificationsRead) {
		o.Today.UnreadNotifications = b.count(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND read_at IS NULL`)
		o.Notifications = b.latestNotifications()
	}
	if can(authz.ResearchRead) {
		o.Today.Research = b.research()
	}
	if can(authz.ContactsRead) {
		o.Pipeline = b.pipeline(o.Currency, monthStart, midnight)
	}
	if can(authz.CampaignsRead) {
		o.Marketing = b.marketing(can(authz.ContactsRead))
	}
	if can(authz.TrendsRead) {
		o.Trends = b.trends()
	}
	o.Setup = b.setup(can, env)
	if can(authz.AnalyticsRead) {
		// The analytics summary's own 28-day window ends yesterday (GA4/Search
		// Console lag today's partial day), unlike the leads/conversations
		// charts above which run through today.
		periodEnd := midnight.AddDate(0, 0, -1)
		periodStart := periodEnd.AddDate(0, 0, -27)
		prevEnd := periodStart.AddDate(0, 0, -1)
		prevStart := prevEnd.AddDate(0, 0, -27)
		o.Traffic = b.traffic(periodStart, periodEnd, prevStart, prevEnd)
		o.YouTube = b.youtube()
	}

	if b.err != nil {
		return nil, b.err
	}
	return o, nil
}

// ErrNotFound: the workspace is gone (or not visible through RLS).
var ErrNotFound = errors.New("workspace not found")

// builder runs the section queries and keeps the first error, so Build reads
// as a list of sections rather than a ladder of error checks.
type builder struct {
	ctx context.Context
	q   queryer
	ws  string
	err error
}

func (b *builder) fail(err error) bool {
	if err != nil && b.err == nil {
		b.err = err
	}
	return b.err != nil
}

func (b *builder) count(q string, args ...any) *int64 {
	if b.err != nil {
		return nil
	}
	var n int64
	if b.fail(b.q.QueryRowContext(b.ctx, q, append([]any{b.ws}, args...)...).Scan(&n)) {
		return nil
	}
	return &n
}

func (b *builder) exists(q string) bool {
	if b.err != nil {
		return false
	}
	var ok bool
	b.fail(b.q.QueryRowContext(b.ctx, q, b.ws).Scan(&ok))
	return ok
}

// each runs q and hands every row to scan.
func (b *builder) each(q string, args []any, scan func(*sql.Rows) error) {
	if b.err != nil {
		return
	}
	rows, err := b.q.QueryContext(b.ctx, q, append([]any{b.ws}, args...)...)
	if b.fail(err) {
		return
	}
	defer rows.Close()
	for rows.Next() {
		if b.fail(scan(rows)) {
			return
		}
	}
	b.fail(rows.Err())
}

var leadStatuses = []string{"new", "contacted", "qualified", "converted", "spam"}

func (b *builder) leadCounts(midnight time.Time) (*LeadCounts, *int64) {
	counts := LeadCounts{}
	for _, s := range leadStatuses {
		counts[s] = 0
	}
	b.each(`SELECT status, count(*) FROM leads WHERE workspace_id = $1 GROUP BY status`, nil, func(r *sql.Rows) error {
		var s string
		var n int64
		if err := r.Scan(&s, &n); err != nil {
			return err
		}
		counts[s] = n
		return nil
	})
	today := b.count(`SELECT count(*) FROM leads WHERE workspace_id = $1 AND created_at >= $2`, midnight)
	if b.err != nil {
		return nil, nil
	}
	return &counts, today
}

func (b *builder) latestLeads() []LeadItem {
	list := []LeadItem{}
	b.each(`
		SELECT id, name, email, company, status, origin, utm_source, utm_medium, referrer, gclid, created_at
		FROM leads WHERE workspace_id = $1
		ORDER BY created_at DESC, id LIMIT $2`, []any{Latest}, func(r *sql.Rows) error {
		var l LeadItem
		a := &l.Attribution
		if err := r.Scan(&l.ID, &l.Name, &l.Email, &l.Company, &l.Status, &l.Origin, &a.UtmSource, &a.UtmMedium, &a.Referrer, &a.Gclid, &l.CreatedAt); err != nil {
			return err
		}
		list = append(list, l)
		return nil
	})
	return list
}

func (b *builder) latestConversations() []ConversationItem {
	list := []ConversationItem{}
	b.each(`
		SELECT c.id, c.channel, c.status, c.customer_name, c.customer_email, c.customer_phone, c.subject, c.unread, c.last_message_at,
		       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		                 ORDER BY m.created_at DESC LIMIT 1), '')
		FROM conversations c WHERE c.workspace_id = $1
		ORDER BY c.unread DESC, c.last_message_at DESC, c.id LIMIT $2`, []any{Latest}, func(r *sql.Rows) error {
		var c ConversationItem
		if err := r.Scan(&c.ID, &c.Channel, &c.Status, &c.CustomerName, &c.CustomerEmail, &c.CustomerPhone, &c.Subject, &c.Unread, &c.LastMessageAt, &c.Preview); err != nil {
			return err
		}
		c.Preview = trim(c.Preview, 140)
		list = append(list, c)
		return nil
	})
	return list
}

func (b *builder) latestNotifications() []NotificationItem {
	list := []NotificationItem{}
	b.each(`
		SELECT id, severity, title, source_name, url, read_at IS NOT NULL, occurred_at
		FROM notification_items WHERE workspace_id = $1
		ORDER BY occurred_at DESC, id LIMIT $2`, []any{Latest}, func(r *sql.Rows) error {
		var n NotificationItem
		if err := r.Scan(&n.ID, &n.Severity, &n.Title, &n.SourceName, &n.URL, &n.Read, &n.OccurredAt); err != nil {
			return err
		}
		list = append(list, n)
		return nil
	})
	return list
}

func (b *builder) research() *ResearchLive {
	live := b.count(`SELECT count(*) FROM research_runs WHERE workspace_id = $1 AND status IN ('queued', 'running')`)
	if live == nil {
		return nil
	}
	r := &ResearchLive{Live: *live}
	b.each(`
		SELECT id, topic, status FROM research_runs
		WHERE workspace_id = $1 AND status IN ('queued', 'running')
		ORDER BY created_at DESC LIMIT 1`, nil, func(rows *sql.Rows) error {
		ref := &ResearchRef{}
		if err := rows.Scan(&ref.ID, &ref.Topic, &ref.Status); err != nil {
			return err
		}
		r.Latest = ref
		return nil
	})
	return r
}

func (b *builder) pipeline(currency string, monthStart, midnight time.Time) *Pipeline {
	p := &Pipeline{Stages: []StageValue{}, NextCloses: []DealItem{}}
	b.each(`
		SELECT st.id, st.name, count(d.id),
		       coalesce(sum(d.value_minor) FILTER (WHERE d.currency = $2), 0),
		       count(d.id) FILTER (WHERE d.currency <> $2)
		FROM pipeline_stages st
		LEFT JOIN deals d ON d.workspace_id = st.workspace_id AND d.stage_id = st.id
		WHERE st.workspace_id = $1 AND st.kind = 'open'
		GROUP BY st.id ORDER BY st.position, st.created_at`, []any{currency}, func(r *sql.Rows) error {
		var s StageValue
		var other int64
		if err := r.Scan(&s.ID, &s.Name, &s.DealCount, &s.ValueMinor, &other); err != nil {
			return err
		}
		p.Stages = append(p.Stages, s)
		p.OpenDeals += s.DealCount
		p.OpenValueMinor += s.ValueMinor
		p.OtherCurrencyDeals += other
		return nil
	})

	// Won this month: a deal on a won stage whose closed_at (stamped when it
	// entered a closed stage) falls in this calendar month.
	p.WonThisMonth.Since = monthStart
	b.each(`
		SELECT count(d.id),
		       coalesce(sum(d.value_minor) FILTER (WHERE d.currency = $2), 0),
		       count(d.id) FILTER (WHERE d.currency <> $2)
		FROM deals d
		JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		WHERE d.workspace_id = $1 AND st.kind = 'won' AND d.closed_at >= $3`, []any{currency, monthStart}, func(r *sql.Rows) error {
		w := &p.WonThisMonth
		return r.Scan(&w.Count, &w.ValueMinor, &w.OtherCurrencyDeals)
	})

	b.each(`
		SELECT d.id, d.title, d.value_minor, d.currency, st.name, co.name, to_char(d.expected_close, 'YYYY-MM-DD')
		FROM deals d
		JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id
		WHERE d.workspace_id = $1 AND st.kind = 'open' AND d.expected_close >= $2::date
		ORDER BY d.expected_close, d.created_at, d.id LIMIT $3`, []any{midnight.Format("2006-01-02"), Latest}, func(r *sql.Rows) error {
		var d DealItem
		var company sql.NullString
		if err := r.Scan(&d.ID, &d.Title, &d.ValueMinor, &d.Currency, &d.StageName, &company, &d.ExpectedClose); err != nil {
			return err
		}
		if company.Valid {
			d.CompanyName = &company.String
		}
		p.NextCloses = append(p.NextCloses, d)
		return nil
	})
	return p
}

func (b *builder) marketing(lists bool) *Marketing {
	m := &Marketing{Drafts: []DraftItem{}}
	if lists {
		m.Lists = []ListCount{}
		b.each(`
			SELECT l.id, l.name,
			       count(s.id) FILTER (WHERE s.status = 'subscribed'),
			       count(s.id) FILTER (WHERE s.status = 'pending')
			FROM mail_lists l LEFT JOIN list_subscriptions s ON s.list_id = l.id AND s.workspace_id = l.workspace_id
			WHERE l.workspace_id = $1
			GROUP BY l.id ORDER BY lower(l.name)`, nil, func(r *sql.Rows) error {
			var l ListCount
			if err := r.Scan(&l.ID, &l.Name, &l.Subscribed, &l.Pending); err != nil {
				return err
			}
			m.Lists = append(m.Lists, l)
			return nil
		})
	}
	if n := b.count(`SELECT count(*) FROM campaigns WHERE workspace_id = $1 AND status = 'draft'`); n != nil {
		m.DraftCount = *n
	}
	b.each(`
		SELECT c.id, c.subject, c.channel, l.name, c.created_at
		FROM campaigns c JOIN mail_lists l ON l.id = c.list_id AND l.workspace_id = c.workspace_id
		WHERE c.workspace_id = $1 AND c.status = 'draft'
		ORDER BY c.created_at DESC, c.id LIMIT 3`, nil, func(r *sql.Rows) error {
		var d DraftItem
		if err := r.Scan(&d.ID, &d.Subject, &d.Channel, &d.ListName, &d.CreatedAt); err != nil {
			return err
		}
		m.Drafts = append(m.Drafts, d)
		return nil
	})
	return m
}

func (b *builder) trends() *Trends {
	t := &Trends{Items: []TrendItem{}}
	if n := b.count(`SELECT count(*) FROM topics WHERE workspace_id = $1`); n != nil {
		t.Topics = *n
	}
	if n := b.count(`SELECT count(*) FROM news_items WHERE workspace_id = $1 AND cardinality(topic_ids) > 0`); n != nil {
		t.Matched = *n
	}
	b.each(`
		SELECT n.id, n.title, n.url, n.source_name, n.published_at,
		       coalesce((SELECT array_agg(tp.name ORDER BY tp.name) FROM topics tp
		                 WHERE tp.workspace_id = n.workspace_id AND tp.id = ANY (n.topic_ids)), '{}')
		FROM news_items n
		WHERE n.workspace_id = $1 AND cardinality(n.topic_ids) > 0
		ORDER BY n.published_at DESC, n.id LIMIT $2`, []any{Latest}, func(r *sql.Rows) error {
		var it TrendItem
		var topics pgtext.Array
		if err := r.Scan(&it.ID, &it.Title, &it.URL, &it.SourceName, &it.PublishedAt, &topics); err != nil {
			return err
		}
		it.Topics = []string(topics)
		if it.Topics == nil {
			it.Topics = []string{}
		}
		t.Items = append(t.Items, it)
		return nil
	})
	return t
}

// traffic reads the enabled GA4 properties' 28-day snapshots and the enabled
// Search Console sites' imported clicks, plus the cross-source summary
// (MH-567): totals, deltas vs the previous 28 days, a daily trend and the top
// properties. nil when nothing is enabled: the card is not shown at all.
func (b *builder) traffic(periodStart, periodEnd, prevStart, prevEnd time.Time) *Traffic {
	t := &Traffic{Properties: []TrafficProperty{}, Sites: []TrafficSite{}}
	b.each(`
		SELECT r.id, r.display_name, g.users, g.sessions, to_char(g.start_date, 'YYYY-MM-DD'), to_char(g.end_date, 'YYYY-MM-DD'),
		       g.fetched_at, r.last_error
		FROM google_resources r
		LEFT JOIN ga4_reports g ON g.workspace_id = r.workspace_id AND g.resource_id = r.id AND g.days = 28
		WHERE r.workspace_id = $1 AND r.kind = 'ga4_property' AND r.enabled
		ORDER BY lower(r.display_name), r.id`, nil, func(r *sql.Rows) error {
		var p TrafficProperty
		if err := r.Scan(&p.ResourceID, &p.Name, &p.Users, &p.Sessions, &p.StartDate, &p.EndDate, &p.FetchedAt, &p.LastError); err != nil {
			return err
		}
		t.Properties = append(t.Properties, p)
		return nil
	})
	b.each(`
		SELECT r.id, r.display_name, r.last_error, x.last,
		       (SELECT sum(g.clicks) FROM gsc_search_totals_daily g WHERE g.workspace_id = r.workspace_id AND g.resource_id = r.id
		          AND g.date BETWEEN x.last - 27 AND x.last),
		       (SELECT sum(g.impressions) FROM gsc_search_totals_daily g WHERE g.workspace_id = r.workspace_id AND g.resource_id = r.id
		          AND g.date BETWEEN x.last - 27 AND x.last)
		FROM google_resources r
		LEFT JOIN LATERAL (SELECT max(date) AS last FROM gsc_search_totals_daily g
		                   WHERE g.workspace_id = r.workspace_id AND g.resource_id = r.id) x ON true
		WHERE r.workspace_id = $1 AND r.kind = 'gsc_site' AND r.enabled
		ORDER BY lower(r.display_name), r.id`, nil, func(r *sql.Rows) error {
		var st TrafficSite
		var last sql.NullTime
		if err := r.Scan(&st.ResourceID, &st.Name, &st.LastError, &last, &st.Clicks, &st.Impressions); err != nil {
			return err
		}
		if last.Valid {
			e := last.Time.Format("2006-01-02")
			s := last.Time.AddDate(0, 0, -27).Format("2006-01-02")
			st.StartDate, st.EndDate = &s, &e
		}
		t.Sites = append(t.Sites, st)
		return nil
	})
	if b.err != nil || len(t.Properties)+len(t.Sites) == 0 {
		return nil
	}
	t.Summary = b.trafficSummary(t.Properties, periodStart, periodEnd, prevStart, prevEnd)
	return t
}

func ymd(t time.Time) string { return t.Format("2006-01-02") }

// trafficSummary builds the cross-source rollup once traffic() already knows
// at least one property or site is enabled.
func (b *builder) trafficSummary(props []TrafficProperty, periodStart, periodEnd, prevStart, prevEnd time.Time) *TrafficSummary {
	if b.err != nil {
		return nil
	}
	s := &TrafficSummary{
		PeriodStart: ymd(periodStart), PeriodEnd: ymd(periodEnd),
		Daily: []TrafficDay{}, TopProperties: []TopProperty{},
	}
	s.GA4 = b.ga4Summary(len(props), periodStart, periodEnd, prevStart, prevEnd)
	s.SearchConsole = b.searchConsoleSummary(periodStart, periodEnd, prevStart, prevEnd)
	s.YouTube = b.youtubeSummary()
	s.Daily = b.trafficDaily(periodStart, periodEnd)
	s.TopProperties = topProperties(props)
	if b.err != nil {
		return nil
	}
	return s
}

// ga4Summary sums the stored 28-day snapshot (ga4_reports: GA4's own dedup'd
// totals) across every enabled property for the headline figures, and sums
// the daily history (ga4_daily) on both sides of the boundary for the %
// change — the same source on both sides, so the comparison is honest even
// though it can read a little differently from the headline sum.
func (b *builder) ga4Summary(propCount int, periodStart, periodEnd, prevStart, prevEnd time.Time) *GA4Summary {
	if propCount == 0 || b.err != nil {
		return nil
	}
	g := &GA4Summary{PropertiesCount: propCount}
	var users, sessions, views, engSess, engSec, keyEvents sql.NullInt64
	row := b.q.QueryRowContext(b.ctx, `
		SELECT sum(g.users), sum(g.sessions), sum(g.views),
		       sum((g.payload->'totals'->>'engaged_sessions')::bigint),
		       sum((g.payload->'totals'->>'engagement_seconds')::bigint),
		       sum((g.payload->'totals'->>'key_events')::bigint)
		FROM google_resources r
		JOIN ga4_reports g ON g.workspace_id = r.workspace_id AND g.resource_id = r.id AND g.days = 28
		WHERE r.workspace_id = $1 AND r.kind = 'ga4_property' AND r.enabled`, b.ws)
	if b.fail(row.Scan(&users, &sessions, &views, &engSess, &engSec, &keyEvents)) {
		return nil
	}
	if users.Valid {
		g.Users = &users.Int64
	}
	if sessions.Valid {
		g.Sessions = &sessions.Int64
	}
	if views.Valid {
		g.Views = &views.Int64
	}
	if keyEvents.Valid {
		g.KeyEvents = &keyEvents.Int64
	}
	if sessions.Valid && sessions.Int64 > 0 && engSess.Valid {
		rate := float64(engSess.Int64) / float64(sessions.Int64)
		g.EngagementRate = &rate
	}
	if sessions.Valid && sessions.Int64 > 0 && engSec.Valid {
		avg := float64(engSec.Int64) / float64(sessions.Int64)
		g.AvgSessionSeconds = &avg
	}

	curUsers, curSessions, curViews, _ := b.ga4DailySums(periodStart, periodEnd)
	prevUsers, prevSessions, prevViews, prevDays := b.ga4DailySums(prevStart, prevEnd)
	// "Known" means the previous window's daily history is essentially
	// complete — at least 20 of its 28 days were actually synced. Anything
	// thinner is a workspace still filling in its history, not a real trend.
	known := prevDays >= 20
	g.UsersPct = pctDelta(curUsers, prevUsers, known)
	g.SessionsPct = pctDelta(curSessions, prevSessions, known)
	g.ViewsPct = pctDelta(curViews, prevViews, known)
	return g
}

// ga4DailySums sums ga4_daily across every enabled GA4 property for one
// window, and reports how many distinct days in that window have any row at
// all (so the caller can tell a real, complete window from a thin one).
func (b *builder) ga4DailySums(start, end time.Time) (users, sessions, views sql.NullInt64, days int64) {
	if b.err != nil {
		return
	}
	row := b.q.QueryRowContext(b.ctx, `
		SELECT sum(g.users), sum(g.sessions), sum(g.views), count(DISTINCT g.day)
		FROM google_resources r
		JOIN ga4_daily g ON g.workspace_id = r.workspace_id AND g.resource_id = r.id
		WHERE r.workspace_id = $1 AND r.kind = 'ga4_property' AND r.enabled AND g.day BETWEEN $2 AND $3`,
		b.ws, ymd(start), ymd(end))
	b.fail(row.Scan(&users, &sessions, &views, &days))
	return
}

// pctDelta is (cur-prev)/prev as a percentage, or nil when the previous
// period is not known well enough to trust, or was itself 0 (a % of nothing
// is not a number).
func pctDelta(cur, prev sql.NullInt64, known bool) *float64 {
	if !known || !cur.Valid || !prev.Valid || prev.Int64 == 0 {
		return nil
	}
	v := (float64(cur.Int64) - float64(prev.Int64)) / float64(prev.Int64) * 100
	return &v
}

// searchConsoleSummary sums gsc_search_totals_daily (the site's real totals,
// anonymised queries included) across every enabled site. CTR and average
// position are impression-weighted, the same rule the Search Console page
// itself uses when summing more than one row.
func (b *builder) searchConsoleSummary(periodStart, periodEnd, prevStart, prevEnd time.Time) *SearchConsoleSummary {
	if b.err != nil {
		return nil
	}
	var siteCount int
	if b.fail(b.q.QueryRowContext(b.ctx, `SELECT count(*) FROM google_resources WHERE workspace_id = $1 AND kind = 'gsc_site' AND enabled`, b.ws).Scan(&siteCount)) {
		return nil
	}
	if siteCount == 0 {
		return nil
	}
	sc := &SearchConsoleSummary{SitesCount: siteCount}
	clicks, impressions := b.gscSums(periodStart, periodEnd)
	if clicks.Valid {
		sc.Clicks = &clicks.Int64
	}
	if impressions.Valid {
		sc.Impressions = &impressions.Int64
		if impressions.Int64 > 0 && clicks.Valid {
			ctr := float64(clicks.Int64) / float64(impressions.Int64)
			sc.CTR = &ctr
		}
	}
	var avgPos sql.NullFloat64
	if b.fail(b.q.QueryRowContext(b.ctx, `
		SELECT CASE WHEN sum(g.impressions) > 0 THEN sum(g.position * g.impressions) / sum(g.impressions) END
		FROM google_resources r
		JOIN gsc_search_totals_daily g ON g.workspace_id = r.workspace_id AND g.resource_id = r.id
		WHERE r.workspace_id = $1 AND r.kind = 'gsc_site' AND r.enabled AND g.date BETWEEN $2 AND $3`,
		b.ws, ymd(periodStart), ymd(periodEnd)).Scan(&avgPos)) {
		return nil
	}
	if avgPos.Valid {
		sc.AvgPosition = &avgPos.Float64
	}
	prevClicks, prevImpressions := b.gscSums(prevStart, prevEnd)
	// Known when both a click and an impression row exist somewhere in the
	// previous window (Search Console has actually been imported that far back).
	known := prevClicks.Valid && prevImpressions.Valid
	sc.ClicksPct = pctDelta(clicks, prevClicks, known)
	sc.ImpressionsPct = pctDelta(impressions, prevImpressions, known)
	return sc
}

func (b *builder) gscSums(start, end time.Time) (clicks, impressions sql.NullInt64) {
	if b.err != nil {
		return
	}
	row := b.q.QueryRowContext(b.ctx, `
		SELECT sum(g.clicks), sum(g.impressions)
		FROM google_resources r
		JOIN gsc_search_totals_daily g ON g.workspace_id = r.workspace_id AND g.resource_id = r.id
		WHERE r.workspace_id = $1 AND r.kind = 'gsc_site' AND r.enabled AND g.date BETWEEN $2 AND $3`,
		b.ws, ymd(start), ymd(end))
	b.fail(row.Scan(&clicks, &impressions))
	return
}

// youtubeSummary sums each enabled channel's current counters. There is no
// history to compare against (youtube_channels only ever holds the latest
// sync), so this never carries a delta.
func (b *builder) youtubeSummary() *YouTubeSummary {
	if b.err != nil {
		return nil
	}
	var channelCount int
	if b.fail(b.q.QueryRowContext(b.ctx, `SELECT count(*) FROM google_resources WHERE workspace_id = $1 AND kind = 'yt_channel' AND enabled`, b.ws).Scan(&channelCount)) {
		return nil
	}
	if channelCount == 0 {
		return nil
	}
	y := &YouTubeSummary{ChannelsCount: channelCount}
	var views, subs sql.NullInt64
	row := b.q.QueryRowContext(b.ctx, `
		SELECT
		  (SELECT sum((y.payload->'totals'->>'views')::bigint) FROM youtube_analytics y
		     JOIN google_resources r ON r.workspace_id = y.workspace_id AND r.id = y.resource_id
		     WHERE r.workspace_id = $1 AND r.kind = 'yt_channel' AND r.enabled AND y.scope = 'channel' AND y.days = 28),
		  (SELECT sum(CASE WHEN c.subscribers_hidden THEN NULL ELSE c.subscriber_count END) FROM youtube_channels c
		     JOIN google_resources r ON r.workspace_id = c.workspace_id AND r.id = c.resource_id
		     WHERE r.workspace_id = $1 AND r.kind = 'yt_channel' AND r.enabled)`, b.ws)
	if b.fail(row.Scan(&views, &subs)) {
		return nil
	}
	if views.Valid {
		y.Views28 = &views.Int64
	}
	if subs.Valid {
		y.Subscribers = &subs.Int64
	}
	return y
}

// trafficDaily is the traffic-over-time chart: GA4 users/sessions and Search
// Console clicks, one row per day, summed across enabled sources. A day with
// no row at all for a source (nothing fetched that far back, or a brand new
// property with no data yet) is null — the same "never a fake number" rule
// as everything else this card shows.
func (b *builder) trafficDaily(start, end time.Time) []TrafficDay {
	list := []TrafficDay{}
	b.each(`
		SELECT d::date,
		  (SELECT sum(g.users) FROM ga4_daily g JOIN google_resources r ON r.workspace_id = g.workspace_id AND r.id = g.resource_id
		     WHERE r.workspace_id = $1 AND r.kind = 'ga4_property' AND r.enabled AND g.day = d::date),
		  (SELECT sum(g.sessions) FROM ga4_daily g JOIN google_resources r ON r.workspace_id = g.workspace_id AND r.id = g.resource_id
		     WHERE r.workspace_id = $1 AND r.kind = 'ga4_property' AND r.enabled AND g.day = d::date),
		  (SELECT sum(g.clicks) FROM gsc_search_totals_daily g JOIN google_resources r ON r.workspace_id = g.workspace_id AND r.id = g.resource_id
		     WHERE r.workspace_id = $1 AND r.kind = 'gsc_site' AND r.enabled AND g.date = d::date)
		FROM generate_series($2::date, $3::date, interval '1 day') d
		ORDER BY d`, []any{ymd(start), ymd(end)}, func(r *sql.Rows) error {
		var day time.Time
		var td TrafficDay
		var users, sessions, clicks sql.NullInt64
		if err := r.Scan(&day, &users, &sessions, &clicks); err != nil {
			return err
		}
		td.Date = day.Format("2006-01-02")
		if users.Valid {
			td.Users = &users.Int64
		}
		if sessions.Valid {
			td.Sessions = &sessions.Int64
		}
		if clicks.Valid {
			td.Clicks = &clicks.Int64
		}
		list = append(list, td)
		return nil
	})
	return list
}

// topProperties picks the top 5 GA4 properties by users, in memory: traffic()
// already has the whole (short) list loaded, so this needs no extra query.
// A property never fetched sorts last, not first.
func topProperties(props []TrafficProperty) []TopProperty {
	sorted := make([]TrafficProperty, len(props))
	copy(sorted, props)
	sort.SliceStable(sorted, func(i, j int) bool {
		ui, uj := sorted[i].Users, sorted[j].Users
		if ui == nil && uj == nil {
			return false
		}
		if ui == nil {
			return false
		}
		if uj == nil {
			return true
		}
		return *ui > *uj
	})
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}
	out := make([]TopProperty, len(sorted))
	for i, p := range sorted {
		out[i] = TopProperty{ResourceID: p.ResourceID, Name: p.Name, Kind: "ga4_property", Users: p.Users}
	}
	return out
}

// leadsDaily zero-fills a lead-count-per-day series over [start, end], in the
// workspace's own time zone.
func (b *builder) leadsDaily(tz string, start, end time.Time) []CountDay {
	list := []CountDay{}
	b.each(`
		SELECT d::date, count(l.id)
		FROM generate_series($2::date, $3::date, interval '1 day') d
		LEFT JOIN leads l ON l.workspace_id = $1 AND (l.created_at AT TIME ZONE $4::text)::date = d::date
		GROUP BY d ORDER BY d`, []any{ymd(start), ymd(end), tz}, func(r *sql.Rows) error {
		var day time.Time
		var n int64
		if err := r.Scan(&day, &n); err != nil {
			return err
		}
		list = append(list, CountDay{Date: day.Format("2006-01-02"), Count: n})
		return nil
	})
	return list
}

// conversationsDaily zero-fills a conversations-started-per-day series, split
// by channel, over [start, end], in the workspace's own time zone.
func (b *builder) conversationsDaily(tz string, start, end time.Time) []ConversationDay {
	list := []ConversationDay{}
	b.each(`
		SELECT d::date,
		       count(c.id) FILTER (WHERE c.channel = 'chat'),
		       count(c.id) FILTER (WHERE c.channel = 'email'),
		       count(c.id) FILTER (WHERE c.channel = 'whatsapp')
		FROM generate_series($2::date, $3::date, interval '1 day') d
		LEFT JOIN conversations c ON c.workspace_id = $1 AND (c.created_at AT TIME ZONE $4::text)::date = d::date
		GROUP BY d ORDER BY d`, []any{ymd(start), ymd(end), tz}, func(r *sql.Rows) error {
		var day time.Time
		var cd ConversationDay
		if err := r.Scan(&day, &cd.Chat, &cd.Email, &cd.WhatsApp); err != nil {
			return err
		}
		cd.Date = day.Format("2006-01-02")
		list = append(list, cd)
		return nil
	})
	return list
}

// youtube reads each enabled channel. nil when none is enabled.
func (b *builder) youtube() []YouTubeChannel {
	var list []YouTubeChannel
	b.each(`
		SELECT r.id, coalesce(nullif(c.title, ''), r.display_name),
		       CASE WHEN c.subscribers_hidden THEN NULL ELSE c.subscriber_count END, c.video_count, c.updated_at, r.last_error,
		       (SELECT (y.payload->'totals'->>'views')::numeric::bigint FROM youtube_analytics y
		        WHERE y.workspace_id = r.workspace_id AND y.resource_id = r.id AND y.scope = 'channel' AND y.days = 28),
		       (SELECT y.payload->>'end_date' FROM youtube_analytics y
		        WHERE y.workspace_id = r.workspace_id AND y.resource_id = r.id AND y.scope = 'channel' AND y.days = 28)
		FROM google_resources r
		LEFT JOIN youtube_channels c ON c.workspace_id = r.workspace_id AND c.resource_id = r.id
		WHERE r.workspace_id = $1 AND r.kind = 'yt_channel' AND r.enabled
		ORDER BY lower(r.display_name), r.id`, nil, func(r *sql.Rows) error {
		var y YouTubeChannel
		if err := r.Scan(&y.ResourceID, &y.Title, &y.Subscribers, &y.Videos, &y.SyncedAt, &y.LastError, &y.Views28, &y.Views28To); err != nil {
			return err
		}
		list = append(list, y)
		return nil
	})
	if b.err != nil || len(list) == 0 {
		return nil
	}
	return list
}

// setup lists what is connected. Each item is shown only to a role that can
// read the area it belongs to — the same check that area's own page makes.
func (b *builder) setup(can func(string) bool, env Env) []SetupItem {
	items := []SetupItem{}
	add := func(key, action, q string, available bool) {
		if !can(action) {
			return
		}
		items = append(items, SetupItem{Key: key, Done: b.exists(q), Available: available})
	}
	add(SetupInquiryForm, authz.ContactsRead,
		`SELECT EXISTS (SELECT 1 FROM subscribe_forms WHERE workspace_id = $1 AND kind = 'inquiry')`, true)
	// The campaigns page tells every campaigns:read member whether sending is
	// configured; this says the same thing, no more.
	add(SetupSMTP, authz.CampaignsRead,
		`SELECT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1)`, true)
	add(SetupChatWidget, authz.InboxRead,
		`SELECT EXISTS (SELECT 1 FROM chat_widgets WHERE workspace_id = $1)`, true)
	// Linked means the phone finished pairing (state open), the same test the
	// campaigns page uses before offering a WhatsApp send.
	add(SetupWhatsApp, authz.InboxRead,
		`SELECT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1 AND state = 'open')`, env.WhatsAppAvailable)
	add(SetupGoogle, authz.IntegrationsManage,
		`SELECT EXISTS (SELECT 1 FROM google_connections WHERE workspace_id = $1)`, env.GoogleAvailable)
	add(SetupBrowser, authz.AutomationRead,
		`SELECT EXISTS (SELECT 1 FROM browser_agents WHERE workspace_id = $1 AND revoked_at IS NULL)`, true)
	return items
}

// trim is the inbox's own preview rule (internal/inbox/inbox.go), so a
// snippet here reads exactly as it does in the inbox list.
func trim(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
