package opportunities

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The Opportunity Finder with the caller's agent as the provider, against a real
database: candidates are validated (evidence on a public URL is required),
deduped against the CRM, scored, stored with the agent's name on them, their
openers kept as drafts that nothing sends; a token is held to its hourly
limit; and one workspace never sees another's tasks. A summary task's brief
carries the pages the server collected, and submit_research writes the answer.
*/

func agentCaller(sd seeder, token string) (context.Context, *tenant.Scope) {
	s := &tenant.Scope{WorkspaceID: sd.ws, UserID: sd.u, Role: "owner", Via: "mcp", TokenID: token, TokenName: "test agent",
		Scopes: []string{authz.ResearchRun, authz.ResearchRead, authz.ContactsRead}}
	return tenant.With(context.Background(), s), s
}

func quote(url, q string) []CandidateEvidence {
	return []CandidateEvidence{{URL: url, Quote: q, Title: "News", PublishedAt: "2026-09-01"}}
}

func TestAgentSubmitsOpportunities(t *testing.T) {
	a := testApp(t)
	sd := newWorkspace(t, a)
	ctx, s := agentCaller(sd, "tok-opps")
	c := Claimant(s, "claude", "opus")
	sd.exec(`INSERT INTO companies (workspace_id, name, domain) VALUES ($1, 'Known Bank', 'knownbank.com')`)

	task, err := research.EnqueueAgentTask(ctx, a.SQLDB, sd.ws, research.AgentTaskInput{Kind: "discovery", Topic: "fintech hiring in Egypt", RequestedBy: sd.u})
	if err != nil {
		t.Fatal(err)
	}
	briefs, _, err := TakeTasks(ctx, a.SQLDB, s, c, "discovery", 5, false)
	if err != nil || len(briefs) != 1 || briefs[0].Task.ID != task.ID {
		t.Fatalf("take: %v %+v", err, briefs)
	}
	b := briefs[0]
	if b.Submit.Tool != "submit_opportunities" || b.Submit.OutputSchema == nil || len(b.Signals) == 0 {
		t.Fatalf("the brief says how to submit: %+v", b.Submit)
	}
	found := false
	for _, k := range b.Dedupe.CRMCompanies {
		found = found || k.Name == "Known Bank"
	}
	if !found {
		t.Fatalf("the brief lists the CRM's companies: %+v", b.Dedupe.CRMCompanies)
	}

	res, err := SubmitOpportunities(ctx, a.SQLDB, s, c, task.ID, []Candidate{
		{Company: "Nile Pay", Domain: "https://www.nilepay.com/careers", SignalType: "hiring", Confidence: 0.9,
			WhyNow:          "Hiring 40 engineers in Cairo after its Series A.",
			SuggestedOpener: "Saw you are hiring 40 engineers in Cairo — we help fintechs ship faster.", OpenerSubject: "Your Cairo team",
			Person:   &CandidatePerson{Name: "Sara Ali", Role: "CTO", Email: "sara@nilepay.com"},
			Evidence: quote("https://news.example.com/nile-pay", "Nile Pay is hiring 40 engineers in Cairo to build its merchant platform.")},
		{Company: "Nile Pay", Domain: "nilepay.com", SignalType: "funding", Confidence: 0.8,
			Evidence: quote("https://news.example.com/nile-pay-a", "Nile Pay has raised $12 million in a Series A round this year.")},
		{Company: "Known Bank", Domain: "knownbank.com", SignalType: "expansion", Confidence: 0.7,
			Evidence: quote("https://news.example.com/known-bank", "Known Bank opens a new branch in Alexandria next month.")},
		{Company: "No Proof Ltd", SignalType: "launch", Confidence: 0.5},
		{Company: "Intranet Co", SignalType: "launch", Confidence: 0.5,
			Evidence: quote("http://192.168.1.10/news", "Intranet Co launched a product on its own private network.")},
		{Company: "Bad Signal", SignalType: "rumour", Confidence: 0.5,
			Evidence: quote("https://news.example.com/bad", "Bad Signal is said to be thinking about something new soon.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent {
		t.Fatal("nothing is ever sent")
	}
	if len(res.Rejected) != 3 {
		t.Fatalf("three are refused (no evidence, private URL, unknown signal): %+v", res.Rejected)
	}
	byIndex := map[int]CandidateResult{}
	for _, r := range res.Results {
		byIndex[r.Index] = r
	}
	created := byIndex[0]
	if created.Outcome != "created" || byIndex[1].Outcome != "merged" || byIndex[2].Outcome != "in_crm" || byIndex[2].CRMCompanyID == "" {
		t.Fatalf("outcomes: %+v", res.Results)
	}
	if created.ProspectID == "" || created.DraftID == "" || created.Score <= 0 {
		t.Fatalf("the new prospect is scored and its opener drafted: %+v", created)
	}

	var author, model string
	if err := a.SysDB.QueryRow(`SELECT author_kind, model FROM research_runs WHERE workspace_id = $1 AND id = $2`, sd.ws, res.RunID).Scan(&author, &model); err != nil {
		t.Fatal(err)
	}
	if author != "agent" || model != "agent:claude/opus" {
		t.Fatalf("provenance: %s %s", author, model)
	}
	var maxConf float64
	var extractor string
	if err := a.SysDB.QueryRow(`SELECT max(confidence), min(extractor) FROM research_evidence WHERE workspace_id = $1 AND run_id = $2`, sd.ws, res.RunID).Scan(&maxConf, &extractor); err != nil {
		t.Fatal(err)
	}
	if maxConf > research.AgentMaxConfidence || !strings.HasPrefix(extractor, "agent:") {
		t.Fatalf("agent evidence is capped and labelled: %v %s", maxConf, extractor)
	}
	var generator, prospectStatus string
	if err := a.SysDB.QueryRow(`SELECT d.generator, p.status FROM prospect_drafts d JOIN prospects p ON p.workspace_id = d.workspace_id AND p.id = d.prospect_id
		WHERE d.workspace_id = $1 AND d.id = $2`, sd.ws, created.DraftID).Scan(&generator, &prospectStatus); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(generator, "agent:") || prospectStatus != "new" {
		t.Fatalf("the opener is a draft on a new prospect: %s %s", generator, prospectStatus)
	}

	// The same company again only updates it.
	again, err := SubmitOpportunities(ctx, a.SQLDB, s, c, task.ID, []Candidate{{Company: "Nile Pay", Domain: "nilepay.com", SignalType: "launch", Confidence: 0.6,
		Evidence: quote("https://news.example.com/nile-pay-app", "Nile Pay launched a merchant app for small shops in Egypt.")}})
	if err != nil || len(again.Results) != 1 || again.Results[0].Outcome != "updated" || again.Results[0].ProspectID != created.ProspectID {
		t.Fatalf("resubmitted: %v %+v", err, again)
	}

	if _, err := CloseTask(ctx, a, s, c, task.ID, "done", "found 2"); err != nil {
		t.Fatal(err)
	}

	// Another workspace's agent cannot touch this task.
	other := newWorkspace(t, a)
	octx, os := agentCaller(other, "tok-other")
	oc := Claimant(os, "claude", "opus")
	if got, _, err := TakeTasks(octx, a.SQLDB, os, oc, "discovery", 5, false); err != nil || len(got) != 0 {
		t.Fatalf("other workspace took: %v %+v", err, got)
	}
	if _, err := SubmitOpportunities(octx, a.SQLDB, os, oc, task.ID, []Candidate{{Company: "X", SignalType: "launch",
		Evidence: quote("https://news.example.com/x", "X launched a product for merchants in Egypt.")}}); err == nil {
		t.Fatal("another workspace submitted to this task")
	}
}

func TestAgentRateLimit(t *testing.T) {
	a := testApp(t)
	sd := newWorkspace(t, a)
	ctx, s := agentCaller(sd, "tok-busy")
	c := Claimant(s, "codex", "gpt")
	task, err := research.EnqueueAgentTask(ctx, a.SQLDB, sd.ws, research.AgentTaskInput{Kind: "discovery", Topic: "anything", RequestedBy: sd.u})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxSubmitsPerHour; i++ {
		logSubmission(ctx, a.SQLDB, s, c, task.ID, "opportunities", 1, 1, 0, 0)
	}
	cand := []Candidate{{Company: "Late Co", SignalType: "launch", Evidence: quote("https://news.example.com/late", "Late Co launched a product for merchants in Egypt.")}}
	if _, err := SubmitOpportunities(ctx, a.SQLDB, s, c, task.ID, cand); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("the 61st submission in an hour: %v", err)
	}
	// The limit is per token: another token in the workspace may still submit.
	ctx2, s2 := agentCaller(sd, "tok-fresh")
	if _, err := SubmitResearch(ctx2, a.SQLDB, s2, Claimant(s2, "codex", "gpt"), task.ID, "", nil, nil); errors.Is(err, ErrRateLimited) {
		t.Fatal("another token was rate limited")
	}
}

func TestSummaryTaskBriefAndSubmit(t *testing.T) {
	a := testApp(t)
	sd := newWorkspace(t, a)
	ctx, s := agentCaller(sd, "tok-sum")
	c := Claimant(s, "claude", "opus")
	run := sd.id(`INSERT INTO research_runs (workspace_id, topic, status, requested_by) VALUES ($1, 'fintech hiring in Egypt', 'ok', $2) RETURNING id`, sd.u)
	sd.exec(`INSERT INTO research_sources (workspace_id, run_id, position, url, title, content, fetched)
		VALUES ($1, $2, 1, 'https://news.example.com/nile-pay', 'Nile Pay raises', 'Nile Pay is hiring 40 engineers in Cairo.', true)`, run)

	task, err := research.QueueSummary(ctx, a.SQLDB, sd.ws, run, "")
	if err != nil {
		t.Fatal(err)
	}
	briefs, _, err := TakeTasks(ctx, a.SQLDB, s, c, "summary", 5, true)
	if err != nil || len(briefs) != 1 || briefs[0].Task.ID != task.ID {
		t.Fatalf("take summary: %v %+v", err, briefs)
	}
	b := briefs[0]
	if b.Submit.Tool != "submit_research" || len(b.Pages) != 1 || !strings.Contains(b.Pages[0].Content, "40 engineers") || len(b.SearchQueries) != 0 {
		t.Fatalf("summary brief: %+v", b)
	}
	if _, err := SubmitResearch(ctx, a.SQLDB, s, c, task.ID, "", []string{"orphan insight"}, nil); err == nil {
		t.Fatal("insights with no answer were accepted")
	}
	if _, err := SubmitResearch(ctx, a.SQLDB, s, c, task.ID, "Nile Pay is hiring 40 engineers in Cairo [1].",
		[]string{"Hiring is the strongest signal", "<b>Cairo</b> is the hub"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseTask(ctx, a, s, c, task.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	var summary, model string
	if err := a.SysDB.QueryRow(`SELECT summary, model FROM research_runs WHERE workspace_id = $1 AND id = $2`, sd.ws, run).Scan(&summary, &model); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "40 engineers") || !strings.Contains(summary, "Insights:\n- Hiring is the strongest signal") ||
		strings.Contains(summary, "<b>") || model != "agent:claude/opus" {
		t.Fatalf("summary %q model %q", summary, model)
	}
}

func TestAgentPromptNamesTheTools(t *testing.T) {
	p := AgentPrompt("Acme")
	for _, want := range []string{`"Acme"`, "agent_tasks", "submit_research", "opportunity_tasks", "submit_opportunities", "complete_task", "Do not contact anyone"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not mention %q:\n%s", want, p)
		}
	}
}
