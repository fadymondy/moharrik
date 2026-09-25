// Package leads is Moharrik's lead intake (MH-390): the public endpoint an
// inquiry form on a customer's website posts to, the workspace's Leads list,
// and converting a lead into a contact, a company and a deal in one step.
//
// Same rules as internal/crm and internal/marketing: every workspace handler
// runs on the app (RLS) pool AND names workspace_id in every predicate; links
// are composite (workspace_id, id) foreign keys (internal/db/post/32_leads.sql).
// The one exception is the public POST /api/inquiry/{key}, which has no
// session: the form key plus the Origin allow-list resolve the workspace
// (marketing.ResolvePublicForm, the gate the subscribe form uses too), and
// everything after runs inside it.
//
// The core functions below take a *sql.DB and a workspace id rather than a
// request, so the REST routes and the MCP tools (internal/mcp) share them.
package leads

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/paging"
)

// Statuses. converted is reached only by Convert, never set directly.
const (
	StatusNew       = "new"
	StatusContacted = "contacted"
	StatusQualified = "qualified"
	StatusConverted = "converted"
	StatusSpam      = "spam"
)

// Statuses is every status, in the order the UI lists them.
var Statuses = []string{StatusNew, StatusContacted, StatusQualified, StatusConverted, StatusSpam}

func validStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

var (
	ErrNotFound = errors.New("lead not found")
	// ErrAlreadyConverted: converting twice would make a second deal for the
	// same inquiry. The lead already links to what it became.
	ErrAlreadyConverted = errors.New("this lead is already converted")
	// ErrStatus: converted is reached by converting, and a converted lead keeps
	// its status — its links would otherwise say one thing and the status another.
	ErrStatus = errors.New("status must be new, contacted, qualified or spam, and a converted lead stays converted")
	// ErrSpam: converting something marked spam is almost certainly a misclick.
	ErrSpam = errors.New("this lead is marked as spam — change its status first if it is real")
)

// LeadRef is a link to what a lead converted into.
type LeadRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Attribution is where the visitor came from, as their browser reported it.
type Attribution struct {
	UtmSource   string `json:"utm_source"`
	UtmMedium   string `json:"utm_medium"`
	UtmCampaign string `json:"utm_campaign"`
	UtmTerm     string `json:"utm_term"`
	UtmContent  string `json:"utm_content"`
	Referrer    string `json:"referrer"`
	Gclid       string `json:"gclid"`
	LandingPage string `json:"landing_page"`
}

