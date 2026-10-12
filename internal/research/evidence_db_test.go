package research

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The evidence store against a real database (MH-388): two runs on related
questions searched across by find_evidence, a company found in research
suggested and reviewed, schedules held to the budget, and a model's claims
kept only when their quotes are on the page.
*/

// web is a small fake internet: a search engine that answers from a table of
// query words, and a reader that serves the pages.
type web struct {
	pages   map[string]string // url -> markdown
	titles  map[string]string
	results map[string][]string // a word in the query -> urls
	queries int
}

func (w *web) engine(t *testing.T) *Engine {
	t.Helper()
	search := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.queries++
		q := strings.ToLower(r.URL.Query().Get("q"))
		type result struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		var out struct {
			Results []result `json:"results"`
		}
		seen := map[string]bool{}
		for word, urls := range w.results {
			if !strings.Contains(q, word) {
				continue
			}
			for _, u := range urls {
				if !seen[u] {
					seen[u] = true
					out.Results = append(out.Results, result{URL: u, Title: w.titles[u], Content: firstLineOf(w.pages[u])})
				}
			}
		}
		_ = json.NewEncoder(rw).Encode(out)
	}))
	t.Cleanup(search.Close)
	reader := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		md, ok := w.pages[in.URL]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"success": true, "data": map[string]string{"markdown": md}})
	}))
	t.Cleanup(reader.Close)
	return &Engine{
		Search: &Searcher{BaseURL: search.URL, Client: search.Client()},
		Read:   &Reader{BaseURL: reader.URL, Client: reader.Client()},
		Log:    slog.New(slog.NewTextHandler(testLogOut, nil)),
	}
}

var testLogOut io.Writer = io.Discard

// firstLineOf is a page's first body line: what a search engine shows as its snippet.
func firstLineOf(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "Published") {
			return l
		}
	}
	return ""
}

func fintechWeb() *web {
	return &web{
		pages: map[string]string{
			"https://news.test/nile-pay": `# Nile Pay raises $12 million for fintech expansion in Egypt

Published March 3, 2026

Nile Pay has raised $12 million in a Series A round to grow its fintech payments business in Egypt.
The fintech company is hiring 40 engineers in Cairo to build its merchant platform.`,
			"https://careers.nilepay.test/jobs": `# Careers | Nile Pay

Nile Pay is hiring backend engineers in Cairo to scale fintech payments across Egypt.
Our engineers work on the payments platform used by thousands of merchants in Egypt.`,
			"https://news.test/paymob": `# Paymob Ltd partners with a bank

Paymob Ltd signed a partnership with a regional bank to offer fintech merchant payments in Egypt.
Fintech funding in Egypt reached a record this quarter, with Paymob Ltd among the leaders.`,
		},
		titles: map[string]string{
			"https://news.test/nile-pay":        "Nile Pay raises $12 million | News",
			"https://careers.nilepay.test/jobs": "Careers | Nile Pay",
			"https://news.test/paymob":          "Paymob Ltd partners with a bank | News",
		},
		results: map[string][]string{
			"hiring":  {"https://careers.nilepay.test/jobs", "https://news.test/nile-pay"},
			"funding": {"https://news.test/paymob", "https://news.test/nile-pay"},
		},
	}
}

