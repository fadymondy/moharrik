package crm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type scanner interface{ Scan(dest ...any) error }

// ---- stages ---------------------------------------------------------------

// EnsureStages seeds the default pipeline the first time a workspace touches
// the CRM — from a REST request or (internal/mcp's create_deal) an MCP tool
// call, whichever gets there first. The advisory lock makes two racing first
// touches seed once.
func EnsureStages(ctx context.Context, db *sql.DB, wsID string) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pipeline_stages WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "crm-stages:"+wsID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pipeline_stages WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var locale string
	_ = tx.QueryRowContext(ctx, `SELECT locale FROM workspaces WHERE id = $1`, wsID).Scan(&locale)
	for i, st := range defaultStages {
		name := st.en
		if locale == "ar" {
			name = st.ar
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, $2, $3, $4)`,
			wsID, name, i, st.kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func listStages(ctx context.Context, db *sql.DB, wsID string) ([]StageOut, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT st.id, st.name, st.position, st.kind, count(d.id), coalesce(sum(d.value_minor), 0)
		FROM pipeline_stages st
		LEFT JOIN deals d ON d.workspace_id = st.workspace_id AND d.stage_id = st.id
		WHERE st.workspace_id = $1
		GROUP BY st.id ORDER BY st.position, st.created_at`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []StageOut{}
	for rows.Next() {
		var s StageOut
		if err := rows.Scan(&s.ID, &s.Name, &s.Position, &s.Kind, &s.DealCount, &s.ValueMinor); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// ---- deals ----------------------------------------------------------------

const dealSelect = `
	SELECT d.id, d.title, d.value_minor, d.currency, d.stage_id, st.name, st.kind, d.position,
	       d.contact_id, ct.email, ct.name, ct.title, d.company_id, co.name,
	       d.owner_user_id, d.expected_close, d.closed_at, d.created_at
	FROM deals d
	JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
	LEFT JOIN contacts ct ON ct.workspace_id = d.workspace_id AND ct.id = d.contact_id
	LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id
	WHERE d.workspace_id = $1`

func scanDeal(r scanner) (DealOut, error) {
	var d DealOut
	var contactID, cEmail, cName, cTitle, companyID, coName, owner sql.NullString
	var expected, closed sql.NullTime
	err := r.Scan(&d.ID, &d.Title, &d.ValueMinor, &d.Currency, &d.StageID, &d.StageName, &d.StageKind, &d.Position,
		&contactID, &cEmail, &cName, &cTitle, &companyID, &coName, &owner, &expected, &closed, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	if contactID.Valid {
		d.Contact = &ContactRef{ID: contactID.String, Email: cEmail.String, Name: cName.String, Title: cTitle.String}
	}
	if companyID.Valid {
		d.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	d.OwnerUserID, d.ExpectedClose, d.ClosedAt = strp(owner), datep(expected), timep(closed)
	return d, nil
}

// listDeals returns the workspace's deals; `where` narrows with $2 (e.g. "AND d.contact_id = $2").
func listDeals(ctx context.Context, db *sql.DB, wsID, where string, args ...any) ([]DealOut, error) {
	rows, err := db.QueryContext(ctx, dealSelect+" "+where+" ORDER BY st.position, d.position, d.created_at LIMIT 2000",
		append([]any{wsID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []DealOut{}
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func getDeal(ctx context.Context, db *sql.DB, wsID, id string) (*DealOut, error) {
	d, err := scanDeal(db.QueryRowContext(ctx, dealSelect+" AND d.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ---- companies ------------------------------------------------------------

const companySelect = `
	SELECT co.id, co.name, co.domain, co.industry, co.size, co.notes, co.owner_user_id, co.created_at,
	       (SELECT count(*) FROM contacts c WHERE c.workspace_id = co.workspace_id AND c.company_id = co.id),
	       (SELECT count(*) FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
	         WHERE d.workspace_id = co.workspace_id AND d.company_id = co.id AND st.kind = 'open')
	FROM companies co WHERE co.workspace_id = $1`

func scanCompany(r scanner) (CompanyOut, error) {
	var c CompanyOut
	var owner sql.NullString
	err := r.Scan(&c.ID, &c.Name, &c.Domain, &c.Industry, &c.Size, &c.Notes, &owner, &c.CreatedAt, &c.ContactCount, &c.OpenDeals)
	c.OwnerUserID = strp(owner)
	return c, err
}

func getCompany(ctx context.Context, db *sql.DB, wsID, id string) (*CompanyOut, error) {
	c, err := scanCompany(db.QueryRowContext(ctx, companySelect+" AND co.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---- contacts -------------------------------------------------------------

func getContact(ctx context.Context, db *sql.DB, wsID, id string) (*ContactDetail, error) {
	var c ContactDetail
	var companyID, coName, owner sql.NullString
	var tags string
	err := db.QueryRowContext(ctx, `
		SELECT c.id, c.email, c.name, c.locale, c.source, c.phone, c.title, array_to_json(c.tags)::text,
		       c.company_id, co.name, c.owner_user_id, c.created_at
		FROM contacts c
		LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id
		WHERE c.workspace_id = $1 AND c.id = $2`, wsID, id,
	).Scan(&c.ID, &c.Email, &c.Name, &c.Locale, &c.Source, &c.Phone, &c.Title, &tags, &companyID, &coName, &owner, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	c.Tags = []string{}
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	if companyID.Valid {
		c.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	c.OwnerUserID = strp(owner)
	c.Subscriptions = []ListSubscription{}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.list_id, l.name, s.status
		FROM list_subscriptions s JOIN mail_lists l ON l.id = s.list_id AND l.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.contact_id = $2 ORDER BY lower(l.name)`, wsID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ListSubscription
		if err := rows.Scan(&s.ID, &s.ListID, &s.ListName, &s.Status); err != nil {
			return nil, err
		}
		c.Subscriptions = append(c.Subscriptions, s)
	}
	return &c, rows.Err()
}

func companyContacts(ctx context.Context, db *sql.DB, wsID, companyID string) ([]ContactRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, email, name, title FROM contacts
		WHERE workspace_id = $1 AND company_id = $2 ORDER BY lower(coalesce(nullif(name, ''), email))`, wsID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ContactRef{}
	for rows.Next() {
		var c ContactRef
		if err := rows.Scan(&c.ID, &c.Email, &c.Name, &c.Title); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ---- activities -----------------------------------------------------------

const activitySelect = `
	SELECT a.id, a.kind, a.body, a.due_at, a.done_at,
	       a.contact_id, ct.email, ct.name, ct.title, a.company_id, co.name, a.deal_id, d.title,
	       a.created_by, a.created_at
	FROM activities a
	LEFT JOIN contacts ct ON ct.workspace_id = a.workspace_id AND ct.id = a.contact_id
	LEFT JOIN companies co ON co.workspace_id = a.workspace_id AND co.id = a.company_id
	LEFT JOIN deals d ON d.workspace_id = a.workspace_id AND d.id = a.deal_id
	WHERE a.workspace_id = $1`

func scanActivity(r scanner) (ActivityOut, error) {
	var a ActivityOut
	var due, done sql.NullTime
	var contactID, cEmail, cName, cTitle, companyID, coName, dealID, dTitle sql.NullString
	err := r.Scan(&a.ID, &a.Kind, &a.Body, &due, &done, &contactID, &cEmail, &cName, &cTitle,
		&companyID, &coName, &dealID, &dTitle, &a.CreatedBy, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	a.DueAt, a.DoneAt = timep(due), timep(done)
	if contactID.Valid {
		a.Contact = &ContactRef{ID: contactID.String, Email: cEmail.String, Name: cName.String, Title: cTitle.String}
	}
	if companyID.Valid {
		a.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	if dealID.Valid {
		a.Deal = &Ref{ID: dealID.String, Name: dTitle.String}
	}
	return a, nil
}

// Timeline scopes. Each narrows activitySelect with $2 = the record's id.
const (
	// A contact's own activities plus those on its deals.
	timelineContact = `AND (a.contact_id = $2 OR a.deal_id IN (SELECT id FROM deals WHERE workspace_id = $1 AND contact_id = $2))`
	// A company's, its people's and its deals'.
	timelineCompany = `AND (a.company_id = $2
		OR a.contact_id IN (SELECT id FROM contacts WHERE workspace_id = $1 AND company_id = $2)
		OR a.deal_id IN (SELECT id FROM deals WHERE workspace_id = $1 AND company_id = $2))`
	timelineDeal = `AND a.deal_id = $2`
)

// timeline lists newest first; open tasks are not buried under later notes
// because the UI groups them separately.
func timeline(ctx context.Context, db *sql.DB, wsID, where, id string) ([]ActivityOut, error) {
	rows, err := db.QueryContext(ctx, activitySelect+" "+where+" ORDER BY a.created_at DESC LIMIT 500", wsID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ActivityOut{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func getActivity(ctx context.Context, db *sql.DB, wsID, id string) (*ActivityOut, error) {
	a, err := scanActivity(db.QueryRowContext(ctx, activitySelect+" AND a.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
