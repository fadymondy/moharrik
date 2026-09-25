package server

/*
TestSendCampaignChannel — MH-519: send_campaign over MCP sends a campaign on
its own channel. A WhatsApp campaign must never go through SMTP. With no
WhatsApp linked it is refused with the WhatsApp reason and stays a draft; an
email campaign in the same workspace still asks for SMTP. Nothing is sent.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run SendCampaignChannel -v
*/

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestSendCampaignChannel(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "send-campaign-secret-send-campaign-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "camp-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	alice.ws = alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Camp " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)

	listID := alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "Everyone"}, 201)["list"].(map[string]any)["id"].(string)
	waID := alice.must("POST", "/api/w/campaigns", map[string]any{"list_id": listID, "channel": "whatsapp", "subject": "WA launch", "body": "Hello"}, 201)["campaign"].(map[string]any)["id"].(string)
	alice.must("POST", "/api/w/campaigns", map[string]any{"list_id": listID, "subject": "Mail launch", "body": "Hello"}, 201)

	tk := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "camp", "scopes": []string{"campaigns:read", "campaigns:write"}}, 201)
	agent := newClient(t, srv.URL)
	agent.bearer = tk["plaintext"].(string)

	_, text, isErr := mcpCall(agent, "/api/mcp", "send_campaign", map[string]any{"subject": "WA launch"})
	if !isErr || strings.Contains(text, "SMTP") || !strings.Contains(text, "WhatsApp") {
		t.Fatalf("WhatsApp campaign: want the WhatsApp refusal, never SMTP; got isError=%v %q", isErr, text)
	}
	var status string
	if err := a.SysDB.QueryRow(`SELECT status FROM campaigns WHERE id = $1`, waID).Scan(&status); err != nil || status != "draft" {
		t.Fatalf("WhatsApp campaign status = %q (%v), want draft", status, err)
	}

	_, text, isErr = mcpCall(agent, "/api/mcp", "send_campaign", map[string]any{"subject": "Mail launch"})
	if !isErr || !strings.Contains(text, "SMTP") {
		t.Fatalf("email campaign: want the SMTP refusal; got isError=%v %q", isErr, text)
	}
}
