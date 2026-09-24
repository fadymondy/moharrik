package channels_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/channels"
)

// fakeGateway is a minimal Discord gateway: HELLO, expects IDENTIFY, answers
// READY, then pushes one MESSAGE_CREATE. It records the IDENTIFY payload and
// every heartbeat it receives, so a test can assert the real protocol ran —
// never a real Discord socket (the batch rules).
type fakeGateway struct {
	srv *httptest.Server
	url string

	mu         sync.Mutex
	identify   map[string]any
	heartbeats int
}

func startFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	f := &fakeGateway{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteJSON(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 200}}); err != nil {
			return
		}
		for {
			var p struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
			}
			if err := conn.ReadJSON(&p); err != nil {
				return
			}
			switch p.Op {
			case 2: // IDENTIFY
				var d map[string]any
				_ = json.Unmarshal(p.D, &d)
				f.mu.Lock()
				f.identify = d
				f.mu.Unlock()
				_ = conn.WriteJSON(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{}})
				_ = conn.WriteJSON(map[string]any{"op": 0, "s": 2, "t": "MESSAGE_CREATE", "d": map[string]any{
					"id": "42", "channel_id": "C1", "guild_id": "",
					"author":  map[string]any{"id": "U-CUST", "bot": false, "username": "dana"},
					"content": "need help",
				}})
			case 1: // HEARTBEAT
				f.mu.Lock()
				f.heartbeats++
				f.mu.Unlock()
				_ = conn.WriteJSON(map[string]any{"op": 11})
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	f.url = "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/"
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGateway) sawIdentify() (map[string]any, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identify, f.identify != nil
}

func (f *fakeGateway) heartbeatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heartbeats
}

func TestDiscordGatewayIdentifiesAndFilesAMessage(t *testing.T) {
	fake := startFakeGateway(t)
	defer channels.SetDiscordGatewayURL(fake.url)()

	f := newFixture(t)
	t.Setenv("DISCORD_GATEWAY", "on") // this one test wants the real connect path
	got := captureFiler(t)

	res := f.api.PostCtx(f.ctx, "/api/w/discord-channels", map[string]any{"name": "Support", "bot_token": "the-token"})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := fake.sawIdentify(); ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	identify, ok := fake.sawIdentify()
	if !ok {
		t.Fatal("the gateway never received IDENTIFY")
	}
	if identify["token"] != "the-token" {
		t.Fatalf("IDENTIFY carried %q, want the connection's own token", identify["token"])
	}

	for time.Now().Before(deadline) {
		if len(*got) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(*got) != 1 {
		t.Fatalf("got %d filed calls, want 1", len(*got))
	}
	c := (*got)[0]
	if c.Channel != "discord" || c.ThreadID != "C1" || c.Handle != "U-CUST" || c.Name != "dana" || c.Text != "need help" || c.ExternalID != "discord:42" {
		t.Fatalf("got %+v", c)
	}

	for time.Now().Before(deadline) {
		if fake.heartbeatCount() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fake.heartbeatCount() == 0 {
		t.Error("no heartbeat was sent on the fake gateway's 200ms interval")
	}
}
