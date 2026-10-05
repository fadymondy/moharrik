// Package opportunities is the signals view of the Opportunities page (MH-1065): the things
// in the workspace that are worth a person's next look, each with the real reason it is listed.
//
// Nothing here is scored by Moharrik. There is no intent or engagement score in the backend,
// so every signal is a plain predicate over rows that exist:
//
//	at_risk_deal        an open deal (pipeline_stages.kind = 'open') whose latest activity is 7 days ago or more.
//	                    "Latest activity" is home.LastActivitySQL: the deal's creation, its latest stage
//	                    entry, an activity on the deal or its contact, or a message on a conversation
//	                    linked to the deal or its contact. The same predicate the Home page uses.
//	new_lead            a lead (leads) created in the last 7 days whose status is new, contacted or
//	                    qualified: not converted, not spam.
//	high_fit_prospect   a prospect (prospects + research_companies) still waiting for a decision
//	                    (status = 'new') whose prospect score is 70 or more (0-100, the number the
//	                    Prospects page shows, with its own reasons).
//
// Read-only, in one REPEATABLE READ READ ONLY transaction on the RLS pool, workspace_id in every
// predicate. A kind whose source the caller may not read (contacts:read, leads:read,
// research:read; for an MCP token or OAuth grant the token must carry it too) is absent from
// `sources` and from the counts, never zeroed.
//
// Text is never pre-rendered: reasons are codes plus the data behind them; the web writes the
// sentence in English and Arabic.
package opportunities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/home"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/prospects"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// The thresholds are part of the contract: the response repeats them, so the page can state them.
const (
	QuietAfterDays  = int(home.QuietAfter / (24 * time.Hour)) // an open deal with nothing for this many days is at risk
	NewLeadDays     = 7
	HighFitScore    = home.HighFitScore
	DefaultPerKind  = 25
	MaxPerKind      = 100
	KindAtRisk      = "at_risk_deal"
	KindNewLead     = "new_lead"
	KindHighFit     = "high_fit_prospect"
	ActionOpen      = "open"
	ActionConvert   = "convert_lead"
	ActionAccept    = "accept_prospect"
	ActionDismiss   = "dismiss_prospect"
	sourceDeals     = "deals"
	sourceLeads     = "leads"
	sourceProspects = "prospects"
)

const newLeadStatusSQL = `l.status IN ('new', 'contacted', 'qualified')`

// SignalThresholds are the numbers the signals were derived with.
type SignalThresholds struct {
	QuietDays    int `json:"quiet_days" doc:"An open deal with no activity, stage move or message for this many days is at risk."`
	NewLeadDays  int `json:"new_lead_days" doc:"A lead created within this many days and not converted or spam is new."`
	HighFitScore int `json:"high_fit_score" doc:"A prospect waiting for a decision scoring at least this (0-100) is high-fit."`
}

// SignalCounts are how many signals there are per kind, before the per-kind page limit. A kind the
// caller cannot read is null.
type SignalCounts struct {
	AtRiskDeals      *int64 `json:"at_risk_deals"`
	NewLeads         *int64 `json:"new_leads"`
	HighFitProspects *int64 `json:"high_fit_prospects"`
}

// SignalItem is one signal.
type SignalItem struct {
	Kind     string `json:"kind" enum:"at_risk_deal,new_lead,high_fit_prospect"`
	Tone     string `json:"tone" enum:"warning,info,violet"`
	ID       string `json:"id" doc:"The deal, lead or prospect this is about."`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty" doc:"The company or contact (deals), the company or email (leads), the domain (prospects)."`
	// Value is the deal's value (deals only), in its own currency.
	Value *home.HomeMoney `json:"value,omitempty"`
	// Score is the prospect's 0-100 score (high-fit prospects only).
	Score   *float64                   `json:"score,omitempty"`
	Reasons []prospects.ProspectReason `json:"reasons,omitempty" doc:"A prospect's score reasons, each with its points."`
	Why     []home.HomeReason          `json:"why" doc:"The real reason, as codes: quiet_days (n, at = last activity), in_stage (text = stage, at = when it entered), new_lead (at), lead_status (text), utm / form (text), high_fit (n = score), evidence (n)."`
	// OccurredAt is the moment the signal is about: last activity (deals), created (leads), found (prospects).
	OccurredAt time.Time `json:"occurred_at"`
	// Actions are the ones that exist for this signal and that this caller may perform.
	Actions []string `json:"actions" doc:"open | convert_lead | accept_prospect | dismiss_prospect. dismiss_prospect is the prospect reject (similar prospects rank lower afterwards). Deals and leads have no dismiss."`
}

// OpportunitySignals is the whole answer.
type OpportunitySignals struct {
	Currency   string           `json:"currency"`
	Thresholds SignalThresholds `json:"thresholds"`
	Counts     SignalCounts     `json:"counts"`
	Sources    []string         `json:"sources" doc:"The sources this caller may read: deals, leads, prospects."`
	Items      []SignalItem     `json:"items" doc:"At-risk deals first (largest first), then new leads (newest first), then high-fit prospects (best score first); each kind capped at the page limit."`
}

