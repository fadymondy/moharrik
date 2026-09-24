package channels_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
ListChannels (MH-571): every inbox channel connection of every kind, in one
list, against a real database with RLS on — the same shape channels_test.go's
own fixture uses for the per-type routes, minus the HTTP layer (this endpoint
is a pure read, so exercising channels.ListChannels directly covers the same
SQL the REST/MCP surfaces both call).
*/

func aggSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type aggFixture struct {
	a    *app.App
	wsID string
	ctx  context.Context
}

func newAggFixture(t *testing.T) aggFixture {
	t.Helper()
	a := testApp(t)
	ctx := context.Background()
	suffix := aggSuffix()
	userID := "u-" + suffix
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		userID, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var wsID string
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Channels Test', $1, $2, 'en', 'USD') RETURNING id`,
		"agg-"+suffix, userID).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		wsID, userID); err != nil {
		t.Fatal(err)
	}
	return aggFixture{a: a, wsID: wsID, ctx: tenant.WithWorkspace(ctx, wsID)}
}

func (f aggFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.a.SQLDB.ExecContext(f.ctx, q, args...); err != nil {
		t.Fatalf("exec: %v\n%s", err, q)
	}
}

func (f aggFixture) mailbox(t *testing.T, name string, enabled bool, lastError string) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO mailboxes (workspace_id, name, imap_host, username, password_sealed, enabled, last_error, created_by)
		VALUES ($1, $2, 'imap.example.test', $3, 'sealed-should-never-leak', $4, $5, 'u') RETURNING id`,
		f.wsID, name, strings.ToLower(name)+"@example.test", enabled, lastError,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f aggFixture) telegram(t *testing.T, name string, enabled bool, lastError string) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO telegram_channels (workspace_id, name, bot_token_sealed, webhook_id, secret_token_sealed, enabled, last_error, created_by)
		VALUES ($1, $2, 'sealed-bot-token-should-never-leak', $3, 'sealed-secret-should-never-leak', $4, $5, 'u') RETURNING id`,
		f.wsID, name, "wh-"+aggSuffix(), enabled, lastError,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f aggFixture) chatWidget(t *testing.T, name string, enabled bool) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO chat_widgets (workspace_id, name, public_key, enabled, created_by)
		VALUES ($1, $2, $3, $4, 'u') RETURNING id`,
		f.wsID, name, "pk-"+aggSuffix(), enabled,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f aggFixture) workflowChannel(t *testing.T, name, network string, enabled bool, lastError string) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO workflow_channels (workspace_id, name, network, enabled, last_error, created_by)
		VALUES ($1, $2, $3, $4, $5, 'u') RETURNING id`,
		f.wsID, name, network, enabled, lastError,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f aggFixture) whatsapp(t *testing.T, phone, state, lastError string) {
	t.Helper()
	f.exec(t, `
		INSERT INTO whatsapp_links (workspace_id, instance, hook_id, token_sealed, state, phone, last_error)
		VALUES ($1, $2, $3, 'sealed-token-should-never-leak', $4, $5, $6)`,
		f.wsID, "inst-"+aggSuffix(), "hook-"+aggSuffix(), state, phone, lastError)
}

