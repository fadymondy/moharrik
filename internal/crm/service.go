package crm

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/paging"
)

/*
The CRM's operations, once (MH-393).

routes.go's REST handlers and internal/mcp's CRM tools both call these, so an
agent's change goes through exactly the validation a member's does — the
owner must be a member, links must stay inside the workspace, a stage change
closes or reopens the deal — and the two cannot drift.

Each reads the caller from ctx and checks the role itself. Errors are huma
status errors: REST returns them as they are, MCP hands back their message.
*/

// CompanyFields is a company create/update. nil = unchanged.
type CompanyFields struct {
	Name        *string `json:"name,omitempty" maxLength:"120"`
	Domain      *string `json:"domain,omitempty" maxLength:"253"`
	Industry    *string `json:"industry,omitempty" maxLength:"80"`
	Size        *string `json:"size,omitempty" enum:",1-10,11-50,51-200,201-1000,1000+"`
	Notes       *string `json:"notes,omitempty" maxLength:"5000"`
	OwnerUserID *string `json:"owner_user_id,omitempty" doc:"A member's user id; empty string unassigns."`
}

// DealFields is a deal create/update. nil = unchanged.
type DealFields struct {
	Title         *string  `json:"title,omitempty" maxLength:"160"`
	ValueMinor    *int64   `json:"value_minor,omitempty" minimum:"0" doc:"Minor units (cents)."`
	Currency      *string  `json:"currency,omitempty" pattern:"^[A-Z]{3}$"`
	StageID       *string  `json:"stage_id,omitempty"`
	Position      *float64 `json:"position,omitempty" doc:"Order inside the stage column; omitted on a stage change = last."`
	ContactID     *string  `json:"contact_id,omitempty" doc:"Empty string unlinks."`
	CompanyID     *string  `json:"company_id,omitempty" doc:"Empty string unlinks."`
	OwnerUserID   *string  `json:"owner_user_id,omitempty"`
	ExpectedClose *string  `json:"expected_close,omitempty" doc:"YYYY-MM-DD; empty string clears."`
}

// ContactFields is the CRM side of a contact update. nil = unchanged.
type ContactFields struct {
	Name        *string   `json:"name,omitempty" maxLength:"120"`
	Phone       *string   `json:"phone,omitempty" maxLength:"40"`
	Title       *string   `json:"title,omitempty" maxLength:"120"`
	CompanyID   *string   `json:"company_id,omitempty" doc:"Empty string unlinks."`
	OwnerUserID *string   `json:"owner_user_id,omitempty"`
	Tags        *[]string `json:"tags,omitempty" maxItems:"20"`
}

// ActivityInput is a new timeline entry.
type ActivityInput struct {
	Kind      string `json:"kind" enum:"note,call,meeting,task"`
	Body      string `json:"body" minLength:"1" maxLength:"5000"`
	DueAt     string `json:"due_at,omitempty" doc:"Tasks only. RFC 3339 or YYYY-MM-DD."`
	ContactID string `json:"contact_id,omitempty"`
	CompanyID string `json:"company_id,omitempty"`
	DealID    string `json:"deal_id,omitempty"`
}

// ---- companies ---------------------------------------------------------------

