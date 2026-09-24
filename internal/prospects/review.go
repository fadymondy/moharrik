package prospects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/research"
)

// Prospect is one company discovery found, with its score and verdict.
type Prospect struct {
	ID            string           `json:"id"`
	SuggestionID  string           `json:"suggestion_id"`
	Name          string           `json:"name"`
	Domain        string           `json:"domain"`
	CompanyKey    string           `json:"company_key"`
	Status        string           `json:"status" enum:"new,accepted,rejected"`
	Score         float64          `json:"score"`
	Reasons       []ProspectReason `json:"reasons"`
	SignalTypes   []string         `json:"signal_types"`
	EvidenceCount int              `json:"evidence_count"`
	Contact       ProspectContact  `json:"contact" doc:"The person the pages name, if any. Empty name: write to the team."`
	CRMCompanyID  string           `json:"crm_company_id"`
	CRMContactID  string           `json:"crm_contact_id"`
	CRMDealID     string           `json:"crm_deal_id"`
	RejectReason  string           `json:"reject_reason"`
	FirstRunID    string           `json:"first_run_id"`
	DecidedAt     *time.Time       `json:"decided_at"`
	ScoredAt      time.Time        `json:"scored_at"`
	CreatedAt     time.Time        `json:"created_at"`
}

// ProspectDetail is a prospect with its signal timeline and drafts.
type ProspectDetail struct {
	Prospect Prospect `json:"prospect"`
	// Timeline is the evidence, oldest first: the order is the "why now".
	Timeline []research.Evidence `json:"timeline"`
	Drafts   []ProspectDraft     `json:"drafts"`
}

