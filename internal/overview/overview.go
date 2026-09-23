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
}

// Traffic is the last 28 days of each enabled GA4 property and Search
// Console site, from the stored snapshots (the collector refreshes them
// nightly; opening the Analytics page refreshes them hourly). A figure that
// has never been fetched is null, never 0.
type Traffic struct {
	Properties []TrafficProperty `json:"properties"`
	Sites      []TrafficSite     `json:"sites"`
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

	if can(authz.LeadsRead) {
		o.Today.Leads, o.Today.LeadsReceivedToday = b.leadCounts(midnight)
		o.Leads = b.latestLeads()
	}
	if can(authz.InboxRead) {
		o.Today.UnreadConversations = b.count(`SELECT count(*) FROM conversations WHERE workspace_id = $1 AND unread`)
		o.Conversations = b.latestConversations()
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
		o.Traffic = b.traffic()
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
		SELECT c.id, c.channel, c.status, c.customer_name, c.customer_email, c.subject, c.unread, c.last_message_at,
		       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		                 ORDER BY m.created_at DESC LIMIT 1), '')
		FROM conversations c WHERE c.workspace_id = $1
		ORDER BY c.unread DESC, c.last_message_at DESC, c.id LIMIT $2`, []any{Latest}, func(r *sql.Rows) error {
		var c ConversationItem
		if err := r.Scan(&c.ID, &c.Channel, &c.Status, &c.CustomerName, &c.CustomerEmail, &c.Subject, &c.Unread, &c.LastMessageAt, &c.Preview); err != nil {
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
// Search Console sites' imported clicks. nil when nothing is enabled: the
// card is not shown at all.
func (b *builder) traffic() *Traffic {
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
	return t
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
		`SELECT EXISTS (SELECT 1 FROM smtp_settings WHERE workspace_id = $1)`, true)
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
