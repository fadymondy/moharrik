package inbox

import "testing"

// A value sealed by this package's previous secret.go (before internal/secret,
// MH-439) must still open: production stores them.
func TestSealedByThePreviousCodeStillOpens(t *testing.T) {
	t.Setenv("AUTH_SECRET", "compat-fixture-secret-0123456789abcdef")
	got, err := unseal("eG6asWL52Jp8aXq2h8OiumMjjHIQ7+/qYV8rfHiMrdLPL4rssUz9tm9hY9ySuA==")
	if err != nil || got != "plain-inbox-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}
