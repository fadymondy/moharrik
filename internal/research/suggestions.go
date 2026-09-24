package research

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/crm"
)

/*
Company suggestions (MH-388): the companies research found, waiting for a
person.

Nothing here creates a CRM company on its own. A suggestion is accepted by a
member (REST or MCP, with contacts:write) — which creates the company, or links
the one that already has that domain or name — and the evidence goes onto the
company's timeline as a note, so why it was added is on record. A dismissed
suggestion is remembered: the next run that finds the same company updates its
counts and leaves it dismissed.
*/

// Suggestion is one company research found.
type CompanySuggestion struct {
	ID            string     `json:"id"`
	CompanyKey    string     `json:"company_key"`
	Name          string     `json:"name"`
	Domain        string     `json:"domain"`
	Status        string     `json:"status" enum:"suggested,accepted,dismissed,linked"`
	CRMCompanyID  string     `json:"crm_company_id" doc:"The CRM company, once accepted or when it was already there. Empty if that company was since deleted."`
	EvidenceCount int        `json:"evidence_count"`
	Strongest     float64    `json:"strongest"`
	SignalTypes   []string   `json:"signal_types"`
	FirstRunID    string     `json:"first_run_id"`
	LastRunID     string     `json:"last_run_id"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	Evidence      []Evidence `json:"evidence" doc:"The strongest few pieces of evidence naming it."`
}

var (
	ErrNoSuggestion  = errors.New("no such company suggestion")
	ErrAlreadyReview = errors.New("that suggestion has already been reviewed")
)

const suggestionColumns = `s.id, s.company_key, s.name, s.domain, s.status, coalesce(c.id, ''), s.evidence_count, s.strongest,
	s.signal_types::text, s.first_run_id, s.last_run_id, s.first_seen_at, s.last_seen_at, s.reviewed_at`

func scanSuggestion(row interface{ Scan(...any) error }) (CompanySuggestion, error) {
	var s CompanySuggestion
	var sig string
	var rev sql.NullTime
	err := row.Scan(&s.ID, &s.CompanyKey, &s.Name, &s.Domain, &s.Status, &s.CRMCompanyID, &s.EvidenceCount, &s.Strongest,
		&sig, &s.FirstRunID, &s.LastRunID, &s.FirstSeenAt, &s.LastSeenAt, &rev)
	s.SignalTypes = parsePgArray(sig)
	if rev.Valid {
		s.ReviewedAt = &rev.Time
	}
	s.Evidence = []Evidence{}
	return s, err
}

const suggestionFrom = `FROM research_companies s
	LEFT JOIN companies c ON c.workspace_id = s.workspace_id AND c.id = s.crm_company_id`

// ListSuggestions returns the workspace's suggestions with a status (default
// suggested), strongest first, each with its top evidence.
func ListSuggestions(ctx context.Context, db *sql.DB, ws, status string, limit int) ([]CompanySuggestion, int, error) {
	if status == "" {
		status = "suggested"
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM research_companies WHERE workspace_id = $1 AND status = $2`, ws, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+suggestionColumns+` `+suggestionFrom+`
		WHERE s.workspace_id = $1 AND s.status = $2
		ORDER BY s.strongest DESC, s.evidence_count DESC, s.last_seen_at DESC LIMIT $3`, ws, status, limit)
	if err != nil {
		return nil, 0, err
	}
	var out []CompanySuggestion
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, s)
	}
	rows.Close()
	for i := range out {
		if out[i].Evidence, err = companyEvidence(ctx, db, ws, out[i].CompanyKey, 3); err != nil {
			return nil, 0, err
		}
	}
	if out == nil {
		out = []CompanySuggestion{}
	}
	return out, total, nil
}

