package inbox

import (
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
MH-600: "group by sender" on the inbox's email tab. These tests run against a
real Postgres database with RLS on, the same fixture shape channel_counts_test.go
and mailbox_db_test.go use.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run Sender -v
*/

// sgConversation inserts one email conversation with a given sender, unread
// flag and last_message_at (so ordering is deterministic instead of racing
// now()), plus the one message MH-599 requires for it to be listed at all.
func (f mbFixture) sgConversation(t *testing.T, email, name, subject string, unread bool, at time.Time) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, subject, status, unread, last_message_at)
		VALUES ($1, 'email', $2, $3, $4, 'open', $5, $6) RETURNING id`,
		f.wsID, name, email, subject, unread, at,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ($1, $2, 'in', $3)`,
		f.wsID, id, subject+" body"); err != nil {
		t.Fatal(err)
	}
	return id
}

// sgConversationOnMailbox is sgConversation plus a mailbox_id, for the
// mailbox_id filter test.
func (f mbFixture) sgConversationOnMailbox(t *testing.T, email, subject string, at time.Time, mailboxID string) string {
	t.Helper()
	id := f.sgConversation(t, email, "Test Customer", subject, false, at)
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `UPDATE conversations SET mailbox_id = $1 WHERE workspace_id = $2 AND id = $3`,
		mailboxID, f.wsID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustGroups(t *testing.T, a *app.App, f mbFixture, filt SenderGroupFilter) *SenderGroupList {
	t.Helper()
	res, err := ListConversationsBySender(f.ctx, a, filt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestSenderGroupsRollUpBySenderCaseInsensitive: three conversations from the
// same address (two different cases) become one group with the right count
// and unread count; a fourth from someone else becomes its own group.
func TestSenderGroupsRollUpBySenderCaseInsensitive(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	base := time.Now().UTC().Add(-time.Hour)

	f.sgConversation(t, "Jane@Example.test", "Jane", "First", true, base)
	f.sgConversation(t, "jane@example.test", "Jane", "Second", false, base.Add(time.Minute))
	f.sgConversation(t, "jane@example.test", "Jane", "Third — latest", true, base.Add(2*time.Minute))
	f.sgConversation(t, "bob@example.test", "Bob", "Bob's only one", false, base.Add(90*time.Second))

	res := mustGroups(t, a, f, SenderGroupFilter{Limit: 50})
	if len(res.Groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(res.Groups), res.Groups)
	}
	byEmail := map[string]SenderGroup{}
	for _, g := range res.Groups {
		byEmail[g.SenderEmail] = g
	}
	jane, ok := byEmail["jane@example.test"]
	if !ok {
		t.Fatalf("no jane@example.test group in %+v", res.Groups)
	}
	if jane.ConversationCount != 3 {
		t.Fatalf("jane conversation_count = %d, want 3", jane.ConversationCount)
	}
	if jane.UnreadCount != 2 {
		t.Fatalf("jane unread_count = %d, want 2", jane.UnreadCount)
	}
	if jane.LatestSubject != "Third — latest" {
		t.Fatalf("jane latest_subject = %q, want %q", jane.LatestSubject, "Third — latest")
	}
	bob, ok := byEmail["bob@example.test"]
	if !ok || bob.ConversationCount != 1 || bob.UnreadCount != 0 {
		t.Fatalf("bob group = %+v (ok=%v), want count=1 unread=0", bob, ok)
	}
}

// TestSenderGroupsSortUnreadFirstThenNewest: a group with any unread
// conversation sorts before an all-read group, however old its last message;
// within each bucket, newest activity first.
func TestSenderGroupsSortUnreadFirstThenNewest(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	base := time.Now().UTC().Add(-time.Hour)

	// old-but-unread should still beat new-but-read.
	f.sgConversation(t, "old-unread@example.test", "Old", "Old unread", true, base)
	f.sgConversation(t, "new-read@example.test", "New", "New read", false, base.Add(time.Hour))
	f.sgConversation(t, "newer-unread@example.test", "Newer", "Newer unread", true, base.Add(30*time.Minute))

	res := mustGroups(t, a, f, SenderGroupFilter{Limit: 50})
	if len(res.Groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(res.Groups))
	}
	var order []string
	for _, g := range res.Groups {
		order = append(order, g.SenderEmail)
	}
	want := []string{"newer-unread@example.test", "old-unread@example.test", "new-read@example.test"}
	for i, e := range want {
		if order[i] != e {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestSenderGroupsPaging walks every group exactly once across several small
// pages, the same keyset contract ListConversations' own tests rely on.
func TestSenderGroupsPaging(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	base := time.Now().UTC().Add(-time.Hour)

	senders := []string{"a@example.test", "b@example.test", "c@example.test", "d@example.test", "e@example.test"}
	for i, s := range senders {
		f.sgConversation(t, s, s, "Subject "+s, false, base.Add(time.Duration(i)*time.Minute))
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		res := mustGroups(t, a, f, SenderGroupFilter{Limit: 2, Cursor: cursor})
		pages++
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		for _, g := range res.Groups {
			if seen[g.SenderEmail] {
				t.Fatalf("sender %s returned twice", g.SenderEmail)
			}
			seen[g.SenderEmail] = true
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(seen) != len(senders) {
		t.Fatalf("saw %d distinct senders across pages, want %d: %v", len(seen), len(senders), seen)
	}
}

// TestSenderGroupsWorkspaceIsolation: one workspace's groups never leak into
// another's, even for the exact same sender address.
func TestSenderGroupsWorkspaceIsolation(t *testing.T) {
	a := testApp(t)
	f1 := ccWorkspace(t, a)
	f2 := ccWorkspace(t, a)
	now := time.Now().UTC()

	f1.sgConversation(t, "shared@example.test", "Shared", "From workspace 1", false, now)
	f2.sgConversation(t, "shared@example.test", "Shared", "From workspace 2", false, now)

	res1 := mustGroups(t, a, f1, SenderGroupFilter{Limit: 50})
	if len(res1.Groups) != 1 || res1.Groups[0].ConversationCount != 1 {
		t.Fatalf("workspace 1 groups = %+v, want exactly one group with count 1", res1.Groups)
	}
	res2 := mustGroups(t, a, f2, SenderGroupFilter{Limit: 50})
	if len(res2.Groups) != 1 || res2.Groups[0].ConversationCount != 1 {
		t.Fatalf("workspace 2 groups = %+v, want exactly one group with count 1", res2.Groups)
	}
}

// TestSenderGroupsExcludesConversationsWithoutMessages mirrors MH-599: a
// conversation with zero messages must not create (or pad) a group.
func TestSenderGroupsExcludesConversationsWithoutMessages(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)

	var emptyID string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, status, unread)
		VALUES ($1, 'email', 'Empty', 'empty@example.test', 'open', false) RETURNING id`, f.wsID,
	).Scan(&emptyID); err != nil {
		t.Fatal(err)
	}
	f.sgConversation(t, "real@example.test", "Real", "Real", false, time.Now().UTC())

	res := mustGroups(t, a, f, SenderGroupFilter{Limit: 50})
	if len(res.Groups) != 1 || res.Groups[0].SenderEmail != "real@example.test" {
		t.Fatalf("groups = %+v, want exactly one (real@example.test)", res.Groups)
	}
}

// TestSenderGroupsMailboxFilter: mailbox_id narrows groups to just that
// mailbox's conversations, the same as ListConversations' own mailbox_id.
func TestSenderGroupsMailboxFilter(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	mb1 := f.mailbox(t, "Support", true, "")
	mb2 := f.mailbox(t, "Sales", false, "")
	now := time.Now().UTC()

	f.sgConversationOnMailbox(t, "buyer@example.test", "On sales", now, mb2)
	f.sgConversationOnMailbox(t, "buyer@example.test", "On support", now.Add(time.Minute), mb1)
	f.sgConversationOnMailbox(t, "other@example.test", "Also support", now, mb1)

	res := mustGroups(t, a, f, SenderGroupFilter{MailboxID: mb1, Limit: 50})
	if len(res.Groups) != 2 {
		t.Fatalf("groups on mb1 = %+v, want 2", res.Groups)
	}
	for _, g := range res.Groups {
		if g.SenderEmail == "buyer@example.test" && g.ConversationCount != 1 {
			t.Fatalf("buyer under mb1 filter = %+v, want count 1 (the sales one must be excluded)", g)
		}
	}
}

// TestSenderGroupThenNarrowBySender: after seeing a group, filtering
// ListConversations by that group's sender_email (as customer_email) returns
// exactly that sender's conversations — the click-through the UI relies on.
func TestSenderGroupThenNarrowBySender(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	now := time.Now().UTC()

	f.sgConversation(t, "Multi@Example.test", "Multi", "One", false, now)
	f.sgConversation(t, "multi@example.test", "Multi", "Two", true, now.Add(time.Minute))
	f.sgConversation(t, "other@example.test", "Other", "Three", false, now)

	groups := mustGroups(t, a, f, SenderGroupFilter{Limit: 50})
	var multi SenderGroup
	for _, g := range groups.Groups {
		if g.SenderEmail == "multi@example.test" {
			multi = g
		}
	}
	if multi.SenderEmail == "" {
		t.Fatalf("no multi@example.test group in %+v", groups.Groups)
	}

	list, err := ListConversations(f.ctx, a, ConversationFilter{Channel: "email", CustomerEmail: multi.SenderEmail, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 2 {
		t.Fatalf("narrowed list = %d conversations, want 2: %+v", len(list.Conversations), list.Conversations)
	}
	for _, c := range list.Conversations {
		if c.CustomerEmail != "Multi@Example.test" && c.CustomerEmail != "multi@example.test" {
			t.Fatalf("unexpected conversation in narrowed list: %+v", c)
		}
	}
}
