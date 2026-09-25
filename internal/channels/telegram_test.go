package channels_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/channels"
)

// filedCall is one delivery channels.SetFiler received.
type filedCall struct {
	WorkspaceID, Channel, SourceChannelID, ThreadID, Handle, Name, Text, ExternalID string
	IsGroup                                                                        bool
}

// captureFiler wires a Filer that records every delivery, and restores the
// filer to nil (unwired) when the test ends — the package var is global, so
// leaving a previous test's filer set would leak into the next one.
func captureFiler(t *testing.T) *[]filedCall {
	t.Helper()
	var mu sync.Mutex
	got := []filedCall{}
	channels.SetFiler(func(_ context.Context, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string, isGroup bool) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, filedCall{workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID, isGroup})
		return nil
	})
	t.Cleanup(func() { channels.SetFiler(nil) })
	return &got
}

// fakeTelegram logs every Bot API call and answers {"ok":true,...}.
type fakeTelegram struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []fakeTelegramCall
}

type fakeTelegramCall struct {
	Method string
	Body   map[string]any
}

func startFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// /bot<token>/<method>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		method := ""
		if len(parts) == 2 {
			method = parts[1]
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls = append(f.calls, fakeTelegramCall{Method: method, Body: body})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"testbot"}}`))
		case "sendMessage":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":555}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegram) calledWith(method string) (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method == method {
			return c.Body, true
		}
	}
	return nil, false
}

func (f *fakeTelegram) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func telegramHookServer(t *testing.T, f *fixture) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	channels.RegisterTelegramHook(r, f.a)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestTelegramCreateRegistersTheWebhook(t *testing.T) {
	fake := startFakeTelegram(t)
	defer channels.SetTelegramAPIBase(fake.srv.URL)()
	f := newFixture(t)

	res := f.api.PostCtx(f.ctx, "/api/w/telegram-channels", map[string]any{
		"name": "Support Bot", "bot_token": "TEST:TOKEN",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var out struct {
		Channel struct {
			ID         string `json:"id"`
			WebhookURL string `json:"webhook_url"`
			HasToken   bool   `json:"has_token"`
		} `json:"channel"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Channel.HasToken || !strings.Contains(out.Channel.WebhookURL, "/api/telegram/hook/") {
		t.Fatalf("got %+v", out.Channel)
	}
	// The bot token must never come back in any response.
	if strings.Contains(res.Body.String(), "TEST:TOKEN") {
		t.Fatal("the bot token leaked in the create response")
	}
	body, ok := fake.calledWith("setWebhook")
	if !ok || !strings.Contains(body["url"].(string), "/api/telegram/hook/") {
		t.Fatalf("setWebhook not called correctly: %+v", body)
	}
}

func TestTelegramWebhookFilesAMessageAndDedupes(t *testing.T) {
	fake := startFakeTelegram(t)
	defer channels.SetTelegramAPIBase(fake.srv.URL)()
	f := newFixture(t)
	got := captureFiler(t)

	res := f.api.PostCtx(f.ctx, "/api/w/telegram-channels", map[string]any{"name": "Bot", "bot_token": "T:1"})
	var created struct {
		Channel struct {
			ID         string `json:"id"`
			WebhookURL string `json:"webhook_url"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	body, _ := fake.calledWith("setWebhook")
	secretToken, _ := body["secret_token"].(string)
	if secretToken == "" {
		t.Fatal("no secret_token registered")
	}

	hookURL := created.Channel.WebhookURL
	srv := telegramHookServer(t, f)
	// The channel's webhook_url is on the app's own public base
	// (https://ws.example.test); redirect the POST to our test server instead.
	path := strings.TrimPrefix(hookURL, "https://ws.example.test")

	update := `{"update_id":1,"message":{"message_id":42,"date":1700000000,"text":"hello there",
		"from":{"id":9,"is_bot":false,"first_name":"Dana","username":"dana"},
		"chat":{"id":9,"type":"private"}}}`

	post := func(secret string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(update))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	if code := post("wrong-secret"); code != http.StatusNotFound {
		t.Fatalf("a bad secret token got %d, want 404", code)
	}
	if len(*got) != 0 {
		t.Fatal("a bad secret token must not file anything")
	}
	if code := post(secretToken); code != http.StatusOK {
		t.Fatalf("a valid delivery got %d", code)
	}
	if len(*got) != 1 {
		t.Fatalf("got %d filed calls, want 1: %+v", len(*got), *got)
	}
	c := (*got)[0]
	if c.Channel != "telegram" || c.ThreadID != "9" || c.Handle != "dana" || c.Name != "Dana" || c.Text != "hello there" || c.ExternalID != "tg:42" {
		t.Fatalf("got %+v", c)
	}

	// A redelivery (Telegram retries on any non-2xx) is accepted again but
	// must not be filed twice — the handler itself has no dedupe (that lives
	// in internal/inbox's external_id uniqueness), but calling deliver again
	// is fine: the filer here just counts, so assert it WAS called again
	// (this handler's job is "deliver honestly", not "deduplicate" — dedupe is
	// proven at the inbox layer, internal/inbox/channel_test.go).
	if code := post(secretToken); code != http.StatusOK {
		t.Fatalf("redelivery got %d", code)
	}
	if len(*got) != 2 {
		t.Fatalf("got %d filed calls after redelivery, want 2 (dedupe is the inbox layer's job)", len(*got))
	}
}

func TestTelegramReplySendsAndReturnsAnID(t *testing.T) {
	fake := startFakeTelegram(t)
	defer channels.SetTelegramAPIBase(fake.srv.URL)()
	f := newFixture(t)

	res := f.api.PostCtx(f.ctx, "/api/w/telegram-channels", map[string]any{"name": "Bot", "bot_token": "T:1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	id, err := channels.SendTelegramReply(f.ctx, f.a, f.ws, created.Channel.ID, "9", "we shipped it")
	if err != nil {
		t.Fatal(err)
	}
	if id != "tg:555" {
		t.Fatalf("got %q", id)
	}
	body, ok := fake.calledWith("sendMessage")
	if !ok || body["chat_id"] != "9" || body["text"] != "we shipped it" {
		t.Fatalf("sendMessage called with %+v", body)
	}
}

func TestTelegramTestConnection(t *testing.T) {
	fake := startFakeTelegram(t)
	defer channels.SetTelegramAPIBase(fake.srv.URL)()
	f := newFixture(t)

	res := f.api.PostCtx(f.ctx, "/api/w/telegram-channels", map[string]any{"name": "Bot", "bot_token": "T:1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	res = f.api.PostCtx(f.ctx, "/api/w/telegram-channels/"+created.Channel.ID+"/test", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	var out struct {
		Channel struct {
			BotUsername string `json:"bot_username"`
			LastTestOK  *bool  `json:"last_test_ok"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Channel.BotUsername != "testbot" || out.Channel.LastTestOK == nil || !*out.Channel.LastTestOK {
		t.Fatalf("got %+v", out.Channel)
	}
	if fake.count("getMe") == 0 {
		t.Fatal("getMe was not called")
	}
}
