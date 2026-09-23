package icp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

/*
The database side: the profile, the rescore, and the lifecycle a member sets
by hand.

Scores are stored in icp_scores, one row per contact and per company, so the
lists can filter and sort on them in SQL — and kept off the contact and
company rows, so a rescore is never an edit of a contact (MH-392's audit log
records every row write). They are kept honest by staleness rather than by
every write path remembering to rescore: the triggers in
internal/db/post/106_lifecycle_icp.sql set icp_scores.stale whenever a
fact a score rests on changes (a title, a company's industry, a deal, a
subscription, a conversation, a converted lead, an activity), and every reader
calls Rescore first, which recomputes the stale rows — and any scored more than
a day ago, because "recent activity" goes stale on its own as the days pass.
Saving the profile marks every row stale and rescores the lot at once, so the
lists re-rank the moment the weights change.

Everything here runs on the app (RLS) pool with workspace_id in every
predicate, like every other workspace handler.
*/

// Lifecycle stages, in order.
var Stages = []string{"subscriber", "lead", "qualified", "customer"}

// ValidStage reports whether s is a lifecycle stage ("" = none).
func ValidStage(s string) bool {
	if s == "" {
		return true
	}
	for _, v := range Stages {
		if v == s {
			return true
		}
	}
	return false
}

// RescoreBatch bounds one pass; Rescore loops until nothing is stale.
const RescoreBatch = 2000

// RefreshAfter is how old a score may get before it is recomputed anyway.
const RefreshAfter = 24 * time.Hour

// StoredProfile is a workspace's profile and whether it was ever saved.
type StoredProfile struct {
	Profile    Profile    `json:"profile"`
	Configured bool       `json:"configured" doc:"False until the profile is saved once: the defaults are in use."`
	UpdatedAt  *time.Time `json:"updated_at"`
	UpdatedBy  *string    `json:"updated_by"`
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// LoadProfile reads the workspace's profile, or the default when it has none.
// A stored profile that no longer normalizes (a hand-edited row) falls back
// to the default rather than stopping every score.
func LoadProfile(ctx context.Context, q queryer, wsID string) (StoredProfile, error) {
	var raw []byte
	var at time.Time
	var by sql.NullString
	err := q.QueryRowContext(ctx, `SELECT criteria, updated_at, updated_by FROM icp_profiles WHERE workspace_id = $1`, wsID).Scan(&raw, &at, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredProfile{Profile: Default()}, nil
	}
	if err != nil {
		return StoredProfile{}, err
	}
	out := StoredProfile{Configured: true, UpdatedAt: &at}
	if by.Valid {
		out.UpdatedBy = &by.String
	}
	var p Profile
	if json.Unmarshal(raw, &p) != nil {
		out.Profile = Default()
		return out, nil
	}
	if out.Profile, err = p.Normalize(); err != nil {
		out.Profile = Default()
	}
	return out, nil
}

// SaveProfile stores a profile (already normalized), marks every score in the
// workspace stale and rescores them, in that order, so the caller's next read
// is ranked by the new weights.
func SaveProfile(ctx context.Context, db *sql.DB, wsID, userID string, p Profile) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var by any
	if userID != "" {
		by = userID
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO icp_profiles (workspace_id, criteria, updated_by, updated_at) VALUES ($1, $2::jsonb, $3, now())
		ON CONFLICT (workspace_id) DO UPDATE SET criteria = EXCLUDED.criteria, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		wsID, string(b), by); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE icp_scores SET stale = true WHERE workspace_id = $1 AND NOT stale`, wsID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return Rescore(ctx, db, wsID)
}

// Rescore recomputes every stale (or day-old) score in the workspace: its
// contacts, then its companies. Each batch locks the rows it scores (SKIP
// LOCKED: a concurrent rescore takes the others), so a change that lands while
// a batch runs waits for it and then marks the row stale again — never lost.
func Rescore(ctx context.Context, db *sql.DB, wsID string) error {
	sp, err := LoadProfile(ctx, db, wsID)
	if err != nil {
		return err
	}
	for _, kind := range []string{"contacts", "companies"} {
		for {
			n, err := rescoreBatch(ctx, db, wsID, kind, sp.Profile)
			if err != nil {
				return fmt.Errorf("rescore %s: %w", kind, err)
			}
			if n < RescoreBatch {
				break
			}
		}
	}
	return nil
}

// rescoreBatch scores up to RescoreBatch contacts or companies and says how
// many. table is one of two constants, never input.
func rescoreBatch(ctx context.Context, db *sql.DB, wsID, table string, p Profile) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	subject := "contact_id"
	if table == "companies" {
		subject = "company_id"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+subject+` FROM icp_scores
		WHERE workspace_id = $1 AND `+subject+` IS NOT NULL AND (stale OR scored_at IS NULL OR scored_at < $2)
		ORDER BY `+subject+` LIMIT $3 FOR UPDATE SKIP LOCKED`, wsID, time.Now().Add(-RefreshAfter), RescoreBatch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	var facts map[string]Facts
	if table == "contacts" {
		facts, err = contactFacts(ctx, tx, wsID, ids)
	} else {
		facts, err = companyFacts(ctx, tx, wsID, ids)
	}
	if err != nil {
		return 0, err
	}
	type row struct {
		ID      string   `json:"id"`
		Score   *int     `json:"score"`
		Reasons []Reason `json:"reasons"`
	}
	out := make([]row, 0, len(ids))
	for _, id := range ids {
		f, ok := facts[id]
		if !ok {
			continue
		}
		s := Compute(p, f)
		out = append(out, row{ID: id, Score: s.Score, Reasons: s.Reasons})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE icp_scores t SET score = v.score, reasons = v.reasons, stale = false, scored_at = now()
		FROM jsonb_to_recordset($2::jsonb) AS v(id text, score int, reasons jsonb)
		WHERE t.workspace_id = $1 AND t.`+subject+` = v.id`, wsID, string(b)); err != nil {
		return 0, err
	}
	return len(ids), tx.Commit()
}

