package inbox

import (
	"errors"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// MH-664 on the non-WhatsApp channels: a reaction is stored in Moharrik only
// (delivered=false), toggles and replaces per member, and aggregates across
// members; an email reply that quotes a message answers THAT message.
func TestReactionsInAppAndEmailQuote(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	conv := f.conversation(t, "email", "open", false)
	var msgID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id)
		VALUES ($1, $2, 'in', 'second question', 'q2-`+idSuffix()+`@mail.test') RETURNING id`, f.wsID, conv).Scan(&msgID); err != nil {
		t.Fatal(err)
	}

	r, err := react(f.ctx, a, msgID, "👍", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Delivered || r.Emoji != "👍" || len(r.Reactions) != 1 || !r.Reactions[0].Mine || r.Reactions[0].Reactors[0].Kind != "member" {
		t.Errorf("in-app reaction: %+v", r)
	}
	// A teammate reacts the same way: one chip, two people.

	other := "u-mate-" + idSuffix()
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, other, other+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := storeReaction(f.ctx, a.SQLDB, f.wsID, msgID, "user:"+other, other, "👍"); err != nil {
		t.Fatal(err)
	}
	r, _ = react(f.ctx, a, msgID, "🎉", true)
	if len(r.Reactions) != 2 {
		t.Fatalf("after replacing: %+v", r.Reactions)
	}
	for _, x := range r.Reactions {
		if (x.Emoji == "🎉") != x.Mine || x.Count != 1 {
			t.Errorf("chip %+v", x)
		}
	}
	_ = storeReaction(f.ctx, a.SQLDB, f.wsID, msgID, "user:"+other, other, "🎉")
	r, _ = react(f.ctx, a, msgID, "🎉", false) // set, not toggle: unchanged
	if len(r.Reactions) != 1 || r.Reactions[0].Count != 2 || !r.Reactions[0].Mine {
		t.Errorf("two members, one emoji: %+v", r.Reactions)
	}
	r, _ = react(f.ctx, a, msgID, "🎉", true) // toggle: mine goes
	if len(r.Reactions) != 1 || r.Reactions[0].Count != 1 || r.Reactions[0].Mine || r.Emoji != "" {
		t.Errorf("after taking mine back: %+v", r)
	}
	if _, err := react(f.ctx, a, msgID, "hello", true); !isStatus(err, 422) {
		t.Errorf("a word is not a reaction: %v", err)
	}
	if _, err := react(f.ctx, a, "00000000-0000-0000-0000-000000000000", "👍", true); !isStatus(err, 404) {
		t.Errorf("unknown message: %v", err)
	}

	// The email quote: In-Reply-To is the quoted message, even though a newer
	// customer message exists.
	var newer string
	_ = a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, created_at)
		VALUES ($1, $2, 'in', 'third', 'q3-`+idSuffix()+`@mail.test', now() + interval '1 minute') RETURNING external_id`, f.wsID, conv).Scan(&newer)
	q, err := quotedMessage(f.ctx, a.SQLDB, f.wsID, conv, msgID)
	if err != nil {
		t.Fatal(err)
	}
	inReplyTo, refs, err := threadHeadersFor(withEmailQuote(f.ctx, q.ExternalID), a, f.wsID, conv)
	if err != nil {
		t.Fatal(err)
	}
	if inReplyTo != q.ExternalID {
		t.Errorf("In-Reply-To %q, want the quoted %q", inReplyTo, q.ExternalID)
	}
	if len(refs) == 0 {
		t.Errorf("References lost: %v", refs)
	}
	if plain, _, _ := threadHeadersFor(f.ctx, a, f.wsID, conv); plain != newer {
		t.Errorf("without a quote In-Reply-To is still the newest: %q want %q", plain, newer)
	}
	// A message of another conversation cannot be quoted here.
	other2 := f.conversation(t, "chat", "open", false)
	if _, err := quotedMessage(f.ctx, a.SQLDB, f.wsID, other2, msgID); !isStatus(err, 404) {
		t.Errorf("quoting across conversations: %v", err)
	}
}

func isStatus(err error, code int) bool {
	var se huma.StatusError
	return errors.As(err, &se) && se.GetStatus() == code
}

func TestValidEmoji(t *testing.T) {
	for _, ok := range []string{"👍", "❤️", "😂", "🙏", "👍🏽", "🇪🇬", "1️⃣", "👨‍👩‍👧"} {
		if !validEmoji(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "a", "ok", "1", "👍 ", "<b>", "\n"} {
		if validEmoji(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
