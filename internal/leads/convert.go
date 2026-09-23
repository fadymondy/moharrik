package leads

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fadymondy/moharrik/internal/crm"
)

// Conversion is what converting a lead did.
type Conversion struct {
	Lead           *Lead  `json:"lead"`
	ContactID      string `json:"contact_id"`
	ContactCreated bool   `json:"contact_created" doc:"false when the email already belonged to a contact, which was reused."`
	// CompanyID is empty when there is no company to make: a free-mail address
	// (gmail, outlook …) says nothing about where somebody works.
	CompanyID      string `json:"company_id"`
	CompanyCreated bool   `json:"company_created"`
	FreeMail       bool   `json:"free_mail" doc:"The email is from a free-mail provider, so no company was made from it."`
	DealID         string `json:"deal_id"`
	ActivityID     string `json:"activity_id"`
}

/*
Convert turns a lead into CRM records in one transaction (MH-390):

 1. The CONTACT, deduped on email: an address the workspace already has is
    reused, never duplicated. Its name and phone are filled in only where
    they were blank — a form must not overwrite what somebody typed by hand.
 2. The COMPANY, from the email's domain — unless the domain is free mail
    (freemail.go), where there is no company to make. A company that already
    has that domain is reused (one per domain per workspace, crm.sql). A
    contact already filed under a company keeps it: somebody chose that.
 3. A DEAL on the workspace's pipeline, in its first open stage, in the
    workspace's currency, owned by whoever converted it.
 4. The visitor's MESSAGE as the first activity on the timeline, dated when
    they sent it rather than when it was converted.
 5. The lead marked converted, linking to all of the above.

db is the APP pool and ctx carries the workspace, so RLS confines every row;
each statement names workspace_id as well.
*/
func Convert(ctx context.Context, db *sql.DB, wsID, leadID, userID string) (*Conversion, error) {
	// Seeded outside the transaction: EnsureStages takes its own lock and
	// commits, and it is a no-op for any workspace that has used the pipeline.
	if err := crm.EnsureStages(ctx, db, wsID); err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var l struct {
		status, name, email, phone, company, message, locale, formName, budget string
		createdAt                                                              sql.NullTime
	}
	// FOR UPDATE: two people clicking Convert at once make one deal, not two.
	err = tx.QueryRowContext(ctx, `
		SELECT status, name, email, phone, company, message, locale, form_name, budget, created_at
		FROM leads WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wsID, leadID).
		Scan(&l.status, &l.name, &l.email, &l.phone, &l.company, &l.message, &l.locale, &l.formName, &l.budget, &l.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	switch l.status {
	case StatusConverted:
		return nil, ErrAlreadyConverted
	case StatusSpam:
		return nil, ErrSpam
	}

	var wsLocale, currency string
	if err := tx.QueryRowContext(ctx, `SELECT locale, currency FROM workspaces WHERE id = $1`, wsID).Scan(&wsLocale, &currency); err != nil {
		return nil, err
	}

	res := &Conversion{}
	email := strings.ToLower(strings.TrimSpace(l.email))
	contactLocale := l.locale
	if contactLocale != "ar" {
		contactLocale = "en"
	}

	// 1. The contact. xmax = 0 is true only for a row this statement inserted.
	var existingCompany sql.NullString
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO contacts (workspace_id, email, name, phone, locale, source) VALUES ($1, $2, $3, $4, $5, 'lead')
		ON CONFLICT (workspace_id, email) DO UPDATE SET
		  name = CASE WHEN contacts.name = '' THEN EXCLUDED.name ELSE contacts.name END,
		  phone = CASE WHEN contacts.phone = '' THEN EXCLUDED.phone ELSE contacts.phone END,
		  updated_at = now()
		RETURNING id, (xmax = 0), company_id`,
		wsID, email, strings.TrimSpace(l.name), strings.TrimSpace(l.phone), contactLocale,
	).Scan(&res.ContactID, &res.ContactCreated, &existingCompany); err != nil {
		return nil, err
	}

	// 2. The company.
	domain := CompanyDomain(email)
	res.FreeMail = domain == "" && EmailDomain(email) != ""
	companyName := ""
	switch {
	case existingCompany.Valid:
		res.CompanyID = existingCompany.String
		_ = tx.QueryRowContext(ctx, `SELECT name FROM companies WHERE workspace_id = $1 AND id = $2`, wsID, res.CompanyID).Scan(&companyName)
	case domain != "":
		companyName = strings.TrimSpace(l.company)
		if companyName == "" {
			companyName = companyNameFromDomain(domain)
		}
		// ON CONFLICT against the one-company-per-domain index (16_crm.sql),
		// so two leads from acme.com converted at once share one company.
		err := tx.QueryRowContext(ctx, `
			INSERT INTO companies (workspace_id, name, domain, owner_user_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (workspace_id, lower(domain)) WHERE domain <> '' DO NOTHING
			RETURNING id`, wsID, companyName, domain, userID).Scan(&res.CompanyID)
		switch {
		case err == nil:
			res.CompanyCreated = true
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `SELECT id, name FROM companies WHERE workspace_id = $1 AND lower(domain) = $2`,
				wsID, domain).Scan(&res.CompanyID, &companyName); err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE contacts SET company_id = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND company_id IS NULL`, wsID, res.ContactID, res.CompanyID); err != nil {
			return nil, err
		}
	}

	// 3. The deal, in the first open stage. Its value is the visitor's budget when that reads as one
	// amount in the workspace's currency (MH-464); otherwise 0, which the board shows as "No value
	// yet" rather than "$0.00".
	value, _ := ParseBudget(l.budget, currency)
	var stageID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open'
		ORDER BY position, created_at LIMIT 1`, wsID).Scan(&stageID); err != nil {
		return nil, err
	}
	who := firstNonEmpty(companyName, strings.TrimSpace(l.company), strings.TrimSpace(l.name), email)
	title := who + " — website inquiry"
	if wsLocale == "ar" {
		title = who + " — استفسار من الموقع"
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:160])
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, position, contact_id, company_id, owner_user_id, created_by)
		VALUES ($1, $2, $8, $3, $4, (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $4),
		        $5, $6, $7, $7)
		RETURNING id`,
		wsID, title, currency, stageID, res.ContactID, nullable(res.CompanyID), userID, value,
	).Scan(&res.DealID); err != nil {
		return nil, err
	}

	// 4. The message, first on the timeline — dated when the visitor sent it.
	header := "Website inquiry"
	if wsLocale == "ar" {
		header = "استفسار من الموقع"
	}
	if f := strings.TrimSpace(l.formName); f != "" {
		header += " (" + f + ")"
	}
	body := header + ":\n\n" + strings.TrimSpace(l.message)
	if b := strings.TrimSpace(l.budget); b != "" {
		label := "Budget"
		if wsLocale == "ar" {
			label = "الميزانية"
		}
		body += "\n\n" + label + ": " + b
	}
	if r := []rune(body); len(r) > 5000 {
		body = string(r[:5000])
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO activities (workspace_id, kind, body, contact_id, company_id, deal_id, created_by, created_at)
		VALUES ($1, 'note', $2, $3, $4, $5, $6, coalesce($7, now()))
		RETURNING id`,
		wsID, body, res.ContactID, nullable(res.CompanyID), res.DealID, userID, l.createdAt,
	).Scan(&res.ActivityID); err != nil {
		return nil, err
	}

	// 5. The lead links to what it became.
	if _, err := tx.ExecContext(ctx, `
		UPDATE leads SET status = 'converted', contact_id = $3, company_id = $4, deal_id = $5,
		  converted_at = now(), converted_by = $6, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, leadID, res.ContactID, nullable(res.CompanyID), res.DealID, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	if res.Lead, err = Get(ctx, db, wsID, leadID); err != nil {
		return nil, err
	}
	return res, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
