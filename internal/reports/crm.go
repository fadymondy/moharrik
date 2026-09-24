// Package reports is Moharrik's CRM reporting (MH-394): pipeline value, win
// rate and deal size, stage-to-stage conversion, deals won and lost over time,
// where leads come from, and how fast the inbox answers — over a date range,
// for one workspace.
//
// The rules the overview (internal/overview) set, kept here:
//
//   - Every figure is computed in SQL, on the app (RLS) pool, with workspace_id
//     in every predicate. Nothing is estimated, sampled or filled in.
//   - The whole read is one REPEATABLE READ, READ ONLY transaction, so the
//     sections agree with each other.
//   - Money is only summed in the workspace's currency. Deals in any other
//     currency are counted, and said to be left out, never converted.
//   - A section the caller may not read is null, not zero; a figure that has no
//     data behind it (a win rate with nothing closed) is null, not 0.
//
// BuildCRM takes a *sql.DB, a workspace, what the caller may read and a clock,
// so the REST route, the MCP tool and the tests all answer from the same code.
package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxRangeDays bounds one report. Two years covers "this year against last",
// and keeps a day-by-day read of the inbox bounded.
const MaxRangeDays = 731

// DefaultRangeDays is the window when none is given: the last 90 days.
const DefaultRangeDays = 90

// CRMReport is the whole report.
type CRMReport struct {
	From     string `json:"from" doc:"First day of the range (YYYY-MM-DD), in the workspace's time zone."`
	To       string `json:"to" doc:"Last day of the range, inclusive."`
	Days     int    `json:"days" doc:"Days in the range."`
	Timezone string `json:"timezone" doc:"The workspace's time zone; the range's days are its days."`
	Currency string `json:"currency" doc:"The workspace's currency; every money figure is in it, in minor units."`
	// Bucket is how Timeline is grouped: by day up to 31 days, by week up to
	// 184, by month beyond.
	Bucket      string    `json:"bucket" enum:"day,week,month"`
	GeneratedAt time.Time `json:"generated_at"`

	Pipeline   ReportPipeline   `json:"pipeline" doc:"Open deals now, whatever the range."`
	Deals      ReportDealStats  `json:"deals" doc:"Deals closed (won or lost) in the range, and deals opened in it."`
	Conversion ReportConversion `json:"conversion" doc:"The deals opened in the range, by the furthest stage each has reached."`
	Timeline   []ReportBucket   `json:"timeline" doc:"Deals won and lost in the range, per bucket. Every bucket is present; an empty one is a real zero."`
	// LeadSources is null without leads:read.
	LeadSources *ReportLeadSources `json:"lead_sources" doc:"Leads that arrived in the range, by source. Null without leads:read."`
	// Inbox is null without inbox:read.
	Inbox *ReportInboxResponse `json:"inbox" doc:"Conversations started in the range and how fast the team first answered. Null without inbox:read."`
}

// ReportPipeline is the open value per stage, as the board shows it now.
type ReportPipeline struct {
	Stages             []ReportStageValue `json:"stages" doc:"Open stages in board order."`
	OpenDeals          int64              `json:"open_deals"`
	OpenValueMinor     int64              `json:"open_value_minor" doc:"Workspace currency only."`
	OtherCurrencyDeals int64              `json:"other_currency_deals" doc:"Open deals left out of the values because they are in another currency."`
}

// ReportStageValue is one open stage's column total.
type ReportStageValue struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DealCount  int64  `json:"deal_count"`
	ValueMinor int64  `json:"value_minor" doc:"Workspace currency only."`
}

// ReportDealStats is what closed in the range.
type ReportDealStats struct {
	Opened int64 `json:"opened" doc:"Deals created in the range."`
	Won    int64 `json:"won" doc:"Deals whose close date is in the range and that sit in a won stage."`
	Lost   int64 `json:"lost"`
	// WinRate is won / (won + lost), 0..1; null when nothing closed.
	WinRate           *float64 `json:"win_rate" doc:"won / (won + lost), 0..1. Null when nothing closed in the range."`
	WonValueMinor     int64    `json:"won_value_minor" doc:"Workspace currency only."`
	LostValueMinor    int64    `json:"lost_value_minor" doc:"Workspace currency only."`
	AvgWonValueMinor  *int64   `json:"avg_won_value_minor" doc:"The average won deal, workspace currency only, rounded to the minor unit. Null with no won deal in it."`
	OtherCurrencyWon  int64    `json:"other_currency_won" doc:"Won deals left out of the values because they are in another currency."`
	OtherCurrencyLost int64    `json:"other_currency_lost"`
}