func (f aggFixture) conversation(t *testing.T, channel string, mailboxID, widgetID, sourceChannelID *string, unread bool) {
	t.Helper()
	f.exec(t, `
		INSERT INTO conversations (workspace_id, channel, mailbox_id, widget_id, source_channel_id, unread)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		f.wsID, channel, mailboxID, widgetID, sourceChannelID, unread)
}

func strp(s string) *string { return &s }

func TestListChannelsMixesTypesAndStatuses(t *testing.T) {
	f := newAggFixture(t)

	mbID := f.mailbox(t, "Support Mailbox", true, "")
	brokenMbID := f.mailbox(t, "Broken Mailbox", true, "imap: auth failed")
	pausedTgID := f.telegram(t, "Paused Bot", false, "")
	okTgID := f.telegram(t, "Sales Bot", true, "")
	widgetID := f.chatWidget(t, "Website Widget", true)
	f.workflowChannel(t, "LinkedIn inbox", "linkedin", true, "session expired")
	f.whatsapp(t, "201000000000", "open", "")

	f.conversation(t, "email", strp(mbID), nil, nil, true)
	f.conversation(t, "email", strp(mbID), nil, nil, false)
	f.conversation(t, "chat", nil, strp(widgetID), nil, false)

	res, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{})
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(res.Channels) != 7 {
		t.Fatalf("want 7 rows (2 mailboxes, 2 telegram, 1 chat, 1 workflow, 1 whatsapp), got %d: %+v", len(res.Channels), res.Channels)
	}

	byID := map[string]channels.ChannelConnection{}
	types := map[string]int{}
	for _, c := range res.Channels {
		byID[c.ID] = c
		types[c.Type]++
	}
	// ---- at least 3 different types show up, correctly tagged --------------
	for _, want := range []string{"email", "telegram", "chat", "workflow", "whatsapp"} {
		if types[want] == 0 {
			t.Errorf("no %s row in the aggregate list", want)
		}
	}

	// ---- per-row status classification --------------------------------------
	if c := byID[mbID]; c.Status != "connected" {
		t.Errorf("healthy mailbox: want connected, got %q", c.Status)
	}
	if c := byID[brokenMbID]; c.Status != "error" {
		t.Errorf("mailbox with last_error: want error, got %q", c.Status)
	}
	if c := byID[pausedTgID]; c.Status != "paused" {
		t.Errorf("disabled telegram channel: want paused, got %q", c.Status)
	}
	if c := byID[okTgID]; c.Status != "connected" {
		t.Errorf("healthy telegram channel: want connected, got %q", c.Status)
	}

	// ---- conversation stats attach to the right row -------------------------
	mb := byID[mbID]
	if mb.Conversations30d != 2 || mb.Unread != 1 {
		t.Errorf("mailbox conversation stats: want 2/1 unread, got %d/%d", mb.Conversations30d, mb.Unread)
	}
	wgt := byID[widgetID]
	if wgt.Conversations30d != 1 {
		t.Errorf("widget conversation stats: want 1, got %d", wgt.Conversations30d)
	}

	// ---- error rows sort first, then alphabetically -------------------------
	var sawNonError bool
	for _, c := range res.Channels {
		if c.Status == "error" && sawNonError {
			t.Fatalf("an error row appeared after a non-error row: %+v", res.Channels)
		}
		if c.Status != "error" {
			sawNonError = true
		}
	}

	// ---- status filter --------------------------------------------------
	errRes, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{Status: "error"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range errRes.Channels {
		if c.Status != "error" {
			t.Errorf("status=error filter let through %q", c.Status)
		}
	}
	if len(errRes.Channels) == 0 {
		t.Fatal("status=error filter returned nothing, want at least the broken mailbox and workflow channel")
	}

	// ---- type filter --------------------------------------------------
	tgRes, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{Types: []string{"telegram"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tgRes.Channels) != 2 {
		t.Fatalf("type=telegram filter: want 2, got %d", len(tgRes.Channels))
	}
	for _, c := range tgRes.Channels {
		if c.Type != "telegram" {
			t.Errorf("type=telegram filter let through %q", c.Type)
		}
	}

	// ---- q filter (name, and whatsapp's phone) -------------------------
	qRes, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{Q: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	if len(qRes.Channels) != 1 || qRes.Channels[0].ID != okTgID {
		t.Fatalf("q=sales: want just %q, got %+v", okTgID, qRes.Channels)
	}
	phoneRes, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{Q: "201000000000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(phoneRes.Channels) != 1 || phoneRes.Channels[0].Type != "whatsapp" {
		t.Fatalf("q=<phone>: want the whatsapp row, got %+v", phoneRes.Channels)
	}

	// ---- no secret ever appears in the JSON response body -------------------
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, secret := range []string{
		"sealed-should-never-leak", "sealed-bot-token-should-never-leak",
		"sealed-secret-should-never-leak", "sealed-token-should-never-leak",
		"password_sealed", "bot_token_sealed", "secret_token_sealed", "token_sealed",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("response body contains %q", secret)
		}
	}
}

func TestListChannelsWhatsAppStatusRule(t *testing.T) {
	f := newAggFixture(t)
	f.whatsapp(t, "201111111111", "close", "evolution: connection refused")

	res, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 || res.Channels[0].Status != "error" {
		t.Fatalf("whatsapp state=close with a last_error: want status=error, got %+v", res.Channels)
	}
}

func TestListChannelsWhatsAppNeverLinkedIsPaused(t *testing.T) {
	f := newAggFixture(t)
	f.whatsapp(t, "", "close", "")

	res, err := channels.ListChannels(f.ctx, f.a, f.wsID, channels.ChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 || res.Channels[0].Status != "paused" {
		t.Fatalf("whatsapp never linked (state=close, no error): want status=paused, got %+v", res.Channels)
	}
}
