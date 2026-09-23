package server

/*
TestCRMReportEndToEnd (MH-394): deals worked through the REST routes the board
uses — opened, moved stage by stage, won and lost with the day they closed —
then the report read over REST and over MCP. The two answers must be the same
figures, because they are the same function; the funnel must see the stages
the deals moved through, because the trigger recorded them on the way.
*/

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCRMReportEndToEnd(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice

	stages := arr(alice.must("GET", "/api/w/pipeline/stages", nil, 200)["stages"])
	id := func(i int) string { return obj(stages[i])["id"].(string) }
	lead, qual, prop, won, lost := id(0), id(1), id(2), id(3), id(4)
	if obj(stages[3])["kind"] != "won" || obj(stages[4])["kind"] != "lost" {
		t.Fatalf("default pipeline: %v", stages)
	}
	deal := func(title string, value int) string {
		return obj(alice.must("POST", "/api/w/deals", map[string]any{"title": title, "value_minor": value}, 201)["deal"])["id"].(string)
	}
	move := func(dealID string, body map[string]any, want int) map[string]any {
		return alice.must("PATCH", "/api/w/deals/"+dealID, body, want)
	}
	today := time.Now().UTC()
	day := func(d int) string { return today.AddDate(0, 0, d).Format("2006-01-02") }

	// A: through every stage, won 3 days ago. B: to Qualified, lost 10 days
	// ago. C: still in Lead. D: straight to Proposal.
	a := deal("A", 120000)
	move(a, map[string]any{"stage_id": qual}, 200)
	move(a, map[string]any{"stage_id": prop}, 200)
	won1 := move(a, map[string]any{"stage_id": won, "closed_on": day(-3)}, 200)
	if closed, _ := obj(won1["deal"])["closed_at"].(string); !strings.HasPrefix(closed, day(-3)) {
		t.Errorf("won with closed_on %s, closed_at = %v", day(-3), obj(won1["deal"])["closed_at"])
	}
	b := deal("B", 30000)
	move(b, map[string]any{"stage_id": qual}, 200)
	move(b, map[string]any{"stage_id": lost}, 200)
	move(b, map[string]any{"closed_on": day(-10)}, 200) // the close day corrected afterwards
	deal("C", 5000)
	d := deal("D", 70000)
	move(d, map[string]any{"stage_id": prop}, 200)

	// A close day only belongs to a closed deal, and never to the future.
	move(d, map[string]any{"closed_on": day(-1)}, 422)
	move(d, map[string]any{"stage_id": lead, "closed_on": day(-1)}, 422)
	move(a, map[string]any{"closed_on": day(2)}, 422)
	move(a, map[string]any{"closed_on": "last week"}, 422)

	from, to := day(-29), day(0)
	rest := alice.must("GET", "/api/w/reports/crm?from="+from+"&to="+to, nil, 200)
	var rep struct {
		Pipeline struct {
			OpenDeals      int   `json:"open_deals"`
			OpenValueMinor int64 `json:"open_value_minor"`
		} `json:"pipeline"`
		Deals struct {
			Opened, Won, Lost int
			WinRate           float64 `json:"win_rate"`
			WonValueMinor     int64   `json:"won_value_minor"`
			AvgWonValueMinor  int64   `json:"avg_won_value_minor"`
		} `json:"deals"`
		Conversion struct {
			Steps []struct {
				Name    string `json:"name"`
				Reached int    `json:"reached"`
			} `json:"steps"`
		} `json:"conversion"`
		Timeline []struct {
			Start     string `json:"start"`
			Won, Lost int
		} `json:"timeline"`
		LeadSources *json.RawMessage `json:"lead_sources"`
	}
	raw, _ := json.Marshal(rest)
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Pipeline.OpenDeals != 2 || rep.Pipeline.OpenValueMinor != 75000 {
		t.Errorf("pipeline: %+v", rep.Pipeline)
	}
	if rep.Deals.Opened != 4 || rep.Deals.Won != 1 || rep.Deals.Lost != 1 || rep.Deals.WinRate != 0.5 ||
		rep.Deals.WonValueMinor != 120000 || rep.Deals.AvgWonValueMinor != 120000 {
		t.Errorf("deals: %+v", rep.Deals)
	}
	// Lead 4, Qualified A+B+D = 3, Proposal A+D = 2, Won A = 1.
	got := []int{}
	for _, s := range rep.Conversion.Steps {
		got = append(got, s.Reached)
	}
	if len(got) != 4 || got[0] != 4 || got[1] != 3 || got[2] != 2 || got[3] != 1 {
		t.Errorf("funnel from the recorded stages: %v", rep.Conversion.Steps)
	}
	var tw, tl int
	for _, k := range rep.Timeline {
		tw += k.Won
		tl += k.Lost
	}
	if tw != 1 || tl != 1 || rep.LeadSources == nil {
		t.Errorf("timeline won %d lost %d; lead sources %v", tw, tl, rep.LeadSources)
	}
	alice.must("GET", "/api/w/reports/crm?from="+to+"&to="+from, nil, 422)

	// MCP answers the same figures from the same function.
	agent, _ := mint(t, alice, "report agent", []string{"contacts:read", "leads:read", "inbox:read"})
	viaMCP := mcpJSON(t, agent, "crm_report", map[string]any{"from": from, "to": to})
	delete(viaMCP, "generated_at")
	delete(rest, "generated_at")
	delete(rest, "$schema")
	a1, _ := json.Marshal(rest)
	a2, _ := json.Marshal(viaMCP)
	if string(a1) != string(a2) {
		t.Errorf("REST and MCP disagree:\nREST %s\nMCP  %s", a1, a2)
	}
	// A CRM-only token gets the CRM, and nulls where it has no scope.
	narrow, _ := mint(t, alice, "crm only", []string{"contacts:read"})
	crmOnly := mcpJSON(t, narrow, "crm_report", map[string]any{"from": from, "to": to})
	if crmOnly["lead_sources"] != nil || crmOnly["inbox"] != nil || obj(crmOnly["deals"])["won"].(float64) != 1 {
		t.Errorf("CRM-only token: lead_sources %v inbox %v deals %v", crmOnly["lead_sources"], crmOnly["inbox"], crmOnly["deals"])
	}
	if msg := mcpRefused(t, narrow, "crm_report", map[string]any{"from": to, "to": from}); !strings.Contains(msg, "after") {
		t.Errorf("reversed range over MCP: %s", msg)
	}
}