// ReportConversion is the funnel for the deals opened in the range.
type ReportConversion struct {
	Opened int64 `json:"opened" doc:"Deals created in the range: the funnel's cohort."`
	// Steps are the open stages in board order, then the won stage(s) as one
	// step. Reached counts the cohort's deals that have been in that stage or
	// any later one — a deal's furthest stage, from the stage history
	// (deal_stage_events). A won deal has reached every stage.
	Steps []ReportConversionStep `json:"steps"`
	Lost  int64                  `json:"lost" doc:"Deals of the cohort that are lost now."`
}

// ReportConversionStep is one stage of the funnel.
type ReportConversionStep struct {
	StageID string `json:"stage_id" doc:"The stage's id; empty for the won step when the pipeline has several won stages."`
	Name    string `json:"name"`
	Kind    string `json:"kind" enum:"open,won"`
	Reached int64  `json:"reached"`
	// FromPrevious is reached / the previous step's reached, 0..1; null on the
	// first step or when the previous step reached none.
	FromPrevious *float64 `json:"from_previous"`
	// FromStart is reached / opened, 0..1; null when nothing was opened.
	FromStart *float64 `json:"from_start"`
}

// ReportBucket is one day, week or month of closed deals.
type ReportBucket struct {
	Start          string `json:"start" doc:"The bucket's first day (YYYY-MM-DD). A week starts on Monday."`
	Won            int64  `json:"won"`
	Lost           int64  `json:"lost"`
	WonValueMinor  int64  `json:"won_value_minor" doc:"Workspace currency only."`
	LostValueMinor int64  `json:"lost_value_minor" doc:"Workspace currency only."`
}

// ReportLeadSources is where the range's leads came from.
type ReportLeadSources struct {
	Leads     int64              `json:"leads" doc:"Leads that arrived in the range, spam excluded."`
	Spam      int64              `json:"spam" doc:"Leads in the range marked spam (not in any source row)."`
	Converted int64              `json:"converted" doc:"Of the leads, how many were converted."`
	Sources   []ReportLeadSource `json:"sources" doc:"Most leads first."`
}

// ReportLeadSource is one source's leads and what they turned into.
type ReportLeadSource struct {
	// Source is the lead's utm_source (lower-cased); failing that "google" for a
	// Google Ads click id; failing that the referring site's host; failing
	// that "direct".
	Source        string `json:"source"`
	Leads         int64  `json:"leads"`
	Converted     int64  `json:"converted"`
	WonDeals      int64  `json:"won_deals" doc:"Deals opened from these leads that are won now."`
	WonValueMinor int64  `json:"won_value_minor" doc:"Workspace currency only."`
}

// ReportInboxResponse is the range's conversations and first-response times.
type ReportInboxResponse struct {
	Channels []ReportChannelResponse `json:"channels" doc:"Always chat, email and whatsapp, in that order."`
	Total    ReportChannelResponse   `json:"total"`
}

// ReportChannelResponse is one channel's conversations and first reply.
type ReportChannelResponse struct {
	Channel       string   `json:"channel" doc:"chat, email, whatsapp, or all."`
	Conversations int64    `json:"conversations" doc:"Started in the range."`
	Answered      int64    `json:"answered" doc:"Of those, how many the team has replied to."`
	Awaiting      int64    `json:"awaiting" doc:"Of those, how many are still waiting for a first reply."`
	MedianSeconds *float64 `json:"median_seconds" doc:"From the customer's first message to the team's first reply after it. Null when nothing was answered."`
	MeanSeconds   *float64 `json:"mean_seconds"`
}

// Access is what the caller may read beyond the CRM itself.
type Access struct {
	Leads bool
	Inbox bool
}