// ProspectDraft is one outreach draft. Moharrik never sends it.
type ProspectDraft struct {
	ID          string    `json:"id"`
	Locale      string    `json:"locale" enum:"en,ar"`
	Subject     string    `json:"subject"`
	Body        string    `json:"body"`
	Generator   string    `json:"generator" doc:"template (no model configured) or model:<provider>."`
	EvidenceIDs []string  `json:"evidence_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

var (
	ErrNoProspect = errors.New("no such prospect")
	ErrDecided    = errors.New("that prospect has already been decided")
)

const prospectColumns = `p.id, p.suggestion_id, s.name, s.domain, s.company_key, p.status, p.score, p.reasons::text, s.signal_types::text,
	s.evidence_count, p.contact_name, p.contact_title, p.contact_email, p.contact_source_url,
	coalesce(p.crm_company_id, ''), coalesce(p.crm_contact_id, ''), coalesce(p.crm_deal_id, ''), p.reject_reason, p.first_run_id,
	p.decided_at, p.scored_at, p.created_at`

const prospectFrom = `FROM prospects p JOIN research_companies s ON s.workspace_id = p.workspace_id AND s.id = p.suggestion_id`

func scanProspect(row interface{ Scan(...any) error }) (Prospect, error) {
	var p Prospect
	var reasons, sig string
	var decided sql.NullTime
	err := row.Scan(&p.ID, &p.SuggestionID, &p.Name, &p.Domain, &p.CompanyKey, &p.Status, &p.Score, &reasons, &sig,
		&p.EvidenceCount, &p.Contact.Name, &p.Contact.Title, &p.Contact.Email, &p.Contact.SourceURL,
		&p.CRMCompanyID, &p.CRMContactID, &p.CRMDealID, &p.RejectReason, &p.FirstRunID, &decided, &p.ScoredAt, &p.CreatedAt)
	_ = json.Unmarshal([]byte(reasons), &p.Reasons)
	if p.Reasons == nil {
		p.Reasons = []ProspectReason{}
	}
	p.SignalTypes = pgList(sig)
	if decided.Valid {
		p.DecidedAt = &decided.Time
	}
	return p, err
}

// Query filters the prospect list.
type Query struct {
	Status   string // new (default) | accepted | rejected | all
	Q        string
	MinScore float64
	Limit    int
}

// List returns prospects, best first.
func List(ctx context.Context, db *sql.DB, ws string, q Query) ([]Prospect, map[string]int, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	status := q.Status
	if status == "" {
		status = "new"
	}
	args := []any{ws, status, q.MinScore, q.Limit}
	where := `p.workspace_id = $1 AND ($2 = 'all' OR p.status = $2) AND p.score >= $3`
	if s := strings.TrimSpace(q.Q); s != "" {
		args = append(args, "%"+s+"%")
		where += ` AND (s.name ILIKE $5 OR s.domain ILIKE $5)`
	}
	rows, err := db.QueryContext(ctx, `SELECT `+prospectColumns+` `+prospectFrom+` WHERE `+where+`
		ORDER BY p.score DESC, p.created_at DESC LIMIT $4`, args...)
	if err != nil {
		return nil, nil, err
	}
	out := []Prospect{}
	for rows.Next() {
		p, err := scanProspect(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	counts := map[string]int{"new": 0, "accepted": 0, "rejected": 0}
	crows, err := db.QueryContext(ctx, `SELECT status, count(*) FROM prospects WHERE workspace_id = $1 GROUP BY status`, ws)
	if err != nil {
		return nil, nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var s string
		var n int
		if crows.Scan(&s, &n) == nil {
			counts[s] = n
		}
	}
	return out, counts, crows.Err()
}

func get(ctx context.Context, db *sql.DB, ws, id string) (*Prospect, error) {
	p, err := scanProspect(db.QueryRowContext(ctx, `SELECT `+prospectColumns+` `+prospectFrom+` WHERE p.workspace_id = $1 AND p.id = $2`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoProspect
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Get returns one prospect with its signal timeline (evidence, oldest first)
// and its drafts.
func Get(ctx context.Context, db *sql.DB, ws, id string) (*ProspectDetail, error) {
	p, err := get(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	ev, err := research.CompanyEvidence(ctx, db, ws, p.CompanyKey, 100)
	if err != nil {
		return nil, err
	}
	when := func(e research.Evidence) time.Time {
		if e.PublishedAt != nil {
			return *e.PublishedAt
		}
		return e.CapturedAt
	}
	// The same sentence from the same page, found by two runs, is one event.
	seen := map[string]bool{}
	uniq := ev[:0]
	for _, e := range ev {
		k := research.DedupeKey(e.Quote) + "|" + e.SourceURL
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, e)
		}
	}
	ev = uniq
	sort.SliceStable(ev, func(i, j int) bool { return when(ev[i]).Before(when(ev[j])) })
	d := &ProspectDetail{Prospect: *p, Timeline: ev, Drafts: []ProspectDraft{}}
	rows, err := db.QueryContext(ctx, `SELECT id, locale, subject, body, generator, evidence_ids::text, created_at FROM prospect_drafts
		WHERE workspace_id = $1 AND prospect_id = $2 ORDER BY created_at DESC LIMIT 10`, ws, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dr ProspectDraft
		var ids string
		if err := rows.Scan(&dr.ID, &dr.Locale, &dr.Subject, &dr.Body, &dr.Generator, &ids, &dr.CreatedAt); err != nil {
			return nil, err
		}
		dr.EvidenceIDs = pgList(ids)
		d.Drafts = append(d.Drafts, dr)
	}
	return d, rows.Err()
}

// ProspectAcceptInput is what the reviewer confirms.
type ProspectAcceptInput struct {
	ContactName  string `json:"contact_name,omitempty" maxLength:"120"`
	ContactEmail string `json:"contact_email,omitempty" maxLength:"254" doc:"Needed to add the contact: a contact is an address. Without one, the company and the deal are added and the contact is not."`
	ContactTitle string `json:"contact_title,omitempty" maxLength:"120"`
	DealTitle    string `json:"deal_title,omitempty" maxLength:"160"`
	ValueMinor   int64  `json:"value_minor,omitempty" minimum:"0"`
}

// ProspectAccepted is what accepting did.
type ProspectAccepted struct {
	Prospect       *Prospect `json:"prospect"`
	CompanyID      string    `json:"company_id"`
	CompanyCreated bool      `json:"company_created"`
	ContactID      string    `json:"contact_id" doc:"Empty when no email was given: a contact needs an address."`
	DealID         string    `json:"deal_id"`
}

// Accept adds the prospect to the CRM: the company (or the one already
// there), the contact when there is an address, and a deal on the pipeline,
// with the evidence and the score's reasons on the timeline. It is recorded
// as feedback, which ranks similar prospects higher.
func Accept(ctx context.Context, db *sql.DB, ws, userID, id string, in ProspectAcceptInput) (*ProspectAccepted, error) {
	p, err := get(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(in.ContactEmail))
	if email != "" {
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			return nil, errors.New("that contact email is not an address")
		}
	}
	res, err := db.ExecContext(ctx, `UPDATE prospects SET status = 'accepted', decided_by = $3, decided_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'new'`, ws, id, userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrDecided
	}
	undo := func() {
		_, _ = db.ExecContext(context.WithoutCancel(ctx), `UPDATE prospects SET status = 'new', decided_by = '', decided_at = NULL
			WHERE workspace_id = $1 AND id = $2`, ws, id)
	}

	out := &ProspectAccepted{}
	// 1. The company: through research's own accept, so the suggestion and
	// the evidence note are one path whichever page it is accepted from.
	sug, err := research.GetSuggestion(ctx, db, ws, p.SuggestionID, 0)
	if err != nil {
		undo()
		return nil, err
	}
	switch {
	case sug.Status == "suggested" || sug.Status == "dismissed":
		acc, err := research.Accept(ctx, db, ws, userID, p.SuggestionID, research.SuggestionAcceptInput{})
		if err != nil {
			undo()
			return nil, err
		}
		out.CompanyID, out.CompanyCreated = acc.CompanyID, acc.CompanyCreated
	case sug.CRMCompanyID != "":
		out.CompanyID = sug.CRMCompanyID
	default:
		undo()
		return nil, errors.New("this company was accepted once and its CRM record has since been deleted; add it on the Companies page")
	}

	// 2. The contact, when there is an address. Deduped on email like every
	// other way in: an address the workspace has is reused.
	name := strings.TrimSpace(in.ContactName)
	if name == "" {
		name = p.Contact.Name
	}
	title := strings.TrimSpace(in.ContactTitle)
	if title == "" {
		title = p.Contact.Title
	}
	if email != "" {
		if err := db.QueryRowContext(ctx, `
			INSERT INTO contacts (workspace_id, email, name, title, company_id, source) VALUES ($1, $2, $3, $4, $5, 'prospect')
			ON CONFLICT (workspace_id, email) DO UPDATE SET
			  name = CASE WHEN contacts.name = '' THEN EXCLUDED.name ELSE contacts.name END,
			  title = CASE WHEN contacts.title = '' THEN EXCLUDED.title ELSE contacts.title END,
			  company_id = coalesce(contacts.company_id, EXCLUDED.company_id), updated_at = now()
			RETURNING id`, ws, email, name, title, out.CompanyID).Scan(&out.ContactID); err != nil {
			return nil, fmt.Errorf("the company was added, but the contact could not be: %w", err)
		}
	}

	// 3. The deal, in the first open stage, in the workspace's currency.
	if err := crm.EnsureStages(ctx, db, ws); err != nil {
		return nil, err
	}
	var stage, currency, locale string
	if err := db.QueryRowContext(ctx, `SELECT id FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open' ORDER BY position, created_at LIMIT 1`,
		ws).Scan(&stage); err != nil {
		return nil, err
	}
	_ = db.QueryRowContext(ctx, `SELECT currency, locale FROM workspaces WHERE id = $1`, ws).Scan(&currency, &locale)
	dealTitle := strings.TrimSpace(in.DealTitle)
	if dealTitle == "" {
		dealTitle = p.Name + " — prospect"
		if locale == "ar" {
			dealTitle = p.Name + " — عميل محتمل"
		}
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, position, contact_id, company_id, owner_user_id, created_by)
		VALUES ($1, $2, $3, $4, $5, (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $5), $6, $7, $8, $8)
		RETURNING id`, ws, trim(dealTitle, 160), in.ValueMinor, currency, stage, nullable(out.ContactID), out.CompanyID, userID).Scan(&out.DealID); err != nil {
		return nil, fmt.Errorf("the company was added, but the deal could not be: %w", err)
	}

	// 4. Why, on the deal's timeline: the score's reasons.
	var b strings.Builder
	fmt.Fprintf(&b, "Prospect accepted from discovery, score %.0f/100:\n", p.Score)
	for _, r := range p.Reasons {
		fmt.Fprintf(&b, "• %s (%+.0f)\n", r.Text, r.Points)
	}
	if p.Contact.SourceURL != "" && (p.Contact.Name != "" || p.Contact.Email != "") {
		fmt.Fprintf(&b, "\nContact found on %s\n", p.Contact.SourceURL)
	}
	if _, err := crm.CreateActivity(ctx, db, crm.ActivityInput{Kind: "note", Body: trim(b.String(), 4900), DealID: out.DealID}); err != nil {
		return nil, fmt.Errorf("the deal was added, but its note could not be: %w", err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE prospects SET crm_company_id = $3, crm_contact_id = $4, crm_deal_id = $5, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, ws, id, out.CompanyID, nullable(out.ContactID), out.DealID); err != nil {
		return nil, err
	}
	if err := feedback(ctx, db, ws, userID, p, "accepted", ""); err != nil {
		return nil, err
	}
	if _, err := RescoreOpen(ctx, db, ws); err != nil {
		return nil, err
	}
	if out.Prospect, err = get(ctx, db, ws, id); err != nil {
		return nil, err
	}
	return out, nil
}

// Reject says no. The research suggestion is dismissed with it (so the
// Companies page stops offering it), and the verdict is feedback: similar
// prospects are ranked lower from now on, with the reason saying so.
func Reject(ctx context.Context, db *sql.DB, ws, userID, id, reason string) (*Prospect, error) {
	p, err := get(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	reason = trim(strings.TrimSpace(reason), 500)
	res, err := db.ExecContext(ctx, `UPDATE prospects SET status = 'rejected', reject_reason = $4, decided_by = $3, decided_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'new'`, ws, id, userID, reason)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrDecided
	}
	if _, err := db.ExecContext(ctx, `UPDATE research_companies SET status = 'dismissed', reviewed_by = $3, reviewed_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'suggested'`, ws, p.SuggestionID, userID); err != nil {
		return nil, err
	}
	if err := feedback(ctx, db, ws, userID, p, "rejected", reason); err != nil {
		return nil, err
	}
	if _, err := RescoreOpen(ctx, db, ws); err != nil {
		return nil, err
	}
	return get(ctx, db, ws, id)
}

// feedback records a verdict with the prospect's features as they are now.
func feedback(ctx context.Context, db *sql.DB, ws, userID string, p *Prospect, verdict, reason string) error {
	prof, err := GetProfile(ctx, db, ws)
	if err != nil {
		return err
	}
	ev, err := research.CompanyEvidence(ctx, db, ws, p.CompanyKey, 50)
	if err != nil {
		return err
	}
	_, _, f := Score(Input{Name: p.Name, Evidence: ev, Profile: prof})
	fj, _ := json.Marshal(f)
	_, err = db.ExecContext(ctx, `INSERT INTO prospect_feedback (workspace_id, prospect_id, verdict, reason, features, score_at, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`, ws, p.ID, verdict, reason, string(fj), p.Score, userID)
	return err
}

// ProspectDigest is the prospects found since the last digest.
type ProspectDigest struct {
	Since     time.Time  `json:"since"`
	Prospects []Prospect `json:"prospects"`
}

// TakeDigest returns the prospects created since the last digest and, when
// mark is true, moves the mark to now.
func TakeDigest(ctx context.Context, db *sql.DB, ws string, mark bool) (*ProspectDigest, error) {
	if err := ensureProfile(ctx, db, ws); err != nil {
		return nil, err
	}
	var since time.Time
	if err := db.QueryRowContext(ctx, `SELECT digest_at FROM prospect_profiles WHERE workspace_id = $1`, ws).Scan(&since); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+prospectColumns+` `+prospectFrom+`
		WHERE p.workspace_id = $1 AND p.created_at > $2 ORDER BY p.score DESC LIMIT 100`, ws, since)
	if err != nil {
		return nil, err
	}
	d := &ProspectDigest{Since: since, Prospects: []Prospect{}}
	for rows.Next() {
		p, err := scanProspect(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		d.Prospects = append(d.Prospects, p)
	}
	rows.Close()
	if mark {
		if _, err := db.ExecContext(ctx, `UPDATE prospect_profiles SET digest_at = now() WHERE workspace_id = $1`, ws); err != nil {
			return nil, err
		}
		_, _ = db.ExecContext(ctx, `UPDATE prospects SET digested_at = now() WHERE workspace_id = $1 AND digested_at IS NULL AND created_at > $2`, ws, since)
	}
	return d, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
