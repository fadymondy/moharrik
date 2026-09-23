package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/marketing"
)

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

/*
CrmTools: contacts, companies, deals and the activity timeline, over
contacts:read / contacts:write — the same scope pair internal/crm's REST
routes use (leads have their own pair since MH-390, user_tools_leads.go).

Since MH-393 every tool here calls the function the REST handler calls
(internal/crm's service.go, internal/marketing's AddContact and
ListContactsPage): the same validation, the same composite-key links that
cannot leave the workspace, the same stage rules. Every answer is JSON and every
record carries its id, so search_contacts → update_contact, search_companies →
company_timeline and get_deal → update_deal chain without guessing.
*/
func CrmTools() []Tool {
	return []Tool{
		// ---- contacts ---------------------------------------------------------
		{
			// MH-408 made this a page; MH-393 made its description say what the
			// answer actually holds — including the ids the other tools need.
			Name: "list_contacts",
			Description: "CRM contacts, newest first, one page at a time. Each has its id, email, name, phone, title, company (id and name), " +
				"WhatsApp consent (opted in / opted out) and its status on each mailing list. total is how many match; pass next_cursor " +
				"back as cursor for the next page. Filter with q (name, email, phone or company), or list (a mailing list's exact name) and status.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"q":      prop("string", "Search name, email, phone and company"),
				"list":   prop("string", "Only contacts on this mailing list (its exact name)"),
				"status": map[string]any{"type": "string", "enum": []string{"subscribed", "pending", "unsubscribed", "bounced"}, "description": "With list: only this status on that list"},
				"cursor": prop("string", "next_cursor from the previous answer"),
				"limit":  prop("integer", "Page size, default 100, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				args := decodeArgs(raw)
				f := marketing.ContactFilter{Q: str(args, "q"), Status: str(args, "status")}
				if list := str(args, "list"); list != "" {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM mail_lists WHERE workspace_id = $1 AND lower(name) = lower($2)`,
						s.WorkspaceID, list).Scan(&f.ListID); err != nil {
						if errNoRows(err) {
							return "", fmt.Errorf("no mailing list named %q — call list_mail_lists to see them", list)
						}
						return "", fmt.Errorf("could not read the list: %w", err)
					}
				}
				return contactPage(ctx, a, f, str(args, "cursor"), intArg(args, "limit", 100))
			},
		},
		{
			Name: "search_contacts",
			Description: "Find CRM contacts by name, email, phone or company name (any part of it, case-insensitive). Same answer as " +
				"list_contacts: ids, company, WhatsApp consent and list statuses, total, and next_cursor for more.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"q"}, map[string]any{
				"q":      prop("string", "Text to find in name, email, phone or company"),
				"cursor": prop("string", "next_cursor from the previous answer"),
				"limit":  prop("integer", "Page size, default 25, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				q, err := required("q", str(args, "q"))
				if err != nil {
					return "", err
				}
				return contactPage(ctx, a, marketing.ContactFilter{Q: q}, str(args, "cursor"), intArg(args, "limit", 25))
			},
		},
		{
			Name: "get_contact",
			Description: "One contact in full: CRM fields (name, email, phone, title, tags, owner), its company, its mailing-list " +
				"subscriptions, its deals (deals_total says how many in all), and the first page of its timeline — its own and its " +
				"deals' activities, plus its linked inbox conversations when the token may read the inbox. activities_next_cursor " +
				"pages on through list_activities.",
			Scope:       "contacts:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Contact id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				c, err := crm.GetContactPage(ctx, a.SQLDB, id)
				if err != nil {
					return "", err
				}
				return jsonResult(c)
			},
		},
		{
			Name: "create_contact",
			Description: "Add a contact by email (an existing contact with that email is reused, not duplicated). Optional: name, locale (en/ar), " +
				"phone, title, company_id, and list (a mailing list's exact name) to subscribe them — with the double opt-in email when " +
				"the list asks for one. Returns the contact with its id.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"email"}, map[string]any{
				"email":      prop("string", ""),
				"name":       prop("string", ""),
				"locale":     map[string]any{"type": "string", "enum": []string{"en", "ar"}},
				"phone":      prop("string", "With country code, e.g. +20 101 234 5678"),
				"title":      prop("string", "Job title"),
				"company_id": prop("string", "A company id (search_companies)"),
				"list":       prop("string", "A mailing list's exact name, to subscribe them"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Email     string  `json:"email"`
					Name      string  `json:"name"`
					Locale    string  `json:"locale"`
					Phone     *string `json:"phone"`
					Title     *string `json:"title"`
					CompanyID *string `json:"company_id"`
					List      string  `json:"list"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				if in.Locale != "" && in.Locale != "en" && in.Locale != "ar" {
					return "", fmt.Errorf("locale must be en or ar")
				}
				add := marketing.AddContactInput{Email: in.Email, Name: in.Name, Locale: in.Locale}
				if list := strings.TrimSpace(in.List); list != "" {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM mail_lists WHERE workspace_id = $1 AND lower(name) = lower($2)`,
						s.WorkspaceID, list).Scan(&add.ListID); err != nil {
						if errNoRows(err) {
							return "", fmt.Errorf("no mailing list named %q — call list_mail_lists to see them", list)
						}
						return "", fmt.Errorf("could not read the list: %w", err)
					}
				}
				res, err := marketing.AddContact(ctx, a, add)
				if err != nil {
					return "", err
				}
				out := map[string]any{"contact": res.Contact, "list_status": res.Status, "confirmation": res.Confirmation}
				if res.Error != "" {
					out["confirmation_error"] = res.Error
				}
				// The CRM fields the add does not take go through the same update
				// the contact page uses.
				if in.Phone != nil || in.Title != nil || in.CompanyID != nil {
					c, err := crm.UpdateContact(ctx, a.SQLDB, res.Contact.ID, crm.ContactFields{Phone: in.Phone, Title: in.Title, CompanyID: in.CompanyID})
					if err != nil {
						return "", fmt.Errorf("the contact was added (id %s) but its details were not saved: %w", res.Contact.ID, err)
					}
					out["contact"] = c
				}
				return jsonResult(out)
			},
		},
		{
			Name: "update_contact",
			Description: "Change a contact's CRM fields. Give only what changes: name, phone, title, company_id (empty string unlinks), " +
				"owner (a member's user id or email; empty unassigns), tags (replaces them all).",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":         prop("string", "Contact id"),
				"name":       prop("string", ""),
				"phone":      prop("string", ""),
				"title":      prop("string", ""),
				"company_id": prop("string", "A company id; empty string unlinks"),
				"owner":      prop("string", "A member's user id or email; empty string unassigns"),
				"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Replaces the tags (at most 20)"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID        string    `json:"id"`
					Name      *string   `json:"name"`
					Phone     *string   `json:"phone"`
					Title     *string   `json:"title"`
					CompanyID *string   `json:"company_id"`
					Owner     *string   `json:"owner"`
					Tags      *[]string `json:"tags"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				f := crm.ContactFields{Name: in.Name, Phone: in.Phone, Title: in.Title, CompanyID: in.CompanyID, Tags: in.Tags}
				if in.Owner != nil {
					owner, err := memberID(ctx, a, *in.Owner)
					if err != nil {
						return "", err
					}
					f.OwnerUserID = &owner
				}
				c, err := crm.UpdateContact(ctx, a.SQLDB, id, f)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"contact": c})
			},
		},

		// ---- companies --------------------------------------------------------
		{
			// MH-467 made this a page with a total; MH-393 made it answer with ids.
			Name: "list_companies",
			Description: "Companies in the CRM, A-Z, one page at a time: each with its id, domain, industry, size, contact count and " +
				"open-deal count. total is how many match; pass next_cursor back as cursor for the next page. q searches name and domain.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"q":      prop("string", "Search name and domain"),
				"cursor": prop("string", "next_cursor from the previous answer"),
				"limit":  prop("integer", "Page size, default 50, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				return companyPage(ctx, a, str(args, "q"), str(args, "cursor"), intArg(args, "limit", 0))
			},
		},
		{
			Name: "search_companies",
			Description: "Find companies whose name or domain contains q (case-insensitive). Same answer as list_companies: ids, " +
				"counts, total and next_cursor.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"q"}, map[string]any{
				"q":      prop("string", "Text to find in the name or domain"),
				"cursor": prop("string", "next_cursor from the previous answer"),
				"limit":  prop("integer", "Page size, default 50, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				q, err := required("q", str(args, "q"))
				if err != nil {
					return "", err
				}
				return companyPage(ctx, a, q, str(args, "cursor"), intArg(args, "limit", 0))
			},
		},
		{
			Name: "get_company",
			Description: "One company: its fields (domain, industry, size, notes, owner), its people (contact ids, emails, titles), " +
				"its deals with stage and value (deals_total says how many in all), and how many activities its timeline holds. " +
				"company_timeline has the history itself.",
			Scope:       "contacts:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Company id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				d, err := crm.GetCompanyDetail(ctx, a.SQLDB, id)
				if err != nil {
					return "", err
				}
				deals := make([]map[string]any, 0, len(d.Deals))
				for _, dl := range d.Deals {
					deals = append(deals, dealView(dl))
				}
				return jsonResult(map[string]any{
					"company": d.Company, "contacts": d.Contacts, "deals": deals, "deals_total": d.DealsTotal,
					"activities_total": d.ActivitiesTotal,
				})
			},
		},
		{
			Name:        "create_company",
			Description: "Add a company (one per domain in a workspace). name is required; domain, industry, size and notes are optional. Returns it with its id.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"name"}, map[string]any{
				"name":     prop("string", "Company name"),
				"domain":   prop("string", "e.g. acme.com — one company per domain in a workspace"),
				"industry": prop("string", ""),
				"size":     map[string]any{"type": "string", "enum": []string{"1-10", "11-50", "51-200", "201-1000", "1000+"}},
				"notes":    prop("string", ""),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in crm.CompanyFields
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				in.OwnerUserID = nil
				c, err := crm.CreateCompany(ctx, a.SQLDB, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"company": c})
			},
		},
		{
			Name: "update_company",
			Description: "Change a company. Give only what changes: name, domain, industry, size, notes, owner (a member's user id or email; " +
				"empty unassigns).",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":       prop("string", "Company id"),
				"name":     prop("string", ""),
				"domain":   prop("string", ""),
				"industry": prop("string", ""),
				"size":     map[string]any{"type": "string", "enum": []string{"", "1-10", "11-50", "51-200", "201-1000", "1000+"}},
				"notes":    prop("string", ""),
				"owner":    prop("string", "A member's user id or email; empty string unassigns"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID    string  `json:"id"`
					Owner *string `json:"owner"`
					crm.CompanyFields
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				f := in.CompanyFields
				f.OwnerUserID = nil
				if in.Owner != nil {
					owner, err := memberID(ctx, a, *in.Owner)
					if err != nil {
						return "", err
					}
					f.OwnerUserID = &owner
				}
				c, err := crm.UpdateCompany(ctx, a.SQLDB, id, f)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"company": c})
			},
		},
		{
			Name: "company_timeline",
			Description: "Everything that happened with one company, newest first, one page at a time: activities (notes, calls, " +
				"meetings, tasks — on the company, its people or its deals), its linked inbox conversations (when the token may read " +
				"the inbox), and its deals opening and closing (won, lost). Each entry has its type and id, so get_conversation and " +
				"get_deal can open it. Pass next_cursor back as cursor for older entries.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":     prop("string", "Company id"),
				"cursor": prop("string", "next_cursor from the previous answer"),
				"limit":  prop("integer", "Activities per page, default 50, at most 200 (conversations and deal events in the same stretch of time come with them)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				id, err := required("id", str(args, "id"))
				if err != nil {
					return "", err
				}
				res, err := crm.CompanyTimeline(ctx, a.SQLDB, id, str(args, "cursor"), intArg(args, "limit", 0))
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
		{
			Name: "list_activities",
			Description: "One record's timeline, newest first, one page at a time: name exactly one of contact_id, company_id or " +
				"deal_id. Each activity has its id, kind, body, due and done times and what it is attached to; total counts them " +
				"all. The linked inbox conversations in the page's stretch of time come with it when the token may read the inbox.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"contact_id": prop("string", ""),
				"company_id": prop("string", ""),
				"deal_id":    prop("string", ""),
				"cursor":     prop("string", "next_cursor from the previous answer"),
				"limit":      prop("integer", "Page size, default 50, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				page, err := crm.TimelinePage(ctx, a.SQLDB, crm.TimelineQuery{ContactID: str(args, "contact_id"), CompanyID: str(args, "company_id"),
					DealID: str(args, "deal_id"), Cursor: str(args, "cursor"), Limit: intArg(args, "limit", 0)})
				if err != nil {
					return "", err
				}
				return jsonResult(page)
			},
		},

		// ---- deals ------------------------------------------------------------
		{
			// MH-467 made this a page with a total; MH-393 made it answer with ids.
			Name: "list_deals",
			Description: "The pipeline in board order (stage, then position), one page at a time: each deal with its id, stage (id, " +
				"name, open/won/lost), value in major units, contact and company, plus the stages with their full counts. total is " +
				"every deal; pass next_cursor back as cursor for the next page. stage_id narrows to one stage.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"stage_id": prop("string", "Only this stage's deals"),
				"cursor":   prop("string", "next_cursor from the previous answer"),
				"limit":    prop("integer", "Page size, default 50, at most 500"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				q := crm.DealsQuery{View: "list", Cursor: str(args, "cursor"), Limit: intArg(args, "limit", 0)}
				if st := str(args, "stage_id"); st != "" {
					q.View, q.StageID = "board", st
				}
				page, err := crm.DealsPageFor(ctx, a.SQLDB, q)
				if err != nil {
					return "", err
				}
				deals := make([]map[string]any, 0, len(page.Deals))
				for _, d := range page.Deals {
					deals = append(deals, dealView(d))
				}
				stages := make([]map[string]any, 0, len(page.Stages))
				for _, st := range page.Stages {
					stages = append(stages, map[string]any{"id": st.ID, "name": st.Name, "kind": st.Kind, "deal_count": st.DealCount, "value_minor": st.ValueMinor})
				}
				return jsonResult(map[string]any{"total": page.Total, "count": len(deals), "deals": deals, "stages": stages,
					"currency": page.Currency, "next_cursor": page.NextCursor})
			},
		},
		{
			Name: "get_deal",
			Description: "One deal: its stage, value, contact, company, owner, expected close date, and the first page of its timeline " +
				"(activities, and the inbox conversation it was opened from when the token may read the inbox). " +
				"activities_next_cursor pages on through list_activities.",
			Scope:       "contacts:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Deal id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				d, err := crm.GetDealDetail(ctx, a.SQLDB, id)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deal": dealView(d.Deal), "activities": d.Activities, "activities_total": d.ActivitiesTotal,
					"activities_next_cursor": d.ActivitiesNextCursor, "conversations": d.Conversations})
			},
		},
		{
			Name: "create_deal",
			Description: "Open a deal. title is required; value is in major units (1500.50). It starts on the first open stage unless you name " +
				"a stage (its id or name), in the workspace's currency unless given, owned by you. Optional contact_id and company_id link it.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"title"}, map[string]any{
				"title":          prop("string", ""),
				"value":          prop("number", "Major units, e.g. 1500.00"),
				"currency":       prop("string", "3-letter code, e.g. USD"),
				"stage":          prop("string", "A stage id or name (list_deals shows them)"),
				"contact_id":     prop("string", ""),
				"company_id":     prop("string", ""),
				"expected_close": prop("string", "YYYY-MM-DD"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				f, err := dealFields(ctx, a, raw)
				if err != nil {
					return "", err
				}
				d, err := crm.CreateDeal(ctx, a.SQLDB, f)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deal": dealView(*d)})
			},
		},
		{
			Name: "update_deal",
			Description: "Change a deal. Give only what changes: title, value (major units), currency, stage (id or name — moving it to a " +
				"won or lost stage closes it, back to an open one reopens it), contact_id / company_id (empty string unlinks), owner " +
				"(user id or email), expected_close (YYYY-MM-DD, empty clears).",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":             prop("string", "Deal id"),
				"title":          prop("string", ""),
				"value":          prop("number", "Major units"),
				"currency":       prop("string", "3-letter code"),
				"stage":          prop("string", "A stage id or name"),
				"contact_id":     prop("string", "Empty string unlinks"),
				"company_id":     prop("string", "Empty string unlinks"),
				"owner":          prop("string", "A member's user id or email; empty string unassigns"),
				"expected_close": prop("string", "YYYY-MM-DD; empty string clears"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				f, err := dealFields(ctx, a, raw)
				if err != nil {
					return "", err
				}
				d, err := crm.UpdateDeal(ctx, a.SQLDB, id, f)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deal": dealView(*d)})
			},
		},
		{
			Name:        "move_deal",
			Description: "Move a deal to another stage. Name the deal by id (or its exact title) and the stage by id or name. Won/lost stages close it.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"stage"}, map[string]any{
				"id":    prop("string", "Deal id"),
				"title": prop("string", "The deal's exact title, if you have no id"),
				"stage": prop("string", "The destination stage's id or name"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				args := decodeArgs(raw)
				id := str(args, "id")
				if id == "" {
					title, err := required("id or title", str(args, "title"))
					if err != nil {
						return "", err
					}
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM deals WHERE workspace_id = $1 AND lower(title) = lower($2) ORDER BY created_at DESC LIMIT 1`,
						s.WorkspaceID, title).Scan(&id); err != nil {
						if errNoRows(err) {
							return "", fmt.Errorf("no deal titled %q", title)
						}
						return "", fmt.Errorf("could not find the deal: %w", err)
					}
				}
				stageID, err := stageRef(ctx, a, str(args, "stage"))
				if err != nil {
					return "", err
				}
				d, err := crm.UpdateDeal(ctx, a.SQLDB, id, crm.DealFields{StageID: &stageID})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deal": dealView(*d)})
			},
		},

		// ---- timeline ---------------------------------------------------------
		{
			Name: "log_activity",
			Description: "Log a note, call or meeting, or add a task, on a contact, company and/or deal (name at least one, by id — or a " +
				"contact's email, a company's exact name, a deal's exact title). A task can carry a due date. Returns the activity with its id.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"kind", "body"}, map[string]any{
				"kind":       map[string]any{"type": "string", "enum": []string{"note", "call", "meeting", "task"}},
				"body":       prop("string", ""),
				"contact_id": prop("string", ""),
				"company_id": prop("string", ""),
				"deal_id":    prop("string", ""),
				"contact":    prop("string", "A contact's email, instead of contact_id"),
				"company":    prop("string", "A company's exact name, instead of company_id"),
				"deal":       prop("string", "A deal's exact title, instead of deal_id"),
				"due":        prop("string", "Tasks only: YYYY-MM-DD or RFC 3339"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				args := decodeArgs(raw)
				in := crm.ActivityInput{Kind: str(args, "kind"), Body: str(args, "body"), DueAt: str(args, "due"),
					ContactID: str(args, "contact_id"), CompanyID: str(args, "company_id"), DealID: str(args, "deal_id")}
				lookup := func(dst *string, q, v, what string) error {
					if *dst != "" || v == "" {
						return nil
					}
					if err := a.SQLDB.QueryRowContext(ctx, q, s.WorkspaceID, v).Scan(dst); err != nil {
						if errNoRows(err) {
							return fmt.Errorf("no %s %q", what, v)
						}
						return fmt.Errorf("could not find the %s: %w", what, err)
					}
					return nil
				}
				if err := lookup(&in.ContactID, `SELECT id FROM contacts WHERE workspace_id = $1 AND lower(email) = lower($2)`, str(args, "contact"), "contact with email"); err != nil {
					return "", err
				}
				if err := lookup(&in.CompanyID, `SELECT id FROM companies WHERE workspace_id = $1 AND lower(name) = lower($2) LIMIT 1`, str(args, "company"), "company named"); err != nil {
					return "", err
				}
				if err := lookup(&in.DealID, `SELECT id FROM deals WHERE workspace_id = $1 AND lower(title) = lower($2) ORDER BY created_at DESC LIMIT 1`, str(args, "deal"), "deal titled"); err != nil {
					return "", err
				}
				act, err := crm.CreateActivity(ctx, a.SQLDB, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"activity": act})
			},
		},
	}
}

// contactPage is list_contacts' and search_contacts' shared answer.
func contactPage(ctx context.Context, a *app.App, f marketing.ContactFilter, cursor string, limit int) (string, error) {
	s, err := need(ctx)
	if err != nil {
		return "", err
	}
	page, err := marketing.ListContactsPage(ctx, a.SQLDB, s.WorkspaceID, f, cursor, limit)
	switch {
	case errors.Is(err, marketing.ErrBadCursor), errors.Is(err, marketing.ErrStatusNeedsList), errors.Is(err, marketing.ErrBadStatus):
		return "", err
	case err != nil:
		return "", fmt.Errorf("could not list contacts: %w", err)
	}
	list := make([]map[string]any, 0, len(page.Contacts))
	for _, c := range page.Contacts {
		subs := make([]map[string]any, 0, len(c.Subscriptions))
		for _, sub := range c.Subscriptions {
			subs = append(subs, map[string]any{"list_id": sub.ListID, "list": sub.ListName, "status": sub.Status})
		}
		list = append(list, map[string]any{
			"id": c.ID, "email": c.Email, "name": c.Name, "phone": c.Phone, "title": c.Title,
			"company_id": c.CompanyID, "company": c.CompanyName, "locale": c.Locale, "source": c.Source, "created_at": c.CreatedAt,
			"whatsapp_opted_in": c.WhatsAppOptInAt != nil, "whatsapp_opted_out": c.WhatsAppOptOutAt != nil,
			"subscriptions": subs,
		})
	}
	return jsonResult(map[string]any{"total": page.Total, "count": len(list), "contacts": list, "next_cursor": page.NextCursor})
}

// companyPage is list_companies' and search_companies' shared answer.
func companyPage(ctx context.Context, a *app.App, q, cursor string, limit int) (string, error) {
	page, err := crm.CompaniesPage(ctx, a.SQLDB, q, cursor, limit)
	if err != nil {
		return "", err
	}
	return jsonResult(map[string]any{"total": page.Total, "count": len(page.Companies), "companies": page.Companies, "next_cursor": page.NextCursor})
}

// dealView is a deal as the tools answer it: the value in major units too.
func dealView(d crm.DealOut) map[string]any {
	return map[string]any{
		"id": d.ID, "title": d.Title, "value": float64(d.ValueMinor) / 100, "value_minor": d.ValueMinor, "currency": d.Currency,
		"stage":   map[string]any{"id": d.StageID, "name": d.StageName, "kind": d.StageKind},
		"contact": d.Contact, "company": d.Company, "owner_user_id": d.OwnerUserID,
		"expected_close": d.ExpectedClose, "closed_at": d.ClosedAt, "created_at": d.CreatedAt,
	}
}

// dealFields reads a deal's create/update arguments: value in major units,
// the stage by id or name, the owner by id or email.
func dealFields(ctx context.Context, a *app.App, raw json.RawMessage) (crm.DealFields, error) {
	var in struct {
		Title         *string  `json:"title"`
		Value         *float64 `json:"value"`
		Currency      *string  `json:"currency"`
		Stage         *string  `json:"stage"`
		ContactID     *string  `json:"contact_id"`
		CompanyID     *string  `json:"company_id"`
		Owner         *string  `json:"owner"`
		ExpectedClose *string  `json:"expected_close"`
	}
	var f crm.DealFields
	if err := bind(raw, &in); err != nil {
		return f, err
	}
	f.Title, f.ContactID, f.CompanyID, f.ExpectedClose = in.Title, in.ContactID, in.CompanyID, in.ExpectedClose
	if in.Value != nil {
		if *in.Value < 0 {
			return f, fmt.Errorf("value must be 0 or more")
		}
		v := minor(*in.Value)
		f.ValueMinor = &v
	}
	if in.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*in.Currency))
		f.Currency = &c
	}
	if in.Stage != nil && strings.TrimSpace(*in.Stage) != "" {
		id, err := stageRef(ctx, a, *in.Stage)
		if err != nil {
			return f, err
		}
		f.StageID = &id
	}
	if in.Owner != nil {
		owner, err := memberID(ctx, a, *in.Owner)
		if err != nil {
			return f, err
		}
		f.OwnerUserID = &owner
	}
	return f, nil
}

// stageRef resolves a stage given by id or by name.
func stageRef(ctx context.Context, a *app.App, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("stage is required")
	}
	stages, err := crm.ListStages(ctx, a.SQLDB)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(stages))
	for _, st := range stages {
		if st.ID == ref || strings.EqualFold(st.Name, ref) {
			return st.ID, nil
		}
		names = append(names, strconv.Quote(st.Name))
	}
	return "", fmt.Errorf("no stage %q — the stages are %s", ref, strings.Join(names, ", "))
}

func errNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
