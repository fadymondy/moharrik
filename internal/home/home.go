// Package home is the workspace's Home page (MH-1062): one aggregate read of
// what Moharrik is finding, what needs a person, what is running right now,
// what just happened, how open deals are moving and which connections feed it.
//
// Nothing is estimated. Every number is a query over the workspace's own rows
// (workspace_id in every predicate, on the RLS pool, in one REPEATABLE READ
// READ ONLY transaction so the page agrees with itself). A section the
// caller's role cannot read is null — not zero — and a source that does not
// exist for this workspace (no Google connection, CircleXO off) is omitted.
//
// Text is never pre-rendered: reasons and events come back as codes plus the
// data they were derived from, and the web translates them (en/ar).
package home

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/fadymondy/moharrik/internal/authz"
)

// ErrNotFound: the workspace is gone (or not visible through RLS).
var ErrNotFound = errors.New("workspace not found")

// Windows the derived signals look back over. They are part of the contract:
// the response documents them, so a number can be explained to the user.
const (
	HotLeadReplyWindow = 72 * time.Hour
	NewLeadWindow      = 24 * time.Hour
	QuietAfter         = 7 * 24 * time.Hour
	NewProspectWindow  = 7 * 24 * time.Hour
	FailureWindow      = 24 * time.Hour
	UnansweredAfter    = 4 * time.Hour
	UnansweredWithin   = 14 * 24 * time.Hour
	HighFitScore       = 70
	ActivityLimit      = 20
	AttentionLimit     = 6
	JourneyLimit       = 5
	SparkBucket        = 5 * time.Minute
	SparkBuckets       = 12
)

// Home is the whole page. A nil section is one the caller's role may not read.
type Home struct {
	Timezone string `json:"timezone" doc:"The workspace's time zone; 'today' is in it."`
	Currency string `json:"currency"`

	Today       HomeToday        `json:"today"`
	Attention   *HomeAttention   `json:"attention" doc:"Null when the role can read none of the sources. Ordered by urgency, at most 6 shown; total says how many there were."`
	Working     HomeWorking      `json:"working"`
	Activity    []HomeEvent      `json:"activity" doc:"The latest events merged across audit log, inbound messages and runs, newest first."`
	Journeys    *HomeJourneys    `json:"journeys" doc:"Null without contacts:read."`
	Connections *HomeConnections `json:"connections" doc:"Null when the role can read none and nothing is configured."`
	// Has is whether the workspace has any customers, conversations and automations at all —
	// what the empty states key off.
	Has HomeHas `json:"has"`
}

// Has says whether each area has any rows; null when the role cannot read the area.
type HomeHas struct {
	Contacts      *bool `json:"contacts"`
	Conversations *bool `json:"conversations"`
	Automations   *bool `json:"automations"`
}

// Today are the four tiles.
type HomeToday struct {
	Signals       HomeSignals       `json:"signals"`
	Opportunities HomeOpportunities `json:"opportunities"`
	Actions       HomeActions       `json:"actions"`
	Attention     int64             `json:"attention" doc:"How many attention items there are (before the display cap)."`
}

// Signals: new things found today, by source. A source the role cannot read is null.
type HomeSignals struct {
	Total     int64  `json:"total"`
	Leads     *int64 `json:"leads"`
	Prospects *int64 `json:"prospects"`
	Companies *int64 `json:"companies" doc:"Research companies first seen today."`
	Trends    *int64 `json:"trends" doc:"Trend clusters first seen today."`
}

// Money is a sum in one currency (minor units).
type HomeMoney struct {
	Currency string `json:"currency"`
	Minor    int64  `json:"minor"`
	Deals    int64  `json:"deals,omitempty"`
}

// Opportunities: deals in an open stage and what they are worth, per currency (never converted).
type HomeOpportunities struct {
	Open  *int64      `json:"open" doc:"Null without contacts:read."`
	Value []HomeMoney `json:"value"`
}

// Actions Moharrik took today.
type HomeActions struct {
	Total         int64  `json:"total"`
	RuleRuns      *int64 `json:"rule_runs" doc:"Rule runs that finished ok or partial today (dry runs excluded)."`
	BrowserRuns   *int64 `json:"browser_runs" doc:"Browser/workflow runs that finished ok today."`
	CampaignSends *int64 `json:"campaign_sends" doc:"Messages sent today by campaigns that went out today."`
}

