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
// contacts:write — the same scope pair internal/crm's REST routes use
// (scopes.go's own comment already promises "CRM contacts, companies and
// leads" under that name). Queries run on the app pool; ctx carries the
// workspace, so RLS confines every row the same way the REST handlers are —
// each query still names workspace_id explicitly too, matching that package's
// defense-in-depth convention.
func CrmTools() []Tool {
	return []Tool{
		{
			Name:        "list_companies",
			Description: "Companies in the CRM, with their contact and open-deal counts.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT name, domain, industry, size, (SELECT count(*) FROM contacts c WHERE c.workspace_id = co.workspace_id AND c.company_id = co.id),
					       (SELECT count(*) FROM deals d JOIN pipeline_stages ps ON ps.workspace_id = d.workspace_id AND ps.id = d.stage_id WHERE d.workspace_id = co.workspace_id AND d.company_id = co.id AND ps.kind = 'open')
					FROM companies co WHERE workspace_id = $1 ORDER BY lower(name) LIMIT 200`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list companies: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var name, domain, industry, size string
					var contacts, openDeals int
					if err := rows.Scan(&name, &domain, &industry, &size, &contacts, &openDeals); err != nil {
						return "", fmt.Errorf("could not list companies: %w", err)
					}
					n++
					fmt.Fprintf(&b, "%s", name)
					if domain != "" {
						fmt.Fprintf(&b, " (%s)", domain)
					}
					fmt.Fprintf(&b, " — %d contact(s), %d open deal(s)", contacts, openDeals)
					if industry != "" {
						fmt.Fprintf(&b, ", %s", industry)
					}
					if size != "" {
						fmt.Fprintf(&b, ", %s employees", size)
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					return "No companies yet.", nil
				}
				return fmt.Sprintf("%d compan%s:\n%s", n, plural(n, "y", "ies"), b.String()), nil
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
			Name:        "list_contacts",
			Description: "CRM contacts, with their company and lifecycle status.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT c.email, c.name, c.title, coalesce(co.name, '')
					FROM contacts c LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id
					WHERE c.workspace_id = $1 ORDER BY lower(c.email) LIMIT 500`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list contacts: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var email, name, title, company string
					if err := rows.Scan(&email, &name, &title, &company); err != nil {
						return "", fmt.Errorf("could not list contacts: %w", err)
					}
					n++
					fmt.Fprintf(&b, "%s", email)
					if name != "" {
						fmt.Fprintf(&b, " (%s)", name)
					}
					if title != "" || company != "" {
						fmt.Fprintf(&b, " — %s", strings.TrimSpace(strings.Join([]string{title, company}, " at ")))
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					return "No contacts yet.", nil
				}
				return fmt.Sprintf("%d contact(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "list_deals",
			Description: "The pipeline: every deal, its stage and value.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT d.title, ps.name, ps.kind, d.value_minor, d.currency, coalesce(co.name, ''), coalesce(ct.email, '')
					FROM deals d
					JOIN pipeline_stages ps ON ps.workspace_id = d.workspace_id AND ps.id = d.stage_id
					LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id
					LEFT JOIN contacts ct ON ct.workspace_id = d.workspace_id AND ct.id = d.contact_id
					WHERE d.workspace_id = $1 ORDER BY ps.position, d.position LIMIT 500`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list deals: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var title, stage, kind, currency, company, contact string
					var valueMinor int64
					if err := rows.Scan(&title, &stage, &kind, &valueMinor, &currency, &company, &contact); err != nil {
						return "", fmt.Errorf("could not list deals: %w", err)
					}
					n++
					fmt.Fprintf(&b, "%s — %s [%s] %s %s", title, stage, kind, money(valueMinor, currency), currency)
					if company != "" || contact != "" {
						fmt.Fprintf(&b, " — %s", strings.TrimSpace(strings.Join([]string{company, contact}, " / ")))
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					return "No deals yet.", nil
				}
				return fmt.Sprintf("%d deal(s):\n%s", n, b.String()), nil
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
