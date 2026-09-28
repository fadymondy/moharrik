package inbox

import (
	"context"
	"testing"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MH-657: the row context menu — pin, archive, mute, favourite are per member;
// colour, icon, labels and block are the team's; clear and delete.

func ptr[T any](v T) *T { return &v }

func (f mbFixture) as(role string) context.Context {
	return tenant.With(context.Background(), &tenant.Scope{WorkspaceID: f.wsID, UserID: f.userID, Role: role, Via: "system"})
}

func (f mbFixture) messageCount(t *testing.T, convID string) int {
	t.Helper()
	var n int
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, f.wsID, convID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ids(cs []ConversationOut) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		out[c.ID] = true
	}
	return out
}

func TestOrganisePinArchiveFavouriteMute(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	c1 := f.conversation(t, "chat", "open", false)
	c2 := f.conversation(t, "chat", "open", false)

	got, err := Organise(f.ctx, a, c1, OrganisePatch{Pinned: ptr(true), Favourite: ptr(true), Mute: ptr("8h")})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned || !got.Favourite || got.MutedUntil == nil {
		t.Fatalf("pinned %v favourite %v muted %v", got.Pinned, got.Favourite, got.MutedUntil)
	}

	list, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !ids(list.Pinned)[c1] || ids(list.Conversations)[c1] {
		t.Errorf("a pinned chat must be in pinned, not the main list")
	}
	fav, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50, Favourites: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(fav.Pinned) + len(fav.Conversations); n != 1 {
		t.Errorf("favourites lists %d chats, want 1", n)
	}

	// Archiving unpins and hides; the archived view shows it and counts it.
	if _, err := Organise(f.ctx, a, c1, OrganisePatch{Archived: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	list, _ = ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if ids(list.Pinned)[c1] || ids(list.Conversations)[c1] || !ids(list.Conversations)[c2] {
		t.Errorf("an archived chat is still listed")
	}
	if list.ArchivedCount != 1 {
		t.Errorf("archived_count %d, want 1", list.ArchivedCount)
	}
	arch, _ := ListConversations(f.ctx, a, ConversationFilter{Limit: 50, Archived: true})
	if !ids(arch.Conversations)[c1] || ids(arch.Conversations)[c2] {
		t.Errorf("the archived view is wrong")
	}

	// A muted chat stays archived when the customer writes; its alert skips the member.
	if muted := inboundPrefs(f.ctx, a.SQLDB, f.wsID, c1); len(muted) != 1 || muted[0] != f.userID {
		t.Errorf("muted members %v", muted)
	}
	ev := realtime.Event{Kind: "message", ConversationID: c1, Permission: authz.InboxRead, Alerting: true}
	viewer := realtime.Viewer{UserID: f.userID, Role: "owner"}
	if got, _ := ev.For(viewer); !got.Alert {
		t.Fatalf("an unmuted member was not alerted")
	}
	ev.Muted = []string{f.userID}
	if got, _ := ev.For(viewer); got.Alert {
		t.Errorf("a muted member was alerted")
	}
	if _, err := Organise(f.ctx, a, c1, OrganisePatch{Mute: ptr("")}); err != nil {
		t.Fatal(err)
	}
	if muted := inboundPrefs(f.ctx, a.SQLDB, f.wsID, c1); len(muted) != 0 {
		t.Errorf("still muted after unmute: %v", muted)
	}
	list, _ = ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if !ids(list.Conversations)[c1] {
		t.Errorf("an inbound message did not unarchive an unmuted chat")
	}

	// Pinning is capped at five.
	for i := 0; i < maxPinned; i++ {
		c := f.conversation(t, "chat", "open", false)
		if _, err := Organise(f.ctx, a, c, OrganisePatch{Pinned: ptr(true)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Organise(f.ctx, a, c2, OrganisePatch{Pinned: ptr(true)}); err == nil {
		t.Errorf("a sixth pin was accepted")
	}
}

func TestOrganiseSharedFieldsNeedTheRightRole(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	c := f.conversation(t, "chat", "open", false)

	if _, err := Organise(f.as("viewer"), a, c, OrganisePatch{Color: ptr("teal")}); err == nil {
		t.Errorf("a viewer set a chat's colour")
	}
	if _, err := Organise(f.as("viewer"), a, c, OrganisePatch{Favourite: ptr(true)}); err == nil {
		t.Errorf("a viewer, who cannot read the inbox, favourited a chat")
	}
	if _, err := Organise(f.ctx, a, c, OrganisePatch{Color: ptr("fuchsia")}); err == nil {
		t.Errorf("an unknown colour was accepted")
	}
	got, err := Organise(f.ctx, a, c, OrganisePatch{Color: ptr("teal"), Icon: ptr("star")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Color != "teal" || got.Icon != "star" {
		t.Errorf("colour %q icon %q", got.Color, got.Icon)
	}
}

func TestConversationLabels(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	c1 := f.conversation(t, "chat", "open", false)
	c2 := f.conversation(t, "chat", "open", false)

	vip, err := CreateLabel(f.ctx, a, LabelInput{Name: ptr("VIP"), Color: ptr("violet")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLabel(f.ctx, a, LabelInput{Name: ptr("vip")}); err == nil {
		t.Errorf("a duplicate label name (case-insensitive) was accepted")
	}
	got, err := Organise(f.ctx, a, c1, OrganisePatch{LabelIDs: &[]string{vip.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Labels) != 1 || got.Labels[0].Name != "VIP" || got.Labels[0].Color != "violet" {
		t.Errorf("labels %+v", got.Labels)
	}
	if _, err := Organise(f.ctx, a, c2, OrganisePatch{LabelIDs: &[]string{"no-such-label"}}); err == nil {
		t.Errorf("an unknown label id was accepted")
	}
	byLabel, _ := ListConversations(f.ctx, a, ConversationFilter{Limit: 50, LabelID: vip.ID})
	if !ids(byLabel.Conversations)[c1] || ids(byLabel.Conversations)[c2] {
		t.Errorf("the label filter is wrong")
	}
	ls, _ := ListLabels(f.ctx, a)
	if len(ls) != 1 || ls[0].Count != 1 {
		t.Errorf("labels %+v", ls)
	}
	if _, err := UpdateLabel(f.ctx, a, vip.ID, LabelInput{Color: ptr("pink")}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLabel(f.as("member"), a, vip.ID); err == nil {
		t.Errorf("a member deleted a label (inbox:manage)")
	}
	if err := DeleteLabel(f.ctx, a, vip.ID); err != nil {
		t.Fatal(err)
	}
	c, err := readConversation(f.ctx, a.SQLDB, f.wsID, c1)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Labels) != 0 {
		t.Errorf("a deleted label is still on the chat: %+v", c.Labels)
	}
}

func TestBlockDropsInboundAndRefusesReplies(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	var calls []bool
	old := blockOnWhatsApp
	blockOnWhatsApp = func(_ context.Context, _ *app.App, _, _ string, block bool) error {
		calls = append(calls, block)
		return nil
	}
	t.Cleanup(func() { blockOnWhatsApp = old })

	phone := "20155501" + numericIDSuffix()[:4]
	var c string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, customer_name, subject, last_message_at)
		VALUES ($1, 'whatsapp', $2, 'Blocked One', 'hi', now()) RETURNING id`, f.wsID, phone).Scan(&c); err != nil {
		t.Fatal(err)
	}
	if err := FileWhatsApp(f.ctx, a, f.wsID, phone, "Blocked One", "first", "WA"+idSuffix()); err != nil {
		t.Fatal(err)
	}
	before := f.messageCount(t, c)
	if before != 1 {
		t.Fatalf("the first message was not filed on the existing chat (%d)", before)
	}

	if _, err := Organise(f.as("member"), a, c, OrganisePatch{Blocked: ptr(true)}); err == nil {
		t.Errorf("a member blocked a customer (inbox:manage)")
	}
	got, err := Organise(f.ctx, a, c, OrganisePatch{Blocked: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Blocked || len(calls) != 1 || !calls[0] {
		t.Fatalf("blocked %v, WhatsApp calls %v", got.Blocked, calls)
	}
	if err := FileWhatsApp(f.ctx, a, f.wsID, phone, "Blocked One", "second", "WA"+idSuffix()); err != nil {
		t.Fatal(err)
	}
	if n := f.messageCount(t, c); n != before {
		t.Errorf("a blocked customer's message was filed (%d → %d)", before, n)
	}
	if _, err := Reply(f.ctx, a, c, "hello"); err == nil {
		t.Errorf("a reply to a blocked customer was accepted")
	}

	if _, err := Organise(f.ctx, a, c, OrganisePatch{Blocked: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] {
		t.Errorf("unblocking did not reach WhatsApp: %v", calls)
	}
	// Setting the same state again does not call WhatsApp again.
	if _, err := Organise(f.ctx, a, c, OrganisePatch{Blocked: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Errorf("an unchanged block state called WhatsApp: %v", calls)
	}
}

func TestClearAndDeleteConversation(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	c1 := f.conversation(t, "chat", "open", true)
	c2 := f.conversation(t, "chat", "open", false)

	if err := ClearConversation(f.as("member"), a, c1); err == nil {
		t.Errorf("a member cleared a chat (inbox:manage)")
	}
	if err := ClearConversation(f.ctx, a, c1); err != nil {
		t.Fatal(err)
	}
	if n := f.messageCount(t, c1); n != 0 {
		t.Errorf("%d messages left after clear", n)
	}
	list, _ := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if !ids(list.Conversations)[c1] {
		t.Errorf("a cleared chat dropped out of the list")
	}

	if err := DeleteConversation(f.ctx, a, c2); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND id = $2`, f.wsID, c2).Scan(&n)
	if n != 0 || f.messageCount(t, c2) != 0 {
		t.Errorf("the deleted chat or its messages remain")
	}
}
