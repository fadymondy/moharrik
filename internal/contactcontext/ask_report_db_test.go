package contactcontext

import (
	"context"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/ai"
)

/*
MH-586: ask_about_contact and generate_contact_report, both with no AI
provider configured (the deterministic/raw-passages path every requirement
insists must work end to end) and with internal/ai's Fake provider standing
in for a real model — this package NEVER calls a real LLM in tests.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/contactcontext/ -run 'DB' -v
*/

func seedContext(t *testing.T, f ccFixture) string {
	t.Helper()
	res, err := Commit(f.ctx, f.a, f.wsID, f.contactID, f.userID, "android_export.txt", androidFixtureBytes(t), "Sara Ali")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := AddNote(f.ctx, f.a, f.wsID, f.contactID, "note", "Renewal", "Sara asked about renewing her plan next month.", "", f.userID); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	return res.ConversationID
}

func TestDBRetrieveFindsMessagesAndNotes(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	seedContext(t, f)

	passages, err := Retrieve(f.ctx, a, f.wsID, f.contactID, "renewing")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	foundNote := false
	for _, p := range passages {
		if p.Kind == "note" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("expected the renewal note among the results, got %+v", passages)
	}

	passages, err = Retrieve(f.ctx, a, f.wsID, f.contactID, "plan")
	if err != nil {
		t.Fatalf("Retrieve(plan): %v", err)
	}
	if len(passages) == 0 {
		t.Fatalf("expected at least one passage for 'plan' (the multi-line message mentions 'full plan')")
	}
}

func TestDBAskAboutContactNoProvider(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	seedContext(t, f)

	ans, err := AskAboutContact(f.ctx, a, nil, f.wsID, f.contactID, "What did Sara ask about?")
	if err != nil {
		t.Fatalf("AskAboutContact: %v", err)
	}
	if !ans.NoProvider {
		t.Fatalf("expected NoProvider=true when no ai.Client is passed")
	}
	if !strings.Contains(ans.Text, "No AI provider is configured") {
		t.Fatalf("fallback text does not say so plainly: %q", ans.Text)
	}
	if len(ans.Citations) == 0 {
		t.Fatalf("expected raw passages as citations even with no provider")
	}
}

func TestDBAskAboutContactWithFakeProvider(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	seedContext(t, f)

	t.Setenv("AI_PROVIDERS", "fakemodel")
	t.Setenv("AI_PROVIDER_FAKEMODEL_KIND", "fake")
	t.Setenv("AI_TASK_DEFAULT", "fakemodel")
	cfg, err := ai.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := ai.New(cfg, a.Log)
	if err != nil {
		t.Fatal(err)
	}
	fake := ai.NewFake("fakemodel")
	fake.Replies = []string{"Sara asked about renewing her plan [1]."}
	client = client.WithProviders(fake)

	ans, err := AskAboutContact(f.ctx, a, client, f.wsID, f.contactID, "What did Sara ask about?")
	if err != nil {
		t.Fatalf("AskAboutContact: %v", err)
	}
	if ans.NoProvider {
		t.Fatalf("expected NoProvider=false with a working fake provider")
	}
	if ans.Provider != "fakemodel" {
		t.Fatalf("provenance provider = %q, want fakemodel", ans.Provider)
	}
	if !strings.Contains(ans.Text, "renewing") {
		t.Fatalf("expected the fake's scripted reply verbatim, got %q", ans.Text)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("expected exactly one call to the fake provider, got %d", fake.CallCount())
	}
}

func TestDBGenerateReportNoProvider(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	seedContext(t, f)

	rep, err := GenerateReport(f.ctx, a, nil, f.wsID, f.contactID, f.userID)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if !rep.NoProvider {
		t.Fatalf("expected NoProvider=true with no ai.Client")
	}
	if rep.Version != 1 {
		t.Fatalf("first report version = %d, want 1", rep.Version)
	}
	for _, want := range []string{"# Relationship report", "## Last touch", "## Timeline (cited)"} {
		if !strings.Contains(rep.Markdown, want) {
			t.Fatalf("deterministic report missing %q:\n%s", want, rep.Markdown)
		}
	}

	// Regenerating adds a new version rather than overwriting.
	rep2, err := GenerateReport(f.ctx, a, nil, f.wsID, f.contactID, f.userID)
	if err != nil {
		t.Fatalf("GenerateReport (2nd): %v", err)
	}
	if rep2.Version != 2 {
		t.Fatalf("second report version = %d, want 2", rep2.Version)
	}
	list, err := ListReports(context.Background(), a, f.wsID, f.contactID)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 stored report versions, got %d", len(list))
	}
}

func TestDBGenerateReportWithFakeProvider(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	seedContext(t, f)

	t.Setenv("AI_PROVIDERS", "fakemodel")
	t.Setenv("AI_PROVIDER_FAKEMODEL_KIND", "fake")
	t.Setenv("AI_TASK_DEFAULT", "fakemodel")
	cfg, err := ai.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := ai.New(cfg, a.Log)
	if err != nil {
		t.Fatal(err)
	}
	fake := ai.NewFake("fakemodel")
	fake.Replies = []string{"## Relationship summary\n\nSara is an active contact [1]."}
	client = client.WithProviders(fake)

	rep, err := GenerateReport(f.ctx, a, client, f.wsID, f.contactID, f.userID)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if rep.NoProvider {
		t.Fatalf("expected NoProvider=false with a working fake provider")
	}
	if rep.Provider != "fakemodel" {
		t.Fatalf("provenance provider = %q, want fakemodel", rep.Provider)
	}
	if !strings.Contains(rep.Markdown, "Sara is an active contact") {
		t.Fatalf("expected the fake's narrative in the saved markdown, got:\n%s", rep.Markdown)
	}
	// The deterministic, cited timeline section is still appended even with a
	// model narrative, so every fact stays traceable regardless of the model.
	if !strings.Contains(rep.Markdown, "## Timeline (cited)") {
		t.Fatalf("expected the cited timeline section appended after the model narrative")
	}
}
