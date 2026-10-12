package prospects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

/*
Prospects an agent submitted (bring-your-own-agent discovery over MCP).

The agent names the person and how to reach them, which a server run has to
dig out of the pages (FindContact). Everything else is discovery's own: the
prospect is a research suggestion, scored from its evidence by Score, and it
reaches the CRM only through Accept. The opener the agent suggests is stored
as a draft — Moharrik never sends it.
*/

// ContactChannels are the ways to reach a prospect's person that an agent found.
type ContactChannels struct {
	Email    string `json:"email,omitempty"`
	LinkedIn string `json:"linkedin,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Website  string `json:"website,omitempty"`
	Other    string `json:"other,omitempty" doc:"Another public profile URL (X, GitHub, a team page)."`
}

// AgentProspect is what UpsertAgentProspect stores.
type AgentProspect struct {
	RunID      string
	CompanyKey string
	Contact    ProspectContact
	Channels   ContactChannels
	WhyNow     string
}

// AgentProspectResult says what became of one candidate.
type AgentProspectResult struct {
	ProspectID string  `json:"prospect_id"`
	Created    bool    `json:"created"`
	Score      float64 `json:"score"`
	Status     string  `json:"status" doc:"The prospect's status: new (waiting for a person), accepted, rejected."`
}

// ErrNotSuggested: the company is not a suggestion any more (in the CRM, accepted or dismissed).
var ErrNotSuggested = errors.New("that company is not waiting to be a prospect")

// UpsertAgentProspect makes the suggestion with this key a prospect (or
// updates the one that exists: a missing person or why-now is filled, nothing
// a person decided is touched) and scores it.
func UpsertAgentProspect(ctx context.Context, db *sql.DB, ws string, in AgentProspect) (*AgentProspectResult, error) {
	var sugID, name, status string
	err := db.QueryRowContext(ctx, `SELECT id, name, status FROM research_companies WHERE workspace_id = $1 AND company_key = $2`,
		ws, in.CompanyKey).Scan(&sugID, &name, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotSuggested
	}
	if err != nil {
		return nil, err
	}
	out := &AgentProspectResult{}
	var pStatus string
	err = db.QueryRowContext(ctx, `SELECT id, status FROM prospects WHERE workspace_id = $1 AND suggestion_id = $2`, ws, sugID).Scan(&out.ProspectID, &pStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if status != "suggested" {
			return nil, ErrNotSuggested
		}
		ch, _ := json.Marshal(in.Channels)
		err = db.QueryRowContext(ctx, `INSERT INTO prospects (workspace_id, suggestion_id, first_run_id, author_kind, why_now, contact_channels,
				contact_name, contact_title, contact_email, contact_source_url)
			VALUES ($1, $2, $3, 'agent', $4, $5::jsonb, $6, $7, $8, $9)
			ON CONFLICT (workspace_id, suggestion_id) DO NOTHING RETURNING id`,
			ws, sugID, in.RunID, in.WhyNow, string(ch), in.Contact.Name, in.Contact.Title, in.Contact.Email, in.Contact.SourceURL).Scan(&out.ProspectID)
		if errors.Is(err, sql.ErrNoRows) { // created a moment ago by another submission
			return UpsertAgentProspect(ctx, db, ws, in)
		}
		if err != nil {
			return nil, err
		}
		out.Created, pStatus = true, "new"
	case err != nil:
		return nil, err
	default:
		if pStatus == "new" {
			ch, _ := json.Marshal(in.Channels)
			if _, err := db.ExecContext(ctx, `UPDATE prospects SET
					why_now = CASE WHEN why_now = '' THEN $3 ELSE why_now END,
					contact_channels = CASE WHEN contact_channels = '{}'::jsonb THEN $4::jsonb ELSE contact_channels END,
					contact_name = CASE WHEN contact_name = '' THEN $5 ELSE contact_name END,
					contact_title = CASE WHEN contact_name = '' THEN $6 ELSE contact_title END,
					contact_email = CASE WHEN contact_email = '' THEN $7 ELSE contact_email END,
					contact_source_url = CASE WHEN contact_source_url = '' THEN $8 ELSE contact_source_url END,
					updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, ws, out.ProspectID, in.WhyNow, string(ch),
				in.Contact.Name, in.Contact.Title, in.Contact.Email, in.Contact.SourceURL); err != nil {
				return nil, err
			}
		}
	}
	out.Status = pStatus
	if pStatus == "new" {
		p, err := GetProfile(ctx, db, ws)
		if err != nil {
			return nil, err
		}
		hist, err := history(ctx, db, ws)
		if err != nil {
			return nil, err
		}
		if out.Score, err = scoreOne(ctx, db, ws, out.ProspectID, name, in.CompanyKey, p, hist); err != nil {
			return nil, err
		}
	} else {
		_ = db.QueryRowContext(ctx, `SELECT score FROM prospects WHERE workspace_id = $1 AND id = $2`, ws, out.ProspectID).Scan(&out.Score)
	}
	return out, nil
}

// SaveAgentDraft stores the opener an agent suggested, as a draft. It is
// never sent: a person copies it.
func SaveAgentDraft(ctx context.Context, db *sql.DB, ws, userID, prospectID, locale, subject, body, generator string) (*ProspectDraft, error) {
	if locale != "ar" {
		locale = "en"
	}
	var ids []string
	rows, err := db.QueryContext(ctx, `SELECT e.id FROM research_evidence e
		JOIN research_companies s ON s.workspace_id = e.workspace_id AND s.company_key = e.company_key
		JOIN prospects p ON p.workspace_id = s.workspace_id AND p.suggestion_id = s.id
		WHERE p.workspace_id = $1 AND p.id = $2 ORDER BY e.confidence DESC LIMIT 5`, ws, prospectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := ProspectDraft{Locale: locale, Subject: trim(subject, 200), Body: trim(body, 4000), Generator: generator, EvidenceIDs: ids}
	if out.EvidenceIDs == nil {
		out.EvidenceIDs = []string{}
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO prospect_drafts (workspace_id, prospect_id, locale, subject, body, generator, evidence_ids, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		ws, prospectID, locale, out.Subject, out.Body, generator, pgArr(ids), userID).Scan(&out.ID, &out.CreatedAt); err != nil {
		return nil, err
	}
	return &out, nil
}
