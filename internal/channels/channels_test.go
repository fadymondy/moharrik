package channels_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Telegram, Slack, Discord (MH-563) and workflow channels (MH-564), through the
real routes on a real database, against fake platform servers — never a real
bot, account or network (the batch rules).

Each fake server logs what it was called with, so a reply test proves the
outbound call carried the right channel/text, and a webhook test proves the
inbound delivery reached the filer with the right conversation identity.
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "channels", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "channels-test-secret-channels-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_URL", "https://ws.example.test")
	// No real gateway socket from any test that does not explicitly want one.
	t.Setenv("DISCORD_GATEWAY", "off")

	ctx := context.Background()
	k := togo.New()
	a := app.New(ctx, k)
	t.Cleanup(func() { k.Close() })
	return a
}

type fixture struct {
	a   *app.App
	api humatest.TestAPI
	ctx context.Context
	ws  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	a := testApp(t)
	wsID := "ws-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f := &fixture{a: a, ws: wsID}
	f.ctx = tenant.With(context.Background(), &tenant.Scope{
		WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session",
	})
	_, f.api = humatest.New(t)
	channels.RegisterTelegram(f.api, a)
	channels.RegisterSlack(f.api, a)
	channels.RegisterDiscord(f.api, a)
	channels.RegisterWorkflowChannels(f.api, a)
	return f
}
