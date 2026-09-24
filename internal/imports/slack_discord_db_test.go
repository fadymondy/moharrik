package imports_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/imports"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
MH-562: the Slack and Discord import sources against fake platform servers
(httptest — never the real Slack/Discord APIs, per the batch rules; no bot
token, no message is ever sent). Covers the shape that matters for these two
providers specifically: a thread imported here uses exactly the same key a
live delivery would (internal/channels/slack_routes.go's RegisterSlackHook
and internal/channels/discord_gateway.go's handleDiscordMessage), and a
message dedupes on external_id the same way, so importing history and a live
delivery on the same thread never produce two conversations or a duplicate
row.
*/

// fakeSlackAPI serves conversations.list (one page, one public channel) and
// conversations.history (two messages, one authored by the bot itself).
func fakeSlackAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/conversations.list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channels":[
			{"id":"C1","name":"support","is_im":false,"is_private":false,"num_members":3}
		],"response_metadata":{"next_cursor":""}}`))
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[
			{"type":"message","user":"U-CUST","text":"need help","ts":"1700000001.000200"},
			{"type":"message","user":"U-BOT","text":"on it","ts":"1700000002.000300"}
		]}`))
	})
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","team_id":"T1","user_id":"U-BOT"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeDiscordAPI serves /users/@me/guilds, /guilds/{id}/channels and
// /channels/{id}/messages (one customer message, one bot-authored message
// Discord's own client already filters out).
func fakeDiscordAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/@me/guilds", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"G1"}]`))
	})
	mux.HandleFunc("/guilds/G1/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"D1","name":"support","type":0},{"id":"D2","name":"voice","type":2}]`))
	})
	mux.HandleFunc("/channels/D1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"999","content":"need help","timestamp":"2024-01-01T00:00:01Z","author":{"id":"U-CUST","bot":false,"username":"customer"}},
			{"id":"998","content":"on it","timestamp":"2024-01-01T00:00:02Z","author":{"id":"U-BOT","bot":true,"username":"supportbot"}}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSlackImportKeysAndDedupesLikeALiveDelivery(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	fake := fakeSlackAPI(t)
	defer channels.SetSlackAPIBase(fake.URL)()

	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})
	conn, err := channels.CreateSlack(ctx, a, wsID, "u-test", channels.SlackInput{
		Name: "Support", BotToken: "xoxb-fake", SigningSecret: "shh",
	})
	if err != nil {
		t.Fatal(err)
	}

	imports.RegisterProvider(imports.SlackProvider{})

	sources, err := imports.ListSources(ctx, a, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var slackReady bool
	for _, s := range sources {
		if s.Source == "slack" {
			slackReady = len(s.Connections) == 1 && s.Connections[0].Ready
		}
	}
	if !slackReady {
		t.Fatalf("slack source not ready: %+v", sources)
	}

	items, err := imports.Preview(ctx, a, wsID, "slack", conn.ID, imports.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "C1" || items[0].Kind != "group" || items[0].Name != "support" {
		t.Fatalf("preview: %+v", items)
	}

	job, err := imports.StartImport(ctx, a, wsID, "slack", imports.ImportRequest{ConnectionID: conn.ID, Items: []string{"C1"}})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "done" {
		t.Fatalf("job failed: %+v", job)
	}
	if job.ConversationsCreated != 1 {
		t.Fatalf("conversations_created: got %d, want 1", job.ConversationsCreated)
	}
	// Both messages come back from the fake, but the bot's own is filtered:
	// only the customer's message is filed.
	if job.MessagesImported != 1 {
		t.Fatalf("messages_imported: got %d, want 1 (the bot's own message must not be filed)", job.MessagesImported)
	}

	// A live delivery on the exact same channel, keyed exactly the way
	// RegisterSlackHook's deliver() call keys it, must land in the SAME
	// conversation the import created, not a second one.
	if err := inbox.FileChannelMessage(ctx, a, wsID, "slack", conn.ID, "C1", "U-CUST", "", "one more thing", "slack:1700000003.000400"); err != nil {
		t.Fatal(err)
	}
	var convCount int
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'slack'`, wsID).Scan(&convCount); err != nil {
		t.Fatal(err)
	}
	if convCount != 1 {
		t.Fatalf("import and a live delivery on the same channel produced %d conversations, want 1", convCount)
	}

	// Re-importing the same history must dedupe on external_id: no new
	// messages, no new conversation.
	job2, err := imports.StartImport(ctx, a, wsID, "slack", imports.ImportRequest{ConnectionID: conn.ID, Items: []string{"C1"}})
	if err != nil {
		t.Fatal(err)
	}
	job2 = waitJob(t, ctx, a, wsID, job2.ID)
	if job2.Status != "done" || job2.ConversationsCreated != 0 || job2.MessagesImported != 0 {
		t.Fatalf("re-import was not a no-op: %+v", job2)
	}
	if job2.ConversationsReused != 1 {
		t.Fatalf("re-import did not report the reuse: %+v", job2)
	}
	var msgCount int
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT count(*) FROM messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE c.workspace_id = $1 AND c.channel = 'slack'`, wsID).Scan(&msgCount); err != nil {
		t.Fatal(err)
	}
	if msgCount != 2 { // the imported customer message + the live one filed above
		t.Fatalf("message count after re-import: got %d, want 2", msgCount)
	}
}