// The signal columns, in Signals order, shared by both fact queries.
func scanSignals() []any {
	vals := make([]any, len(Signals))
	for i := range Signals {
		vals[i] = new(bool)
	}
	return vals
}

func fillSignals(dest map[string]bool, vals []any) {
	for i, s := range Signals {
		dest[s] = *(vals[i].(*bool))
	}
}

// contactFacts: a contact's own title and tags, its company's firmographics,
// and its signals — its deals or its company's, a converted lead, a linked
// conversation, a confirmed subscription, WhatsApp consent, recent activity
// (its own, its deals', its company's).
func contactFacts(ctx context.Context, q queryer, wsID string, ids []string) (map[string]Facts, error) {
	rows, err := q.QueryContext(ctx, `
		WITH c AS (SELECT * FROM contacts WHERE workspace_id = $1 AND id = ANY($2)),
		d AS (
			SELECT c.id AS cid, st.kind
			FROM c JOIN deals dl ON dl.workspace_id = c.workspace_id
			      AND (dl.contact_id = c.id OR (c.company_id IS NOT NULL AND dl.company_id = c.company_id))
			JOIN pipeline_stages st ON st.workspace_id = dl.workspace_id AND st.id = dl.stage_id
		)
		SELECT c.id, c.title, array_to_string(c.tags, ', '), co.id IS NOT NULL,
		       coalesce(co.industry, ''), coalesce(co.size, ''), coalesce(co.country, ''), coalesce(co.name, ''), coalesce(co.notes, ''),
		       EXISTS (SELECT 1 FROM d WHERE d.cid = c.id AND d.kind = 'open'),
		       EXISTS (SELECT 1 FROM d WHERE d.cid = c.id AND d.kind = 'won'),
		       EXISTS (SELECT 1 FROM d WHERE d.cid = c.id AND d.kind = 'lost'),
		       EXISTS (SELECT 1 FROM leads l WHERE l.workspace_id = c.workspace_id AND l.contact_id = c.id AND l.converted_at IS NOT NULL),
		       EXISTS (SELECT 1 FROM conversations v WHERE v.workspace_id = c.workspace_id AND v.contact_id = c.id),
		       EXISTS (SELECT 1 FROM list_subscriptions s WHERE s.workspace_id = c.workspace_id AND s.contact_id = c.id AND s.status = 'subscribed'),
		       c.whatsapp_opt_in_at IS NOT NULL AND c.whatsapp_opt_out_at IS NULL,
		       EXISTS (SELECT 1 FROM activities a WHERE a.workspace_id = c.workspace_id AND a.created_at > now() - make_interval(days => $3)
		                 AND (a.contact_id = c.id
		                      OR (c.company_id IS NOT NULL AND a.company_id = c.company_id)
		                      OR a.deal_id IN (SELECT dl.id FROM deals dl WHERE dl.workspace_id = c.workspace_id AND dl.contact_id = c.id)))
		FROM c LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id`, wsID, ids, RecentDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Facts{}
	for rows.Next() {
		var id, title, tags, industry, size, country, coName, notes string
		var hasCo bool
		sig := scanSignals()
		dest := append([]any{&id, &title, &tags, &hasCo, &industry, &size, &country, &coName, &notes}, sig...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		f := Facts{Industry: industry, Size: size, Country: country, HasCompany: hasCo, Signals: map[string]bool{},
			Text: map[string]string{"title": title, "tags": tags}}
		if hasCo {
			f.Text["company"] = coName
			f.Text["industry"] = industry
			f.Text["notes"] = notes
		}
		fillSignals(f.Signals, sig)
		out[id] = f
	}
	return out, rows.Err()
}

// companyFacts: a company's own firmographics and text, and its signals over
// its deals and its people.
func companyFacts(ctx context.Context, q queryer, wsID string, ids []string) (map[string]Facts, error) {
	rows, err := q.QueryContext(ctx, `
		WITH co AS (SELECT * FROM companies WHERE workspace_id = $1 AND id = ANY($2)),
		d AS (
			SELECT dl.company_id AS cid, st.kind FROM deals dl
			JOIN pipeline_stages st ON st.workspace_id = dl.workspace_id AND st.id = dl.stage_id
			WHERE dl.workspace_id = $1 AND dl.company_id = ANY($2)
		),
		p AS (SELECT * FROM contacts WHERE workspace_id = $1 AND company_id = ANY($2))
		SELECT co.id, co.name, co.industry, co.size, co.country, co.notes,
		       EXISTS (SELECT 1 FROM d WHERE d.cid = co.id AND d.kind = 'open'),
		       EXISTS (SELECT 1 FROM d WHERE d.cid = co.id AND d.kind = 'won'),
		       EXISTS (SELECT 1 FROM d WHERE d.cid = co.id AND d.kind = 'lost'),
		       EXISTS (SELECT 1 FROM leads l WHERE l.workspace_id = co.workspace_id AND l.converted_at IS NOT NULL
		                 AND (l.company_id = co.id OR l.contact_id IN (SELECT p.id FROM p WHERE p.company_id = co.id))),
		       EXISTS (SELECT 1 FROM conversations v JOIN p ON p.id = v.contact_id WHERE v.workspace_id = co.workspace_id AND p.company_id = co.id),
		       EXISTS (SELECT 1 FROM list_subscriptions s JOIN p ON p.id = s.contact_id
		                 WHERE s.workspace_id = co.workspace_id AND p.company_id = co.id AND s.status = 'subscribed'),
		       EXISTS (SELECT 1 FROM p WHERE p.company_id = co.id AND p.whatsapp_opt_in_at IS NOT NULL AND p.whatsapp_opt_out_at IS NULL),
		       EXISTS (SELECT 1 FROM activities a WHERE a.workspace_id = co.workspace_id AND a.created_at > now() - make_interval(days => $3)
		                 AND (a.company_id = co.id
		                      OR a.contact_id IN (SELECT p.id FROM p WHERE p.company_id = co.id)
		                      OR a.deal_id IN (SELECT dl.id FROM deals dl WHERE dl.workspace_id = co.workspace_id AND dl.company_id = co.id)))
		FROM co`, wsID, ids, RecentDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Facts{}
	for rows.Next() {
		var id, name, industry, size, country, notes string
		sig := scanSignals()
		if err := rows.Scan(append([]any{&id, &name, &industry, &size, &country, &notes}, sig...)...); err != nil {
			return nil, err
		}
		f := Facts{Industry: industry, Size: size, Country: country, HasCompany: true, Signals: map[string]bool{},
			Text: map[string]string{"company": name, "industry": industry, "notes": notes}}
		fillSignals(f.Signals, sig)
		out[id] = f
	}
	return out, rows.Err()
}

// ---- lifecycle ---------------------------------------------------------------------

// LifecycleEvent is one change of a contact's stage.
type LifecycleEvent struct {
	ID          string    `json:"id"`
	From        string    `json:"from" doc:"The stage before; empty for none."`
	To          string    `json:"to"`
	Source      string    `json:"source" enum:"subscribed,lead_converted,deal_opened,deal_won,manual,backfill" doc:"What moved it: a confirmed subscription, a converted lead, a deal opened or won, a member (manual), or the one-time backfill."`
	ActorUserID *string   `json:"actor_user_id" doc:"The member, for a manual change (GET /api/w/members names them)."`
	At          time.Time `json:"at"`
}

// ErrNoContact is a contact that is not in the workspace.
var ErrNoContact = errors.New("contact not found")

// ErrBadStage is not one of the lifecycle stages.
var ErrBadStage = errors.New("lifecycle is one of subscriber, lead, qualified, customer, or empty")

// SetLifecycle sets a contact's stage by hand, in any direction, and records
// who did it. Setting the stage it already has changes nothing and records
// nothing.
func SetLifecycle(ctx context.Context, db *sql.DB, wsID, contactID, userID, stage string) error {
	stage = strings.TrimSpace(stage)
	if !ValidStage(stage) {
		return ErrBadStage
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var from string
	err = tx.QueryRowContext(ctx, `SELECT lifecycle FROM contacts WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wsID, contactID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoContact
	} else if err != nil {
		return err
	}
	if from == stage {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE contacts SET lifecycle = $3, lifecycle_at = now(), updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		wsID, contactID, stage); err != nil {
		return err
	}
	var actor any
	if userID != "" {
		actor = userID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source, actor_user_id)
		VALUES ($1, $2, $3, $4, 'manual', $5)`, wsID, contactID, from, stage, actor); err != nil {
		return err
	}
	return tx.Commit()
}

// MaxHistory bounds the history a contact page carries; the total says if there is more.
const MaxHistory = 100

// LifecycleHistory is a contact's stage changes, newest first, and how many in all.
func LifecycleHistory(ctx context.Context, q queryer, wsID, contactID string) ([]LifecycleEvent, int64, error) {
	var total int64
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM contact_lifecycle_events WHERE workspace_id = $1 AND contact_id = $2`,
		wsID, contactID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT e.id, e.from_stage, e.to_stage, e.source, e.actor_user_id, e.created_at
		FROM contact_lifecycle_events e
		WHERE e.workspace_id = $1 AND e.contact_id = $2
		ORDER BY e.created_at DESC, e.id DESC LIMIT $3`, wsID, contactID, MaxHistory)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []LifecycleEvent{}
	for rows.Next() {
		var e LifecycleEvent
		var actor sql.NullString
		if err := rows.Scan(&e.ID, &e.From, &e.To, &e.Source, &actor, &e.At); err != nil {
			return nil, 0, err
		}
		if actor.Valid {
			e.ActorUserID = &actor.String
		}
		list = append(list, e)
	}
	return list, total, rows.Err()
}
