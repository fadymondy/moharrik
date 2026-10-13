package alerts

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fadymondy/moharrik/internal/realtime"
)

func TestPrefsWants(t *testing.T) {
	me := "u-me"
	msg := realtime.Event{Kind: "message"}
	mine := realtime.Event{Kind: "message", AlertOnly: me}
	cases := []struct {
		name string
		p    Prefs
		ev   realtime.Event
		want bool
	}{
		{"default: a message", DefaultPrefs, msg, true},
		{"default: an assignment", DefaultPrefs, realtime.Event{Kind: "assigned"}, true},
		{"default: a notification", DefaultPrefs, realtime.Event{Kind: "notification"}, true},
		{"messages off", Prefs{Assignments: true, Notifications: true}, msg, false},
		{"assigned only: unassigned stays quiet", Prefs{Messages: true, AssignedOnly: true}, msg, false},
		{"assigned only: mine rings", Prefs{Messages: true, AssignedOnly: true}, mine, true},
		{"assignments off", Prefs{Messages: true, Notifications: true}, realtime.Event{Kind: "assigned"}, false},
		{"notifications off", Prefs{Messages: true}, realtime.Event{Kind: "notification"}, false},
		{"a test always goes", Prefs{}, realtime.Event{Kind: "test"}, true},
	}
	for _, c := range cases {
		if got := c.p.wants(c.ev, me); got != c.want {
			t.Errorf("%s: wants = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNativeMessageCarriesTheDeepLink(t *testing.T) {
	m := nativeMessage(Payload{Kind: "message", Title: "T", Body: "B", URL: "/ar/w/acme/inbox?c=c1", Tag: "conv-c1", ConversationID: "c1"}, "ws-1", "acme")
	if m.Title != "T" || m.Body != "B" || m.Collapse != "conv-c1" {
		t.Fatalf("message: %+v", m)
	}
	want := map[string]string{"kind": "message", "workspace_id": "ws-1", "slug": "acme", "url": "/ar/w/acme/inbox?c=c1", "conversation_id": "c1"}
	for k, v := range want {
		if m.Data[k] != v {
			t.Errorf("data[%s] = %q, want %q", k, m.Data[k], v)
		}
	}
	if _, ok := m.Data["item_id"]; ok {
		t.Error("an empty item id must be omitted")
	}
}

func TestAssignedPayload(t *testing.T) {
	p := payloadFor(realtime.Event{Kind: "assigned", ConversationID: "c9", Sender: "Rana", Snippet: "hello"}, "en", "acme")
	if p.Title != "Assigned to you" || !strings.Contains(p.Body, "Rana") || !strings.Contains(p.Body, "hello") ||
		!strings.HasSuffix(p.URL, "/inbox?c=c9") || p.Tag != "conv-c9" || p.ConversationID != "c9" {
		t.Errorf("en: %+v", p)
	}
	if ar := payloadFor(realtime.Event{Kind: "assigned", ConversationID: "c9"}, "ar", "acme"); ar.Title != "أُسندت إليك" {
		t.Errorf("ar: %+v", ar)
	}
}

func TestIsDeadFCMToken(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{404, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, true},
		{400, `{"error":{"status":"INVALID_ARGUMENT","message":"The registration token is not a valid FCM registration token"}}`, true},
		{400, `{"error":{"status":"INVALID_ARGUMENT","message":"Invalid JSON payload received. Unknown name \"x\" at 'message.android'"}}`, false},
		{403, `{"error":{"details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, true},
		{429, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, false},
		{503, ``, false},
	}
	for _, c := range cases {
		if got := isDeadFCMToken(c.status, []byte(c.body)); got != c.want {
			t.Errorf("%d %s: dead = %v, want %v", c.status, c.body, got, c.want)
		}
	}
}

// fakeGoogle is the OAuth token endpoint and the FCM v1 API in one server.
type fakeGoogle struct {
	*httptest.Server
	mu      sync.Mutex
	tokens  int
	sent    []map[string]any
	respond func(token string) (int, string)
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{respond: func(string) (int, string) { return 200, `{"name":"projects/p/messages/1"}` }}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(r.Form.Get("assertion"), ".") != 2 {
				http.Error(w, "bad grant", 400)
				return
			}
			g.mu.Lock()
			g.tokens++
			g.mu.Unlock()
			_, _ = io.WriteString(w, `{"access_token":"ya29.test","expires_in":3600,"token_type":"Bearer"}`)
		case r.URL.Path == "/v1/projects/moharrik-test/messages:send":
			if r.Header.Get("Authorization") != "Bearer ya29.test" {
				http.Error(w, "no auth", 401)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			g.sent = append(g.sent, body)
			g.mu.Unlock()
			tok, _ := body["message"].(map[string]any)["token"].(string)
			code, out := g.respond(tok)
			w.WriteHeader(code)
			_, _ = io.WriteString(w, out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func testServiceAccount(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	sa, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "moharrik-test", "client_email": "push@moharrik-test.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURI,
	})
	return string(sa)
}

func TestFCMSend(t *testing.T) {
	g := newFakeGoogle(t)
	f, err := NewFCMFromJSON(testServiceAccount(t, g.URL+"/token"))
	if err != nil {
		t.Fatal(err)
	}
	if f.tokenURL != fcmTokenURL {
		t.Fatalf("a key file's own token_uri must not redirect the assertion: %s", f.tokenURL)
	}
	f.tokenURL, f.baseURL = g.URL+"/token", g.URL
	ctx := context.Background()
	m := NativeMessage{Title: "New message from Rana", Body: "Is it in stock?", Collapse: "conv-c1", Data: map[string]string{"conversation_id": "c1"}}

	if err := f.Send(ctx, "device-a", m); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := f.Send(ctx, "device-a", m); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if g.tokens != 1 {
		t.Errorf("minted %d access tokens for two sends, want 1 (cached)", g.tokens)
	}
	msg := g.sent[0]["message"].(map[string]any)
	if msg["token"] != "device-a" || msg["notification"].(map[string]any)["title"] != m.Title ||
		msg["data"].(map[string]any)["conversation_id"] != "c1" ||
		msg["android"].(map[string]any)["priority"] != "HIGH" ||
		msg["apns"].(map[string]any)["headers"].(map[string]any)["apns-collapse-id"] != "conv-c1" {
		t.Errorf("FCM got: %+v", msg)
	}

	g.respond = func(string) (int, string) {
		return 404, `{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`
	}
	if err := f.Send(ctx, "gone", m); !errors.Is(err, ErrDeadToken) {
		t.Errorf("an unregistered token: %v, want ErrDeadToken", err)
	}
	g.respond = func(string) (int, string) { return 500, `secret internals` }
	err = f.Send(ctx, "x", m)
	if err == nil || errors.Is(err, ErrDeadToken) || strings.Contains(err.Error(), "secret internals") {
		t.Errorf("a 500: %v (must be transient, and never echo Google's body)", err)
	}
}