func canSee(s *tenant.Scope, perm string) bool {
	if !authz.Can(s.Role, perm) {
		return false
	}
	if s.Via == "mcp" || s.Via == "oauth" {
		for _, sc := range s.Scopes {
			if sc == perm {
				return true
			}
		}
		return false
	}
	return true
}

// Read lists the signals for the caller in ctx. kind limits it to one kind ("" = all); perKind is
// the page size of each kind (0 = default, at most MaxPerKind).
func Read(ctx context.Context, db *sql.DB, kind string, perKind int, now time.Time) (*OpportunitySignals, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, authz.WorkspaceRead) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	switch kind {
	case "", KindAtRisk, KindNewLead, KindHighFit:
	default:
		return nil, huma.Error422UnprocessableEntity("kind must be at_risk_deal, new_lead or high_fit_prospect")
	}
	n, err := paging.Limit(perKind, DefaultPerKind, MaxPerKind)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the signals")
	}
	defer func() { _ = tx.Rollback() }()

	out := &OpportunitySignals{Items: []SignalItem{}, Sources: []string{},
		Thresholds: SignalThresholds{QuietDays: QuietAfterDays, NewLeadDays: NewLeadDays, HighFitScore: HighFitScore}}
	if err := tx.QueryRowContext(ctx, `SELECT currency FROM workspaces WHERE id = $1`, s.WorkspaceID).Scan(&out.Currency); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("workspace not found")
		}
		return nil, huma.Error500InternalServerError("could not read the signals")
	}
	want := func(k string) bool { return kind == "" || kind == k }
	fail := huma.Error500InternalServerError("could not read the signals")

	if canSee(s, authz.ContactsRead) {
		out.Sources = append(out.Sources, sourceDeals)
		items, total, err := atRisk(ctx, tx, s, n, now, want(KindAtRisk))
		if err != nil {
			return nil, fail
		}
		out.Counts.AtRiskDeals = &total
		out.Items = append(out.Items, items...)
	}
	if canSee(s, authz.LeadsRead) {
		out.Sources = append(out.Sources, sourceLeads)
		items, total, err := newLeads(ctx, tx, s, n, now, want(KindNewLead))
		if err != nil {
			return nil, fail
		}
		out.Counts.NewLeads = &total
		out.Items = append(out.Items, items...)
	}
	if canSee(s, authz.ResearchRead) {
		out.Sources = append(out.Sources, sourceProspects)
		items, total, err := highFit(ctx, tx, s, n, want(KindHighFit))
		if err != nil {
			return nil, fail
		}
		out.Counts.HighFitProspects = &total
		out.Items = append(out.Items, items...)
	}
	return out, nil
}

func i64(n int64) *int64 { return &n }

