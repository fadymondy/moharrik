package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/prospects"
	"github.com/fadymondy/moharrik/internal/research"
)

/*
ProspectTools: prospect discovery over MCP (MH-397), named after
fadymondy.com's Opportunity Radar tools so an agent that knows one knows the
other — capability_profile, find_opportunities, get_opportunity,
mark_opportunity, draft_message — plus run_discovery and prospect_digest.

An "opportunity" here is a prospect: a company research found, scored against
the workspace's ideal customer, with the evidence behind it. Nothing reaches
the CRM without mark_opportunity(accept), and draft_message only ever writes
text: there is no tool that sends outreach.
*/
func ProspectTools(engine *research.Engine) []Tool {
	return []Tool{
		{
			Name: "capability_profile",
			Description: "The workspace's ideal customer, as prospect discovery searches for and scores it: industries, keywords, regions, " +
				"excluded words, signal weights, the offer the outreach draft pitches, the queries discovery will search, and whether " +
				"scheduled discovery is on. Pass any field to change it (waiting prospects are rescored).",
			Scope: "research:read",
			InputSchema: schema(nil, map[string]any{
				"offer":            prop("string", "What the workspace sells, in a sentence or two."),
				"industries":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"keywords":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"regions":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"exclude_keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"signal_weights":   map[string]any{"type": "object", "description": "Signal type -> weight 0..3: hiring, funding, launch, expansion, partnership, leadership, technology, regulatory."},
				"queries":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What discovery searches, in turn. Empty: built from the profile."},
				"enabled":          prop("boolean", "Scheduled discovery on or off (spends the daily research budget)."),
				"cadence":          map[string]any{"type": "string", "enum": []string{"daily", "weekly"}},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in prospects.ProspectProfileInput
				if err := bind(args, &in); err != nil {
					return "", err
				}
				changing := in.Offer != nil || in.Industries != nil || in.Keywords != nil || in.Regions != nil || in.ExcludeKeywords != nil ||
					in.SignalWeights != nil || in.Queries != nil || in.Enabled != nil || in.Cadence != nil
				var p *prospects.ProspectProfile
				if changing {
					if !authz.Can(s.Role, authz.ResearchRun) || !hasScope(s.Scopes, "research:run") {
						return "", errors.New("changing the profile needs a manager's role and the research:run scope")
					}
					p, err = prospects.SaveProfile(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in)
				} else {
					p, err = prospects.GetProfile(ctx, a.SQLDB, s.WorkspaceID)
				}
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"profile": p, "can_search": engine.Caps().CanSearch})
			},
		},
		{
			Name: "run_discovery",
			Description: "Run one prospect-discovery pass now: the profile's next query, as a research run. Returns the run id; when it " +
				"settles, the companies it found become prospects (find_opportunities). Does not spend the daily budget.",
			Scope:       "research:run",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ResearchRun) {
					return "", errors.New("your role cannot start research in this workspace")
				}
				if engine == nil {
					return "", research.ErrNoSearch
				}
				runID, q, err := prospects.Discover(ctx, a, engine, s.WorkspaceID, s.UserID, false)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"run_id": runID, "query": q, "note": "read it with get_research; prospects appear when it settles"})
			},
		},
		{
			Name: "find_opportunities",
			Description: "Prospects discovery found, best first: score 0-100 with every point explained (signals, fit, region, evidence, " +
				"recency, exclusions, and feedback from your past verdicts), the person the pages name if any, and the CRM ids once accepted.",
			Scope: "research:read",
			InputSchema: schema(nil, map[string]any{
				"status":    map[string]any{"type": "string", "enum": []string{"new", "accepted", "rejected", "all"}, "description": "Default new."},
				"q":         prop("string", "Name or domain contains."),
				"min_score": prop("number", "Only prospects scoring at least this."),
				"limit":     prop("integer", "Default 50."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Status   string  `json:"status"`
					Q        string  `json:"q"`
					MinScore float64 `json:"min_score"`
					Limit    int     `json:"limit"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				list, counts, err := prospects.List(ctx, a.SQLDB, s.WorkspaceID, prospects.Query{Status: in.Status, Q: in.Q, MinScore: in.MinScore, Limit: in.Limit})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"count": len(list), "counts": counts, "opportunities": list})
			},
		},
		{
			Name: "get_opportunity",
			Description: "One prospect in full: its score and reasons, the person the pages name, its signal timeline (every piece of " +
				"evidence, oldest first, with its source URL) and the outreach drafts written for it.",
			Scope:       "research:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The prospect id (find_opportunities).")}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				d, err := prospects.Get(ctx, a.SQLDB, s.WorkspaceID, id)
				if err != nil {
					return "", err
				}
				return jsonResult(d)
			},
		},
		{
			Name: "mark_opportunity",
			Description: "Decide a prospect. accept adds the company, the contact (given an email — a contact is an address) and a deal " +
				"to the CRM with the evidence on the timeline; reject dismisses it and ranks similar prospects lower from now on.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id", "mark"}, map[string]any{
				"id":            prop("string", "The prospect id."),
				"mark":          map[string]any{"type": "string", "enum": []string{"accept", "reject"}},
				"reason":        prop("string", "reject: why, in a sentence."),
				"contact_name":  prop("string", "accept: the person, if not the one the pages named."),
				"contact_email": prop("string", "accept: their address. Without it no contact is added."),
				"contact_title": prop("string", "accept: their role."),
				"deal_title":    prop("string", "accept: defaults to '<company> — prospect'."),
				"value_minor":   prop("integer", "accept: the deal's value in minor units."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID     string `json:"id"`
					Mark   string `json:"mark"`
					Reason string `json:"reason"`
					prospects.ProspectAcceptInput
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ContactsWrite) {
					return "", errors.New("your role cannot change the CRM in this workspace")
				}
				switch strings.ToLower(strings.TrimSpace(in.Mark)) {
				case "accept":
					res, err := prospects.Accept(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id, in.ProspectAcceptInput)
					if err != nil {
						return "", err
					}
					return jsonResult(res)
				case "reject":
					p, err := prospects.Reject(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id, in.Reason)
					if err != nil {
						return "", err
					}
					return jsonResult(p)
				}
				return "", errors.New("mark is accept or reject")
			},
		},
		{
			Name: "draft_message",
			Description: "Draft first outreach to a prospect, in en or ar, from its evidence only. A DRAFT: it is stored and returned, and " +
				"Moharrik never sends it — a person copies it. With no model configured it is a template (generator=template).",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":     prop("string", "The prospect id."),
				"locale": map[string]any{"type": "string", "enum": []string{"en", "ar"}},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID     string `json:"id"`
					Locale string `json:"locale"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ContactsWrite) {
					return "", errors.New("your role cannot draft outreach in this workspace")
				}
				var client *ai.Client
				if engine != nil {
					client = engine.AI
				}
				d, err := prospects.MakeDraft(ctx, a.SQLDB, client, s.WorkspaceID, s.UserID, id, in.Locale)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"draft": d, "sent": false, "note": "a draft only — nothing was sent"})
			},
		},
		{
			Name:        "prospect_digest",
			Description: "The prospects found since the last digest, best first. mark=true moves the mark to now, so the next digest starts after these.",
			Scope:       "research:read",
			InputSchema: schema(nil, map[string]any{"mark": prop("boolean", "Move the digest mark to now. Default false.")}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Mark bool `json:"mark"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				d, err := prospects.TakeDigest(ctx, a.SQLDB, s.WorkspaceID, in.Mark)
				if err != nil {
					return "", err
				}
				return jsonResult(d)
			},
		},
	}
}