// CreateCompany adds a company (one per domain). The caller owns it unless
// another member is named.
func CreateCompany(ctx context.Context, db *sql.DB, b CompanyFields) (*CompanyOut, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	name := val(b.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("a name is required")
	}
	if !companySizes[val(b.Size)] {
		return nil, huma.Error422UnprocessableEntity("unknown company size")
	}
	owner := any(s.UserID)
	if b.OwnerUserID != nil {
		if err := checkOwner(ctx, db, s.WorkspaceID, b.OwnerUserID); err != nil {
			return nil, err
		}
		owner = nullable(*b.OwnerUserID)
	}
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO companies (workspace_id, name, domain, industry, size, notes, owner_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		s.WorkspaceID, name, normDomain(val(b.Domain)), val(b.Industry), val(b.Size), val(b.Notes), owner,
	).Scan(&id)
	if pgCode(err) == "23505" {
		return nil, huma.Error409Conflict("a company with that domain already exists")
	} else if err != nil {
		return nil, linkErr(err, "could not add the company")
	}
	c, err := getCompany(ctx, db, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	return c, nil
}

// UpdateCompany changes a company.
func UpdateCompany(ctx context.Context, db *sql.DB, id string, b CompanyFields) (*CompanyOut, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if b.Name != nil {
		n := strings.TrimSpace(*b.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		u.set("name", n)
	}
	if b.Domain != nil {
		u.set("domain", normDomain(*b.Domain))
	}
	if b.Industry != nil {
		u.set("industry", strings.TrimSpace(*b.Industry))
	}
	if b.Size != nil {
		if !companySizes[*b.Size] {
			return nil, huma.Error422UnprocessableEntity("unknown company size")
		}
		u.set("size", *b.Size)
	}
	if b.Notes != nil {
		u.set("notes", strings.TrimSpace(*b.Notes))
	}
	if b.OwnerUserID != nil {
		if err := checkOwner(ctx, db, s.WorkspaceID, b.OwnerUserID); err != nil {
			return nil, err
		}
		u.set("owner_user_id", nullable(*b.OwnerUserID))
	}
	if len(u.sets) > 0 {
		res, err := db.ExecContext(ctx, `UPDATE companies SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("a company with that domain already exists")
		} else if err != nil {
			return nil, linkErr(err, "could not update the company")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("company not found")
		}
	}
	c, err := getCompany(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("company not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	return c, nil
}

// ---- deals ---------------------------------------------------------------------

// CreateDeal opens a deal: the first open stage, the workspace currency and
// the caller as owner unless given.
func CreateDeal(ctx context.Context, db *sql.DB, b DealFields) (*DealOut, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	title := val(b.Title)
	if title == "" {
		return nil, huma.Error422UnprocessableEntity("a title is required")
	}
	if b.ValueMinor != nil && *b.ValueMinor < 0 {
		return nil, huma.Error422UnprocessableEntity("the value must be 0 or more")
	}
	if b.Currency != nil && *b.Currency != "" && !validCurrency(*b.Currency) {
		return nil, huma.Error422UnprocessableEntity("the currency is a 3-letter code such as USD")
	}
	if err := EnsureStages(ctx, db, s.WorkspaceID); err != nil {
		return nil, huma.Error500InternalServerError("could not prepare the pipeline")
	}
	stageID, kind := "", ""
	if b.StageID != nil && *b.StageID != "" {
		err = db.QueryRowContext(ctx, `SELECT id, kind FROM pipeline_stages WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, *b.StageID).Scan(&stageID, &kind)
	} else {
		err = db.QueryRowContext(ctx, `SELECT id, kind FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open' ORDER BY position LIMIT 1`, s.WorkspaceID).Scan(&stageID, &kind)
	}
	if notFound(err) {
		return nil, huma.Error422UnprocessableEntity("stage not found in this pipeline")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the pipeline")
	}
	currency := val(b.Currency)
	if currency == "" {
		_ = db.QueryRowContext(ctx, `SELECT currency FROM workspaces WHERE id = $1`, s.WorkspaceID).Scan(&currency)
	}
	owner := any(s.UserID)
	if b.OwnerUserID != nil {
		if err := checkOwner(ctx, db, s.WorkspaceID, b.OwnerUserID); err != nil {
			return nil, err
		}
		owner = nullable(*b.OwnerUserID)
	}
	var value int64
	if b.ValueMinor != nil {
		value = *b.ValueMinor
	}
	var expected any
	if b.ExpectedClose != nil {
		if expected, err = parseDate(*b.ExpectedClose); err != nil {
			return nil, err
		}
	}
	ref := func(p *string) any {
		if p == nil {
			return nil
		}
		return nullable(*p)
	}
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, position, contact_id, company_id,
		                   owner_user_id, expected_close, closed_at, created_by)
		VALUES ($1, $2, $3, $4, $5,
		        (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $5),
		        $6, $7, $8, $9::date, CASE WHEN $10 = 'open' THEN NULL ELSE now() END, $11)
		RETURNING id`,
		s.WorkspaceID, title, value, currency, stageID, ref(b.ContactID), ref(b.CompanyID), owner, expected, kind, s.UserID,
	).Scan(&id)
	if err != nil {
		return nil, linkErr(err, "could not create the deal")
	}
	d, err := getDeal(ctx, db, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the deal")
	}
	return d, nil
}

// UpdateDeal changes a deal — including moving it to another stage, which
// closes it (won/lost) or reopens it.
func UpdateDeal(ctx context.Context, db *sql.DB, id string, b DealFields) (*DealOut, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if b.Title != nil {
		t := strings.TrimSpace(*b.Title)
		if t == "" {
			return nil, huma.Error422UnprocessableEntity("a title is required")
		}
		u.set("title", t)
	}
	if b.ValueMinor != nil {
		if *b.ValueMinor < 0 {
			return nil, huma.Error422UnprocessableEntity("the value must be 0 or more")
		}
		u.set("value_minor", *b.ValueMinor)
	}
	if b.Currency != nil && *b.Currency != "" {
		if !validCurrency(*b.Currency) {
			return nil, huma.Error422UnprocessableEntity("the currency is a 3-letter code such as USD")
		}
		u.set("currency", *b.Currency)
	}
	if b.ContactID != nil {
		u.set("contact_id", nullable(*b.ContactID))
	}
	if b.CompanyID != nil {
		u.set("company_id", nullable(*b.CompanyID))
	}
	if b.OwnerUserID != nil {
		if err := checkOwner(ctx, db, s.WorkspaceID, b.OwnerUserID); err != nil {
			return nil, err
		}
		u.set("owner_user_id", nullable(*b.OwnerUserID))
	}
	if b.ExpectedClose != nil {
		v, err := parseDate(*b.ExpectedClose)
		if err != nil {
			return nil, err
		}
		u.set("expected_close", v)
	}
	if b.StageID != nil && *b.StageID != "" {
		var kind string
		err := db.QueryRowContext(ctx, `SELECT kind FROM pipeline_stages WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, *b.StageID).Scan(&kind)
		if notFound(err) {
			return nil, huma.Error422UnprocessableEntity("stage not found in this pipeline")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the pipeline")
		}
		u.set("stage_id", *b.StageID)
		u.args = append(u.args, kind)
		u.sets = append(u.sets, "closed_at = CASE WHEN $"+strconv.Itoa(len(u.args)+2)+" = 'open' THEN NULL ELSE coalesce(closed_at, now()) END")
		if b.Position == nil {
			u.args = append(u.args, *b.StageID)
			u.sets = append(u.sets, "position = (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $"+strconv.Itoa(len(u.args)+2)+")")
		}
	}
	if b.Position != nil {
		u.set("position", *b.Position)
	}
	if len(u.sets) > 0 {
		res, err := db.ExecContext(ctx, `UPDATE deals SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if err != nil {
			return nil, linkErr(err, "could not update the deal")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("deal not found")
		}
	}
	d, err := getDeal(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("deal not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the deal")
	}
	return d, nil
}

// ListStages is the pipeline in order (seeded on first use).
func ListStages(ctx context.Context, db *sql.DB) ([]StageOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	if err := EnsureStages(ctx, db, s.WorkspaceID); err != nil {
		return nil, huma.Error500InternalServerError("could not prepare the pipeline")
	}
	st, err := listStages(ctx, db, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list stages")
	}
	return st, nil
}

// ---- contacts ------------------------------------------------------------------

// UpdateContact changes a contact's CRM fields: name, phone, title, company, owner, tags.
func UpdateContact(ctx context.Context, db *sql.DB, id string, b ContactFields) (*ContactDetail, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if b.Name != nil {
		u.set("name", strings.TrimSpace(*b.Name))
	}
	if b.Phone != nil {
		u.set("phone", strings.TrimSpace(*b.Phone))
	}
	if b.Title != nil {
		u.set("title", strings.TrimSpace(*b.Title))
	}
	if b.CompanyID != nil {
		u.set("company_id", nullable(*b.CompanyID))
	}
	if b.OwnerUserID != nil {
		if err := checkOwner(ctx, db, s.WorkspaceID, b.OwnerUserID); err != nil {
			return nil, err
		}
		u.set("owner_user_id", nullable(*b.OwnerUserID))
	}
	if b.Tags != nil {
		u.args = append(u.args, tagsJSON(normTags(*b.Tags)))
		u.sets = append(u.sets, "tags = ARRAY(SELECT jsonb_array_elements_text($"+strconv.Itoa(len(u.args)+2)+"::jsonb))")
	}
	if len(u.sets) > 0 {
		res, err := db.ExecContext(ctx, `UPDATE contacts SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if err != nil {
			return nil, linkErr(err, "could not update the contact")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("contact not found")
		}
	}
	c, err := getContact(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("contact not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the contact")
	}
	return c, nil
}

