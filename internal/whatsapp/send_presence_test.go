package whatsapp

// MH-660: SendPresence against a FAKE Evolution server (httptest) — asserts
// the endpoint, method and body shape, never talks to a real Evolution or a
// real phone number.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendPresencePostsToEvolution(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/sendPresence/ws-test", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := clientFor(t, srv.URL)
	if err := c.SendPresence(context.Background(), "201234567890", "composing"); err != nil {
		t.Fatalf("SendPresence: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/chat/sendPresence/ws-test" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["number"] != "201234567890" || gotBody["presence"] != "composing" {
		t.Errorf("body = %+v", gotBody)
	}
}