// EntityRef points at a record the web can link to.
type HomeEntityRef struct {
	Type string `json:"type" doc:"contact | company | deal | lead | conversation | prospect | rule | run | research | campaign"`
	ID   string `json:"id"`
}

// Reason is one structured line of "why this is here". The web translates Code and
// fills it from the other fields.
type HomeReason struct {
	Code    string     `json:"code" doc:"replied | new_lead | utm | form | quiet_days | in_stage | high_fit | rule_failed | browser_failed | waiting_for_reply"`
	Channel string     `json:"channel,omitempty"`
	Text    string     `json:"text,omitempty" doc:"A value taken from the data (an utm source, a stage name…)."`
	N       *int64     `json:"n,omitempty"`
	At      *time.Time `json:"at,omitempty"`
}

// Action is a suggested next step; Route is workspace-relative and always an existing page.
type HomeAction struct {
	Key   string `json:"key" doc:"open | review | open_runs | open_inbox | open_prospects"`
	Route string `json:"route"`
}

// AttentionItem is one card.
type HomeAttentionItem struct {
	Kind       string         `json:"kind" enum:"hot_lead,at_risk,new_prospects,failed_automation,unanswered"`
	Tone       string         `json:"tone" enum:"warning,danger,info,success"`
	Title      string         `json:"title" doc:"The record's own name (lead, deal…); empty for grouped items."`
	Count      *int64         `json:"count,omitempty" doc:"For grouped items."`
	Why        []HomeReason   `json:"why"`
	Entity     *HomeEntityRef `json:"entity,omitempty"`
	Value      *HomeMoney     `json:"value,omitempty"`
	OccurredAt *time.Time     `json:"occurred_at,omitempty"`
	Actions    []HomeAction   `json:"actions"`
}

// Attention is the list plus the uncapped count.
type HomeAttention struct {
	Total int64               `json:"total"`
	Items []HomeAttentionItem `json:"items"`
}

// Lane is one of the four things Moharrik does continuously.
type HomeLane struct {
	Key      string         `json:"key" enum:"discover,listen,understand,act"`
	Active   bool           `json:"active"`
	Headline *int64         `json:"headline" doc:"Null when the role can read none of the lane's sources."`
	Subs     []HomeLaneItem `json:"subs"`
}

// LaneItem is one real number under a lane.
type HomeLaneItem struct {
	Key  string     `json:"key"`
	N    *int64     `json:"n,omitempty"`
	Text string     `json:"text,omitempty" doc:"A data value (a channel kind…)."`
	At   *time.Time `json:"at,omitempty"`
}

// Working is the four lanes and the last hour's pulse.
type HomeWorking struct {
	Lanes []HomeLane `json:"lanes"`
	Pulse HomePulse  `json:"pulse"`
}

// Pulse is the activity sparkline: BucketMinutes-minute buckets over the last hour, oldest
// first, zero-filled. Counts audit-log events on customer records, leads, campaigns and rules,
// plus inbound messages — the same sources as the activity list.
type HomePulse struct {
	BucketMinutes int     `json:"bucket_minutes"`
	Buckets       []int64 `json:"buckets"`
	Total         int64   `json:"total"`
}

// Actor of an event.
type HomeActor struct {
	Kind string `json:"kind" doc:"member | token | oauth | agent | admin"`
	Name string `json:"name"`
	Via  string `json:"via"`
}

// Event is one line of Today's activity.
type HomeEvent struct {
	Kind       string         `json:"kind" doc:"contact.created, deal.stage_changed, lead.created, message.received, rule.ran, run.finished, research.finished …"`
	OccurredAt time.Time      `json:"occurred_at"`
	Subject    string         `json:"subject,omitempty" doc:"The record's name / the message sender / the rule or run name."`
	Channel    string         `json:"channel,omitempty"`
	Snippet    string         `json:"snippet,omitempty" doc:"First words of an inbound message, whitespace-collapsed and cut at 140 characters."`
	Status     string         `json:"status,omitempty"`
	N          *int64         `json:"n,omitempty"`
	Actor      *HomeActor     `json:"actor,omitempty"`
	Link       *HomeEntityRef `json:"link,omitempty"`
}

// Journeys: open deals shown against the pipeline.
type HomeJourneys struct {
	Stages []HomeStage   `json:"stages" doc:"Open stages then won, in pipeline order; every deal's stage_index points here."`
	Deals  []HomeJourney `json:"deals"`
}

// Stage of the pipeline.
type HomeStage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind" enum:"open,won"`
}

