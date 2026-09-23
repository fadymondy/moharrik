package mcp

import (
	"testing"

	"github.com/fadymondy/moharrik/internal/secret"
)

// A webhook secret sealed from the MCP tool must open in internal/notifications
// (the same domain), or a source created by an agent could never verify.
func TestMCPWebhookSecretUsesTheNotificationsDomain(t *testing.T) {
	t.Setenv("AUTH_SECRET", "compat-fixture-secret-0123456789abcdef")
	sealed, err := sealWebhookSecret("whsec-plain")
	if err != nil {
		t.Fatal(err)
	}
	got, err := secret.New(secret.DomainWebhookSecret, "x", "y").Open(sealed)
	if err != nil || got != "whsec-plain" {
		t.Fatalf("got %q, %v", got, err)
	}
}
