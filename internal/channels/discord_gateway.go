package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The Discord gateway (MH-563): "the Discord gateway or interactions". Ordinary
channel messages and DMs reach a bot only over the gateway — the Interactions
Endpoint (discord_routes.go's Ed25519-verified webhook) carries slash-command
and component payloads only, never a plain message. So one persistent
websocket per enabled connection is the mechanism, the same shape
internal/automation's browser socket uses gorilla/websocket for, just
outbound instead of inbound.

Replying needs no gateway at all — SendDiscordReply is a plain REST call —
so a connection with the gateway down can still answer what it already has.

gatewayURL and discordGatewayEnabled are package vars so a test can dial a
fake server (httptest + gorilla/websocket's own Upgrader) and so
DISCORD_GATEWAY=off keeps every test and the OpenAPI export from ever
touching a real socket, the same switch TRENDS_COLLECTOR already sets for the
trends collector.
*/

// gatewayURLOverride is set by SetDiscordGatewayURL (tests). DISCORD_GATEWAY_URL
// itself is read lazily by gatewayURL() — see telegramAPIBase's comment in
// telegram.go for why: envfile.Load(.env) runs inside main(), after every
// package's init() has already run.
var gatewayURLOverride string

func gatewayURL() string {
	if gatewayURLOverride != "" {
		return gatewayURLOverride
	}
	if v := strings.TrimSpace(os.Getenv("DISCORD_GATEWAY_URL")); v != "" {
		return v
	}
	return "wss://gateway.discord.gg/?v=10&encoding=json"
}

func discordGatewayEnabled() bool { return os.Getenv("DISCORD_GATEWAY") != "off" }

// globalApp is the app the gateway supervisor uses for its own database
// writes, made available the way internal/trends' collector needs one: set
// once at boot (SetApp), read only from background goroutines that carry no
// request context of their own.
var globalApp *app.App

// SetApp wires the app the gateway sessions and their status writes use.
func SetApp(a *app.App) { globalApp = a }

func discordCtx(workspaceID string) context.Context {
	return tenant.WithWorkspace(context.Background(), workspaceID)
}

type gatewaySession struct{ cancel context.CancelFunc }

var (
	sessMu   sync.Mutex
	sessions = map[string]*gatewaySession{}
)

func sessKey(workspaceID, id string) string { return workspaceID + "/" + id }

// StartAllGateways starts a session for every enabled Discord connection that
// has a bot token — called once at boot, so a restart reconnects what was
// already running, the same as internal/trends.StartCollector.
func StartAllGateways(a *app.App) {
	SetApp(a)
	if !discordGatewayEnabled() {
		return
	}
	rows, err := a.SysDB.Query(`SELECT workspace_id, id FROM discord_channels WHERE enabled AND bot_token_sealed <> ''`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var wsID, id string
		if rows.Scan(&wsID, &id) == nil {
			startGateway(a, wsID, id)
		}
	}
}

// startGateway (re)starts one connection's session: any previous session for
// the same connection is stopped first, so changing a token or toggling
// enabled off-and-on never leaves two sockets open for one row.
func startGateway(a *app.App, workspaceID, id string) {
	SetApp(a)
	if !discordGatewayEnabled() {
		return
	}
	stopGateway(workspaceID, id)

	sctx := discordCtx(workspaceID)
	c, botSealed, _, err := getDiscord(sctx, a.SQLDB, workspaceID, id)
	if err != nil || botSealed == "" || !c.Enabled {
		return
	}
	token, err := discordBotBox.Open(botSealed)
	if err != nil || token == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	sessMu.Lock()
	sessions[sessKey(workspaceID, id)] = &gatewaySession{cancel: cancel}
	sessMu.Unlock()
	go runGateway(ctx, workspaceID, id, token, c.AllowedGuildIDs, c.AllowedChannelIDs)
}

// stopGateway cancels a connection's session, if one is running.
func stopGateway(workspaceID, id string) {
	sessMu.Lock()
	s, ok := sessions[sessKey(workspaceID, id)]
	delete(sessions, sessKey(workspaceID, id))
	sessMu.Unlock()
	if ok {
		s.cancel()
	}
}

// ---- the gateway protocol -------------------------------------------------

type gatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int            `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

const (
	opDispatch           = 0
	opHeartbeat          = 1
	opIdentify           = 2
	opInvalidSession     = 9
	opHello              = 10
	opHeartbeatAck       = 11
	intentGuildMessages  = 1 << 9
	intentDirectMessages = 1 << 12
	intentMessageContent = 1 << 15
)

// discordMessageCreate is the MESSAGE_CREATE dispatch payload, flattened to
// what the inbox needs — the same shape TelegramUpdate.Read and
// SlackEnvelope.Read give their own callers.
type discordMessageCreate struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	Content   string `json:"content"`
	Author    struct {
		ID       string `json:"id"`
		Bot      bool   `json:"bot"`
		Username string `json:"username"`
	} `json:"author"`
}

// readDiscordMessage reports ok=false for a bot's own message or empty text —
// the same shape as the Telegram/Slack Read methods.
func readDiscordMessage(m discordMessageCreate) (channelID, handle, name, text, msgID string, ok bool) {
	if m.Author.Bot {
		return "", "", "", "", "", false
	}
	text = strings.TrimSpace(m.Content)
	if text == "" || m.ChannelID == "" {
		return "", "", "", "", "", false
	}
	return m.ChannelID, m.Author.ID, m.Author.Username, text, m.ID, true
}

// runGateway owns one connection's socket for its lifetime: connect, IDENTIFY,
// heartbeat on the interval HELLO gives, dispatch MESSAGE_CREATE, and — on any
// disconnect that ctx did not cause — reconnect with backoff. It returns only
// when ctx is cancelled (stopGateway, or the process shutting down).
func runGateway(ctx context.Context, workspaceID, id, token, allowedGuilds, allowedChannels string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		setGatewayState(workspaceID, id, "connecting")
		err := connectOnce(ctx, workspaceID, id, token, allowedGuilds, allowedChannels)
		if ctx.Err() != nil {
			setGatewayState(workspaceID, id, "disconnected")
			return
		}
		if err != nil {
			msg, fatal := gatewayErrorReason(err)
			recordDiscordError(workspaceID, id, msg)
			// Discord will refuse again until someone changes a setting (a
			// token, an intent): retry slowly rather than hammer it, so the
			// connection still comes up by itself once the switch is flipped.
			if fatal && backoff < 60*time.Second {
				backoff = 60 * time.Second
			}
		}
		setGatewayState(workspaceID, id, "disconnected")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

// gatewayErrorReason turns a gateway failure into what the connection card
// shows (MH-593): for the close codes that only a change on Discord's side can
// fix, the reason in plain words and exactly where to fix it, instead of
// "websocket: close 4014: Disallowed intent(s)". fatal reports that retrying
// unchanged cannot succeed. The texts fit last_error's 300 characters.
func gatewayErrorReason(err error) (msg string, fatal bool) {
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		return err.Error(), false
	}
	switch ce.Code {
	case 4004:
		return "Discord refused the bot token (4004). Reset the token in the Developer Portal under Bot, paste the new one here and save.", true
	case 4013:
		return "Discord refused the requested intents as invalid (4013). This is a Moharrik bug; please report it.", true
	case 4014:
		return "Discord refused the Message Content intent (4014), so no messages can arrive. In the Developer Portal open your app, go to Bot, turn on Message Content Intent under Privileged Gateway Intents and save. Moharrik reconnects within a minute.", true
	case 4010, 4011, 4012:
		return fmt.Sprintf("Discord closed the connection (%d: %s). Moharrik will keep retrying.", ce.Code, ce.Text), true
	}
	return fmt.Sprintf("Discord closed the connection (%d: %s). Reconnecting.", ce.Code, ce.Text), false
}

// connectOnce runs one connection attempt to completion (until the socket
// closes or ctx is cancelled). A session that reaches HELLO and IDENTIFY
// resets the backoff by simply having taken long enough to be worth retrying
// promptly — the caller resets nothing explicitly, keeping this function pure
// per attempt.
func connectOnce(ctx context.Context, workspaceID, id, token, allowedGuilds, allowedChannels string) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, gatewayURL(), nil)
	if err != nil {
		return fmt.Errorf("could not connect to the gateway: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	var hello struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	dec := payloadInto(&hello)
	if err := conn.ReadJSON(&dec); err != nil {
		return fmt.Errorf("no HELLO from the gateway: %w", err)
	}
	if hello.HeartbeatInterval <= 0 {
		hello.HeartbeatInterval = 41250
	}

	identify := gatewayPayload{Op: opIdentify, D: mustJSON(map[string]any{
		"token":   token,
		"intents": intentGuildMessages | intentDirectMessages | intentMessageContent,
		"properties": map[string]string{
			"os": "linux", "browser": "moharrik", "device": "moharrik",
		},
	})}
	if err := conn.WriteJSON(identify); err != nil {
		return fmt.Errorf("could not send IDENTIFY: %w", err)
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go heartbeatLoop(hbCtx, conn, time.Duration(hello.HeartbeatInterval)*time.Millisecond)

	for {
		var p gatewayPayload
		if err := conn.ReadJSON(&p); err != nil {
			return err
		}
		switch p.Op {
		case opDispatch:
			if p.T == "READY" {
				setGatewayState(workspaceID, id, "connected")
			}
			if p.T == "MESSAGE_CREATE" {
				var m discordMessageCreate
				if json.Unmarshal(p.D, &m) == nil {
					handleDiscordMessage(workspaceID, id, m, allowedGuilds, allowedChannels)
				}
			}
		case opInvalidSession:
			return fmt.Errorf("invalid session")
		case opHeartbeatAck:
			// nothing to do; absence of one is not currently watched for.
		}
	}
}

func handleDiscordMessage(workspaceID, id string, m discordMessageCreate, allowedGuilds, allowedChannels string) {
	channelID, handle, name, text, msgID, ok := readDiscordMessage(m)
	if !ok {
		return
	}
	if m.GuildID != "" && !discordAllowed(allowedGuilds, m.GuildID) {
		return
	}
	if !discordAllowed(allowedChannels, channelID) {
		return
	}
	markDiscordDelivery(workspaceID, id)
	sctx := discordCtx(workspaceID)
	if err := deliver(sctx, workspaceID, "discord", id, channelID, handle, name, text, "discord:"+msgID); err != nil {
		recordDiscordError(workspaceID, id, "could not file a message: "+err.Error())
	}
}

func heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := conn.WriteJSON(gatewayPayload{Op: opHeartbeat}); err != nil {
				return
			}
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("channels: could not encode a gateway payload: " + err.Error())
	}
	return b
}

// payloadInto adapts ReadJSON (which decodes the whole payload) to read just
// the "d" field of a HELLO into dst, without a second allocation-heavy type.
func payloadInto(dst any) helloDecoder { return helloDecoder{dst: dst} }

type helloDecoder struct{ dst any }

func (h *helloDecoder) UnmarshalJSON(b []byte) error {
	var p gatewayPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if len(p.D) == 0 {
		return nil
	}
	return json.Unmarshal(p.D, h.dst)
}
