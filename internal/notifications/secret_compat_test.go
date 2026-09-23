package notifications

import "testing"

// A value sealed by this package's previous secret.go (before internal/secret,
// MH-439) must still open: production stores them.
func TestSealedByThePreviousCodeStillOpens(t *testing.T) {
	t.Setenv("AUTH_SECRET", "compat-fixture-secret-0123456789abcdef")
	got, err := unseal("4LhsOxFrv+ZSA9zbujere/1J/QsuXEMW574p/v3uahoflRXVrSf5RwpDxbiPRBIYxw86ApFH")
	if err != nil || got != "plain-notifications-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}
