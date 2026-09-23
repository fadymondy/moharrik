package research

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPlanQueryFocusesTheQuestion(t *testing.T) {
	p := PlanQuery("What are green coffee import rules and tariffs for Egypt in 2026?")
	if p.Language != "en" {
		t.Errorf("language %q, want en", p.Language)
	}
	if !reflect.DeepEqual(p.Phrases, []string{"green coffee"}) {
		t.Errorf("phrases %q, want [green coffee]", p.Phrases)
	}
	if !reflect.DeepEqual(p.Keywords, []string{"import", "rules", "tariffs", "Egypt", "2026"}) {
		t.Errorf("keywords %q", p.Keywords)
	}
	want := []string{
		`"green coffee" import rules tariffs Egypt 2026`,
		`"green coffee" import tariffs Egypt`,
		`"green coffee" Egypt`,
	}
	if !reflect.DeepEqual(p.Queries, want) {
		t.Errorf("queries\n got %q\nwant %q", p.Queries, want)
	}
	for _, q := range p.Queries {
		for _, w := range []string{"What", "are", "and", "for", " in "} {
			if strings.Contains(q, w) {
				t.Errorf("query %q kept the stop word %q", q, w)
			}
		}
	}
}

func TestPlanQueryKeepsQuotedPhrasesAndNames(t *testing.T) {
	p := PlanQuery(`How do "cold brew" cafés price delivery in New Cairo?`)
	if !reflect.DeepEqual(p.Phrases, []string{"cold brew", "New Cairo"}) {
		t.Errorf("phrases %q, want the quoted phrase and the place name", p.Phrases)
	}
	if p.Queries[0] != `"cold brew" "New Cairo" cafés price delivery` {
		t.Errorf("first query %q", p.Queries[0])
	}
}

func TestPlanQueryArabic(t *testing.T) {
	p := PlanQuery("ما هي شروط استيراد البن الأخضر إلى مصر في عام 2026؟")
	if p.Language != "ar" {
		t.Errorf("language %q, want ar", p.Language)
	}
	if !reflect.DeepEqual(p.Phrases, []string{"البن الأخضر"}) {
		t.Errorf("phrases %q, want [البن الأخضر]", p.Phrases)
	}
	for _, q := range p.Queries {
		for _, stop := range []string{"ما ", "هي ", " في ", " إلى ", "عام"} {
			if strings.Contains(" "+q+" ", stop) {
				t.Errorf("query %q kept %q", q, stop)
			}
		}
	}
	if !strings.Contains(p.Queries[0], "استيراد") || !strings.Contains(p.Queries[0], "مصر") {
		t.Errorf("first query %q lost the subject", p.Queries[0])
	}
}

func TestRelevantDropsResultsAboutOneWordOfTheQuery(t *testing.T) {
	p := PlanQuery("What are green coffee import rules and tariffs for Egypt in 2026?")
	for _, c := range []struct {
		title, snippet string
		want           bool
	}{
		{"Green - Wikipedia", "Green is the color between cyan and yellow on the visible spectrum.", false},
		{"134 Shades of Green: Color Names, Hex, RGB", "Explore shades of green in 2026.", false},
		{"Egypt's Green Coffee Market Report 2026", "Imports of green coffee into Egypt ...", true},
		{"Import Duties Egypt 2026: Rates & Exemptions", "Customs tariffs on imports to Egypt explained.", true},
		{"HS Code 090111 Tariff Classification For Egypt", "Coffee, not roasted — import tariff into Egypt.", true},
	} {
		if got := p.Relevant(c.title, c.snippet); got != c.want {
			t.Errorf("Relevant(%q) = %v, want %v", c.title, got, c.want)
		}
	}
	ar := PlanQuery("ما هي شروط استيراد البن الأخضر إلى مصر؟")
	if !ar.Relevant("استيراد البن الاخضر في مصر", "") {
		t.Error("an Arabic title naming the phrase (spelt without hamza) must be relevant")
	}
	if ar.Relevant("اللون الأخضر", "درجات اللون الأخضر") {
		t.Error("a page about the colour green is not about green coffee")
	}
}

// Every variant is searched with the question's language, and the merged list drops the
// results that only matched one word of it.
func TestSearchPlanMergesVariantsAndFilters(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.Query().Get("language")+" "+r.URL.Query().Get("q"))
		mu.Unlock()
		type res struct{ URL, Title, Content string }
		out := struct {
			Results []res `json:"results"`
		}{[]res{
			{"https://en.wikipedia.org/wiki/Green", "Green - Wikipedia", "Green is a colour."},
			{"https://example.test/egypt-green-coffee", "Egypt green coffee import guide", "Tariffs on green coffee in Egypt."},
		}}
		if strings.Contains(r.URL.Query().Get("q"), "tariffs") {
			out.Results = append(out.Results, res{"https://example.test/duties", "Egypt import tariffs 2026", "Customs duty on coffee imports to Egypt."})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	s := &Searcher{BaseURL: srv.URL, Client: srv.Client()}
	found, err := s.Search(context.Background(), "What are green coffee import rules and tariffs for Egypt in 2026?", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("searched %d variants, want 3: %q", len(got), got)
	}
	for _, q := range got {
		if !strings.HasPrefix(q, "en ") {
			t.Errorf("a variant was searched without the question's language: %q", q)
		}
	}
	var urls []string
	for _, f := range found {
		urls = append(urls, f.URL)
	}
	want := []string{"https://example.test/egypt-green-coffee", "https://example.test/duties"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("found %q, want %q (the colour page dropped, the page in every variant first)", urls, want)
	}
}

// A question the first phrasing answers costs one query: every extra variant is a query to every
// engine, and a metasearch instance that asks too often gets CAPTCHAs.
func TestSearchPlanStopsWhenTheFirstVariantIsEnough(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		type res struct{ URL, Title, Content string }
		var out struct {
			Results []res `json:"results"`
		}
		for i := 0; i < 10; i++ {
			out.Results = append(out.Results, res{"https://example.test/" + string(rune('a'+i)), "Green coffee in Egypt", "import tariffs"})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	s := &Searcher{BaseURL: srv.URL, Client: srv.Client()}
	found, err := s.Search(context.Background(), "What are green coffee import rules and tariffs for Egypt in 2026?", 8)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(found) != 8 {
		t.Errorf("%d queries for %d results, want 1 query for 8", calls, len(found))
	}
}
