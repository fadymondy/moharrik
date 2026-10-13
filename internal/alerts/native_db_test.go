package alerts

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
Native push (the Moharrik apps) against a real database: the same audience as
web push, gated by each member's own push preferences, and a token FCM says is
gone is removed.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/alerts/ -run Native -v
*/

// fakeFCM records what each device token was sent.
type fakeFCM struct {
	mu   sync.Mutex
	got  map[string][]NativeMessage
	dead map[string]bool
}

func (f *fakeFCM) Send(_ context.Context, token string, m NativeMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead[token] {
		return ErrDeadToken
	}
	f.got[token] = append(f.got[token], m)
	return nil
}

func (f *fakeFCM) take(token string) []NativeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.got[token]
	delete(f.got, token)
	return out
}

func withFakeFCM(t *testing.T) *fakeFCM {
	fake := &fakeFCM{got: map[string][]NativeMessage{}, dead: map[string]bool{}}
	prev := fcmFromEnv
	fcmFromEnv = func() (NativeSender, bool) { return fake, true }
	t.Cleanup(func() { fcmFromEnv = prev })
	return fake
}

// phone registers an app install; its FCM token is unique to this fixture,
// since the test database outlives a run.
func (f *fixture) phone(t *testing.T, who, token, locale string) {
	t.Helper()
	token = f.tok(token)
	if _, err := f.a.SysDB.Exec(`INSERT INTO device_tokens (user_id, platform, token, locale) VALUES ($1, 'android', $2, $3)`,
		f.users[who], token, locale); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) tok(name string) string { return name + "-" + f.ws }

func TestNativePush(t *testing.T) {
	f := newFixture(t)
	fcm := withFakeFCM(t)
	f.phone(t, "owner", "tok-owner", "en")
	f.phone(t, "member", "tok-member", "ar")
	f.phone(t, "client", "tok-client", "en")

	t.Run("a customer message reaches every inbox reader's phone, in their language, with the deep link", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, inboundEvent("conv-n1"))
		o := fcm.take(f.tok("tok-owner"))
		if len(o) != 1 || o[0].Title != "New message from Rana" || o[0].Data["conversation_id"] != "conv-n1" ||
			o[0].Data["workspace_id"] != f.ws || o[0].Collapse != "conv-conv-n1" {
			t.Fatalf("owner's phone: %+v", o)
		}
		if m := fcm.take(f.tok("tok-member")); len(m) != 1 || m[0].Data["url"] == "" || m[0].Data["url"][:4] != "/ar/" {
			t.Fatalf("member's phone: %+v", m)
		}
		if c := fcm.take(f.tok("tok-client")); len(c) != 0 {
			t.Fatalf("a client's phone got a customer's message: %+v", c)
		}
	})

	t.Run("a member's own preferences silence their devices only", func(t *testing.T) {
		res := f.api.PutCtx(f.ctx["member"], "/api/w/push/prefs", map[string]any{
			"messages": false, "assigned_only": false, "assignments": true, "notifications": true,
		})
		if res.Code != http.StatusOK {
			t.Fatalf("set prefs: %d %s", res.Code, res.Body)
		}
		if got := f.api.GetCtx(f.ctx["member"], "/api/w/push/prefs"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"messages":false`) {
			t.Fatalf("read prefs back: %d %s", got.Code, got.Body)
		}
		Emit(context.Background(), f.a, f.ws, inboundEvent("conv-n2"))
		if m := fcm.take(f.tok("tok-member")); len(m) != 0 {
			t.Errorf("messages are off for the member, yet: %+v", m)
		}
		if o := fcm.take(f.tok("tok-owner")); len(o) != 1 {
			t.Errorf("the owner's preferences are untouched; got %d pushes", len(o))
		}
	})

	t.Run("an assignment goes to the assignee alone", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, realtime.Event{Kind: "assigned", ConversationID: "conv-n3", Sender: "Rana",
			Permission: "inbox:read", Alerting: true, AlertOnly: f.users["member"]})
		if m := fcm.take(f.tok("tok-member")); len(m) != 1 || m[0].Title != "أُسندت إليك" || m[0].Data["kind"] != "assigned" {
			t.Errorf("assignee's phone: %+v", m)
		}
		if o := fcm.take(f.tok("tok-owner")); len(o) != 0 {
			t.Errorf("someone else's assignment reached the owner: %+v", o)
		}
	})

	t.Run("a token FCM says is gone is removed", func(t *testing.T) {
		fcm.dead[f.tok("tok-owner")] = true
		Emit(context.Background(), f.a, f.ws, inboundEvent("conv-n4"))
		var n int
		if err := f.a.SysDB.QueryRow(`SELECT count(*) FROM device_tokens WHERE token = $1`, f.tok("tok-owner")).Scan(&n); err != nil || n != 0 {
			t.Errorf("dead token rows left: %d (%v)", n, err)
		}
	})

	t.Run("the test push reaches the caller's phones", func(t *testing.T) {
		res := f.api.PostCtx(f.ctx["member"], "/api/w/push/test", map[string]any{})
		if res.Code != http.StatusOK {
			t.Fatalf("test push: %d %s", res.Code, res.Body)
		}
		if m := fcm.take(f.tok("tok-member")); len(m) != 1 || m[0].Data["kind"] != "test" {
			t.Errorf("test push on the phone: %+v", m)
		}
	})
}
