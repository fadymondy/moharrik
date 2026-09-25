package imports_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/imports"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// fakeTelegramAPI answers every Bot API call with {"ok":true} — just enough
// for CreateTelegram's setWebhook call to succeed without ever reaching the
// real api.telegram.org (the batch rules).
func fakeTelegramAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

/*
MH-562: Telegram and workflow (LinkedIn/Facebook/Instagram/X) are registered
as import sources so every connection kind shows up in the "Import from a
connection" list, but both are honestly not-ready — Telegram's Bot API has
no chat-history endpoint (and setWebhook disables getUpdates), and workflow
channels are fed only by a browser workflow's push step with no API behind
them at all. These tests assert the honesty: Connections() always reports
Ready=false with a non-empty Note, and Preview/Import fail cleanly (a plain
error, never a panic and never a fake success) rather than silently
returning an empty list that would look like "this chat has no history."
*/

func TestTelegramImportIsHonestlyNotReady(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	fake := fakeTelegramAPI(t)
	defer channels.SetTelegramAPIBase(fake.URL)()

	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})
	if _, err := channels.CreateTelegram(ctx, a, wsID, "u-test", channels.TelegramInput{
		Name: "Support Bot", BotToken: "123:fake",
	}); err != nil {
		t.Fatal(err)
	}

	imports.RegisterProvider(imports.TelegramProvider{})

	sources, err := imports.ListSources(ctx, a, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range sources {
		if s.Source != "telegram" {
			continue
		}
		found = true
		if len(s.Connections) != 1 {
			t.Fatalf("connections: %+v", s.Connections)
		}
		c := s.Connections[0]
		if c.Ready {
			t.Fatalf("a Telegram connection must never report Ready: true — there is no history API behind it: %+v", c)
		}
		if c.Note == "" {
			t.Fatalf("a not-ready Telegram connection must explain why in Note: %+v", c)
		}
	}
	if !found {
		t.Fatal("telegram source missing from ListSources")
	}

	if _, err := imports.Preview(ctx, a, wsID, "telegram", "", imports.Filters{}); err == nil {
		t.Fatal("Preview on Telegram must fail cleanly, not succeed")
	}

	job, err := imports.StartImport(ctx, a, wsID, "telegram", imports.ImportRequest{Items: []string{"whatever"}})
	if err != nil {
		t.Fatal(err) // starting the job itself is fine; the job's own run must fail
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "failed" {
		t.Fatalf("a Telegram import job must fail cleanly (status=failed), got %+v", job)
	}
	if job.LastError == "" {
		t.Fatalf("a failed Telegram import job must record why: %+v", job)
	}
}

func TestWorkflowImportIsHonestlyNotReady(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)

	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})
	if _, err := channels.CreateWorkflowChannel(ctx, a, wsID, "u-test", channels.WorkflowChannelInput{
		Name: "LinkedIn inbox", Network: "linkedin",
	}); err != nil {
		t.Fatal(err)
	}

	imports.RegisterProvider(imports.WorkflowProvider{})

	sources, err := imports.ListSources(ctx, a, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range sources {
		if s.Source != "workflow" {
			continue
		}
		found = true
		if len(s.Connections) != 1 {
			t.Fatalf("connections: %+v", s.Connections)
		}
		c := s.Connections[0]
		if c.Ready {
			t.Fatalf("a workflow connection must never report Ready: true — it has no history API behind it: %+v", c)
		}
		if c.Note == "" {
			t.Fatalf("a not-ready workflow connection must explain why in Note: %+v", c)
		}
	}
	if !found {
		t.Fatal("workflow source missing from ListSources")
	}

	if _, err := imports.Preview(ctx, a, wsID, "workflow", "", imports.Filters{}); err == nil {
		t.Fatal("Preview on workflow must fail cleanly, not succeed")
	}

	job, err := imports.StartImport(ctx, a, wsID, "workflow", imports.ImportRequest{Items: []string{"whatever"}})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "failed" {
		t.Fatalf("a workflow import job must fail cleanly (status=failed), got %+v", job)
	}
	if job.LastError == "" {
		t.Fatalf("a failed workflow import job must record why: %+v", job)
	}
}
