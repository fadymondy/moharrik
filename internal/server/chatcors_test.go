package server

import "testing"

func TestPublicCrossOriginCoversTheEmbeddableEndpointsOnly(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/chat/ck_1/messages": true,
		"/api/subscribe/sf_abc":   true,
		"/api/w/contacts":         false,
		"/api/w/subscribe-forms":  false,
		"/api/mail/unsubscribe":   false,
		"/api/subscribers":        false,
	} {
		if got := isPublicCrossOrigin(path); got != want {
			t.Errorf("isPublicCrossOrigin(%q) = %v, want %v", path, got, want)
		}
	}
}
