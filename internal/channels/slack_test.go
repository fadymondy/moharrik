package channels_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/channels"
)

type fakeSlack struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []fakeSlackCall
}

type fakeSlackCall struct {
	Method string
	Form   url.Values
}

func startFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	f := &fakeSlack{}
	mux := http.NewServeMux()
	handle := func(method string) {
		mux.HandleFunc("/"+method, func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			f.mu.Lock()
			f.calls = append(f.calls, fakeSlackCall{Method: method, Form: r.PostForm})
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch method {
			case "auth.test":
				_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","team_id":"T123","user_id":"U-BOT"}`))
			case "chat.postMessage":
				_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000000.000100"}`))
			default:
				_, _ = w.Write([]byte(`{"ok":true}`))
			}
		})
	}
	handle("auth.test")
	handle("chat.postMessage")
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) calledWith(method string) (url.Values, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Method == method {
			return c.Form, true
		}
	}
	return nil, false
}

func slackSign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func slackHookServer(t *testing.T, f *fixture) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	channels.RegisterSlackHook(r, f.a)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestSlackURLVerificationChallenge(t *testing.T) {
	f := newFixture(t)
	res := f.api.PostCtx(f.ctx, "/api/w/slack-channels", map[string]any{
		"name": "Support", "bot_token": "xoxb-1", "signing_secret": "shh-secret",
	})
	var created struct {
		Channel struct {
			ID         string `json:"id"`
			WebhookURL string `json:"webhook_url"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	srv := slackHookServer(t, f)
	path := strings.TrimPrefix(created.Channel.WebhookURL, "https://ws.example.test")
	body := []byte(`{"type":"url_verification","challenge":"abc123"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(body)))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackSign("shh-secret", ts, body))
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	buf := make([]byte, 200)
	n, _ := res2.Body.Read(buf)
	if res2.StatusCode != http.StatusOK || string(buf[:n]) != "abc123" {
		t.Fatalf("got %d %q", res2.StatusCode, string(buf[:n]))
	}
}

func TestSlackWebhookVerifiesSignatureAndFiles(t *testing.T) {
	f := newFixture(t)
	got := captureFiler(t)
	res := f.api.PostCtx(f.ctx, "/api/w/slack-channels", map[string]any{
		"name": "Support", "bot_token": "xoxb-1", "signing_secret": "shh-secret",
	})
	var created struct {
		Channel struct {
			ID         string `json:"id"`
			WebhookURL string `json:"webhook_url"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	srv := slackHookServer(t, f)
	path := strings.TrimPrefix(created.Channel.WebhookURL, "https://ws.example.test")
	body := []byte(`{"type":"event_callback","team_id":"T123","event":{"type":"message","user":"U-CUST","text":"need help","channel":"C1","ts":"1700000001.000200"}}`)

	post := func(secret string) int {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sig := slackSign(secret, ts, body)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(string(body)))
		req.Header.Set("X-Slack-Request-Timestamp", ts)
		req.Header.Set("X-Slack-Signature", sig)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	if code := post("wrong-secret"); code != http.StatusNotFound {
		t.Fatalf("a bad signature got %d, want 404", code)
	}
	if len(*got) != 0 {
		t.Fatal("a bad signature must not file anything")
	}
	if code := post("shh-secret"); code != http.StatusOK {
		t.Fatalf("a valid delivery got %d", code)
	}
	if len(*got) != 1 {
		t.Fatalf("got %d filed calls, want 1: %+v", len(*got), *got)
	}
	c := (*got)[0]
	if c.Channel != "slack" || c.ThreadID != "C1" || c.Handle != "U-CUST" || c.Text != "need help" || c.ExternalID != "slack:1700000001.000200" {
		t.Fatalf("got %+v", c)
	}
}

func TestSlackReplyAndTestConnection(t *testing.T) {
	fake := startFakeSlack(t)
	defer channels.SetSlackAPIBase(fake.srv.URL)()
	f := newFixture(t)
	res := f.api.PostCtx(f.ctx, "/api/w/slack-channels", map[string]any{
		"name": "Support", "bot_token": "xoxb-1", "signing_secret": "shh",
	})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)

	res = f.api.PostCtx(f.ctx, "/api/w/slack-channels/"+created.Channel.ID+"/test", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	var out struct {
		Channel struct {
			TeamName  string `json:"team_name"`
			BotUserID string `json:"bot_user_id"`
		} `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Channel.TeamName != "Acme" || out.Channel.BotUserID != "U-BOT" {
		t.Fatalf("got %+v", out.Channel)
	}

	id, err := channels.SendSlackReply(f.ctx, f.a, f.ws, created.Channel.ID, "C1", "we're on it")
	if err != nil {
		t.Fatal(err)
	}
	if id != "slack:1700000000.000100" {
		t.Fatalf("got %q", id)
	}
	form, ok := fake.calledWith("chat.postMessage")
	if !ok || form.Get("channel") != "C1" || form.Get("text") != "we're on it" {
		t.Fatalf("chat.postMessage called with %+v", form)
	}
}
