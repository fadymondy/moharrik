package whatsapp

import "testing"

// A value sealed by this package's previous secret.go (before internal/secret,
// MH-439) must still open: production stores them.
func TestSealedByThePreviousCodeStillOpens(t *testing.T) {
	t.Setenv("AUTH_SECRET", "compat-fixture-secret-0123456789abcdef")
	got, err := unseal("UP3EwzFkukKkl7+DcRh9Ql020QguTfEDeQZaehhISQ6lXtMQZdh/oDZxWhpP3Ekocg==")
	if err != nil || got != "plain-whatsapp-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}
