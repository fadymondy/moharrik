package automation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Push to inbox (MH-564): a workflow step that takes what an earlier extract
step read and files it into a workflow channel — the mechanism a browser
workflow uses to bring LinkedIn/Facebook/Instagram/X messages into the shared
inbox, since those networks have no usable API to poll instead.

This runs against a REAL Hub and a real paired browser over a real socket
(runtime_db_test.go's own harness), with a fake filer capturing what the
inbox would have received — never a real network, per the batch rules; the
fake browser answers "extract" with rows shaped like a read preset's output.

What it pins down:
  - push_inbox never reaches the browser at all (dispatch runs it itself);
  - its rows are correctly mapped from the extract step's fields into the
    inbox's identity (thread, message, handle, text);
  - a second run of the very same steps — the same thread/message ids —
    pushes nothing twice.
*/

type filedPush struct {
	workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string
}

func captureAutomationFiler(t *testing.T) *[]filedPush {
	t.Helper()
	got := []filedPush{}
	channels.SetFiler(func(_ context.Context, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string) error {
		got = append(got, filedPush{workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID})
		return nil
	})
	t.Cleanup(func() { channels.SetFiler(nil) })
	return &got
}

func TestPushInboxStepFilesRowsAndNeverReachesTheBrowser(t *testing.T) {
	answer := func(env automation.Envelope) *automation.Envelope {
		var body struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(env.Payload, &body)
		if body.Command == "extract" {
			result, _ := json.Marshal(map[string]any{"rows": []map[string]any{
				{"thread_id": "t1", "message_id": "m1", "profile_url": "https://linkedin.com/in/dana", "name": "Dana", "text": "are you free for a call?"},
				{"thread_id": "t1", "message_id": "m2", "profile_url": "https://linkedin.com/in/dana", "name": "Dana", "text": "let me know"},
			}})
			return &automation.Envelope{Payload: result}
		}
		return nil
	}
	a := testApp(t)
	hub, wsID, agentID, rb, _ := connectRuntime(t, a, "{}", nil, answer)
	got := captureAutomationFiler(t)

	var channelID string
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), wsID), `
		INSERT INTO workflow_channels (workspace_id, name, network, enabled, created_by)
		VALUES ($1, 'LinkedIn inbox', 'linkedin', true, 'test') RETURNING id`, wsID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}

	steps := []automation.Step{
		{Command: "extract", Args: map[string]any{"selector": "li.message"}},
		{Command: "push_inbox", Args: map[string]any{"workflow_channel_id": channelID}},
	}

	run := func() string {
		runID, err := hub.Start(context.Background(), a, wsID, "u1", "manual", agentID, steps)
		if err != nil {
			t.Fatal(err)
		}
		eventually(t, "the run to finish", func() bool {
			var status string
			_ = a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), wsID), `
				SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, runID).Scan(&status)
			return status == "ok" || status == "failed"
		})
		return runID
	}

	runID := run()
	var status, errText string
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), wsID), `
		SELECT status, error FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, runID).Scan(&status, &errText); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("run status = %q, error = %q", status, errText)
	}

	if len(*got) != 2 {
		t.Fatalf("got %d filed pushes, want 2: %+v", len(*got), *got)
	}
	c := (*got)[0]
	if c.workspaceID != wsID || c.channel != "linkedin" || c.sourceChannelID != channelID ||
		c.threadID != "t1" || c.handle != "https://linkedin.com/in/dana" || c.name != "Dana" ||
		c.text != "are you free for a call?" || c.externalID != "linkedin:m1" {
		t.Fatalf("got %+v", c)
	}

	// push_inbox is dispatched by the server itself: the fake browser must
	// have been asked for "extract" exactly once, and never for push_inbox.
	if n := len(rb.seen("run.step")); n != 1 {
		t.Fatalf("the browser was sent %d run.step frames, want exactly 1 (extract only)", n)
	}

	// A re-run of the very same workflow — the same thread/message ids —
	// pushes nothing twice.
	run()
	if len(*got) != 2 {
		t.Fatalf("a re-run filed more pushes: got %d, want still 2: %+v", len(*got), *got)
	}
	var pushedCount int
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), wsID), `
		SELECT count(*) FROM workflow_channel_pushes WHERE workspace_id = $1 AND workflow_channel_id = $2`,
		wsID, channelID).Scan(&pushedCount); err != nil {
		t.Fatal(err)
	}
	if pushedCount != 2 {
		t.Fatalf("workflow_channel_pushes has %d rows, want 2 (deduped, not doubled)", pushedCount)
	}
}
