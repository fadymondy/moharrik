package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC)

func at(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

func reason(parts []TrendReason, kind string) *TrendReason {
	for i := range parts {
		if parts[i].Kind == kind {
			return &parts[i]
		}
	}
	return nil
}

// The Ratcliffe story as it was carried on 2026-09-23: The Guardian and the
// BBC within the hour, matched by the workspace's "energy prices" topic.
func TestRankExplainsItself(t *testing.T) {
	items := []Signal{
		{Outlet: "The Guardian: Business", Provider: ProviderFeed, Published: at(0), TopicIDs: []string{"t-energy"}},
		{Outlet: "BBC News: Business", Provider: ProviderFeed, Published: at(1)},
	}
	score, parts := Rank(items, map[string]string{"t-energy": "energy prices", "t-other": "coffee prices"})

	src := reason(parts, "sources")
	if src == nil || src.Points != 25 || src.Count != 2 || strings.Join(src.Detail, ",") != "BBC News: Business,The Guardian: Business" {
		t.Fatalf("sources = %+v", src)
	}
	vel := reason(parts, "velocity")
	if vel == nil || vel.Points != 6 || vel.Count != 2 || vel.Label != "Rising: 2 reports within 6 hours" {
		t.Fatalf("velocity = %+v", vel)
	}
	rel := reason(parts, "relevance")
	if rel == nil || rel.Points != 15 || strings.Join(rel.Detail, ",") != "energy prices" {
		t.Fatalf("relevance = %+v", rel)
	}
	if score != 46 {
		t.Fatalf("score = %v, want 25 + 6 + 15", score)
	}
	// Largest part first: the page shows the chips in this order.
	for i := 1; i < len(parts); i++ {
		if parts[i].Points > parts[i-1].Points {
			t.Fatalf("parts out of order: %+v", parts)
		}
	}
}

func TestRankLoneItemAndHackerNews(t *testing.T) {
	// A lone headline: 5 points for its one source, no other parts.
	score, parts := Rank([]Signal{{Outlet: "Daily Coffee News", Provider: ProviderFeed, Published: at(0)}}, nil)
	if score != 5 || len(parts) != 1 {
		t.Fatalf("lone item: %v %+v", score, parts)
	}
	// Hacker News #3 with 412 points: (31-3)/30*15 = 14 + min(15, 4.12).
	score, parts = Rank([]Signal{{Outlet: "Hacker News", Provider: ProviderHN, Rank: 3, Points: 412, Published: at(0)}}, nil)
	vel := reason(parts, "velocity")
	if vel == nil || vel.Label != "Rising: Hacker News #3, 412 points" || vel.Points != 18.1 {
		t.Fatalf("hn velocity = %+v", vel)
	}
	if score != 23.1 {
		t.Fatalf("hn score = %v", score)
	}
	// Two reports three days apart are not a burst.
	_, parts = Rank([]Signal{{Outlet: "A", Published: at(0)}, {Outlet: "B", Published: at(72)}}, nil)
	if reason(parts, "velocity") != nil {
		t.Fatalf("a slow story got velocity: %+v", parts)
	}
	// Relevance is capped at 30 however many topics match.
	_, parts = Rank([]Signal{{Outlet: "A", TopicIDs: []string{"a", "b", "c"}}}, map[string]string{"a": "A", "b": "B", "c": "C"})
	if rel := reason(parts, "relevance"); rel == nil || rel.Points != 30 || len(rel.Detail) != 3 {
		t.Fatalf("relevance cap: %+v", rel)
	}
}

func m(outlet, title, summary string) member {
	return member{outlet: outlet, title: title, summary: summary, tokens: Tokens(title)}
}

func TestExtractiveSummaryStandsOnItsOwn(t *testing.T) {
	// Real items from 2026-09-23. The BBC's excerpt covers the story's shared
	// words (Ratcliffe, production, gas, prices); the Guardian's is about Hull.
	members := []member{
		m("The Guardian: Business", "Jim Ratcliffe halts production at Hull chemical plants over ‘ridiculous’ gas prices",
			"Ineos boss says the UK’s energy costs make the Saltend site uncompetitive."),
		m("BBC News: Business", "Sir Jim Ratcliffe suspends production at key UK plants blaming high gas prices",
			"The billionaire's Ineos says high gas prices have made production at its UK chemical plants unviable. Hundreds of jobs could be affected. More follows."),
	}
	best := representative(members)
	text, source := ExtractiveSummary(members, best)
	if source != "extractive" || !strings.HasPrefix(text, "The billionaire's Ineos says high gas prices") {
		t.Fatalf("summary = %q (%s)", text, source)
	}
	if len([]rune(text)) > 320 {
		t.Fatalf("summary too long: %d", len([]rune(text)))
	}

	// No usable excerpt (HN's RSS mirror writes only a Comments link): the
	// other outlet's headline tells the story a second way.
	members = []member{
		m("Hacker News", "OpenAI GPT–6 Astra breaks Enigma message that has resisted solution since 2005", "Comments"),
		m("DEV Community", "GPT-6 Sol and Luna", ""),
	}
	text, source = ExtractiveSummary(members, representative(members))
	if source != "extractive:headlines" || text == "" {
		t.Fatalf("headline fallback = %q (%s)", text, source)
	}

	// A lone headline with nothing else is its own summary: empty, not invented.
	text, source = ExtractiveSummary([]member{m("Daily Coffee News", "A roaster opens in Cairo", "")}, 0)
	if text != "" || source != "extractive" {
		t.Fatalf("lone = %q (%s)", text, source)
	}
}

func TestHackerNewsAndDevAPIs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v0/topstories.json":
			_, _ = w.Write([]byte(`[101, 102, 103]`))
		case r.URL.Path == "/v0/item/101.json":
			_ = json.NewEncoder(w).Encode(HNItem{ID: 101, Type: "story", Title: "Show HN: A coffee roast logger", URL: "https://example.test/roast", Score: 312, Descendants: 40, Time: t0.Unix(), By: "a"})
		case r.URL.Path == "/v0/item/102.json":
			_ = json.NewEncoder(w).Encode(HNItem{ID: 102, Type: "story", Title: "Dead story", Dead: true})
		case r.URL.Path == "/v0/item/103.json":
			_ = json.NewEncoder(w).Encode(HNItem{ID: 103, Type: "story", Title: "Ask HN: Espresso tips?", Score: 12, Time: t0.Unix()})
		case r.URL.Path == "/api/articles":
			if r.URL.Query().Get("top") != "7" {
				http.Error(w, "want top=7", 400)
				return
			}
			fmt.Fprint(w, `[{"id":9,"title":"Brewing better APIs","description":"A long read on pagination.","url":"https://dev.to/x/brewing","published_at":"2026-09-22T10:00:00Z","public_reactions_count":140,"comments_count":8,"user":{"name":"Dev"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldHN, oldDev := HNBase, DevtoBase
	HNBase, DevtoBase = srv.URL+"/v0", srv.URL+"/api"
	defer func() { HNBase, DevtoBase = oldHN, oldDev }()

	items, err := CollectHN(context.Background(), map[string]string{"list": "topstories"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("hn items = %+v (the dead story must be dropped)", items)
	}
	if items[0].Rank != 1 || items[0].Points != 312 || items[0].Comments != 40 || items[0].ExternalID != "hn:101" {
		t.Fatalf("hn #1 = %+v", items[0])
	}
	// A story with no link is its discussion page.
	if items[1].Rank != 3 || items[1].URL != "https://news.ycombinator.com/item?id=103" {
		t.Fatalf("hn #3 = %+v", items[1])
	}
	if _, err := CollectHN(context.Background(), map[string]string{"list": "../secrets"}); err == nil {
		t.Fatal("an unknown list must be refused")
	}

	dev, err := CollectDevto(context.Background(), map[string]string{"top": "7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dev) != 1 || dev[0].Points != 140 || dev[0].Summary != "A long read on pagination." || dev[0].PublishedAt.IsZero() {
		t.Fatalf("dev = %+v", dev)
	}
}

func TestSplitOutlet(t *testing.T) {
	cases := map[string][2]string{
		"Apple unveils a thing - The Verge": {"Apple unveils a thing", "The Verge"},
		"No publisher here":                 {"No publisher here", ""},
		"A - B - Reuters":                   {"A - B", "Reuters"},
		"تراجع الدولار يضغط على الذهب - اليوم السابع": {"تراجع الدولار يضغط على الذهب", "اليوم السابع"},
	}
	for in, want := range cases {
		if title, outlet := splitOutlet(in); title != want[0] || outlet != want[1] {
			t.Errorf("splitOutlet(%q) = %q, %q", in, title, outlet)
		}
	}
}
