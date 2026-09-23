package marketing

import "testing"

// A value sealed by this package's previous secret.go (before internal/secret,
// MH-439) must still open: production stores them.
func TestSealedByThePreviousCodeStillOpens(t *testing.T) {
	t.Setenv("AUTH_SECRET", "compat-fixture-secret-0123456789abcdef")
	got, err := unseal("Hfrh30JZfVn8+kwOMIkccSe3dUuim83loD1eNwL9135pf7MUwy3Bkz8j9tr5DcJ/iXE=")
	if err != nil || got != "plain-marketing-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}
