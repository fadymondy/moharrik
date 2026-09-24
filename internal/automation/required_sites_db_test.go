package automation_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
MH-517 against a real database and a real socket: a workflow's first run adds
the sites it needs to the browser, within the guardrails.
*/

func TestRequiredHostsAreDeclaredAndWrittenOut(t *testing.T) {
	steps := []automation.Step{
		{Command: "goto", Args: map[string]any{"url": "https://news.google.com/search?q={{q}}"}},
		{Command: "goto", Args: map[string]any{"url": "{{url}}"}},
		{Command: "goto", Args: map[string]any{"url": "https://{{tenant}}.example.com/x"}},
		{Command: "click", Args: map[string]any{"selector": "a"}},
	}
	got := automation.RequiredHosts([]string{"Trends.Google.com", "{{x}}"}, steps)
	if strings.Join(got, ",") != "news.google.com,trends.google.com" {
		t.Fatalf("required = %v", got)
	}
}

func TestFirstRunAddsTheWorkflowsSites(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, _ := connectBrowser(t, a, "{}", func(int, map[string]any) automation.Envelope {
		return automation.Envelope{Payload: []byte(`{"ok":true}`)}
	})
	ctx := context.Background()
	sctx := tenant.WithWorkspace(ctx, wsID)
	admin := member(t, a, wsID, "admin")
	manager := member(t, a, wsID, "manager")
	hosts := func() string {
		var h pgtext.Array
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT allowed_hosts FROM browser_agents WHERE workspace_id = $1 AND id = $2`, wsID, agentID).Scan(&h)
		return strings.Join(h, ",")
	}
	setHosts := func(v string) {
		_, _ = a.SQLDB.ExecContext(sctx, `UPDATE browser_agents SET allowed_hosts = $3::text[] WHERE workspace_id = $1 AND id = $2`, wsID, agentID, v)
	}

	wfID, err := automation.CreateWorkflow(ctx, a, wsID, admin, automation.WorkflowInput{
		Name: "News", Steps: []automation.Step{{Command: "goto", Args: map[string]any{"url": "https://news.google.com/"}}, {Command: "extract", Args: map[string]any{"selector": "h1"}}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_workflows SET hosts = '{trends.google.com}' WHERE id = $1`, wfID)

	// A manager may run it, but not widen a browser's sites to do so: told
	// which sites and who can add them, and nothing is added.
	_, err = automation.RunWorkflowFrom(ctx, a, hub, wsID, manager, wfID, agentID, "manual", "", nil)
	if !errors.Is(err, automation.ErrSitesNeedAdmin) || !strings.Contains(err.Error(), "news.google.com, trends.google.com") || hosts() != "" {
		t.Fatalf("manager: %v (browser %q)", err, hosts())
	}

	// An admin's run adds them, and says so as the run's first line.
	runID, err := automation.RunWorkflowFrom(ctx, a, hub, wsID, admin, wfID, agentID, "manual", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hosts() != "news.google.com,trends.google.com" {
		t.Fatalf("browser after the admin's run: %q", hosts())
	}
	waitForRun(t, a, wsID, runID)
	var notes pgtext.Array
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT notes FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, runID).Scan(&notes)
	if len(notes) != 1 || notes[0] != "Added news.google.com, trends.google.com to guardrail browser's allowed sites" {
		t.Fatalf("notes = %v", notes)
	}
	// Nothing missing: nothing added, no note.
	runID, err = automation.RunWorkflowFrom(ctx, a, hub, wsID, manager, wfID, agentID, "manual", "", nil)
	if err != nil {
		t.Fatalf("a manager's run once the sites are there: %v", err)
	}
	waitForRun(t, a, wsID, runID)
	notes = nil
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT notes FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, runID).Scan(&notes)
	if len(notes) != 0 {
		t.Fatalf("a note with nothing added: %v", notes)
	}

	// A host only a value makes, on a run nobody watches: not added unless the
	// workspace allows sites from values.
	anyID, err := automation.CreateWorkflow(ctx, a, wsID, admin, automation.WorkflowInput{
		Name: "Any page", Steps: []automation.Step{{Command: "goto", Args: map[string]any{"url": "{{url}}"}}},
		Params: []automation.Param{{Name: "url", Required: true}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{"url": "https://input.test/page"}
	if _, err := automation.RunWorkflowFrom(ctx, a, hub, wsID, admin, anyID, agentID, "schedule", "", vals); !errors.Is(err, automation.ErrHostNotAllowed) || strings.Contains(hosts(), "input.test") {
		t.Fatalf("schedule, a value's host: %v (browser %q)", err, hosts())
	}
	// Watched (a person pressed Run): added.
	runID, err = automation.RunWorkflowFrom(ctx, a, hub, wsID, admin, anyID, agentID, "manual", "", vals)
	if err != nil || !strings.Contains(hosts(), "input.test") {
		t.Fatalf("manual, a value's host: %v (browser %q)", err, hosts())
	}
	waitForRun(t, a, wsID, runID)
	setHosts("{news.google.com,trends.google.com}")
	on := true
	if err := automation.SaveSiteSettings(ctx, a, wsID, nil, &on); err != nil {
		t.Fatal(err)
	}
	runID, err = automation.RunWorkflowFrom(ctx, a, hub, wsID, admin, anyID, agentID, "mcp", "", vals)
	if err != nil || !strings.Contains(hosts(), "input.test") {
		t.Fatalf("mcp with sites from values allowed: %v (browser %q)", err, hosts())
	}
	waitForRun(t, a, wsID, runID)

	// The switch off: nothing is added, and the run is refused the usual way.
	setHosts("{}")
	off := false
	if err := automation.SaveSiteSettings(ctx, a, wsID, &off, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.RunWorkflowFrom(ctx, a, hub, wsID, admin, wfID, agentID, "manual", "", nil); !errors.Is(err, automation.ErrHostNotAllowed) || hosts() != "" {
		t.Fatalf("switched off: %v (browser %q)", err, hosts())
	}
	if s := automation.ReadSiteSettings(ctx, a, wsID); s.AutoAdd || !s.FromInputs {
		t.Fatalf("settings = %+v", s)
	}
}