// ---- activities ----------------------------------------------------------------

// CreateActivity logs a note, call or meeting, or adds a task, on a contact,
// company and/or deal.
func CreateActivity(ctx context.Context, db *sql.DB, b ActivityInput) (*ActivityOut, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	if !activityKinds[b.Kind] {
		return nil, huma.Error422UnprocessableEntity("the kind must be note, call, meeting or task")
	}
	text := strings.TrimSpace(b.Body)
	if text == "" {
		return nil, huma.Error422UnprocessableEntity("write something")
	}
	if nullable(b.ContactID) == nil && nullable(b.CompanyID) == nil && nullable(b.DealID) == nil {
		return nil, huma.Error422UnprocessableEntity("an activity belongs to a contact, a company or a deal")
	}
	var due any
	if b.Kind == "task" {
		if due, err = parseTime(b.DueAt); err != nil {
			return nil, err
		}
	}
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO activities (workspace_id, kind, body, due_at, contact_id, company_id, deal_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		s.WorkspaceID, b.Kind, text, due, nullable(b.ContactID), nullable(b.CompanyID), nullable(b.DealID), s.UserID,
	).Scan(&id)
	if err != nil {
		return nil, linkErr(err, "could not save the activity")
	}
	act, err := getActivity(ctx, db, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the activity")
	}
	return act, nil
}

