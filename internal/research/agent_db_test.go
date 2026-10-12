package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The agent provider against a real database: a workspace with search but no
model key defaults to agent mode, the server still collects the pages, and the
summary becomes a task for the caller's agent. These workspaces are NOT pinned
to server mode (testWorkspace is), so auto resolves with no model.
*/

func autoWorkspace(t *testing.T, a *app.App) string {
	t.Helper()
	var id string
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('Agent Test', $1, 'agent-test') RETURNING id`,
		"agent-test-"+time.Now().Format("150405.000000000")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func stageOf(r *ResearchRun, name string) ResearchStage {
	for _, st := range r.Stages {
		if st.Stage == name {
			return st
		}
	}
	return ResearchStage{}
}

// waitSummaryTask waits for the summary task queued after a run settled.
func waitSummaryTask(t *testing.T, a *app.App, ws, runID string) AgentTask {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		list, err := ListAgentTasks(tenant.WithWorkspace(context.Background(), ws), a.SQLDB, ws, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range list {
			if tk.Kind == "summary" && tk.RunID == runID {
				return tk
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("no summary task was queued for the run")
	return AgentTask{}
}

func TestEffectiveProvider(t *testing.T) {
	full := Capabilities{CanSearch: true, CanRead: true, CanSummarise: true}
	searchOnly := Capabilities{CanSearch: true, CanRead: true}
	none := Capabilities{}
	cases := []struct {
		mode string
		caps Capabilities
		want string
	}{
		{ProviderAuto, full, ProviderServer},
		{ProviderAuto, searchOnly, ProviderAgent}, // no model key: the agent thinks
		{ProviderAuto, none, ProviderAgent},
		{ProviderServer, searchOnly, ProviderServer},
		{ProviderServer, none, ProviderAgent},
		{ProviderAgent, full, ProviderAgent},
		{"", searchOnly, ProviderAgent},
	}
	for _, c := range cases {
		if got := EffectiveProvider(c.mode, c.caps); got != c.want {
			t.Errorf("EffectiveProvider(%q, %+v) = %q, want %q", c.mode, c.caps, got, c.want)
		}
	}
}

func TestNoModelHandsTheSummaryToTheAgent(t *testing.T) {
	a := testApp(t)
	ws := autoWorkspace(t, a)
	user := testUser(t, a, ws)
	e := fintechWeb().engine(t)
	ctx := tenant.WithWorkspace(context.Background(), ws)

	p, err := e.GetProvider(ctx, a.SQLDB, ws)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ProviderAuto || p.Effective != ProviderAgent || !p.CanServer || p.CanSummarise {
		t.Fatalf("a deployment with search and no model key: %+v", p)
	}

	st, err := e.StartOrQueue(context.Background(), a, ws, user, Request{Topic: "fintech companies hiring engineers in Egypt"})
	if err != nil {
		t.Fatal(err)
	}
	if st.RunID == "" || !st.AgentSummary || !strings.Contains(st.Message, "agent_tasks") {
		t.Fatalf("started: %+v", st)
	}
	run := waitSettled(t, a, ws, st.RunID)
	if run.Status != "ok" || run.Summary != "" || run.Error != "" {
		t.Fatalf("the collected run: status %s summary %q error %q", run.Status, run.Summary, run.Error)
	}
	task := waitSummaryTask(t, a, ws, st.RunID)
	if task.Status != "pending" {
		t.Fatalf("summary task: %+v", task)
	}
	run, _, _ = Get(context.Background(), a, ws, st.RunID, false)
	if s := stageOf(run, "summarise"); s.Status != "pending" || !strings.Contains(s.Note, task.ID) {
		t.Fatalf("summarise stage: %+v", s)
	}
	for _, s := range run.Stages {
		if s.Stage != "summarise" && s.Status != "done" {
			t.Errorf("the server still collects: stage %+v", s)
		}
	}

	// Asking again returns the same open task.
	again, err := QueueSummary(ctx, a.SQLDB, ws, st.RunID, "")
	if err != nil || again.ID != task.ID {
		t.Fatalf("QueueSummary again: %v %+v", err, again)
	}

	c := Claimant{TokenID: "tok-1", Agent: "claude", Model: "opus"}
	claimed, err := ClaimAgentTasks(ctx, a.SQLDB, ws, c, "summary", 5)
	if err != nil || len(claimed) != 1 || claimed[0].ID != task.ID {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	pages, quotes, err := SummaryMaterial(ctx, a.SQLDB, ws, st.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 || len(quotes) == 0 || pages[0].URL == "" || pages[0].Content == "" {
		t.Fatalf("the brief carries the pages and quotes: %d pages, %d quotes", len(pages), len(quotes))
	}

	// Done without a summary is refused.
	if _, err := CloseAgentTask(ctx, a, ws, task.ID, c, "done", ""); !errors.Is(err, ErrNoSummary) {
		t.Fatalf("closing a summary task with no summary: %v", err)
	}
	if err := SetAgentRunSummary(ctx, a.SQLDB, ws, st.RunID, "Nile Pay is hiring 40 engineers in Cairo.", c); err != nil {
		t.Fatal(err)
	}
	if _, err := CloseAgentTask(ctx, a, ws, task.ID, c, "done", "summarised"); err != nil {
		t.Fatal(err)
	}
	run, _, _ = Get(context.Background(), a, ws, st.RunID, false)
	if run.Summary == "" || run.Model != "agent:claude/opus" || run.Status != "ok" {
		t.Fatalf("summarised run: status %s model %q summary %q", run.Status, run.Model, run.Summary)
	}
	if s := stageOf(run, "summarise"); s.Status != "done" || !strings.Contains(s.Note, "AI assistant") {
		t.Fatalf("summarise stage after submit: %+v", s)
	}
	// A summarised run cannot be queued again.
	if _, err := QueueSummary(ctx, a.SQLDB, ws, st.RunID, ""); !errors.Is(err, ErrNotSummarisable) {
		t.Fatalf("queue a summarised run: %v", err)
	}

	// Another workspace sees none of it.
	other := autoWorkspace(t, a)
	octx := tenant.WithWorkspace(context.Background(), other)
	if _, err := QueueSummary(octx, a.SQLDB, other, st.RunID, ""); !errors.Is(err, ErrNoRun) {
		t.Fatalf("another workspace queued this run: %v", err)
	}
	if got, err := ClaimAgentTasks(octx, a.SQLDB, other, c, "", 5); err != nil || len(got) != 0 {
		t.Fatalf("another workspace claimed: %v %+v", err, got)
	}
	if _, err := QueueSummary(ctx, a.SQLDB, ws, "00000000-0000-4000-8000-000000000000", ""); !errors.Is(err, ErrNoRun) {
		t.Fatalf("unknown run: %v", err)
	}
}

func TestAFailedOrCancelledSummarySkipsTheStage(t *testing.T) {
	a := testApp(t)
	ws := autoWorkspace(t, a)
	user := testUser(t, a, ws)
	e := fintechWeb().engine(t)
	ctx := tenant.WithWorkspace(context.Background(), ws)
	c := Claimant{TokenID: "tok-2", Agent: "codex", Model: "gpt"}

	for _, how := range []string{"failed", "cancelled"} {
		st, err := e.StartOrQueue(context.Background(), a, ws, user, Request{Topic: "fintech funding in Egypt " + how})
		if err != nil {
			t.Fatal(err)
		}
		waitSettled(t, a, ws, st.RunID)
		task := waitSummaryTask(t, a, ws, st.RunID)
		if how == "failed" {
			if _, err := ClaimAgentTasks(ctx, a.SQLDB, ws, c, "summary", 5); err != nil {
				t.Fatal(err)
			}
			if _, err := CloseAgentTask(ctx, a, ws, task.ID, c, "failed", "no time"); err != nil {
				t.Fatal(err)
			}
		} else if err := CancelAgentTask(ctx, a.SQLDB, ws, task.ID); err != nil {
			t.Fatal(err)
		}
		run, _, _ := Get(context.Background(), a, ws, st.RunID, false)
		if run.Status != "ok" || run.Summary != "" {
			t.Fatalf("%s: the run itself stays ok: %s %q", how, run.Status, run.Summary)
		}
		if s := stageOf(run, "summarise"); s.Status != "skipped" {
			t.Fatalf("%s: summarise stage %+v", how, s)
		}
		// It can be asked for again.
		if _, err := QueueSummary(ctx, a.SQLDB, ws, st.RunID, ""); err != nil {
			t.Fatalf("%s: queue again: %v", how, err)
		}
	}
}

// The guards the PR review asked for: a research task needs a page before it
// is done, a page with no quote is a source and not evidence, a cancelled task
// takes no new run, a lease that ran out too often settles what it left, and
// one open summary task per run even when asked for at once.
func TestAgentTaskGuards(t *testing.T) {
	a := testApp(t)
	ws := autoWorkspace(t, a)
	user := testUser(t, a, ws)
	ctx := tenant.WithWorkspace(context.Background(), ws)
	c := Claimant{TokenID: "tok-guard", Agent: "claude", Model: "opus"}

	task, err := EnqueueAgentTask(ctx, a.SQLDB, ws, AgentTaskInput{Kind: "research", Topic: "fintech hiring in Egypt", RequestedBy: user})
	if err != nil {
		t.Fatal(err)
	}
	held, err := HoldAgentTask(ctx, a.SQLDB, ws, task.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CloseAgentTask(ctx, a, ws, task.ID, c, "done", ""); !errors.Is(err, ErrNoSources) {
		t.Fatalf("a research task done with no page: %v", err)
	}
	runID, err := AgentRun(ctx, a.SQLDB, ws, held, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := StoreAgentFindings(ctx, a.SQLDB, ws, runID, c, []AgentFinding{{URL: "https://news.example.com/nile-pay", Title: "Nile Pay"}}); err != nil {
		t.Fatalf("a page with no quote: %v", err)
	}
	var sources, evidence int
	_ = a.SysDB.QueryRow(`SELECT (SELECT count(*) FROM research_sources WHERE run_id = $1), (SELECT count(*) FROM research_evidence WHERE run_id = $1)`, runID).Scan(&sources, &evidence)
	if sources != 1 || evidence != 0 {
		t.Fatalf("a page with no quote is a source only: %d sources, %d evidence", sources, evidence)
	}
	if _, err := CloseAgentTask(ctx, a, ws, task.ID, c, "done", ""); err != nil {
		t.Fatalf("done with a page: %v", err)
	}

	// Cancelled between holding the task and the first write: no new run.
	task2, _ := EnqueueAgentTask(ctx, a.SQLDB, ws, AgentTaskInput{Kind: "research", Topic: "payments in Cairo", RequestedBy: user})
	held2, err := HoldAgentTask(ctx, a.SQLDB, ws, task2.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelAgentTask(ctx, a.SQLDB, ws, task2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentRun(ctx, a.SQLDB, ws, held2, c); !errors.Is(err, ErrTaskClosed) {
		t.Fatalf("a cancelled task took a run: %v", err)
	}
	var runs int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM research_runs WHERE workspace_id = $1 AND topic = 'payments in Cairo'`, ws).Scan(&runs)
	if runs != 0 {
		t.Fatalf("a run was left behind for the cancelled task: %d", runs)
	}

	// Writers to one run at once each get their own source position.
	task4, _ := EnqueueAgentTask(ctx, a.SQLDB, ws, AgentTaskInput{Kind: "research", Topic: "wallets in Alexandria", RequestedBy: user})
	held4, _ := HoldAgentTask(ctx, a.SQLDB, ws, task4.ID, c)
	run4, err := AgentRun(ctx, a.SQLDB, ws, held4, c)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func(i int) {
			_, _, err := StoreAgentFindings(ctx, a.SQLDB, ws, run4, c, []AgentFinding{{URL: fmt.Sprintf("https://news.example.com/wallet-%d", i), Title: "Wallet"}})
			errs <- err
		}(i)
	}
	for i := 0; i < 6; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent findings for one run: %v", err)
		}
	}
	_ = a.SysDB.QueryRow(`SELECT count(DISTINCT position) FROM research_sources WHERE run_id = $1`, run4).Scan(&sources)
	if sources != 6 {
		t.Fatalf("concurrent findings: %d distinct positions, want 6", sources)
	}
	// Cancelled once it has a run: the next submission writes nothing.
	held4, _ = GetAgentTask(ctx, a.SQLDB, ws, task4.ID)
	if err := CancelAgentTask(ctx, a.SQLDB, ws, task4.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := AgentRun(ctx, a.SQLDB, ws, held4, c); !errors.Is(err, ErrTaskClosed) {
		t.Fatalf("a cancelled task with a run still took writes: %v", err)
	}

	// A summary task whose lease ran out too often: the stage is skipped.
	var run string
	if err := a.SysDB.QueryRow(`INSERT INTO research_runs (workspace_id, topic, status, requested_by) VALUES ($1, 'banks in Egypt', 'ok', $2) RETURNING id`, ws, user).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO research_sources (workspace_id, run_id, position, url, title, content, fetched)
		VALUES ($1, $2, 0, 'https://news.example.com/banks', 'Banks', 'Banks are hiring.', true)`, ws, run); err != nil {
		t.Fatal(err)
	}
	// Asked for at once, one task.
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		go func() {
			st, err := QueueSummary(ctx, a.SQLDB, ws, run, "")
			if err != nil {
				ids <- "err: " + err.Error()
				return
			}
			ids <- st.ID
		}()
	}
	first := <-ids
	for i := 1; i < 8; i++ {
		if got := <-ids; got != first {
			t.Fatalf("two open summary tasks for one run: %s and %s", first, got)
		}
	}
	research3, _ := EnqueueAgentTask(ctx, a.SQLDB, ws, AgentTaskInput{Kind: "research", Topic: "lenders in Giza", RequestedBy: user})
	held3, _ := HoldAgentTask(ctx, a.SQLDB, ws, research3.ID, c)
	run3, err := AgentRun(ctx, a.SQLDB, ws, held3, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`UPDATE agent_tasks SET status = 'claimed', claimed_by = 'tok-gone', attempts = $3, lease_until = now() - interval '1 minute'
		WHERE workspace_id = $1 AND id IN ($2, $4)`, ws, first, AgentMaxAttempts, research3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ListAgentTasks(ctx, a.SQLDB, ws, 50); err != nil {
		t.Fatal(err)
	}
	got, _, _ := Get(context.Background(), a, ws, run, false)
	if s := stageOf(got, "summarise"); s.Status != "skipped" {
		t.Fatalf("an expired summary leaves the stage: %+v", s)
	}
	got3, _, _ := Get(context.Background(), a, ws, run3, false)
	if got3.Status != "failed" || got3.FinishedAt == nil {
		t.Fatalf("an expired research task leaves its run: %s", got3.Status)
	}
}

// Agent mode chosen on a deployment that has a model key: the server still
// collects, but the summary is the assistant's, not the server model's.
func TestExplicitAgentModeHandsOffWithAModel(t *testing.T) {
	a := testApp(t)
	ws := autoWorkspace(t, a)
	user := testUser(t, a, ws)
	e := fintechWeb().engine(t)
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
	fake.Replies = []string{`{"findings": []}`, "the server model's summary"}
	e.AI = client.WithProviders(fake)
	ctx := tenant.WithWorkspace(context.Background(), ws)
	if _, err := e.SetProvider(ctx, a.SQLDB, ws, ProviderAgent); err != nil {
		t.Fatal(err)
	}
	st, err := e.StartOrQueue(context.Background(), a, ws, user, Request{Topic: "fintech funding in Egypt"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.AgentSummary || st.Provider != ProviderAgent {
		t.Fatalf("started: %+v", st)
	}
	waitSettled(t, a, ws, st.RunID)
	waitSummaryTask(t, a, ws, st.RunID)
	run, _, _ := Get(context.Background(), a, ws, st.RunID, false)
	if run.Summary != "" || stageOf(run, "summarise").Status != "pending" {
		t.Fatalf("the server model summarised an agent-mode run: %q %+v", run.Summary, stageOf(run, "summarise"))
	}
}
