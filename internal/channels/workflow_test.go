package channels_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWorkflowChannelPushDedupesByThreadAndMessage(t *testing.T) {
	f := newFixture(t)
	got := captureFiler(t)

	res := f.api.PostCtx(f.ctx, "/api/w/workflow-channels", map[string]any{
		"name": "LinkedIn inbox", "network": "linkedin",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	push := func() map[string]any {
		res := f.api.PostCtx(f.ctx, "/api/w/workflow-channels/"+created.Channel.ID+"/push", map[string]any{
			"items": []map[string]any{
				{"thread_id": "t1", "message_id": "m1", "profile_url": "https://linkedin.com/in/dana", "name": "Dana", "text": "are you open to a call?"},
				{"thread_id": "t1", "message_id": "m2", "profile_url": "https://linkedin.com/in/dana", "name": "Dana", "text": "let me know"},
			},
		})
		if res.Code != http.StatusOK {
			t.Fatalf("push: %d %s", res.Code, res.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}

	first := push()
	if first["pushed"] != float64(2) || first["deduped"] != float64(0) {
		t.Fatalf("first push: %+v", first)
	}
	if len(*got) != 2 {
		t.Fatalf("got %d filed calls, want 2: %+v", len(*got), *got)
	}
	c0 := (*got)[0]
	if c0.Channel != "linkedin" || c0.ThreadID != "t1" || c0.Handle != "https://linkedin.com/in/dana" || c0.ExternalID != "linkedin:m1" {
		t.Fatalf("got %+v", c0)
	}

	// A re-run of the same workflow (the same thread/message ids) must push
	// nothing twice — the whole point of MH-564's dedupe.
	second := push()
	if second["pushed"] != float64(0) || second["deduped"] != float64(2) {
		t.Fatalf("second push (a re-run): %+v", second)
	}
	if len(*got) != 2 {
		t.Fatalf("a re-run filed more messages: %d calls", len(*got))
	}

	// A workflow channel that is off refuses a push outright.
	res = f.api.PutCtx(f.ctx, "/api/w/workflow-channels/"+created.Channel.ID, map[string]any{
		"name": "LinkedIn inbox", "network": "linkedin", "enabled": false,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", res.Code, res.Body)
	}
	res = f.api.PostCtx(f.ctx, "/api/w/workflow-channels/"+created.Channel.ID+"/push", map[string]any{
		"items": []map[string]any{{"thread_id": "t2", "message_id": "m3", "text": "new thread"}},
	})
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("push on a disabled channel: %d %s", res.Code, res.Body)
	}
}

func TestWorkflowChannelRejectsAnUnknownNetwork(t *testing.T) {
	f := newFixture(t)
	res := f.api.PostCtx(f.ctx, "/api/w/workflow-channels", map[string]any{"name": "X", "network": "myspace"})
	if res.Code != http.StatusBadRequest && res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want a validation error: %s", res.Code, res.Body)
	}
}