// ---- small helpers ---------------------------------------------------------------

func val(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return s
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---- the read side, on MH-467's paging and MH-379's linked conversations ---------
//
// The REST handlers for these lists and record pages were written by MH-467
// (cursor, exact total, a limit refused rather than clamped) and MH-379 (the
// linked conversations on each timeline page). Their bodies live here now, so
// the REST routes and internal/mcp's CRM tools answer from the same code.

// CompaniesPage is one page of the companies, A-Z, searched by name and domain.
func CompaniesPage(ctx context.Context, db *sql.DB, q, cursor string, limit int) (*CompanyPage, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	limit, err = paging.Limit(limit, DefaultCompanyPage, MaxCompanyPage)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	page, err := ListCompaniesPage(ctx, db, s.WorkspaceID, q, cursor, limit)
	if errors.Is(err, paging.ErrBadCursor) {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not list companies")
	}
	return page, nil
}

// DealsPageFor is the pipeline and a page of its deals: the board (every
// stage's first cards), one stage's next page, or the whole pipeline as a list.
// q.Limit 0 takes the view's default.
func DealsPageFor(ctx context.Context, db *sql.DB, q DealsQuery) (*DealsPage, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	if err := EnsureStages(ctx, db, s.WorkspaceID); err != nil {
		return nil, huma.Error500InternalServerError("could not prepare the pipeline")
	}
	def := DefaultDealsPerStage
	if q.View == "list" {
		def = DefaultDealsPage
	}
	if q.Limit, err = paging.Limit(q.Limit, def, MaxDealsPage); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	q.StageID = strings.TrimSpace(q.StageID)
	page, err := ListDealsPage(ctx, db, s.WorkspaceID, q)
	switch {
	case errors.Is(err, paging.ErrBadCursor):
		return nil, huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, ErrNoSuchStage):
		return nil, huma.Error404NotFound(err.Error())
	case err != nil:
		return nil, huma.Error500InternalServerError("could not list deals")
	}
	return page, nil
}