func TestDiscordImportKeysAndDedupesLikeALiveDelivery(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	fake := fakeDiscordAPI(t)
	defer channels.SetDiscordAPIBase(fake.URL)()
	t.Setenv("DISCORD_GATEWAY", "off")

	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})
	conn, err := channels.CreateDiscord(ctx, a, wsID, "u-test", channels.DiscordInput{
		Name: "Support", BotToken: "T.fake",
	})
	if err != nil {
		t.Fatal(err)
	}

	imports.RegisterProvider(imports.DiscordProvider{})

	items, err := imports.Preview(ctx, a, wsID, "discord", conn.ID, imports.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	// Only the GUILD_TEXT channel (type 0) shows up; the voice channel
	// (type 2) is filtered out.
	if len(items) != 1 || items[0].ID != "D1" || items[0].Name != "support" {
		t.Fatalf("preview: %+v", items)
	}

	job, err := imports.StartImport(ctx, a, wsID, "discord", imports.ImportRequest{ConnectionID: conn.ID, Items: []string{"D1"}})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "done" {
		t.Fatalf("job failed: %+v", job)
	}
	if job.ConversationsCreated != 1 {
		t.Fatalf("conversations_created: got %d, want 1", job.ConversationsCreated)
	}
	// DiscordChannelHistory itself already drops bot-authored messages.
	if job.MessagesImported != 1 {
		t.Fatalf("messages_imported: got %d, want 1", job.MessagesImported)
	}

	// A live delivery keyed exactly the way handleDiscordMessage's deliver()
	// call keys it must land in the SAME conversation.
	if err := inbox.FileChannelMessage(ctx, a, wsID, "discord", conn.ID, "D1", "U-CUST", "customer", "one more thing", "discord:997"); err != nil {
		t.Fatal(err)
	}
	var convCount int
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'discord'`, wsID).Scan(&convCount); err != nil {
		t.Fatal(err)
	}
	if convCount != 1 {
		t.Fatalf("import and a live delivery on the same channel produced %d conversations, want 1", convCount)
	}

	job2, err := imports.StartImport(ctx, a, wsID, "discord", imports.ImportRequest{ConnectionID: conn.ID, Items: []string{"D1"}})
	if err != nil {
		t.Fatal(err)
	}
	job2 = waitJob(t, ctx, a, wsID, job2.ID)
	if job2.Status != "done" || job2.ConversationsCreated != 0 || job2.MessagesImported != 0 || job2.ConversationsReused != 1 {
		t.Fatalf("re-import was not a no-op: %+v", job2)
	}
}
