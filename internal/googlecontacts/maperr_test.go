package googlecontacts

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/google"
)

// TestSyncFailureIsNotA5xx: a Google refusal is Google's answer, not a broken
// server. Answered as 502 it was swallowed by Cloudflare's "invalid response
// from the origin" page, so the member never saw that the People API needed
// enabling. It must be a 424 carrying Google's reason in plain words.
func TestSyncFailureIsNotA5xx(t *testing.T) {
	err := mapErr(&google.APIError{Status: 403, Reason: "SERVICE_DISABLED",
		Message: "People API has not been used in project 1 before or it is disabled."})
	var se huma.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("mapErr returned %T, want a huma status error", err)
	}
	if se.GetStatus() != http.StatusFailedDependency {
		t.Fatalf("status %d, want 424", se.GetStatus())
	}
	if !strings.Contains(se.Error(), "not enabled") {
		t.Fatalf("message %q does not say the API is not enabled", se.Error())
	}
}
