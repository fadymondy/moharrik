package channels_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/channels"
)

type fakeDiscord struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []fakeDiscordCall
}

type fakeDiscordCall struct {
	Method, Path string
	Body         map[string]any
}

func startFakeDiscord(t *testing.T) *fakeDiscord {
	t.Helper()
	f := &fakeDiscord{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls = append(f.calls, fakeDiscordCall{Method: r.Method, Path: r.URL.Path, Body: body})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/users/@me":
			_, _ = w.Write([]byte(`{"id":"1","username":"testbot","discriminator":"0"}`))
		case strings.HasPrefix(r.URL.Path, "/channels/") && strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(`{"id":"999"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscord) calledWith(path string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Path == path {
			return c.Body, true
		}
	}
	return nil, false
}

func discordHookServer(t *testing.T, f *fixture) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	channels.RegisterDiscordHook(r, f.a)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscordInteractionsVerifiesEd25519AndAnswersPing(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	res := f.api.PostCtx(f.ctx, "/api/w/discord-channels", map[string]any{
		"name": "Support", "bot_token": "T.1", "public_key": hex.EncodeToString(pub),
	})
	var created struct {
		Channel struct {
			ID              string `json:"id"`
			InteractionsURL string `json:"interactions_url"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	srv := discordHookServer(t, f)
	path := strings.TrimPrefix(created.Channel.InteractionsURL, "https://ws.example.test")
	body := []byte(`{"type":1}`)
	ts := "1700000000"

	sign := func(key ed25519.PrivateKey) string {
		msg := append([]byte(ts), body...)
		return hex.EncodeToString(ed25519.Sign(key, msg))
	}

	// A bad signature (a random key, not the connection's) is refused.
	_, badPriv, _ := ed25519.GenerateKey(nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sign(badPriv))
	req.Header.Set("X-Signature-Timestamp", ts)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a bad signature got %d, want 401", res2.StatusCode)
	}

	// The connection's own key answers PING with {"type":1}.
	req, _ = http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(body)))
	req.Header.Set("X-Signature-Ed25519", sign(priv))
	req.Header.Set("X-Signature-Timestamp", ts)
	res3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	buf := make([]byte, 100)
	n, _ := res3.Body.Read(buf)
	if res3.StatusCode != http.StatusOK || string(buf[:n]) != `{"type":1}` {
		t.Fatalf("got %d %q", res3.StatusCode, string(buf[:n]))
	}
}

func TestDiscordReplyAndTestConnection(t *testing.T) {
	fake := startFakeDiscord(t)
	defer channels.SetDiscordAPIBase(fake.srv.URL)()
	f := newFixture(t)
	res := f.api.PostCtx(f.ctx, "/api/w/discord-channels", map[string]any{"name": "Support", "bot_token": "T.1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	res = f.api.PostCtx(f.ctx, "/api/w/discord-channels/"+created.Channel.ID+"/test", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	var out struct {
		Channel struct {
			BotUsername string `json:"bot_username"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Channel.BotUsername != "testbot" {
		t.Fatalf("got %+v", out.Channel)
	}

	id, err := channels.SendDiscordReply(f.ctx, f.a, f.ws, created.Channel.ID, "C1", "we're on it")
	if err != nil {
		t.Fatal(err)
	}
	if id != "discord:999" {
		t.Fatalf("got %q", id)
	}
	body, ok := fake.calledWith("/channels/C1/messages")
	if !ok || body["content"] != "we're on it" {
		t.Fatalf("POST /channels/C1/messages called with %+v", body)
	}
}
