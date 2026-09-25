package imports_test

import (
	"context"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/imports"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// An imported email thread is dated by its emails, not by the import (MH-590).
func TestEmailImportDatesTheThreadByItsEmails(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})
	var convID string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, subject)
		VALUES ($1, 'email', 'rana@example.test', 'Order NC-20417') RETURNING id`, wsID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	n, err := imports.InsertEmailHistory(ctx, a, wsID, convID, []*inbox.Email{
		{MessageID: "<a@example.test>", Subject: "Order NC-20417", Body: "Has it shipped?", Date: old},
		{MessageID: "<b@example.test>", Subject: "Re: Order NC-20417", Body: "Any news?", Date: old.Add(48 * time.Hour)},
	})
	if err != nil || n != 2 {
		t.Fatalf("inserted %d, err %v", n, err)
	}
	assertDatedByMessages(t, ctx, a, wsID)
}
