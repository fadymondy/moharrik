package inbox

import (
	"context"
	"testing"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
MH-568: the inbox's channel tabs are fed by ConversationList.ChannelCounts, one
row per channel the workspace has a conversation on OR a connection for. These
tests run against a real Postgres database with RLS on, the same fixture shape
mailbox_db_test.go uses.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run ChannelCounts -v

Note: tenant.WithWorkspace (mailbox_db_test.go's fixture helper) sets no Role,
so authz.Can(InboxRead) rejects it. ListConversations needs a real role, so
these tests build their own ctx via tenant.With(&tenant.Scope{..., Role: "owner"}).
*/

func ccWorkspace(t *testing.T, a *app.App) mbFixture {
	t.Helper()
	f := newMailboxWorkspace(t, a)
	f.ctx = tenant.With(context.Background(), &tenant.Scope{
		WorkspaceID: f.wsID, UserID: f.userID, Role: "owner", Via: "system",
	})
	return f
}

func (f mbFixture) conversation(t *testing.T, channel, status string, unread bool) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, status, unread)
		VALUES ($1, $2, 'Test Customer', 'customer@example.test', $3, $4) RETURNING id`,
		f.wsID, channel, status, unread,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// A conversation with no messages is neither listed nor counted (MH-599).
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ($1, $2, 'in', 'hi')`, f.wsID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f mbFixture) chatWidget(t *testing.T, enabled bool) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO chat_widgets (workspace_id, name, public_key, enabled, created_by)
		VALUES ($1, 'Site widget', $2, $3, $4) RETURNING id`,
		f.wsID, "pk-"+idSuffix(), enabled, f.userID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f mbFixture) linkWhatsApp(t *testing.T) {
	t.Helper()
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO whatsapp_links (workspace_id, instance, hook_id, state, created_by)
		VALUES ($1, $2, $3, 'connecting', $4)`,
		f.wsID, "wa-"+idSuffix(), "hook-"+idSuffix(), f.userID,
	); err != nil {
		t.Fatal(err)
	}
}

// TestChannelCountsPerChannelTotals: three channels, several conversations
// each, unread mixed in — channel_counts must report the right total and
// unread for every one of them, and only them (no phantom rows).
func TestChannelCountsPerChannelTotals(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)

	f.conversation(t, "chat", "open", true)
	f.conversation(t, "chat", "open", false)
	f.conversation(t, "email", "open", true)
	f.conversation(t, "whatsapp", "closed", false)

	res, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ChannelCount{}
	for _, c := range res.ChannelCounts {
		got[c.Channel] = c
	}
	if c := got["chat"]; c.Total != 2 || c.Unread != 1 {
		t.Fatalf("chat = %+v, want total=2 unread=1", c)
	}
	if c := got["email"]; c.Total != 1 || c.Unread != 1 {
		t.Fatalf("email = %+v, want total=1 unread=1", c)
	}
	if c := got["whatsapp"]; c.Total != 1 || c.Unread != 0 {
		t.Fatalf("whatsapp = %+v, want total=1 unread=0", c)
	}
}

// TestChannelCountsRespectsStatusFilter: the same status filter that narrows
// the conversation list must narrow channel_counts too, so switching status
// tabs shows counts consistent with what's actually listed.
func TestChannelCountsRespectsStatusFilter(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)

	f.conversation(t, "chat", "open", true)
	f.conversation(t, "chat", "closed", false)
	f.conversation(t, "email", "closed", true)

	res, err := ListConversations(f.ctx, a, ConversationFilter{Status: "closed", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ChannelCount{}
	for _, c := range res.ChannelCounts {
		got[c.Channel] = c
	}
	if c, ok := got["chat"]; !ok || c.Total != 1 || c.Unread != 0 {
		t.Fatalf("chat under status=closed = %+v (ok=%v), want total=1 unread=0", c, ok)
	}
	if c, ok := got["email"]; !ok || c.Total != 1 || c.Unread != 1 {
		t.Fatalf("email under status=closed = %+v (ok=%v), want total=1 unread=1", c, ok)
	}
}

// TestChannelCountsIncludesConnectionsWithoutConversations: an enabled chat
// widget and a linked WhatsApp number with zero conversations still get a row
// (total=0), so the frontend can render their tab before the first message
// ever arrives. A disabled widget does not.
func TestChannelCountsIncludesConnectionsWithoutConversations(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)

	f.chatWidget(t, true)
	f.linkWhatsApp(t)
	// One email conversation only, to prove email's presence is independent.
	f.conversation(t, "email", "open", false)

	res, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ChannelCount{}
	for _, c := range res.ChannelCounts {
		got[c.Channel] = c
	}
	if c, ok := got["chat"]; !ok || c.Total != 0 {
		t.Fatalf("chat (connected, no conversations) = %+v (ok=%v), want total=0 present", c, ok)
	}
	if c, ok := got["whatsapp"]; !ok || c.Total != 0 {
		t.Fatalf("whatsapp (linked, no conversations) = %+v (ok=%v), want total=0 present", c, ok)
	}
	if c, ok := got["email"]; !ok || c.Total != 1 {
		t.Fatalf("email = %+v (ok=%v), want total=1", c, ok)
	}
}

// TestChannelCountsNoPhantomForUnconnectedChannel: a workspace with neither a
// connection nor a conversation on a channel gets no row for it at all — the
// frontend must never render an empty tab for a channel this workspace has
// nothing to do with.
func TestChannelCountsNoPhantomForUnconnectedChannel(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	f.conversation(t, "chat", "open", false)

	res, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.ChannelCounts {
		if c.Channel == "whatsapp" || c.Channel == "email" {
			t.Fatalf("unconnected channel %q got a row: %+v", c.Channel, c)
		}
	}
}

// TestChannelCountsWorkspaceIsolation: RLS — one workspace's counts never
// leak another's conversations or connections, even when both use the same
// channel.
func TestChannelCountsWorkspaceIsolation(t *testing.T) {
	a := testApp(t)
	f1 := ccWorkspace(t, a)
	f2 := ccWorkspace(t, a)

	f1.conversation(t, "chat", "open", true)
	f1.conversation(t, "chat", "open", true)
	f2.conversation(t, "chat", "open", false)
	f2.linkWhatsApp(t)

	res1, err := ListConversations(f1.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got1 := map[string]ChannelCount{}
	for _, c := range res1.ChannelCounts {
		got1[c.Channel] = c
	}
	if c := got1["chat"]; c.Total != 2 || c.Unread != 2 {
		t.Fatalf("workspace 1 chat = %+v, want total=2 unread=2 (leak from workspace 2?)", c)
	}
	if _, ok := got1["whatsapp"]; ok {
		t.Fatalf("workspace 1 sees workspace 2's whatsapp link: %+v", got1["whatsapp"])
	}

	res2, err := ListConversations(f2.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got2 := map[string]ChannelCount{}
	for _, c := range res2.ChannelCounts {
		got2[c.Channel] = c
	}
	if c := got2["chat"]; c.Total != 1 || c.Unread != 0 {
		t.Fatalf("workspace 2 chat = %+v, want total=1 unread=0 (leak from workspace 1?)", c)
	}
	if _, ok := got2["whatsapp"]; !ok {
		t.Fatal("workspace 2 should see its own whatsapp link")
	}
}