// atRisk: open deals quiet for QuietAfterDays or more, biggest first.
func atRisk(ctx context.Context, tx *sql.Tx, s *tenant.Scope, limit int, now time.Time, rows bool) ([]SignalItem, int64, error) {
	const from = `FROM deals d
		JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id AND st.kind = 'open'
		CROSS JOIN LATERAL (SELECT ` + home.LastActivitySQL + ` AS at) x
		WHERE d.workspace_id = $1 AND x.at < $2`
	cutoff := now.Add(-home.QuietAfter)
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) `+from, s.WorkspaceID, cutoff).Scan(&total); err != nil {
		return nil, 0, err
	}
	items := []SignalItem{}
	if !rows || total == 0 {
		return items, total, nil
	}
	r, err := tx.QueryContext(ctx, `
		SELECT d.id, d.title, d.value_minor, d.currency, st.name, x.at,
		       (SELECT max(e.entered_at) FROM deal_stage_events e WHERE e.workspace_id = d.workspace_id AND e.deal_id = d.id),
		       coalesce((SELECT co.name FROM companies co WHERE co.workspace_id = d.workspace_id AND co.id = d.company_id), ''),
		       coalesce((SELECT coalesce(nullif(ct.name, ''), ct.email) FROM contacts ct WHERE ct.workspace_id = d.workspace_id AND ct.id = d.contact_id), '')
		`+from+`
		ORDER BY d.value_minor DESC, x.at, d.id
		LIMIT $3`, s.WorkspaceID, cutoff, limit)
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	for r.Next() {
		var id, title, cur, stage, company, contact string
		var val int64
		var at time.Time
		var entered sql.NullTime
		if err := r.Scan(&id, &title, &val, &cur, &stage, &at, &entered, &company, &contact); err != nil {
			return nil, 0, err
		}
		days := int64(now.Sub(at).Hours() / 24)
		it := SignalItem{Kind: KindAtRisk, Tone: "warning", ID: id, Title: title, OccurredAt: at, Actions: []string{ActionOpen},
			Why: []home.HomeReason{{Code: "quiet_days", N: i64(days), At: &at}, {Code: "in_stage", Text: stage}}}
		if entered.Valid {
			it.Why[1].At = &entered.Time
		}
		it.Subtitle = company
		if it.Subtitle == "" {
			it.Subtitle = contact
		}
		if val > 0 {
			it.Value = &home.HomeMoney{Currency: cur, Minor: val}
		}
		items = append(items, it)
	}
	return items, total, r.Err()
}

// newLeads: leads from the last NewLeadDays that are neither converted nor spam, newest first.
func newLeads(ctx context.Context, tx *sql.Tx, s *tenant.Scope, limit int, now time.Time, rows bool) ([]SignalItem, int64, error) {
	since := now.Add(-time.Duration(NewLeadDays) * 24 * time.Hour)
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM leads l WHERE l.workspace_id = $1 AND `+newLeadStatusSQL+` AND l.created_at > $2`,
		s.WorkspaceID, since).Scan(&total); err != nil {
		return nil, 0, err
	}
	items := []SignalItem{}
	if !rows || total == 0 {
		return items, total, nil
	}
	r, err := tx.QueryContext(ctx, `
		SELECT l.id, l.name, l.email, l.company, l.status, l.form_name, l.utm_source, l.utm_medium, l.utm_campaign, l.created_at
		FROM leads l WHERE l.workspace_id = $1 AND `+newLeadStatusSQL+` AND l.created_at > $2
		ORDER BY l.created_at DESC, l.id LIMIT $3`, s.WorkspaceID, since, limit)
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	convert := canSee(s, authz.LeadsWrite) && canSee(s, authz.ContactsWrite)
	for r.Next() {
		var id, name, email, company, status, form, src, med, camp string
		var created time.Time
		if err := r.Scan(&id, &name, &email, &company, &status, &form, &src, &med, &camp, &created); err != nil {
			return nil, 0, err
		}
		it := SignalItem{Kind: KindNewLead, Tone: "info", ID: id, Title: name, Subtitle: company, OccurredAt: created, Actions: []string{ActionOpen},
			Why: []home.HomeReason{{Code: "new_lead", At: &created}, {Code: "lead_status", Text: status}}}
		if it.Title == "" {
			it.Title = email
		} else if it.Subtitle == "" {
			it.Subtitle = email
		}
		switch {
		case src != "":
			it.Why = append(it.Why, home.HomeReason{Code: "utm", Text: "utm_source=" + src})
		case med != "":
			it.Why = append(it.Why, home.HomeReason{Code: "utm", Text: "utm_medium=" + med})
		case camp != "":
			it.Why = append(it.Why, home.HomeReason{Code: "utm", Text: "utm_campaign=" + camp})
		case form != "":
			it.Why = append(it.Why, home.HomeReason{Code: "form", Text: form})
		}
		if convert {
			it.Actions = append(it.Actions, ActionConvert)
		}
		items = append(items, it)
	}
	return items, total, r.Err()
}

// highFit: prospects waiting for a decision that score HighFitScore or more, best first.
func highFit(ctx context.Context, tx *sql.Tx, s *tenant.Scope, limit int, rows bool) ([]SignalItem, int64, error) {
	const from = `FROM prospects p JOIN research_companies c ON c.workspace_id = p.workspace_id AND c.id = p.suggestion_id
		WHERE p.workspace_id = $1 AND p.status = 'new' AND p.score >= $2`
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) `+from, s.WorkspaceID, HighFitScore).Scan(&total); err != nil {
		return nil, 0, err
	}
	items := []SignalItem{}
	if !rows || total == 0 {
		return items, total, nil
	}
	r, err := tx.QueryContext(ctx, `SELECT p.id, c.name, c.domain, p.score, p.reasons::text, p.created_at, c.evidence_count `+from+`
		ORDER BY p.score DESC, p.created_at DESC, p.id LIMIT $3`, s.WorkspaceID, HighFitScore, limit)
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	decide := canSee(s, authz.ContactsWrite)
	for r.Next() {
		var id, name, domain string
		var score float64
		var raw string
		var created time.Time
		var evidence int64
		if err := r.Scan(&id, &name, &domain, &score, &raw, &created, &evidence); err != nil {
			return nil, 0, err
		}
		reasons := []prospects.ProspectReason{}
		_ = json.Unmarshal([]byte(raw), &reasons)
		sc := score
		it := SignalItem{Kind: KindHighFit, Tone: "violet", ID: id, Title: name, Subtitle: domain, Score: &sc, Reasons: reasons, OccurredAt: created,
			Actions: []string{ActionOpen}, Why: []home.HomeReason{{Code: "high_fit", N: i64(int64(score + 0.5)), At: &created}}}
		if evidence > 0 {
			it.Why = append(it.Why, home.HomeReason{Code: "evidence", N: i64(evidence)})
		}
		if decide {
			it.Actions = append(it.Actions, ActionAccept, ActionDismiss)
		}
		items = append(items, it)
	}
	return items, total, r.Err()
}