// Journey is one deal.
type HomeJourney struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	StageIndex   int       `json:"stage_index"`
	StageName    string    `json:"stage_name"`
	Value        HomeMoney `json:"value"`
	Contact      string    `json:"contact,omitempty"`
	Company      string    `json:"company,omitempty"`
	LastActivity time.Time `json:"last_activity_at"`
	DaysQuiet    int       `json:"days_since_activity"`
	Quiet        bool      `json:"quiet" doc:"7 days or more without activity."`
}

// Connections: what is wired in, from rows that exist.
type HomeConnections struct {
	Channels []HomeChannel `json:"channels"`
	Google   *HomeGoogle   `json:"google"`
	CircleXO *HomeCircleXO `json:"circlexo"`
}

// Channel is one configured inbox connection. The error text is deliberately not here.
type HomeChannel struct {
	ID               string     `json:"id"`
	Type             string     `json:"type"`
	Network          string     `json:"network,omitempty"`
	Name             string     `json:"name"`
	Status           string     `json:"status" enum:"connected,error,paused"`
	HasError         bool       `json:"has_error"`
	LastMessageAt    *time.Time `json:"last_message_at"`
	Conversations30d int        `json:"conversations_30d"`
}

// Google is the workspace's Google connections and what is enabled from them.
type HomeGoogle struct {
	Connections int              `json:"connections"`
	HasError    bool             `json:"has_error"`
	Services    []HomeGoogleKind `json:"services"`
}