func testUser(t *testing.T, a *app.App, ws string) string {
	t.Helper()
	id := "u-research-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ctx := context.Background()
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text)`, id, id+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func memberCtx(ws, user, role string) context.Context {
	return tenant.With(context.Background(), &tenant.Scope{WorkspaceID: ws, UserID: user, Role: role, Via: "session"})
}

func TestTwoRelatedRunsAreSearchedTogether(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	w := fintechWeb()
	e := w.engine(t)
	ctx := context.Background()

	run1, err := e.Start(ctx, a, ws, "u", "fintech companies hiring engineers in Egypt")
	if err != nil {
		t.Fatal(err)
	}
	r1 := waitSettled(t, a, ws, run1)
	run2, err := e.Start(ctx, a, ws, "u", "fintech funding in Egypt")
	if err != nil {
		t.Fatal(err)
	}
	r2 := waitSettled(t, a, ws, run2)

	for _, r := range []*ResearchRun{r1, r2} {
		if r.Status != "ok" || r.EvidenceCount == 0 {
			t.Fatalf("run %q: status %s, %d evidence", r.Topic, r.Status, r.EvidenceCount)
		}
		if len(r.Stages) != 5 {
			t.Fatalf("stages: %+v", r.Stages)
		}
		for _, st := range r.Stages {
			if st.Stage == "summarise" {
				if st.Status != "skipped" || !strings.Contains(st.Note, "no model") {
					t.Errorf("summarise without a model: %+v", st)
				}
			} else if st.Status != "done" {
				t.Errorf("stage %s: %+v", st.Stage, st)
			}
		}
		if r.Cost.SearchQueries == 0 || r.Cost.PagesRead == 0 || r.Cost.ModelCalls != 0 {
			t.Errorf("cost: %+v", r.Cost)
		}
	}

	wctx := memberCtx(ws, "u", "owner")
	// One search, both runs.
	page, err := FindEvidence(wctx, a.SQLDB, ws, EvidenceQuery{Q: "fintech", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]bool{}
	for _, e := range page.Evidence {
		runs[e.RunID] = true
		if e.Extractor != "heuristic" || e.Kind != "quote" {
			t.Errorf("no model is configured, yet: %+v", e)
		}
		if !strings.Contains(w.pages[e.SourceURL], e.Quote) {
			t.Errorf("quote not on its source %s: %q", e.SourceURL, e.Quote)
		}
	}
	if !runs[run1] || !runs[run2] {
		t.Fatalf("find_evidence did not search across both runs: %v", runs)
	}
	// By company, across runs: Nile Pay is in both.
	np, err := FindEvidence(wctx, a.SQLDB, ws, EvidenceQuery{Company: "Nile Pay", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	npRuns := map[string]bool{}
	for _, e := range np.Evidence {
		npRuns[e.RunID] = true
	}
	if len(npRuns) != 2 {
		t.Errorf("Nile Pay's evidence spans %d runs, want 2: %+v", len(npRuns), np.Evidence)
	}
	// By signal.
	fund, _ := FindEvidence(wctx, a.SQLDB, ws, EvidenceQuery{SignalType: "funding", Limit: 100})
	if fund.Total == 0 {
		t.Error("no funding evidence")
	}
	for _, e := range fund.Evidence {
		if e.SignalType != "funding" {
			t.Errorf("signal filter leaked %q", e.SignalType)
		}
	}

	// The companies became suggestions, not CRM companies.
	list, total, err := ListSuggestions(wctx, a.SQLDB, ws, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]CompanySuggestion{}
	for _, s := range list {
		names[s.Name] = s
	}
	if _, ok := names["Nile Pay"]; !ok || total < 2 {
		t.Fatalf("suggestions: %+v", list)
	}
	if _, ok := names["Paymob Ltd"]; !ok {
		t.Errorf("Paymob Ltd not suggested: %+v", list)
	}
	var crm int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM companies WHERE workspace_id = $1`, ws).Scan(&crm)
	if crm != 0 {
		t.Fatalf("research created %d CRM companies on its own", crm)
	}
	// And the owner was told.
	var items int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND kind = 'research'`, ws).Scan(&items)
	if items == 0 {
		t.Error("strong findings did not reach the notification center")
	}
}

func TestSuggestionAcceptAndDismiss(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	user := testUser(t, a, ws)
	e := fintechWeb().engine(t)
	run, err := e.Start(context.Background(), a, ws, user, "fintech funding in Egypt")
	if err != nil {
		t.Fatal(err)
	}
	waitSettled(t, a, ws, run)
	wctx := memberCtx(ws, user, "owner")
	list, _, err := ListSuggestions(wctx, a.SQLDB, ws, "suggested", 50)
	if err != nil || len(list) < 2 {
		t.Fatalf("suggestions: %v %+v", err, list)
	}
	var paymob, nile *CompanySuggestion
	for i := range list {
		switch list[i].Name {
		case "Paymob Ltd":
			paymob = &list[i]
		case "Nile Pay":
			nile = &list[i]
		}
	}
	if paymob == nil || nile == nil {
		t.Fatalf("want Paymob Ltd and Nile Pay: %+v", list)
	}

	// A client role cannot accept.
	if _, err := Accept(memberCtx(ws, user, "client"), a.SQLDB, ws, user, paymob.ID, SuggestionAcceptInput{}); err == nil {
		t.Fatal("a client accepted a suggestion")
	}
	res, err := Accept(wctx, a.SQLDB, ws, user, paymob.ID, SuggestionAcceptInput{Domain: "paymob.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.CompanyCreated || res.Suggestion.Status != "accepted" || res.Suggestion.CRMCompanyID != res.CompanyID {
		t.Fatalf("accept: %+v", res)
	}
	var name, domain string
	_ = a.SysDB.QueryRow(`SELECT name, domain FROM companies WHERE id = $1`, res.CompanyID).Scan(&name, &domain)
	if name != "Paymob Ltd" || domain != "paymob.test" {
		t.Errorf("CRM company: %q %q", name, domain)
	}
	var note string
	_ = a.SysDB.QueryRow(`SELECT body FROM activities WHERE company_id = $1`, res.CompanyID).Scan(&note)
	if !strings.Contains(note, "https://news.test/paymob") {
		t.Errorf("the evidence note does not link its source: %q", note)
	}
	if _, err := Accept(wctx, a.SQLDB, ws, user, paymob.ID, SuggestionAcceptInput{}); err != ErrAlreadyReview {
		t.Errorf("accepting twice: %v", err)
	}

	// Dismissed stays dismissed when the next run finds it again.
	if _, err := Dismiss(wctx, a.SQLDB, ws, user, nile.ID); err != nil {
		t.Fatal(err)
	}
	run2, _ := e.Start(context.Background(), a, ws, user, "fintech companies hiring engineers in Egypt")
	waitSettled(t, a, ws, run2)
	again, err := GetSuggestion(wctx, a.SQLDB, ws, nile.ID, 1)
	if err != nil || again.Status != "dismissed" {
		t.Fatalf("a dismissed company came back as %v %+v", err, again)
	}
	if _, err := Restore(wctx, a.SQLDB, ws, user, nile.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulesSpendTheBudgetAndStopAtIt(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	e := fintechWeb().engine(t)
	wctx := memberCtx(ws, "u", "owner")
	if _, err := SetBudget(wctx, a.SQLDB, ws, 1); err != nil {
		t.Fatal(err)
	}
	topic1, topic2 := "fintech hiring in Egypt", "fintech funding in Egypt"
	s1, err := CreateSchedule(wctx, a.SQLDB, ws, "u", ResearchScheduleInput{Name: &topic1, Topic: &topic1})
	if err != nil {
		t.Fatal(err)
	}
	e.RunDueSchedules(context.Background(), a, 20)
	got, _ := getSchedule(wctx, a.SQLDB, ws, s1.ID)
	if got.LastStatus != "started" || got.LastRunID == "" || !got.NextRunAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("first schedule: %+v", got)
	}
	r := waitSettled(t, a, ws, got.LastRunID)
	if r.Origin != "schedule" {
		t.Errorf("origin %q", r.Origin)
	}

	// A person's run is not counted.
	manual, err := e.Start(context.Background(), a, ws, "u", "a manual question about fintech")
	if err != nil {
		t.Fatal(err)
	}
	waitSettled(t, a, ws, manual)

	s2, err := CreateSchedule(wctx, a.SQLDB, ws, "u", ResearchScheduleInput{Name: &topic2, Topic: &topic2})
	if err != nil {
		t.Fatal(err)
	}
	e.RunDueSchedules(context.Background(), a, 20)
	got2, _ := getSchedule(wctx, a.SQLDB, ws, s2.ID)
	if got2.LastStatus != "over_budget" || got2.LastRunID != "" {
		t.Fatalf("second schedule past the budget: %+v", got2)
	}
	b, _ := GetBudget(wctx, a.SQLDB, ws)
	if b.RunsToday != 1 || b.DailyRunBudget != 1 {
		t.Fatalf("budget: %+v", b)
	}
	var runs int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM research_runs WHERE workspace_id = $1 AND origin = 'schedule'`, ws).Scan(&runs)
	if runs != 1 {
		t.Fatalf("%d scheduled runs, budget 1", runs)
	}

	// Busy does not spend: raise the budget, hold a run open, and tick.
	if _, err := SetBudget(wctx, a.SQLDB, ws, 5); err != nil {
		t.Fatal(err)
	}
	_, _ = a.SysDB.Exec(`UPDATE research_schedules SET next_run_at = now() WHERE id = $1`, s2.ID)
	_, _ = a.SysDB.Exec(`INSERT INTO research_runs (workspace_id, topic, status) VALUES ($1, 'held', 'running')`, ws)
	e.RunDueSchedules(context.Background(), a, 20)
	got2, _ = getSchedule(wctx, a.SQLDB, ws, s2.ID)
	b, _ = GetBudget(wctx, a.SQLDB, ws)
	if got2.LastStatus != "busy" || b.RunsToday != 1 {
		t.Fatalf("busy schedule: %+v, budget %+v", got2, b)
	}

	// With nothing here to search, the question goes to the workspace's AI
	// assistant as a task — said, queued once, and not spent.
	_, _ = a.SysDB.Exec(`UPDATE research_runs SET status = 'ok' WHERE workspace_id = $1 AND topic = 'held'`, ws)
	for i := 0; i < 2; i++ {
		_, _ = a.SysDB.Exec(`UPDATE research_schedules SET next_run_at = now() WHERE id = $1`, s2.ID)
		(&Engine{Log: e.Log}).RunDueSchedules(context.Background(), a, 20)
	}
	got2, _ = getSchedule(wctx, a.SQLDB, ws, s2.ID)
	b, _ = GetBudget(wctx, a.SQLDB, ws)
	var tasks int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM agent_tasks WHERE workspace_id = $1 AND schedule_id = $2 AND kind = 'research'`, ws, s2.ID).Scan(&tasks)
	if got2.LastStatus != "busy" || !strings.Contains(got2.LastNote, "AI assistant") || tasks != 1 || b.RunsToday != 1 {
		t.Fatalf("schedule with no search: %+v, %d tasks, budget %+v", got2, tasks, b)
	}
}

func TestURLModeReadsTheListWithoutSearching(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	w := fintechWeb()
	e := w.engine(t)
	run, err := e.StartRun(context.Background(), a, ws, "u", Request{Topic: "fintech in Egypt", Mode: "urls",
		URLs: []string{"https://news.test/paymob", "https://news.test/paymob", "https://news.test/missing"}})
	if err != nil {
		t.Fatal(err)
	}
	r := waitSettled(t, a, ws, run)
	if w.queries != 0 {
		t.Errorf("URL mode searched %d times", w.queries)
	}
	if r.Mode != "urls" || r.FoundCount != 2 || r.ReadCount != 1 || r.Stages[1].Stage != "urls" {
		t.Fatalf("URL run: %+v", r)
	}
	// And it needs the reader.
	if _, err := (&Engine{Search: e.Search, Log: e.Log}).StartRun(context.Background(), a, ws, "u",
		Request{Topic: "x y z", Mode: "urls", URLs: []string{"https://news.test/paymob"}}); err != ErrNoReader {
		t.Errorf("URL mode with no reader: %v", err)
	}
	if _, err := e.StartRun(context.Background(), a, ws, "u", Request{Topic: "x y z", Mode: "urls", URLs: []string{"ftp://x"}}); err == nil {
		t.Error("a non-web address was accepted")
	}
}

func TestModelClaimsMustQuoteThePage(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	w := fintechWeb()
	e := w.engine(t)

	t.Setenv("AI_PROVIDERS", "fakemodel")
	t.Setenv("AI_PROVIDER_FAKEMODEL_KIND", "fake")
	t.Setenv("AI_TASK_DEFAULT", "fakemodel")
	cfg, err := ai.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := ai.New(cfg, e.Log)
	if err != nil {
		t.Fatal(err)
	}
	fake := ai.NewFake("fakemodel")
	fake.GenerateModelName = "fake-1"
	reply, _ := json.Marshal(map[string]any{"findings": []map[string]any{
		{"source": 1, "quote": "Paymob Ltd signed a partnership with a regional bank to offer fintech merchant payments in Egypt.",
			"claim": "Paymob partnered with a bank.", "company": "Paymob", "signal_type": "partnership"},
		{"source": 1, "quote": "Paymob Ltd raised $500 million from investors last week.",
			"claim": "Paymob raised $500M.", "company": "Paymob", "signal_type": "funding"},
	}})
	// The extract reply, then the summary.
	fake.Replies = []string{string(reply), "Paymob partnered with a bank [1]."}
	e.AI = client.WithProviders(fake)

	run, err := e.StartRun(context.Background(), a, ws, "u", Request{Topic: "fintech in Egypt", Mode: "urls", URLs: []string{"https://news.test/paymob"}})
	if err != nil {
		t.Fatal(err)
	}
	r := waitSettled(t, a, ws, run)
	page, _ := FindEvidence(memberCtx(ws, "u", "owner"), a.SQLDB, ws, EvidenceQuery{RunID: run, Limit: 50})
	if page.Total != 1 {
		t.Fatalf("want only the claim whose quote is on the page, got %+v", page.Evidence)
	}
	got := page.Evidence[0]
	if got.Kind != "claim" || got.Extractor != "model:fakemodel" || got.SignalType != "partnership" || strings.Contains(got.Quote, "500") {
		t.Fatalf("model evidence: %+v", got)
	}
	if r.Cost.ModelCalls != 2 || r.Summary == "" {
		t.Errorf("cost %+v, summary %q", r.Cost, r.Summary)
	}
}
