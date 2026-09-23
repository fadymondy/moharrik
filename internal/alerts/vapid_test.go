package alerts

import (
	"testing"

	"github.com/fadymondy/moharrik/internal/realtime"
)

func TestCheckEndpoint(t *testing.T) {
	t.Setenv("PUSH_ALLOWED_HOSTS", "")
	t.Setenv("APP_ENV", "development")
	good := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://wns2-db5p.notify.windows.com/w/?token=abc",
		"https://web.push.apple.com/abc",
		"http://127.0.0.1:9999/local", // tests, outside production only
	}
	for _, u := range good {
		if err := checkEndpoint(u); err != nil {
			t.Errorf("%s refused: %v", u, err)
		}
	}
	bad := []string{
		"", "not a url", "http://fcm.googleapis.com/fcm/send/abc", "https://fcm.googleapis.com.evil.test/x",
		"https://evilfcm.googleapis.com.attacker.test/", "https://10.0.0.5/x", "https://user:pw@fcm.googleapis.com/x",
		"https://fcm.googleapis.com:8443/x", "https://metadata.google.internal/", "file:///etc/passwd",
	}
	for _, u := range bad {
		if checkEndpoint(u) == nil {
			t.Errorf("%s accepted", u)
		}
	}

	t.Setenv("APP_ENV", "production")
	if checkEndpoint("http://127.0.0.1:9999/local") == nil {
		t.Error("loopback must be refused in production")
	}

	t.Setenv("PUSH_ALLOWED_HOSTS", "push.example.net")
	if checkEndpoint("https://a.push.example.net/x") != nil || checkEndpoint("https://fcm.googleapis.com/x") == nil {
		t.Error("PUSH_ALLOWED_HOSTS must replace the default list")
	}
}

func TestVAPIDSubjectIsNotDoubled(t *testing.T) {
	if got := (vapid{Subject: "mailto:a@b.test"}).subscriber(); got != "a@b.test" {
		t.Errorf("subscriber = %q (the library adds mailto: itself)", got)
	}
	if got := (vapid{Subject: "https://moharrik.com"}).subscriber(); got != "https://moharrik.com" {
		t.Errorf("subscriber = %q", got)
	}
}

// payloadFor writes from the viewer's rendering only: an event rendered for a
// member without inbox:read has no sender and no snippet, so neither can the
// notification it becomes.
func TestPayloadCarriesOnlyWhatTheViewerMaySee(t *testing.T) {
	ev := realtime.Event{Kind: "message", ConversationID: "c1", Sender: "Rana", Snippet: "secret words",
		Permission: "workspace:read", Alerting: true}
	mine, ok := ev.For(realtime.Viewer{UserID: "u", Role: "client"})
	if !ok {
		t.Fatal("expected delivery")
	}
	p := payloadFor(mine, "ar", "shop")
	if p.Body != "" || p.Title != "رسالة جديدة" || p.URL != "/ar/w/shop/inbox?c=c1" {
		t.Errorf("payload = %+v", p)
	}
}
