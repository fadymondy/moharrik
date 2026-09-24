package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/icp"
)

/*
ICPTools (MH-395): the ideal-customer profile and the contact lifecycle.

get_icp_profile and set_icp_profile answer from icp.ProfileFor / icp.SaveFor,
the functions GET and PUT /api/w/icp call; set_contact_lifecycle goes through
crm.UpdateContact, the function PATCH /api/w/contacts/{id} calls, so the
stage an agent sets is recorded as its member's, like a member's own.
*/
func ICPTools() []Tool {
	criterion := func(desc string) map[string]any {
		return map[string]any{"type": "object", "description": desc, "properties": map[string]any{
			"values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"weight": map[string]any{"type": "number", "description": "-10 to 10; negative is a penalty, 0 turns it off"},
		}}
	}
	return []Tool{
		{
			Name: "get_icp_profile",
			Description: "The workspace's ideal-customer profile: industries, company sizes, regions (company country) and keywords, each " +
				"with a weight, and a weight per signal (open_deal, won_deal, lost_deal, inbound_lead, conversation, subscribed, " +
				"whatsapp_opt_in, recent_activity). Every contact and company is scored 0-100 against it: score = the sum of " +
				"weight × how much of each criterion is present, over the sum of the positive weights. configured is false while the " +
				"defaults are in use; scored says how many contacts have a score.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				out, err := icp.ProfileFor(ctx, a.SQLDB)
				if err != nil {
					return "", err
				}
				return jsonResult(out)
			},
		},
		{
			Name: "set_icp_profile",
			Description: "Change the ideal-customer profile and rescore every contact and company by it at once. Give only what changes " +
				"(each criterion replaces its values and weight; signals merges into the current weights). Returns the saved profile. " +
				"Then list_contacts with sort=score shows the new ranking, each with the reasons behind its score.",
			Scope: "crm:manage",
			InputSchema: schema(nil, map[string]any{
				"industries": criterion("Company industries that fit"),
				"sizes":      criterion("Company sizes that fit: 1-10, 11-50, 51-200, 201-1000, 1000+"),
				"regions":    criterion("Company countries or regions that fit"),
				"keywords":   criterion("Words that mark a fit in a contact's title and tags or a company's name, industry and notes"),
				"signals":    map[string]any{"type": "object", "description": "signal name -> weight (-10 to 10), e.g. {\"open_deal\": 3, \"lost_deal\": -2}", "additionalProperties": map[string]any{"type": "number"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Industries *icp.Criterion      `json:"industries"`
					Sizes      *icp.Criterion      `json:"sizes"`
					Regions    *icp.Criterion      `json:"regions"`
					Keywords   *icp.Criterion      `json:"keywords"`
					Signals    map[string]*float64 `json:"signals"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				cur, err := icp.ProfileFor(ctx, a.SQLDB)
				if err != nil {
					return "", err
				}
				p := cur.Profile
				for _, c := range []struct {
					from *icp.Criterion
					to   *icp.Criterion
				}{{in.Industries, &p.Industries}, {in.Sizes, &p.Sizes}, {in.Regions, &p.Regions}, {in.Keywords, &p.Keywords}} {
					if c.from != nil {
						*c.to = *c.from
					}
				}
				for k, w := range in.Signals {
					if w == nil {
						return "", fmt.Errorf("signals: %s needs a number", k)
					}
					p.Signals[k] = *w
				}
				out, err := icp.SaveFor(ctx, a.SQLDB, p)
				if err != nil {
					return "", err
				}
				return jsonResult(out)
			},
		},
		{
			Name: "set_contact_lifecycle",
			Description: "Set a contact's lifecycle stage by hand: subscriber, lead, qualified, customer, or empty. Recorded in its " +
				"history as set by this token's member. (Events also move it forward on their own: a confirmed subscription, a " +
				"converted lead, a deal opened, a deal won.) Returns the contact with its lifecycle history and score.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id", "lifecycle"}, map[string]any{
				"id":        prop("string", "Contact id"),
				"lifecycle": map[string]any{"type": "string", "enum": []string{"", "subscriber", "lead", "qualified", "customer"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID        string  `json:"id"`
					Lifecycle *string `json:"lifecycle"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if in.Lifecycle == nil {
					return "", fmt.Errorf("lifecycle is required (empty string clears it)")
				}
				c, err := crm.UpdateContact(ctx, a.SQLDB, id, crm.ContactFields{Lifecycle: in.Lifecycle})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"contact": c})
			},
		},
	}
}
