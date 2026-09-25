package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/leads"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
LeadTools: the inbound queue from website inquiry forms (MH-390), over
leads:read / leads:write. They call the same internal/leads functions as the
REST routes, on the app pool with the token's workspace on ctx, so RLS and the
explicit workspace_id predicates apply exactly as they do in the browser.

convert_lead is the one tool that needs two scopes. Its own is leads:write,
but converting CREATES CRM rows — a contact, maybe a company, a deal and an
activity — so the token must also carry contacts:write. An agent minted only
to triage leads can mark them contacted or spam, and cannot fill the CRM.
*/
// leadSummary is a lead as list_leads' items answer it.
func leadSummary(l leads.Lead) map[string]any {
	return map[string]any{
		"id": l.ID, "status": l.Status, "name": l.Name, "email": l.Email, "company": l.Company,
		"utm_source": l.Attribution.UtmSource, "utm_medium": l.Attribution.UtmMedium,
		"created_at": l.CreatedAt, "message": clipText(l.Message, 300),
	}
}

func LeadTools() []Tool {
	errText := func(err error) error {
		switch {
		case errors.Is(err, leads.ErrNotFound):
			return errNotFound
		case errors.Is(err, leads.ErrAlreadyConverted), errors.Is(err, leads.ErrStatus), errors.Is(err, leads.ErrSpam):
			return err
		case errors.Is(err, paging.ErrBadCursor):
			return err
		}
		var tooLarge *paging.TooLargeError
		if errors.As(err, &tooLarge) {
			return err
		}
		return fmt.Errorf("could not complete that: %w", err)
	}

	return []Tool{
		{
			Name: "list_leads",
			Description: "Leads from the workspace's website inquiry forms, newest first, a page at a time: each item's id, status, name, " +
				"email, company, UTM source/medium, when it arrived and a clipped preview of the message. counts is how many leads the " +
				"workspace has in each status (unfiltered); total is how many match this filter. Optional status (new, contacted, " +
				"qualified, converted, spam) and q (matches name, email, company and message). Pass next_cursor back as cursor for more. " +
				"get_lead opens one in full.",
			Scope: "leads:read",
			InputSchema: schema(nil, map[string]any{
				"status": map[string]any{"type": "string", "enum": leads.Statuses},
				"q":      prop("string", "Search name, email, company and message"),
				"limit":  prop("integer", "Page size, default 50, at most 500"),
				"cursor": prop("string", "next_cursor from the previous answer"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				args := decodeArgs(raw)
				status := str(args, "status")
				if status != "" && !contains(leads.Statuses, status) {
					return "", fmt.Errorf("status must be one of %s", strings.Join(leads.Statuses, ", "))
				}
				page, err := leads.List(ctx, a.SQLDB, s.WorkspaceID, leads.Filter{
					Status: status, Q: str(args, "q"), Limit: intArg(args, "limit", 0), Cursor: str(args, "cursor"),
				})
				if err != nil {
					return "", errText(err)
				}
				items := make([]map[string]any, 0, len(page.Leads))
				for _, l := range page.Leads {
					items = append(items, leadSummary(l))
				}
				return jsonResult(map[string]any{
					"total": page.Total, "count": len(items), "items": items, "next_cursor": page.NextCursor, "counts": page.Counts,
				})
			},
		},
		{
			Name:        "get_lead",
			Description: "One lead in full: the message, phone and company typed, the form, the attribution (UTM, referrer, gclid, landing page) and what it converted into.",
			Scope:       "leads:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Lead id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				l, err := leads.Get(ctx, a.SQLDB, s.WorkspaceID, str(decodeArgs(raw), "id"))
				if err != nil {
					return "", errText(err)
				}
				return jsonResult(map[string]any{"lead": *l})
			},
		},
		{
			Name:        "update_lead",
			Description: "Set a lead's status: new, contacted, qualified or spam. converted is reached with convert_lead, and a converted lead keeps its status.",
			Scope:       "leads:write",
			InputSchema: schema([]string{"id", "status"}, map[string]any{
				"id":     prop("string", "Lead id"),
				"status": map[string]any{"type": "string", "enum": []string{"new", "contacted", "qualified", "spam"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				args := decodeArgs(raw)
				l, err := leads.SetStatus(ctx, a.SQLDB, s.WorkspaceID, str(args, "id"), str(args, "status"))
				if err != nil {
					return "", errText(err)
				}
				return jsonResult(map[string]any{"ok": true, "lead": *l})
			},
		},
		{
			Name: "convert_lead",
			Description: "Convert a lead in one step: the contact (reused if the email is already one), the company from the email domain " +
				"(none for free mail such as gmail or outlook), a deal on the pipeline's first open stage, and the message as the first " +
				"activity on its timeline. Needs contacts:write as well as leads:write. Returns the lead, and for the contact and company " +
				"whether it was created or an existing one was reused.",
			Scope:       "leads:write",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Lead id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				if !hasScope(s.Scopes, "contacts:write") {
					return "", fmt.Errorf("convert_lead creates a contact, company, deal and activity, so the token also needs the \"contacts:write\" scope (it has: %s)",
						strings.Join(nonEmpty(s.Scopes), ", "))
				}
				c, err := leads.Convert(ctx, a.SQLDB, s.WorkspaceID, str(decodeArgs(raw), "id"), s.UserID)
				if err != nil {
					return "", errText(err)
				}
				out := map[string]any{
					"ok": true, "lead": c.Lead, "contact_id": c.ContactID, "contact_created": c.ContactCreated,
					"company_id": c.CompanyID, "company_created": c.CompanyCreated, "free_mail": c.FreeMail,
					"deal_id": c.DealID,
				}
				return jsonResult(out)
			},
		},
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