// GetSuggestion returns one suggestion with its evidence.
func GetSuggestion(ctx context.Context, db *sql.DB, ws, id string, evidence int) (*CompanySuggestion, error) {
	s, err := scanSuggestion(db.QueryRowContext(ctx, `SELECT `+suggestionColumns+` `+suggestionFrom+`
		WHERE s.workspace_id = $1 AND s.id = $2`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSuggestion
	}
	if err != nil {
		return nil, err
	}
	if s.Evidence, err = companyEvidence(ctx, db, ws, s.CompanyKey, evidence); err != nil {
		return nil, err
	}
	return &s, nil
}

// companyEvidence is the evidence naming a company, strongest and newest first.
func companyEvidence(ctx context.Context, db *sql.DB, ws, key string, limit int) ([]Evidence, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+evidenceColumns+`
		FROM research_evidence e
		LEFT JOIN research_runs r ON r.workspace_id = e.workspace_id AND r.id = e.run_id
		WHERE e.workspace_id = $1 AND e.company_key = $2
		ORDER BY e.confidence DESC, e.captured_at DESC LIMIT $3`, ws, key, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompanyEvidence is companyEvidence for other packages (prospects, MH-397).
func CompanyEvidence(ctx context.Context, db *sql.DB, ws, key string, limit int) ([]Evidence, error) {
	return companyEvidence(ctx, db, ws, key, limit)
}

// AcceptInput lets the reviewer correct what research read.
type SuggestionAcceptInput struct {
	Name   string `json:"name,omitempty" maxLength:"120" doc:"Defaults to the name research found."`
	Domain string `json:"domain,omitempty" maxLength:"253" doc:"Defaults to the domain research found, if any."`
}

// Accepted is what accepting did.
type SuggestionAccepted struct {
	Suggestion     *CompanySuggestion `json:"suggestion"`
	CompanyID      string             `json:"company_id"`
	CompanyCreated bool               `json:"company_created" doc:"false when a CRM company with that domain or name already existed and was linked instead."`
}

// Accept adds a suggested company to the CRM (or links the one that is already
// there) and files its evidence on the company's timeline. ctx must carry the
// member's scope: crm checks contacts:write itself.
func Accept(ctx context.Context, db *sql.DB, ws, userID, id string, in SuggestionAcceptInput) (*SuggestionAccepted, error) {
	s, err := GetSuggestion(ctx, db, ws, id, 5)
	if err != nil {
		return nil, err
	}
	if s.Status != "suggested" && s.Status != "dismissed" {
		return nil, ErrAlreadyReview
	}
	// Claimed first, so two people pressing Accept add one company.
	res, err := db.ExecContext(ctx, `UPDATE research_companies SET status = 'accepted', reviewed_by = $3, reviewed_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status IN ('suggested', 'dismissed')`, ws, id, userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrAlreadyReview
	}
	undo := func() {
		_, _ = db.ExecContext(context.WithoutCancel(ctx), `UPDATE research_companies SET status = $3, reviewed_by = '', reviewed_at = NULL
			WHERE workspace_id = $1 AND id = $2`, ws, id, s.Status)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = s.Name
	}
	domain := strings.TrimSpace(in.Domain)
	if domain == "" {
		domain = s.Domain
	}
	out := &SuggestionAccepted{}
	var existing string
	_ = db.QueryRowContext(ctx, `SELECT id FROM companies WHERE workspace_id = $1
		AND (($2 <> '' AND lower(domain) = lower($2)) OR lower(name) = lower($3)) ORDER BY created_at LIMIT 1`,
		ws, RegistrableDomain(domain), name).Scan(&existing)
	if existing != "" {
		out.CompanyID = existing
	} else {
		note := "Found by research."
		c, err := crm.CreateCompany(ctx, db, crm.CompanyFields{Name: &name, Domain: &domain, Notes: &note})
		if err != nil {
			undo()
			return nil, err
		}
		out.CompanyID, out.CompanyCreated = c.ID, true
	}
	if _, err := db.ExecContext(ctx, `UPDATE research_companies SET crm_company_id = $3 WHERE workspace_id = $1 AND id = $2`,
		ws, id, out.CompanyID); err != nil {
		return nil, err
	}

	// Why it is in the CRM, on its timeline: the evidence, with its links.
	var b strings.Builder
	b.WriteString("Added from research. Evidence:\n")
	var listed []string
	for _, e := range s.Evidence {
		// The same sentence found by two runs, or as a page's title and its
		// heading, is one line of evidence, not two.
		n := dedupeKey(e.Quote)
		seen := false
		for _, l := range listed {
			if strings.Contains(l, n) || strings.Contains(n, l) {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		listed = append(listed, n)
		text := e.Quote
		if e.Claim != "" {
			text = e.Claim + " — “" + e.Quote + "”"
		}
		fmt.Fprintf(&b, "\n• %s\n  %s\n", trim(text, 400), e.SourceURL)
	}
	if _, err := crm.CreateActivity(ctx, db, crm.ActivityInput{Kind: "note", Body: trim(b.String(), 4900), CompanyID: out.CompanyID}); err != nil {
		// The company is added; only the note failed. Said, not hidden.
		return nil, fmt.Errorf("the company was added, but its evidence note could not be saved: %w", err)
	}
	if out.Suggestion, err = GetSuggestion(ctx, db, ws, id, 5); err != nil {
		return nil, err
	}
	return out, nil
}

// Dismiss says no to a suggestion; Restore puts it back.
func Dismiss(ctx context.Context, db *sql.DB, ws, userID, id string) (*CompanySuggestion, error) {
	return setStatus(ctx, db, ws, userID, id, "suggested", "dismissed")
}

func Restore(ctx context.Context, db *sql.DB, ws, userID, id string) (*CompanySuggestion, error) {
	return setStatus(ctx, db, ws, userID, id, "dismissed", "suggested")
}

func setStatus(ctx context.Context, db *sql.DB, ws, userID, id, from, to string) (*CompanySuggestion, error) {
	res, err := db.ExecContext(ctx, `UPDATE research_companies SET status = $4, reviewed_by = $3, reviewed_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = $5`, ws, id, userID, to, from)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := GetSuggestion(ctx, db, ws, id, 0); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("only a %s suggestion can be changed that way", from)
	}
	return GetSuggestion(ctx, db, ws, id, 3)
}
