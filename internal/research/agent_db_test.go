package research

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
