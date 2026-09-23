package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/leads"
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
func LeadTools() []Tool {
	leadLine := func(b *strings.Builder, l leads.Lead) {
		who := firstNonEmpty(l.Name, l.Email)
		fmt.Fprintf(b, "- [%s] %s <%s>", l.Status, who, l.Email)
		if l.Company != "" {
			fmt.Fprintf(b, " — %s", l.Company)
		}
		if src := l.Attribution.UtmSource; src != "" {
			fmt.Fprintf(b, " · %s", src)
			if m := l.Attribution.UtmMedium; m != "" {
				fmt.Fprintf(b, "/%s", m)
			}
		}
		fmt.Fprintf(b, " · %s · id=%s\n  %s\n", l.CreatedAt.Format("2006-01-02 15:04"), l.ID, firstLine(l.Message))
	}
	errText := func(err error) error {
		switch {
		case errors.Is(err, leads.ErrNotFound):
			return errNotFound
		case errors.Is(err, leads.ErrAlreadyConverted), errors.Is(err, leads.ErrStatus), errors.Is(err, leads.ErrSpam):
			return err
		}
		return fmt.Errorf("could not complete that: %w", err)
	}

	return []Tool{
		{
			Name:        "list_leads",
			Description: "Leads from the workspace's website inquiry forms, newest first, with a count per status. Optional status (new, contacted, qualified, converted, spam) and q (matches name, email, company and message).",
			Scope:       "leads:read",
			InputSchema: schema(nil, map[string]any{
				"status": map[string]any{"type": "string", "enum": leads.Statuses},
				"q":      prop("string", "Search name, email, company and message"),
				"limit":  prop("integer", "At most this many (default 50, max 500)"),
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
				list, counts, err := leads.List(ctx, a.SQLDB, s.WorkspaceID, leads.Filter{
					Status: status, Q: str(args, "q"), Limit: intArg(args, "limit", 50),
				})
				if err != nil {
					return "", errText(err)
				}
				var b strings.Builder
				parts := []string{}
				for _, st := range leads.Statuses {
					parts = append(parts, fmt.Sprintf("%s %d", st, counts[st]))
				}
				fmt.Fprintf(&b, "Counts: %s\n", strings.Join(parts, ", "))
				if len(list) == 0 {
					b.WriteString("No leads match.")
					return b.String(), nil
				}
				fmt.Fprintf(&b, "%d lead(s):\n", len(list))
				for _, l := range list {
					leadLine(&b, l)
				}
				return b.String(), nil
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
				return describeLead(*l), nil
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
				return fmt.Sprintf("Lead %s is now %s.", l.ID, l.Status), nil
			},
		},
		{
			Name: "convert_lead",
			Description: "Convert a lead in one step: the contact (reused if the email is already one), the company from the email domain " +
				"(none for free mail such as gmail or outlook), a deal on the pipeline's first open stage, and the message as the first " +
				"activity on its timeline. Needs contacts:write as well as leads:write.",
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
				var b strings.Builder
				fmt.Fprintf(&b, "Converted lead %s.\n", c.Lead.ID)
				verb := "reused existing"
				if c.ContactCreated {
					verb = "created"
				}
				fmt.Fprintf(&b, "contact: %s (%s) id=%s\n", c.Lead.Email, verb, c.ContactID)
				switch {
				case c.CompanyID == "" && c.FreeMail:
					b.WriteString("company: none — the email is free mail\n")
				case c.CompanyID == "":
					b.WriteString("company: none\n")
				default:
					verb = "reused existing"
					if c.CompanyCreated {
						verb = "created"
					}
					name := ""
					if c.Lead.ConvertedCompany != nil {
						name = c.Lead.ConvertedCompany.Name
					}
					fmt.Fprintf(&b, "company: %s (%s) id=%s\n", name, verb, c.CompanyID)
				}
				title := ""
				if c.Lead.ConvertedDeal != nil {
					title = c.Lead.ConvertedDeal.Name
				}
				fmt.Fprintf(&b, "deal: %s id=%s (the message is its first activity)\n", title, c.DealID)
				return b.String(), nil
			},
		},
	}
}

func describeLead(l leads.Lead) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Lead %s — %s\n", l.ID, l.Status)
	row := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	row("name", l.Name)
	row("email", l.Email)
	row("phone", l.Phone)
	row("company", l.Company)
	row("form", l.FormName)
	row("site", l.Origin)
	row("received", l.CreatedAt.Format("2006-01-02 15:04 MST"))
	at := l.Attribution
	row("utm_source", at.UtmSource)
	row("utm_medium", at.UtmMedium)
	row("utm_campaign", at.UtmCampaign)
	row("utm_term", at.UtmTerm)
	row("utm_content", at.UtmContent)
	row("gclid", at.Gclid)
	row("referrer", at.Referrer)
	row("landing_page", at.LandingPage)
	if l.ConvertedContact != nil {
		row("converted contact", l.ConvertedContact.Name+" id="+l.ConvertedContact.ID)
	}
	if l.ConvertedCompany != nil {
		row("converted company", l.ConvertedCompany.Name+" id="+l.ConvertedCompany.ID)
	}
	if l.ConvertedDeal != nil {
		row("converted deal", l.ConvertedDeal.Name+" id="+l.ConvertedDeal.ID)
	}
	fmt.Fprintf(&b, "\nmessage:\n%s\n", l.Message)
	return b.String()
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
