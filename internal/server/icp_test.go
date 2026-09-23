package server

/*
TestICPEndToEnd (MH-395): the ideal-customer profile and the lifecycle through
the routes the pages use and the MCP tools an agent uses, on the real stack.
Changing the weights re-ranks the contacts list with the reasons; the stage
moves on real events and by hand; the filters and sort agree with the scores.
*/

import (
	"strings"
	"testing"
)

func TestICPEndToEnd(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice

	hotel := obj(alice.must("POST", "/api/w/companies", map[string]any{"name": "Nile Hotel", "domain": "nilehotel-" + rnd() + ".test",
		"industry": "Hospitality", "size": "201-1000", "country": "Egypt"}, 201)["company"])["id"].(string)
	soft := obj(alice.must("POST", "/api/w/companies", map[string]any{"name": "Soft Co", "domain": "soft-" + rnd() + ".test", "industry": "Software"}, 201)["company"])["id"].(string)
	contact := func(email, title, company string) string {
		id := obj(alice.must("POST", "/api/w/contacts", map[string]any{"email": email}, 201)["contact"])["id"].(string)
		alice.must("PATCH", "/api/w/contacts/"+id, map[string]any{"title": title, "company_id": company}, 200)
		return id
	}
	fit := contact("fit-"+rnd()+"@x.test", "F&B Director", hotel)
	busy := contact("busy-"+rnd()+"@x.test", "CTO", soft)
	// A deal with busy: the deal-opened event makes them qualified.
	alice.must("POST", "/api/w/deals", map[string]any{"title": "Soft deal", "contact_id": busy, "company_id": soft}, 201)

	order := func(c *client, path string) []string {
		ids := []string{}
		for _, x := range arr(c.must("GET", path, nil, 200)["contacts"]) {
			ids = append(ids, obj(x)["id"].(string))
		}
		return ids
	}
	put := func(industryWeight float64) {
		alice.must("PUT", "/api/w/icp", map[string]any{
			"industries": map[string]any{"values": []string{"Hospitality"}, "weight": industryWeight},
			"sizes":      map[string]any{"values": []string{}, "weight": 0},
			"regions":    map[string]any{"values": []string{"Egypt"}, "weight": 0},
			"keywords":   map[string]any{"values": []string{}, "weight": 0},
			"signals":    map[string]any{"open_deal": 3},
		}, 200)
	}

	// Deal-heavy: busy first. Industry-heavy: fit first — the same list, re-ranked.
	put(2)
	if got := order(alice, "/api/w/contacts?sort=score"); len(got) != 2 || got[0] != busy || got[1] != fit {
		t.Fatalf("deal-heavy order: %v (busy %s fit %s)", got, busy, fit)
	}
	put(6)
	if got := order(alice, "/api/w/contacts?sort=score"); got[0] != fit || got[1] != busy {
		t.Fatalf("industry-heavy order: %v", got)
	}
	page := alice.must("GET", "/api/w/contacts?sort=score&limit=1", nil, 200)
	top := obj(arr(page["contacts"])[0])
	if top["score"].(float64) != 67 || !strings.Contains(obj(arr(top["score_reasons"])[0])["text"].(string), "Hospitality") {
		t.Errorf("the top contact's score and reasons: %v %v", top["score"], top["score_reasons"])
	}
	// The next page carries on in score order.
	if next := page["next_cursor"].(string); next == "" {
		t.Error("a one-row page of two has no next_cursor")
	} else if got := order(alice, "/api/w/contacts?sort=score&limit=1&cursor="+next); len(got) != 1 || got[0] != busy {
		t.Errorf("page two in score order: %v", got)
	}
	if got := order(alice, "/api/w/contacts?min_score=50"); len(got) != 1 || got[0] != fit {
		t.Errorf("min_score=50: %v", got)
	}
	// Companies are scored and sorted the same way.
	comps := arr(alice.must("GET", "/api/w/companies?sort=score", nil, 200)["companies"])
	if obj(comps[0])["id"] != hotel || obj(comps[0])["score"].(float64) != 67 || obj(comps[0])["country"] != "Egypt" {
		t.Errorf("companies by score: %v", comps)
	}

	// The lifecycle: busy was qualified by the deal; fit set by hand.
	if got := order(alice, "/api/w/contacts?lifecycle=qualified"); len(got) != 1 || got[0] != busy {
		t.Errorf("lifecycle=qualified: %v", got)
	}
	alice.must("PATCH", "/api/w/contacts/"+fit, map[string]any{"lifecycle": "vip"}, 422)
	c := obj(alice.must("PATCH", "/api/w/contacts/"+fit, map[string]any{"lifecycle": "lead"}, 200)["contact"])
	hist := arr(c["lifecycle_history"])
	if c["lifecycle"] != "lead" || len(hist) != 1 || obj(hist[0])["source"] != "manual" || obj(hist[0])["actor_user_id"] == nil {
		t.Errorf("hand-set stage: %v %v", c["lifecycle"], hist)
	}
	alice.must("GET", "/api/w/contacts?lifecycle=vip", nil, 422)

	// Over MCP: a manager-level token reads and changes the profile, and the
	// ranking follows; set_contact_lifecycle records its member.
	agent, _ := mint(t, alice, "icp agent", []string{"contacts:read", "contacts:write", "crm:manage"})
	prof := mcpJSON(t, agent, "get_icp_profile", nil)
	if prof["configured"] != true || obj(obj(prof["profile"])["industries"])["weight"].(float64) != 6 {
		t.Errorf("get_icp_profile: %v", prof)
	}
	mcpJSON(t, agent, "set_icp_profile", map[string]any{"industries": map[string]any{"values": []string{"Hospitality"}, "weight": 1}})
	list := mcpJSON(t, agent, "list_contacts", map[string]any{"sort": "score"})
	if first := obj(arr(list["contacts"])[0]); first["id"] != busy || len(arr(first["score_reasons"])) == 0 {
		t.Errorf("after set_icp_profile, busy should lead again: %v", list["contacts"])
	}
	out := mcpJSON(t, agent, "set_contact_lifecycle", map[string]any{"id": busy, "lifecycle": "customer"})
	if obj(out["contact"])["lifecycle"] != "customer" {
		t.Errorf("set_contact_lifecycle: %v", out)
	}
	if msg := mcpRefused(t, agent, "set_icp_profile", map[string]any{"signals": map[string]any{"likes_us": 2}}); !strings.Contains(msg, "likes_us") {
		t.Errorf("an unknown signal: %s", msg)
	}
	// A token without crm:manage cannot change the profile.
	reader, _ := mint(t, alice, "icp reader", []string{"contacts:read"})
	code, text, isErr := mcpCall(reader, "/api/mcp", "set_icp_profile", map[string]any{"signals": map[string]any{"open_deal": 9}})
	if code == 200 && !isErr {
		t.Errorf("a contacts:read token changed the profile: %s", text)
	}
}