// GoogleKind counts enabled resources of one kind.
type HomeGoogleKind struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// CircleXO is the hub link, when the integration is on and the workspace is linked.
type HomeCircleXO struct {
	Linked bool   `json:"linked"`
	Plan   string `json:"plan,omitempty"`
	Status string `json:"status,omitempty"`
	Active bool   `json:"active"`
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// builder keeps the first error, like the overview's.
type builder struct {
	ctx context.Context
	q   queryer
	ws  string
	err error
	now time.Time
	loc *time.Location
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

func (b *builder) flag(q string, args ...any) *bool {
	if b.err != nil {
		return nil
	}
	var v bool
	if b.fail(b.q.QueryRowContext(b.ctx, q, append([]any{b.ws}, args...)...).Scan(&v)) {
		return nil
	}
	return &v
}

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

func add(xs ...*int64) int64 {
	var n int64
	for _, x := range xs {
		if x != nil {
			n += *x
		}
	}
	return n
}

func i64(n int64) *int64 { return &n }

func tptr(t time.Time) *time.Time { return &t }

// Env carries what Build needs beyond plain SQL, so tests can leave those sources out.
type Env struct {
	// Channels returns the workspace's channel aggregate (internal/channels). Nil omits it.
	Channels func(ctx context.Context) ([]HomeChannel, error)
	// CircleXO returns the hub link state. Nil omits it.
	CircleXO func(ctx context.Context) (*HomeCircleXO, error)
}

// Build reads Home for one workspace as seen by one role. ctx must carry the workspace
// (tenant.WithWorkspace) for the RLS pool.
func Build(ctx context.Context, db *sql.DB, wsID, role string, env Env, now time.Time) (*Home, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	h := &Home{}
	if err := tx.QueryRowContext(ctx, `SELECT currency, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&h.Currency, &h.Timezone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		loc, h.Timezone = time.UTC, "UTC"
	}
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	can := func(a string) bool { return authz.Can(role, a) }
	b := &builder{ctx: ctx, q: tx, ws: wsID, now: now, loc: loc}

	h.Today = b.today(can, midnight)
	h.Attention = b.attention(can)
	if h.Attention != nil {
		h.Today.Attention = h.Attention.Total
		if len(h.Attention.Items) > AttentionLimit {
			h.Attention.Items = h.Attention.Items[:AttentionLimit]
		}
	}
	h.Activity = b.activity(can)
	if can(authz.ContactsRead) {
		h.Journeys = b.journeys()
	}
	h.Has = b.has(can)

	var conns *HomeConnections
	ensure := func() *HomeConnections {
		if conns == nil {
			conns = &HomeConnections{Channels: []HomeChannel{}}
		}
		return conns
	}
	if can(authz.InboxRead) && env.Channels != nil && b.err == nil {
		chs, err := env.Channels(ctx)
		if b.fail(err) {
			return nil, b.err
		}
		if len(chs) > 0 {
			ensure().Channels = chs
		}
	}
	if can(authz.AnalyticsRead) || can(authz.IntegrationsManage) {
		if g := b.google(); g != nil {
			ensure().Google = g
		}
	}
	if env.CircleXO != nil && b.err == nil {
		if cx, err := env.CircleXO(ctx); err == nil && cx != nil && cx.Linked {
			ensure().CircleXO = cx
		}
	}
	h.Connections = conns
	h.Working = b.working(can, midnight, h.Today, conns)

	if b.err != nil {
		return nil, b.err
	}
	return h, nil
}

func (b *builder) has(can func(string) bool) HomeHas {
	var h HomeHas
	if can(authz.ContactsRead) {
		h.Contacts = b.flag(`SELECT EXISTS (SELECT 1 FROM contacts WHERE workspace_id = $1)`)
	}
	if can(authz.InboxRead) {
		h.Conversations = b.flag(`SELECT EXISTS (SELECT 1 FROM conversations WHERE workspace_id = $1)`)
	}
	if can(authz.AutomationRead) {
		h.Automations = b.flag(`SELECT EXISTS (SELECT 1 FROM automation_rules WHERE workspace_id = $1)
			OR EXISTS (SELECT 1 FROM automation_workflows WHERE workspace_id = $1)`)
	}
	return h
}

func (b *builder) today(can func(string) bool, midnight time.Time) HomeToday {
	var t HomeToday
	if can(authz.LeadsRead) {
		t.Signals.Leads = b.count(`SELECT count(*) FROM leads WHERE workspace_id = $1 AND created_at >= $2 AND status <> 'spam'`, midnight)
	}
	if can(authz.ResearchRead) {
		t.Signals.Prospects = b.count(`SELECT count(*) FROM prospects WHERE workspace_id = $1 AND created_at >= $2`, midnight)
		t.Signals.Companies = b.count(`SELECT count(*) FROM research_companies WHERE workspace_id = $1 AND first_seen_at >= $2`, midnight)
	}
	if can(authz.TrendsRead) {
		t.Signals.Trends = b.count(`SELECT count(*) FROM trend_clusters WHERE workspace_id = $1 AND first_seen_at >= $2`, midnight)
	}
	t.Signals.Total = add(t.Signals.Leads, t.Signals.Prospects, t.Signals.Companies, t.Signals.Trends)

	t.Opportunities.Value = []HomeMoney{}
	if can(authz.ContactsRead) {
		var n int64
		b.each(`SELECT d.currency, count(*), coalesce(sum(d.value_minor), 0)
			FROM deals d JOIN pipeline_stages s ON s.workspace_id = d.workspace_id AND s.id = d.stage_id
			WHERE d.workspace_id = $1 AND s.kind = 'open'
			GROUP BY d.currency ORDER BY sum(d.value_minor) DESC, d.currency`, nil, func(r *sql.Rows) error {
			var m HomeMoney
			if err := r.Scan(&m.Currency, &m.Deals, &m.Minor); err != nil {
				return err
			}
			n += m.Deals
			t.Opportunities.Value = append(t.Opportunities.Value, m)
			return nil
		})
		if b.err == nil {
			t.Opportunities.Open = i64(n)
		}
	}

	if can(authz.AutomationRead) {
		t.Actions.RuleRuns = b.count(`SELECT count(*) FROM automation_rule_runs
			WHERE workspace_id = $1 AND NOT dry_run AND status IN ('ok', 'partial') AND coalesce(finished_at, started_at) >= $2`, midnight)
		t.Actions.BrowserRuns = b.count(`SELECT count(*) FROM automation_runs
			WHERE workspace_id = $1 AND status = 'ok' AND coalesce(finished_at, created_at) >= $2`, midnight)
	}
	if can(authz.CampaignsRead) {
		t.Actions.CampaignSends = b.count(`SELECT coalesce(sum(sent_count), 0)::bigint FROM campaigns WHERE workspace_id = $1 AND sent_at >= $2`, midnight)
	}
	t.Actions.Total = add(t.Actions.RuleRuns, t.Actions.BrowserRuns, t.Actions.CampaignSends)
	return t
}

// snippet collapses whitespace, drops control characters and cuts at n runes.
func snippet(s string, n int) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = sb.Len() > 0
			continue
		}
		if space {
			sb.WriteByte(' ')
			space = false
		}
		sb.WriteRune(r)
	}
	out := []rune(sb.String())
	if len(out) > n {
		return strings.TrimSpace(string(out[:n-1])) + "…"
	}
	return string(out)
}

func sortEvents(ev []HomeEvent) {
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].OccurredAt.After(ev[j].OccurredAt) })
}
