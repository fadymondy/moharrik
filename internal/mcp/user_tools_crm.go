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
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

// CrmTools: companies, deals and the activity timeline, over contacts:read /
// contacts:write — the same scope pair internal/crm's REST routes use (leads
// have their own pair since MH-390, user_tools_leads.go). Queries run on the app pool; ctx carries the
// workspace, so RLS confines every row the same way the REST handlers are —
// each query still names workspace_id explicitly too, matching that package's
// defense-in-depth convention.
func CrmTools() []Tool {
	return []Tool{
		{
			// MH-467: this stopped at 200 companies without saying so.
			Name: "list_companies",
			Description: "Companies in the CRM, A-Z, with their contact and open-deal counts, one page at a time. " +
				"The reply says how many match in total; pass its cursor to get the next page.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"q":      prop("string", "Search name and domain"),
				"cursor": prop("string", "The cursor from the previous reply, for the next page"),
				"limit":  prop("number", "Page size, default 50, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				args := decodeArgs(raw)
				limit, err := paging.Limit(intArg(args, "limit", 0), crm.DefaultCompanyPage, crm.MaxCompanyPage)
				if err != nil {
					return "", err
				}
				page, err := crm.ListCompaniesPage(ctx, a.SQLDB, s.WorkspaceID, str(args, "q"), str(args, "cursor"), limit)
				if err != nil {
					return "", fmt.Errorf("could not list companies: %w", err)
				}
				if page.Total == 0 {
					if str(args, "q") != "" {
						return "No companies match.", nil
					}
					return "No companies yet.", nil
				}
				var b strings.Builder
				for _, c := range page.Companies {
					b.WriteString(c.Name)
					if c.Domain != "" {
						fmt.Fprintf(&b, " (%s)", c.Domain)
					}
					if c.Industry != "" || c.Size != "" {
						fmt.Fprintf(&b, " — %s", strings.TrimSpace(strings.Join([]string{c.Industry, c.Size}, " ")))
					}
					fmt.Fprintf(&b, " — %d contact(s), %d open deal(s)\n", c.ContactCount, c.OpenDeals)
				}
				head := fmt.Sprintf("Showing %d of %d compan(ies):\n", len(page.Companies), page.Total)
				if page.NextCursor != "" {
					return head + b.String() + "More: call list_companies again with cursor=" + page.NextCursor, nil
				}
				return head + b.String(), nil
			},
		},
		{
			Name:        "create_company",
			Description: "Add a company. name is required; domain, industry and notes are optional.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"name"}, map[string]any{
				"name":     prop("string", "Company name"),
				"domain":   prop("string", "e.g. acme.com — one company per domain in a workspace"),
				"industry": prop("string", ""),
				"notes":    prop("string", ""),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ Name, Domain, Industry, Notes string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				name := strings.TrimSpace(in.Name)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				_, err := a.SQLDB.ExecContext(ctx, `INSERT INTO companies (workspace_id, name, domain, industry, notes) VALUES ($1, $2, $3, $4, $5)`,
					s.WorkspaceID, name, strings.ToLower(strings.TrimSpace(in.Domain)), strings.TrimSpace(in.Industry), strings.TrimSpace(in.Notes))
				if err != nil {
					if pgCode(err) == "23505" {
						return "", fmt.Errorf("a company with that domain already exists")
					}
					return "", fmt.Errorf("could not create the company: %w", err)
				}
				return fmt.Sprintf("Created company %q.", name), nil
			},
		},
		{
			// MH-408: this used to stop at 500 contacts without saying so. It
			// now reads through marketing.ListContactsPage — the same page,
			// filter and total the contacts screen uses — and says how many
			// there are and how to get the next page.
			Name: "list_contacts",
			Description: "CRM contacts, newest first, one page at a time: with their company and their status on each mailing list. " +
				"The reply says how many match in total; pass its cursor to get the next page.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"q":      prop("string", "Search name, email, phone and company"),
				"list":   prop("string", "Only contacts on this mailing list (its exact name)"),
				"status": map[string]any{"type": "string", "enum": []string{"subscribed", "pending", "unsubscribed", "bounced"}, "description": "With list: only this status on that list"},
				"cursor": prop("string", "The cursor from the previous reply, for the next page"),
				"limit":  prop("number", "Page size, default 100, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
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
				page, err := marketing.ListContactsPage(ctx, a.SQLDB, s.WorkspaceID, f, str(args, "cursor"), intArg(args, "limit", 100))
				if err != nil {
					return "", fmt.Errorf("could not list contacts: %w", err)
				}
				if page.Total == 0 {
					if f.Q != "" || f.ListID != "" {
						return "No contacts match.", nil
					}
					return "No contacts yet.", nil
				}
				var b strings.Builder
				for _, c := range page.Contacts {
					b.WriteString(c.Email)
					if c.Name != "" {
						fmt.Fprintf(&b, " (%s)", c.Name)
					}
					switch {
					case c.Title != "" && c.CompanyName != "":
						fmt.Fprintf(&b, " — %s at %s", c.Title, c.CompanyName)
					case c.Title != "" || c.CompanyName != "":
						fmt.Fprintf(&b, " — %s", c.Title+c.CompanyName)
					}
					if len(c.Subscriptions) > 0 {
						subs := make([]string, 0, len(c.Subscriptions))
						for _, sub := range c.Subscriptions {
							subs = append(subs, sub.ListName+": "+sub.Status)
						}
						fmt.Fprintf(&b, " [%s]", strings.Join(subs, ", "))
					}
					b.WriteByte('\n')
				}
				head := fmt.Sprintf("Showing %d of %d contact(s):\n", len(page.Contacts), page.Total)
				if page.NextCursor != "" {
					return head + b.String() + "More: call list_contacts again with cursor=" + page.NextCursor, nil
				}
				return head + b.String(), nil
			},
		},
		{
			// MH-467: this stopped at 500 deals without saying so. It pages the
			// pipeline in board order through crm.ListDealsPage.
			Name: "list_deals",
			Description: "The pipeline: deals in board order (stage, then position), with stage and value, one page at a time. " +
				"The reply says how many there are in total; pass its cursor to get the next page.",
			Scope: "contacts:read",
			InputSchema: schema(nil, map[string]any{
				"cursor": prop("string", "The cursor from the previous reply, for the next page"),
				"limit":  prop("number", "Page size, default 100, at most 500"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				args := decodeArgs(raw)
				limit, err := paging.Limit(intArg(args, "limit", 0), 100, crm.MaxDealsPage)
				if err != nil {
					return "", err
				}
				if err := crm.EnsureStages(ctx, a.SQLDB, s.WorkspaceID); err != nil {
					return "", fmt.Errorf("could not prepare the pipeline: %w", err)
				}
				page, err := crm.ListDealsPage(ctx, a.SQLDB, s.WorkspaceID, crm.DealsQuery{View: "list", Cursor: str(args, "cursor"), Limit: limit})
				if err != nil {
					return "", fmt.Errorf("could not list deals: %w", err)
				}
				if page.Total == 0 {
					return "No deals yet.", nil
				}
				var b strings.Builder
				for _, d := range page.Deals {
					value := "no value yet"
					if d.ValueMinor > 0 {
						value = money(d.ValueMinor, d.Currency) + " " + d.Currency
					}
					fmt.Fprintf(&b, "%s — %s [%s] %s", d.Title, d.StageName, d.StageKind, value)
					var who []string
					if d.Company != nil {
						who = append(who, d.Company.Name)
					}
					if d.Contact != nil {
						who = append(who, d.Contact.Email)
					}
					if len(who) > 0 {
						fmt.Fprintf(&b, " — %s", strings.Join(who, " / "))
					}
					b.WriteByte('\n')
				}
				head := fmt.Sprintf("Showing %d of %d deal(s):\n", len(page.Deals), page.Total)
				if page.NextCursor != "" {
					return head + b.String() + "More: call list_deals again with cursor=" + page.NextCursor, nil
				}
				return head + b.String(), nil
			},
		},
		{
			Name:        "create_deal",
			Description: "Add a deal to the pipeline. title and value (major units, e.g. 1500.00) are required; it starts in the first stage unless you name one.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"title", "value"}, map[string]any{
				"title":    prop("string", ""),
				"value":    prop("number", "Major units, e.g. 1500.00 for $1,500.00"),
				"currency": prop("string", "3-letter code, default USD"),
				"stage":    prop("string", "A stage name; the first stage (usually \"Lead\") if omitted"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Title    string
					Value    float64
					Currency string
					Stage    string
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				title := strings.TrimSpace(in.Title)
				if title == "" {
					return "", fmt.Errorf("title is required")
				}
				if in.Value < 0 {
					return "", fmt.Errorf("value must be 0 or more")
				}
				if err := crm.EnsureStages(ctx, a.SQLDB, s.WorkspaceID); err != nil {
					return "", fmt.Errorf("could not prepare the pipeline: %w", err)
				}
				currency := strings.ToUpper(strings.TrimSpace(in.Currency))
				if currency == "" {
					currency = "USD"
				}
				var stageID, stageName string
				var q string
				var qArgs []any
				if strings.TrimSpace(in.Stage) != "" {
					q = `SELECT id, name FROM pipeline_stages WHERE workspace_id = $1 AND lower(name) = lower($2)`
					qArgs = []any{s.WorkspaceID, strings.TrimSpace(in.Stage)}
				} else {
					q = `SELECT id, name FROM pipeline_stages WHERE workspace_id = $1 ORDER BY position LIMIT 1`
					qArgs = []any{s.WorkspaceID}
				}
				if err := a.SQLDB.QueryRowContext(ctx, q, qArgs...).Scan(&stageID, &stageName); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no such stage %q — open the pipeline editor to see the stage names", in.Stage)
					}
					return "", fmt.Errorf("could not resolve the stage: %w", err)
				}
				valueMinor := int64(in.Value*100 + 0.5)
				_, err := a.SQLDB.ExecContext(ctx, `
					INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, created_by)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					s.WorkspaceID, title, valueMinor, currency, stageID, s.UserID)
				if err != nil {
					return "", fmt.Errorf("could not create the deal: %w", err)
				}
				return fmt.Sprintf("Created deal %q in %s.", title, stageName), nil
			},
		},
		{
			Name:        "move_deal",
			Description: "Move a deal to a different stage by its exact title.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"title", "stage"}, map[string]any{
				"title": prop("string", "The deal's exact current title"),
				"stage": prop("string", "The destination stage's exact name"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ Title, Stage string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				if err := crm.EnsureStages(ctx, a.SQLDB, s.WorkspaceID); err != nil {
					return "", fmt.Errorf("could not prepare the pipeline: %w", err)
				}
				var stageID, stageName string
				if err := a.SQLDB.QueryRowContext(ctx, `SELECT id, name FROM pipeline_stages WHERE workspace_id = $1 AND lower(name) = lower($2)`,
					s.WorkspaceID, strings.TrimSpace(in.Stage)).Scan(&stageID, &stageName); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no such stage %q", in.Stage)
					}
					return "", fmt.Errorf("could not resolve the stage: %w", err)
				}
				res, err := a.SQLDB.ExecContext(ctx, `UPDATE deals SET stage_id = $3, position = 0, updated_at = now() WHERE workspace_id = $1 AND lower(title) = lower($2)`,
					s.WorkspaceID, strings.TrimSpace(in.Title), stageID)
				if err != nil {
					return "", fmt.Errorf("could not move the deal: %w", err)
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return "", fmt.Errorf("no deal titled %q", in.Title)
				}
				return fmt.Sprintf("Moved %q to %s.", in.Title, stageName), nil
			},
		},
		{
			Name:        "log_activity",
			Description: "Log a note, call, meeting or task against a contact, company or deal (name at least one). A task can carry a due date.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"kind", "body"}, map[string]any{
				"kind":    map[string]any{"type": "string", "enum": []string{"note", "call", "meeting", "task"}},
				"body":    prop("string", ""),
				"contact": prop("string", "A contact's email"),
				"company": prop("string", "A company's exact name"),
				"deal":    prop("string", "A deal's exact title"),
				"due":     prop("string", "YYYY-MM-DD, task only"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ Kind, Body, Contact, Company, Deal, Due string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				if !activityKindsMCP[in.Kind] {
					return "", fmt.Errorf("kind must be one of note, call, meeting, task")
				}
				if strings.TrimSpace(in.Body) == "" {
					return "", fmt.Errorf("body is required")
				}
				var contactID, companyID, dealID sql.NullString
				if in.Contact != "" {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND lower(email) = lower($2)`, s.WorkspaceID, in.Contact).Scan(&contactID); err != nil {
						return "", fmt.Errorf("no contact with email %q", in.Contact)
					}
				}
				if in.Company != "" {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM companies WHERE workspace_id = $1 AND lower(name) = lower($2)`, s.WorkspaceID, in.Company).Scan(&companyID); err != nil {
						return "", fmt.Errorf("no company named %q", in.Company)
					}
				}
				if in.Deal != "" {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM deals WHERE workspace_id = $1 AND lower(title) = lower($2)`, s.WorkspaceID, in.Deal).Scan(&dealID); err != nil {
						return "", fmt.Errorf("no deal titled %q", in.Deal)
					}
				}
				if !contactID.Valid && !companyID.Valid && !dealID.Valid {
					return "", fmt.Errorf("name a contact, company or deal")
				}
				var due sql.NullString
				if in.Kind == "task" && strings.TrimSpace(in.Due) != "" {
					due = sql.NullString{String: strings.TrimSpace(in.Due), Valid: true}
				}
				_, err := a.SQLDB.ExecContext(ctx, `
					INSERT INTO activities (workspace_id, kind, body, due_at, contact_id, company_id, deal_id, created_by)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
					s.WorkspaceID, in.Kind, strings.TrimSpace(in.Body), due, contactID, companyID, dealID, s.UserID)
				if err != nil {
					return "", fmt.Errorf("could not log the activity: %w", err)
				}
				return fmt.Sprintf("Logged a %s.", in.Kind), nil
			},
		},
	}
}

var activityKindsMCP = map[string]bool{"note": true, "call": true, "meeting": true, "task": true}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func money(minor int64, currency string) string {
	_ = currency
	return strconv.FormatFloat(float64(minor)/100, 'f', 2, 64)
}

func errNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
