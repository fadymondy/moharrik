package automation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fadymondy/moharrik/internal/automation"
)

/*
MH-478: the workspace profile the extension's own-tab Application autofill
panel reads. A fresh connect's hello.ok carries it, and saving it again while
a browser is already connected reaches that browser's popup without anyone
reconnecting — PushProfileUpdate, called from the REST save route.
*/
func TestProfileReachesConnectedBrowsers(t *testing.T) {
	a := testApp(t)
	hub, wsID, agentID, rb, ackBody := connectRuntime(t, a, "{example.com}", nil, nil)

	// Nothing saved yet: hello.ok still answers with an (empty) profile, never omits the key.
	profile, ok := ackBody["profile"].(map[string]any)
	if !ok {
		t.Fatalf("hello.ok has no profile: %v", ackBody)
	}
	if len(profile) != 0 {
		t.Fatalf("a fresh workspace's profile is not empty: %v", profile)
	}

	if _, err := automation.SaveProfile(context.Background(), a, wsID, map[string]string{"first_name": "Mona", "email": "mona@x.test"}); err != nil {
		t.Fatal(err)
	}
	automation.PushProfileUpdate(context.Background(), a, hub, wsID)

	eventually(t, "an agent.update carrying the new profile", func() bool {
		for _, env := range rb.seen("agent.update") {
			var p struct {
				Profile map[string]string `json:"profile"`
			}
			if json.Unmarshal(env.Payload, &p) == nil && p.Profile["first_name"] == "Mona" && p.Profile["email"] == "mona@x.test" {
				return true
			}
		}
		return false
	})

	// A browser of ANOTHER workspace is not reached.
	_, otherWS, _, otherRB, _ := connectRuntime(t, a, "{example.com}", nil, nil)
	if otherWS == wsID {
		t.Fatal("test setup: expected a distinct workspace")
	}
	if _, err := automation.SaveProfile(context.Background(), a, wsID, map[string]string{"first_name": "Changed"}); err != nil {
		t.Fatal(err)
	}
	automation.PushProfileUpdate(context.Background(), a, hub, wsID)
	if len(otherRB.seen("agent.update")) != 0 {
		t.Fatal("another workspace's browser received this workspace's profile push")
	}
	_ = agentID
}