// Range is the report's days, resolved in the workspace's time zone.
type Range struct {
	From, To string // YYYY-MM-DD, as asked; empty = the default window
	// Days, with no From, is the window ending on To (or today): the page's
	// presets ask for "the last 30 days" and the server decides which days
	// those are, in the workspace's zone rather than the browser's.
	Days int
}

// ErrBadRange is a range that cannot be read: unparseable, reversed or too long.
var ErrBadRange = errors.New("bad range")

// ErrNotFound is a workspace that is gone (or not visible through RLS).
var ErrNotFound = errors.New("workspace not found")

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// window turns the asked days into [start, end) instants in loc.
func window(r Range, loc *time.Location, now time.Time) (from, to time.Time, err error) {
	today := now.In(loc)
	lastDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	parse := func(s string) (time.Time, error) {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: dates are YYYY-MM-DD", ErrBadRange)
		}
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc), nil
	}
	to = lastDay
	if strings.TrimSpace(r.To) != "" {
		if to, err = parse(r.To); err != nil {
			return
		}
	}
	n := DefaultRangeDays
	if r.Days != 0 {
		if r.Days < 1 || r.Days > MaxRangeDays {
			err = fmt.Errorf("%w: days is 1 to %d", ErrBadRange, MaxRangeDays)
			return
		}
		n = r.Days
	}
	from = to.AddDate(0, 0, -(n - 1))
	if strings.TrimSpace(r.From) != "" {
		if from, err = parse(r.From); err != nil {
			return
		}
	}
	if from.After(to) {
		err = fmt.Errorf("%w: from is after to", ErrBadRange)
		return
	}
	if days(from, to) > MaxRangeDays {
		err = fmt.Errorf("%w: a report covers at most %d days", ErrBadRange, MaxRangeDays)
		return
	}
	return from, to, nil
}

// days counts the calendar days from..to inclusive (DST-proof: by date, not hours).
func days(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours()/24) + 1
}

func bucketFor(n int) string {
	switch {
	case n <= 31:
		return "day"
	case n <= 184:
		return "week"
	}
	return "month"
}