// Lead is one inquiry as the workspace sees it.
type Lead struct {
	ID          string      `json:"id"`
	Status      string      `json:"status" enum:"new,contacted,qualified,converted,spam"`
	Name        string      `json:"name"`
	Email       string      `json:"email"`
	Phone       string      `json:"phone"`
	Company     string      `json:"company" doc:"What the visitor typed; the company record is converted_company."`
	Message     string      `json:"message"`
	Budget      string      `json:"budget" doc:"What the visitor typed as a budget, as typed; empty when they left it out."`
	Locale      string      `json:"locale"`
	FormID      *string     `json:"form_id"`
	FormName    string      `json:"form_name"`
	Origin      string      `json:"origin" doc:"The allowed site the form was posted from."`
	Attribution Attribution `json:"attribution"`
	// What it became. Null until converted, or after the record was deleted.
	ConvertedContact *LeadRef   `json:"converted_contact"`
	ConvertedCompany *LeadRef   `json:"converted_company"`
	ConvertedDeal    *LeadRef   `json:"converted_deal"`
	ConvertedAt      *time.Time `json:"converted_at"`
	ConvertedBy      *string    `json:"converted_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const leadSelect = `
	SELECT l.id, l.status, l.name, l.email, l.phone, l.company, l.message, l.budget, l.locale, l.form_id, l.form_name, l.origin,
	       l.utm_source, l.utm_medium, l.utm_campaign, l.utm_term, l.utm_content, l.referrer, l.gclid, l.landing_page,
	       l.contact_id, coalesce(nullif(ct.name, ''), ct.email), l.company_id, co.name, l.deal_id, d.title,
	       l.converted_at, l.converted_by, l.created_at, l.updated_at
	FROM leads l
	LEFT JOIN contacts ct ON ct.workspace_id = l.workspace_id AND ct.id = l.contact_id
	LEFT JOIN companies co ON co.workspace_id = l.workspace_id AND co.id = l.company_id
	LEFT JOIN deals d ON d.workspace_id = l.workspace_id AND d.id = l.deal_id
	WHERE l.workspace_id = $1`

type scanner interface{ Scan(dest ...any) error }

func scanLead(r scanner) (Lead, error) {
	var l Lead
	var formID, contactID, contactName, companyID, companyName, dealID, dealTitle, convertedBy sql.NullString
	var convertedAt sql.NullTime
	a := &l.Attribution
	err := r.Scan(&l.ID, &l.Status, &l.Name, &l.Email, &l.Phone, &l.Company, &l.Message, &l.Budget, &l.Locale, &formID, &l.FormName, &l.Origin,
		&a.UtmSource, &a.UtmMedium, &a.UtmCampaign, &a.UtmTerm, &a.UtmContent, &a.Referrer, &a.Gclid, &a.LandingPage,
		&contactID, &contactName, &companyID, &companyName, &dealID, &dealTitle,
		&convertedAt, &convertedBy, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return l, err
	}
	if formID.Valid {
		l.FormID = &formID.String
	}
	if contactID.Valid {
		l.ConvertedContact = &LeadRef{contactID.String, contactName.String}
	}
	if companyID.Valid {
		l.ConvertedCompany = &LeadRef{companyID.String, companyName.String}
	}
	if dealID.Valid {
		l.ConvertedDeal = &LeadRef{dealID.String, dealTitle.String}
	}
	if convertedAt.Valid {
		l.ConvertedAt = &convertedAt.Time
	}
	if convertedBy.Valid {
		l.ConvertedBy = &convertedBy.String
	}
	return l, nil
}

// Filter narrows List. Q matches name, email, company and message.
type Filter struct {
	Status string
	Q      string
	Cursor string
	Limit  int
}

// LeadCounts is how many leads the workspace has in each status, for the filter.
type LeadCounts map[string]int

// LeadPage is one page of List, newest first, keyset-paged like the rest of
// the workspace's lists (internal/paging, MH-467; MH-603 for leads).
type LeadPage struct {
	Leads      []Lead
	Counts     LeadCounts // per-status counts of the whole workspace, not of the filtered page
	Total      int        // every lead matching the filter (across pages)
	NextCursor string
}

// List returns one page of the workspace's leads, newest first, and the
// per-status counts (of the whole workspace, not of the filtered page — the
// filter shows them).
func List(ctx context.Context, db *sql.DB, wsID string, f Filter) (*LeadPage, error) {
	limit, err := paging.Limit(f.Limit, 50, 500)
	if err != nil {
		return nil, err
	}
	cur, err := paging.Decode(f.Cursor, 2)
	if err != nil {
		return nil, err
	}
	where := `WHERE l.workspace_id = $1`
	args := []any{wsID}
	if f.Status != "" {
		args = append(args, f.Status)
		where += ` AND l.status = $` + strconv.Itoa(len(args))
	}
	if s := strings.TrimSpace(f.Q); s != "" {
		args = append(args, "%"+escapeLike(strings.ToLower(s))+"%")
		n := "$" + strconv.Itoa(len(args))
		where += ` AND (lower(l.name) LIKE ` + n + ` OR lower(l.email) LIKE ` + n + ` OR lower(l.company) LIKE ` + n + ` OR lower(l.message) LIKE ` + n + `)`
	}

	page := &LeadPage{Leads: []Lead{}}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leads l `+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}

	if cur != nil {
		ts, err := time.Parse(time.RFC3339Nano, cur[0])
		if err != nil {
			return nil, paging.ErrBadCursor
		}
		args = append(args, ts, cur[1])
		n := len(args)
		where += ` AND (l.created_at, l.id) < ($` + strconv.Itoa(n-1) + `, $` + strconv.Itoa(n) + `)`
	}
	args = append(args, limit+1)
	q := strings.TrimSuffix(leadSelect, `WHERE l.workspace_id = $1`) + where +
		` ORDER BY l.created_at DESC, l.id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		page.Leads = append(page.Leads, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Leads) > limit {
		page.Leads = page.Leads[:limit]
		last := page.Leads[limit-1]
		page.NextCursor = paging.Encode(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}

	page.Counts = LeadCounts{}
	for _, s := range Statuses {
		page.Counts[s] = 0
	}
	crow, err := db.QueryContext(ctx, `SELECT status, count(*) FROM leads WHERE workspace_id = $1 GROUP BY status`, wsID)
	if err != nil {
		return nil, err
	}
	defer crow.Close()
	for crow.Next() {
		var s string
		var n int
		if err := crow.Scan(&s, &n); err != nil {
			return nil, err
		}
		page.Counts[s] = n
	}
	return page, crow.Err()
}

// Get returns one lead with what it converted into.
func Get(ctx context.Context, db *sql.DB, wsID, id string) (*Lead, error) {
	l, err := scanLead(db.QueryRowContext(ctx, leadSelect+` AND l.id = $2`, wsID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// SetStatus moves a lead between new, contacted, qualified and spam.
func SetStatus(ctx context.Context, db *sql.DB, wsID, id, status string) (*Lead, error) {
	status = strings.TrimSpace(status)
	if !validStatus(status) || status == StatusConverted {
		return nil, ErrStatus
	}
	var current string
	err := db.QueryRowContext(ctx, `SELECT status FROM leads WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if current == StatusConverted {
		return nil, ErrStatus
	}
	// The status guard is in the UPDATE too, so a convert racing this cannot be
	// undone by it.
	if _, err := db.ExecContext(ctx, `UPDATE leads SET status = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status <> 'converted'`, wsID, id, status); err != nil {
		return nil, err
	}
	return Get(ctx, db, wsID, id)
}

// escapeLike makes user input literal inside a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// publicBase is the web origin links are built on (see internal/marketing's
// identical helper — duplicated rather than shared, the per-package convention
// internal/notifications also follows).
func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}