// GetDealDetail reads one deal and the first page of its timeline.
func GetDealDetail(ctx context.Context, db *sql.DB, id string) (*DealDetailOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	d, err := getDeal(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("deal not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the deal")
	}
	tl, err := firstTimelinePage(ctx, db, s, timelineDeal, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}
	return &DealDetailOut{*d, tl}, nil
}

// GetCompanyDetail reads one company: its people, deals and the first page of
// its timeline.
func GetCompanyDetail(ctx context.Context, db *sql.DB, id string) (*CompanyDetailOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	c, err := getCompany(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("company not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	res := &CompanyDetailOut{Company: *c}
	if res.Contacts, err = companyContacts(ctx, db, s.WorkspaceID, id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	if res.Deals, res.DealsTotal, err = relatedDeals(ctx, db, s.WorkspaceID, "AND d.company_id = $2", id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	if res.TimelineOut, err = firstTimelinePage(ctx, db, s, timelineCompany, id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}
	return res, nil
}

// GetContactPage reads one contact: CRM fields, list subscriptions, deals and
// the first page of its timeline.
func GetContactPage(ctx context.Context, db *sql.DB, id string) (*ContactPageOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	c, err := getContact(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("contact not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the contact")
	}
	res := &ContactPageOut{Contact: *c}
	if res.Deals, res.DealsTotal, err = relatedDeals(ctx, db, s.WorkspaceID, "AND d.contact_id = $2", id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the contact")
	}
	if res.TimelineOut, err = firstTimelinePage(ctx, db, s, timelineContact, id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}
	return res, nil
}

// TimelineQuery names one record (exactly one id) and a page of its timeline.
type TimelineQuery struct {
	ContactID, CompanyID, DealID string
	Cursor                       string
	Limit                        int
}

// TimelinePage is one page of a record's timeline, newest first, with the
// linked conversations in that page's stretch of time.
func TimelinePage(ctx context.Context, db *sql.DB, q TimelineQuery) (*ActivityPage, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	where, id, err := timelineScope(q)
	if err != nil {
		return nil, err
	}
	limit, err := paging.Limit(q.Limit, DefaultTimelinePage, MaxTimelinePage)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	page, err := timelinePage(ctx, db, s.WorkspaceID, where, id, q.Cursor, limit, canReadInbox(s))
	if errors.Is(err, paging.ErrBadCursor) {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}
	return page, nil
}

func timelineScope(q TimelineQuery) (string, string, error) {
	var where, id string
	n := 0
	for _, t := range []struct{ id, where string }{
		{q.ContactID, timelineContact}, {q.CompanyID, timelineCompany}, {q.DealID, timelineDeal},
	} {
		if strings.TrimSpace(t.id) != "" {
			n++
			where, id = t.where, strings.TrimSpace(t.id)
		}
	}
	if n != 1 {
		return "", "", huma.Error422UnprocessableEntity("name exactly one of contact_id, company_id or deal_id")
	}
	return where, id, nil
}

// ---- the company timeline (MH-393) ------------------------------------------------

// TimelineEntry is one dated thing that happened with a company.
type TimelineEntry struct {
	At   time.Time `json:"at"`
	Type string    `json:"type" doc:"activity | conversation | deal"`
	// ID is the activity's, conversation's or deal's id.
	ID      string `json:"id"`
	Kind    string `json:"kind" doc:"activity: note/call/meeting/task; conversation: its channel; deal: opened/won/lost."`
	Summary string `json:"summary"`
}

// CompanyTimelineOut is one page of a company's merged timeline.
type CompanyTimelineOut struct {
	Company Ref             `json:"company"`
	Entries []TimelineEntry `json:"entries"`
	// ActivitiesTotal is every activity on the company's timeline, all pages.
	ActivitiesTotal int64 `json:"activities_total"`
	// DealsTotal is every deal of the company; deal events come from the
	// first MaxRelatedDeals of them.
	DealsTotal int64 `json:"deals_total"`
	// Conversations is false when the caller may not read the inbox, so none
	// could be included.
	Conversations bool   `json:"conversations_included"`
	NextCursor    string `json:"next_cursor"`
}

/*
CompanyTimeline is everything that happened with one company, newest first,
one page at a time: its activities (the company's, its people's and its
deals'), its linked inbox conversations, and its deals opening and closing.

It pages on the company's activity timeline (timelinePage: MH-467's cursor,
MH-379's conversations in each page's stretch of time), and puts each deal
event on the page whose stretch of time holds it, the same rule the
conversations follow, so every entry appears on exactly one page and in order.
*/
func CompanyTimeline(ctx context.Context, db *sql.DB, id, cursor string, limit int) (*CompanyTimelineOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	c, err := getCompany(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("company not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the company")
	}
	page, err := TimelinePage(ctx, db, TimelineQuery{CompanyID: id, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	deals, dealsTotal, err := relatedDeals(ctx, db, s.WorkspaceID, "AND d.company_id = $2", id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the company's deals")
	}
	// This page's stretch of time: before the cursor's activity (none on the
	// first page), at or after this page's last activity (none on the last).
	var newerThan, notBefore *time.Time
	if cur, err := paging.Decode(cursor, 2); err == nil && cur != nil {
		if us, err := strconv.ParseInt(cur[0], 10, 64); err == nil {
			t := time.UnixMicro(us).UTC()
			newerThan = &t
		}
	}
	if page.NextCursor != "" && len(page.Activities) > 0 {
		t := time.UnixMicro(page.Activities[len(page.Activities)-1].CreatedAt.UnixMicro()).UTC()
		notBefore = &t
	}
	inWindow := func(at time.Time) bool {
		return (newerThan == nil || at.Before(*newerThan)) && (notBefore == nil || !at.Before(*notBefore))
	}

	res := &CompanyTimelineOut{Company: Ref{ID: c.ID, Name: c.Name}, Entries: []TimelineEntry{},
		ActivitiesTotal: page.Total, DealsTotal: dealsTotal, Conversations: canReadInbox(s), NextCursor: page.NextCursor}
	for _, a := range page.Activities {
		res.Entries = append(res.Entries, TimelineEntry{At: a.CreatedAt, Type: "activity", ID: a.ID, Kind: a.Kind, Summary: oneLine(a.Body)})
	}
	for _, cv := range page.Conversations {
		who := cv.CustomerName
		if cv.Contact != nil {
			who = firstOf(cv.Contact.Name, cv.Contact.Email, who)
		}
		res.Entries = append(res.Entries, TimelineEntry{At: cv.LastMessageAt, Type: "conversation", ID: cv.ID, Kind: cv.Channel,
			Summary: strings.TrimSpace(oneLine(cv.Subject) + " — " + who + " (" + cv.Status + ", " + strconv.FormatInt(cv.MessageCount, 10) + " messages)")})
	}
	for _, dl := range deals {
		value := strconv.FormatFloat(float64(dl.ValueMinor)/100, 'f', 2, 64) + " " + dl.Currency
		if inWindow(dl.CreatedAt) {
			res.Entries = append(res.Entries, TimelineEntry{At: dl.CreatedAt, Type: "deal", ID: dl.ID, Kind: "opened", Summary: dl.Title + " — " + value})
		}
		if dl.ClosedAt != nil && dl.StageKind != "open" && inWindow(*dl.ClosedAt) {
			res.Entries = append(res.Entries, TimelineEntry{At: *dl.ClosedAt, Type: "deal", ID: dl.ID, Kind: dl.StageKind, Summary: dl.Title + " — " + value})
		}
	}
	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].At.After(res.Entries[j].At) })
	return res, nil
}