// BuildCRM reads the whole report for one workspace. ctx must carry the
// workspace (tenant.WithWorkspace or a request's scope) for the RLS pool.
func BuildCRM(ctx context.Context, db *sql.DB, wsID string, access Access, r Range, now time.Time) (*CRMReport, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rep := &CRMReport{GeneratedAt: now.UTC()}
	if err := tx.QueryRowContext(ctx, `SELECT currency, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&rep.Currency, &rep.Timezone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	loc, err := time.LoadLocation(rep.Timezone)
	if err != nil || rep.Timezone == "" {
		// An unknown zone would otherwise fail the page; the answer says which it used.
		loc, rep.Timezone = time.UTC, "UTC"
	}
	fromDay, toDay, err := window(r, loc, now)
	if err != nil {
		return nil, err
	}
	start, end := fromDay, toDay.AddDate(0, 0, 1) // [start, end)
	rep.From, rep.To = fromDay.Format("2006-01-02"), toDay.Format("2006-01-02")
	rep.Days = days(fromDay, toDay)
	rep.Bucket = bucketFor(rep.Days)

	b := &builder{ctx: ctx, q: tx, ws: wsID, cur: rep.Currency, tz: rep.Timezone, start: start, end: end}
	rep.Pipeline = b.pipeline()
	rep.Deals = b.deals()
	rep.Conversion = b.conversion()
	rep.Timeline = b.timeline(rep.Bucket, rep.From, rep.To)
	if access.Leads {
		rep.LeadSources = b.leadSources()
	}
	if access.Inbox {
		rep.Inbox = b.inbox()
	}
	if b.err != nil {
		return nil, b.err
	}
	return rep, nil
}

// builder runs the section queries and keeps the first error.
type builder struct {
	ctx        context.Context
	q          queryer
	ws, cur    string
	tz         string
	start, end time.Time
	err        error
}

func (b *builder) fail(err error) bool {
	if err != nil && b.err == nil {
		b.err = err
	}
	return b.err != nil
}

func (b *builder) each(q string, args []any, scan func(*sql.Rows) error) {
	if b.err != nil {
		return
	}
	rows, err := b.q.QueryContext(b.ctx, q, args...)
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

func (b *builder) row(q string, args []any, dest ...any) {
	if b.err != nil {
		return
	}
	b.fail(b.q.QueryRowContext(b.ctx, q, args...).Scan(dest...))
}

func ratio(n, d int64) *float64 {
	if d <= 0 {
		return nil
	}
	v := float64(n) / float64(d)
	return &v
}

// ---- pipeline now ------------------------------------------------------------

func (b *builder) pipeline() ReportPipeline {
	p := ReportPipeline{Stages: []ReportStageValue{}}
	b.each(`
		SELECT st.id, st.name, count(d.id),
		       coalesce(sum(d.value_minor) FILTER (WHERE d.currency = $2), 0),
		       count(d.id) FILTER (WHERE d.currency <> $2)
		FROM pipeline_stages st
		LEFT JOIN deals d ON d.workspace_id = st.workspace_id AND d.stage_id = st.id
		WHERE st.workspace_id = $1 AND st.kind = 'open'
		GROUP BY st.id ORDER BY st.position, st.created_at`, []any{b.ws, b.cur}, func(r *sql.Rows) error {
		var s ReportStageValue
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
	return p
}

// ---- closed in the range -------------------------------------------------------

func (b *builder) deals() ReportDealStats {
	var s ReportDealStats
	var avg sql.NullFloat64
	b.row(`
		SELECT count(*) FILTER (WHERE st.kind = 'won'),
		       count(*) FILTER (WHERE st.kind = 'lost'),
		       coalesce(sum(d.value_minor) FILTER (WHERE st.kind = 'won' AND d.currency = $4), 0),
		       coalesce(sum(d.value_minor) FILTER (WHERE st.kind = 'lost' AND d.currency = $4), 0),
		       avg(d.value_minor) FILTER (WHERE st.kind = 'won' AND d.currency = $4),
		       count(*) FILTER (WHERE st.kind = 'won' AND d.currency <> $4),
		       count(*) FILTER (WHERE st.kind = 'lost' AND d.currency <> $4)
		FROM deals d
		JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		WHERE d.workspace_id = $1 AND st.kind <> 'open' AND d.closed_at >= $2 AND d.closed_at < $3`,
		[]any{b.ws, b.start, b.end, b.cur},
		&s.Won, &s.Lost, &s.WonValueMinor, &s.LostValueMinor, &avg, &s.OtherCurrencyWon, &s.OtherCurrencyLost)
	b.row(`SELECT count(*) FROM deals WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3`,
		[]any{b.ws, b.start, b.end}, &s.Opened)
	s.WinRate = ratio(s.Won, s.Won+s.Lost)
	if avg.Valid {
		v := int64(avg.Float64 + 0.5)
		s.AvgWonValueMinor = &v
	}
	return s
}

// ---- conversion ---------------------------------------------------------------

// conversionSQL: the cohort is the deals created in the range. For each, the
// furthest OPEN stage it has been in (from its stage history, or where it is
// now), whether it is won now, and whether it is lost now.
const conversionSQL = `
	WITH cohort AS (
		SELECT d.id, cur.kind AS cur_kind,
		       greatest(
		         CASE WHEN cur.kind = 'open' THEN cur.position ELSE -1 END,
		         coalesce((SELECT max(st.position) FROM deal_stage_events e
		                   JOIN pipeline_stages st ON st.workspace_id = e.workspace_id AND st.id = e.stage_id AND st.kind = 'open'
		                   WHERE e.workspace_id = d.workspace_id AND e.deal_id = d.id), -1)
		       ) AS furthest
		FROM deals d
		JOIN pipeline_stages cur ON cur.workspace_id = d.workspace_id AND cur.id = d.stage_id
		WHERE d.workspace_id = $1 AND d.created_at >= $2 AND d.created_at < $3
	), opens AS (
		SELECT st.id, st.name, st.position,
		       row_number() OVER (ORDER BY st.position, st.created_at) AS n
		FROM pipeline_stages st WHERE st.workspace_id = $1 AND st.kind = 'open'
	)
	SELECT o.id, o.name,
	       -- Every deal enters the pipeline, so the first stage is reached by all.
	       (SELECT count(*) FROM cohort c WHERE o.n = 1 OR c.cur_kind = 'won' OR c.furthest >= o.position)
	FROM opens o ORDER BY o.n`

func (b *builder) conversion() ReportConversion {
	c := ReportConversion{Steps: []ReportConversionStep{}}
	b.row(`
		SELECT count(*), count(*) FILTER (WHERE st.kind = 'lost')
		FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		WHERE d.workspace_id = $1 AND d.created_at >= $2 AND d.created_at < $3`,
		[]any{b.ws, b.start, b.end}, &c.Opened, &c.Lost)
	b.each(conversionSQL, []any{b.ws, b.start, b.end}, func(r *sql.Rows) error {
		s := ReportConversionStep{Kind: "open"}
		if err := r.Scan(&s.StageID, &s.Name, &s.Reached); err != nil {
			return err
		}
		c.Steps = append(c.Steps, s)
		return nil
	})
	// The won step: one row whatever the number of won stages (the pipeline
	// may have several; they are one outcome).
	won := ReportConversionStep{Kind: "won"}
	var ids, names []string
	b.each(`SELECT id, name FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'won' ORDER BY position, created_at`,
		[]any{b.ws}, func(r *sql.Rows) error {
			var id, name string
			if err := r.Scan(&id, &name); err != nil {
				return err
			}
			ids, names = append(ids, id), append(names, name)
			return nil
		})
	if len(ids) > 0 {
		if len(ids) == 1 {
			won.StageID = ids[0]
		}
		won.Name = strings.Join(names, " / ")
		b.row(`
			SELECT count(*) FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
			WHERE d.workspace_id = $1 AND d.created_at >= $2 AND d.created_at < $3 AND st.kind = 'won'`,
			[]any{b.ws, b.start, b.end}, &won.Reached)
		c.Steps = append(c.Steps, won)
	}
	for i := range c.Steps {
		c.Steps[i].FromStart = ratio(c.Steps[i].Reached, c.Opened)
		if i > 0 {
			c.Steps[i].FromPrevious = ratio(c.Steps[i].Reached, c.Steps[i-1].Reached)
		}
	}
	return c
}

// ---- over time ----------------------------------------------------------------

func (b *builder) timeline(unit, from, to string) []ReportBucket {
	list := []ReportBucket{}
	// Buckets are the workspace's local days; generate_series makes every one,
	// so a quiet week is a zero that was measured, not a gap.
	b.each(`
		WITH closed AS (
			SELECT date_trunc($6, d.closed_at AT TIME ZONE $5) AS b, st.kind, d.value_minor, d.currency
			FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
			WHERE d.workspace_id = $1 AND st.kind <> 'open' AND d.closed_at >= $2 AND d.closed_at < $3
		)
		SELECT to_char(g.b, 'YYYY-MM-DD'),
		       count(c.kind) FILTER (WHERE c.kind = 'won'),
		       count(c.kind) FILTER (WHERE c.kind = 'lost'),
		       coalesce(sum(c.value_minor) FILTER (WHERE c.kind = 'won' AND c.currency = $4), 0),
		       coalesce(sum(c.value_minor) FILTER (WHERE c.kind = 'lost' AND c.currency = $4), 0)
		FROM generate_series(date_trunc($6, $7::date::timestamp), $8::date::timestamp, ('1 ' || $6)::interval) AS g(b)
		LEFT JOIN closed c ON c.b = g.b
		GROUP BY g.b ORDER BY g.b`,
		[]any{b.ws, b.start, b.end, b.cur, b.tz, unit, from, to}, func(r *sql.Rows) error {
			var k ReportBucket
			if err := r.Scan(&k.Start, &k.Won, &k.Lost, &k.WonValueMinor, &k.LostValueMinor); err != nil {
				return err
			}
			list = append(list, k)
			return nil
		})
	return list
}

// ---- lead sources -------------------------------------------------------------

// leadSourceExpr is the one definition of a lead's source, also used by tests
// and quoted in the API doc: utm_source, else a Google Ads click, else the
// referring host (without www./l./m.), else direct.
const leadSourceExpr = `CASE
	WHEN btrim(l.utm_source) <> '' THEN lower(btrim(l.utm_source))
	WHEN btrim(l.gclid) <> '' THEN 'google'
	ELSE coalesce(nullif(regexp_replace(lower(substring(btrim(l.referrer) from '^[a-zA-Z][a-zA-Z0-9+.-]*://([^/:?#]+)')), '^(www|l|lm|m)\.', ''), ''), 'direct')
END`

func (b *builder) leadSources() *ReportLeadSources {
	ls := &ReportLeadSources{Sources: []ReportLeadSource{}}
	b.row(`
		SELECT count(*) FILTER (WHERE status <> 'spam'), count(*) FILTER (WHERE status = 'spam'),
		       count(*) FILTER (WHERE status <> 'spam' AND converted_at IS NOT NULL)
		FROM leads WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3`,
		[]any{b.ws, b.start, b.end}, &ls.Leads, &ls.Spam, &ls.Converted)
	b.each(`
		SELECT `+leadSourceExpr+` AS src,
		       count(*),
		       count(*) FILTER (WHERE l.converted_at IS NOT NULL),
		       count(d.id) FILTER (WHERE st.kind = 'won'),
		       coalesce(sum(d.value_minor) FILTER (WHERE st.kind = 'won' AND d.currency = $4), 0)
		FROM leads l
		LEFT JOIN deals d ON d.workspace_id = l.workspace_id AND d.id = l.deal_id
		LEFT JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		WHERE l.workspace_id = $1 AND l.created_at >= $2 AND l.created_at < $3 AND l.status <> 'spam'
		GROUP BY src ORDER BY count(*) DESC, src`,
		[]any{b.ws, b.start, b.end, b.cur}, func(r *sql.Rows) error {
			var s ReportLeadSource
			if err := r.Scan(&s.Source, &s.Leads, &s.Converted, &s.WonDeals, &s.WonValueMinor); err != nil {
				return err
			}
			ls.Sources = append(ls.Sources, s)
			return nil
		})
	return ls
}

// ---- inbox ---------------------------------------------------------------------

// inboxSQL is internal/inbox's statsSQL (MH-383) with an upper bound: the first
// reply is the team's first message after the customer's first one; a thread
// nobody answered is `awaiting`, never averaged in as zero.
const inboxSQL = `
	WITH conv AS (
		SELECT c.id, c.channel,
		       (SELECT min(m.created_at) FROM messages m
		         WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id AND m.direction = 'in') AS first_in
		FROM conversations c
		WHERE c.workspace_id = $1 AND c.created_at >= $2 AND c.created_at < $3
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
	       avg(extract(epoch FROM first_out - first_in)) FILTER (WHERE first_out IS NOT NULL)
	FROM timed
	GROUP BY ROLLUP (channel)`

func (b *builder) inbox() *ReportInboxResponse {
	res := &ReportInboxResponse{Total: ReportChannelResponse{Channel: "all"}}
	by := map[string]ReportChannelResponse{}
	b.each(inboxSQL, []any{b.ws, b.start, b.end}, func(r *sql.Rows) error {
		var c ReportChannelResponse
		var med, mean sql.NullFloat64
		if err := r.Scan(&c.Channel, &c.Conversations, &c.Answered, &c.Awaiting, &med, &mean); err != nil {
			return err
		}
		if med.Valid {
			v := med.Float64
			c.MedianSeconds = &v
		}
		if mean.Valid {
			v := mean.Float64
			c.MeanSeconds = &v
		}
		by[c.Channel] = c
		return nil
	})
	for _, ch := range []string{"chat", "email", "whatsapp"} {
		c, ok := by[ch]
		if !ok {
			c = ReportChannelResponse{Channel: ch}
		}
		res.Channels = append(res.Channels, c)
	}
	if c, ok := by["all"]; ok {
		res.Total = c
	}
	return res
}
