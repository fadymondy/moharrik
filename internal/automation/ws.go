package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The browser socket: GET /api/agents/ws.

AUTHENTICATION IS IN THE FIRST FRAME, not a header, and the platform forces
that: a browser's WebSocket API cannot set request headers, so
`Authorization: Bearer` is unavailable. The alternatives were a token in the
query string — which lands in every access log, proxy log and Referer header —
or the Sec-WebSocket-Protocol trick, which smuggles a credential through a
field meant for content negotiation. So the socket opens unauthenticated, must
send `hello` with its token within helloWait, and is closed otherwise. Nothing
secret ever appears in a URL.

The token is resolved on the OWNER pool, because it arrives with no workspace
in hand — the same shape as the chat widget key and the webhook source id. From
the moment the workspace is known, every statement runs on the app pool inside
tenant.WithWorkspace, so RLS applies to a browser exactly as to a request.
*/

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The caller is a Chrome extension (origin chrome-extension://<id>) holding
	// a token, not a page carrying cookies — an origin check would prove
	// nothing. The token in the first frame is the authentication, and no
	// cookie is read on this endpoint at all.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// TokenPrefix marks an agent token so it is recognisable in a list or a log.
const TokenPrefix = "moh_agt_"

// NewToken returns a fresh agent token and the prefix stored alongside its hash.
func NewToken() (token, prefix string) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail in practice, and a panic is far preferable
		// to minting a guessable token for a browser that drives someone's mail.
		panic("automation: no entropy for a token: " + err.Error())
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, token[:len(TokenPrefix)+6]
}

// HashToken is what gets stored. Same treatment as MCP tokens: the plaintext is
// shown once, at mint time, and never again.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func newID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		panic("automation: no entropy for a message id: " + err.Error())
	}
	return hex.EncodeToString(raw)
}

type helloPayload struct {
	Token            string `json:"token"`
	Fingerprint      string `json:"fingerprint"`
	Platform         string `json:"platform"`
	BrowserVersion   string `json:"browser_version"`
	ExtensionVersion string `json:"extension_version"`
	// What this build can do (MH-478): multi_tab, persistent_tabs, cdp. An
	// older extension sends none and is driven the way it always was.
	Capabilities []string `json:"capabilities"`
	TabCapacity  int      `json:"tab_capacity"`
}

// Handler upgrades the request and runs the browser's pumps.
func (h *Hub) Handler(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return // Upgrade has already answered
		}
		ws.SetReadLimit(maxMessageBytes)

		agent, ok := h.authenticate(r.Context(), a, ws)
		if !ok {
			_ = ws.Close()
			return
		}

		c := &conn{agent: agent, ws: ws, send: make(chan []byte, 32), pending: map[string]chan Envelope{}}
		h.add(c)
		h.setStatus(context.Background(), a, agent, "online")
		h.log.Info("browser connected", "agent", agent.AgentID, "workspace", agent.WorkspaceID, "name", agent.Name)

		go h.writePump(c)
		done := make(chan struct{})
		go h.syncLoop(a, c, done)
		h.readPump(a, c)
		close(done)
	}
}

// authenticate reads the first frame and resolves it to a browser row.
func (h *Hub) authenticate(ctx context.Context, a *app.App, ws *websocket.Conn) (Connected, bool) {
	_ = ws.SetReadDeadline(time.Now().Add(helloWait))

	_, raw, err := ws.ReadMessage()
	if err != nil {
		return Connected{}, false
	}

	var env Envelope
	if json.Unmarshal(raw, &env) != nil || env.Type != "hello" {
		writeClose(ws, websocket.ClosePolicyViolation, "expected hello")
		return Connected{}, false
	}
	var hello helloPayload
	if json.Unmarshal(env.Payload, &hello) != nil || hello.Token == "" {
		writeClose(ws, websocket.ClosePolicyViolation, "hello needs a token")
		return Connected{}, false
	}

	agent, hosts, err := lookupAgent(ctx, a.SysDB, HashToken(hello.Token))
	if errors.Is(err, ErrRevoked) {
		writeClose(ws, websocket.ClosePolicyViolation, "token revoked")
		return Connected{}, false
	}
	if err != nil {
		// An unknown token and a database error are answered the same way on
		// purpose: the difference is not the caller's business.
		writeClose(ws, websocket.ClosePolicyViolation, "unknown token")
		return Connected{}, false
	}

	// The kill switch, checked at connect as well as at run time — a paused
	// workspace should not have browsers sitting connected and ready.
	if paused, why, err := IsPaused(ctx, a, agent.WorkspaceID); err == nil && paused {
		writeClose(ws, CloseHalted, "automation is stopped: "+why)
		return Connected{}, false
	}

	// The extension reports what it is on every connect, so a browser upgrade is
	// reflected without re-pairing.
	sctx := tenant.WithWorkspace(ctx, agent.WorkspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE browser_agents SET
			fingerprint = COALESCE(NULLIF($3, ''), fingerprint),
			platform = COALESCE(NULLIF($4, ''), platform),
			browser_version = COALESCE(NULLIF($5, ''), browser_version),
			extension_version = COALESCE(NULLIF($6, ''), extension_version),
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		agent.WorkspaceID, agent.AgentID, hello.Fingerprint, hello.Platform, hello.BrowserVersion, hello.ExtensionVersion)

	if hello.Platform != "" {
		agent.Platform = hello.Platform
	}
	if hello.ExtensionVersion != "" {
		agent.Version = hello.ExtensionVersion
	}
	agent.ConnectedAt = time.Now().UTC()
	agent.Capabilities = hello.Capabilities
	agent.TabCapacity = hello.TabCapacity

	// The browser learns its own identity, and the hosts it may be driven
	// against: Chrome requires a click per host to grant access, and nobody
	// should have to type that list twice.
	//
	// And which workspace it belongs to (MH-478): a browser connected by
	// approval did not know its workspace when it asked, and the popup has to
	// be able to say where this browser answers to.
	if hosts == nil {
		hosts = []string{}
	}
	helloAck := map[string]any{
		"agent_id":       agent.AgentID,
		"name":           agent.Name,
		"allowed_hosts":  hosts,
		"workspace_name": agent.WorkspaceName,
		"workspace_slug": agent.WorkspaceSlug,
	}
	if latest := LatestExtension(); latest != nil {
		helloAck["latest_version"] = latest.Version
	}
	for k, v := range helloExtras() {
		helloAck[k] = v
	}
	ack, _ := json.Marshal(helloAck)
	frame, _ := json.Marshal(Envelope{ID: env.ID, Type: "hello.ok", Payload: ack})
	_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
	if ws.WriteMessage(websocket.TextMessage, frame) != nil {
		return Connected{}, false
	}
	return agent, true
}

func (h *Hub) readPump(a *app.App, c *conn) {
	defer func() {
		h.remove(c)
		_ = c.ws.Close()
		h.setStatus(context.Background(), a, c.agent, "offline")
		h.log.Info("browser disconnected", "agent", c.agent.AgentID, "name", c.agent.Name)
	}()

	_ = c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error { return c.ws.SetReadDeadline(time.Now().Add(pongWait)) })

	// last_seen_at is persisted on a timer rather than per frame: a chatty
	// browser would otherwise write to the database several times a second for
	// no one's benefit.
	lastPersist := time.Now()

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var env Envelope
		if json.Unmarshal(raw, &env) != nil {
			continue // a frame we cannot read is dropped, not fatal
		}

		switch {
		case env.ID != "" && c.deliver(env):
			// Handed to whoever was waiting for it.
		case env.Type == "ping":
			frame, _ := json.Marshal(Envelope{ID: env.ID, Type: "pong"})
			c.trySend(frame)
		case env.Type == "recording":
			// What the operator just did, on its way to becoming a workflow
			// (MH-235). Handled on this goroutine rather than detached: the
			// browser is waiting for the reply to know whether to clear what
			// it recorded, and losing a recording to a silent failure is the
			// one outcome worth avoiding here.
			h.saveRecording(a, c, env)
		case env.Type == "halt":
			// "Stop everything", pressed in a browser's own popup. Honoured from
			// any authenticated browser on purpose: a kill switch that only the
			// web app can throw is not there when the web app is not open.
			h.log.Warn("kill switch requested from a browser", "agent", c.agent.AgentID, "workspace", c.agent.WorkspaceID)
			go h.haltWorkspace(a, c.agent)
		default:
			h.log.Debug("unmatched frame", "agent", c.agent.AgentID, "type", env.Type)
		}

		if time.Since(lastPersist) > 30*time.Second {
			lastPersist = time.Now()
			h.touch(a, c.agent)
		}
	}
}

func (h *Hub) writePump(c *conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.ws.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if c.ws.WriteMessage(websocket.TextMessage, msg) != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if c.ws.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}

func (h *Hub) setStatus(ctx context.Context, a *app.App, agent Connected, status string) {
	sctx := tenant.WithWorkspace(ctx, agent.WorkspaceID)
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE browser_agents SET status = $3, last_seen_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, agent.WorkspaceID, agent.AgentID, status); err != nil {
		h.log.Warn("browser status write failed", "agent", agent.AgentID, "err", err)
	}
	// A browser connecting is the one thing nobody on the page can cause, so
	// nothing there has a reason to refetch: pairing a browser and watching it
	// say "Not connected" for ever was exactly that.
	if a.Events != nil {
		a.Events.Publish(agent.WorkspaceID, realtime.Event{Kind: "browser", Permission: authz.AutomationRead})
	}
}

func (h *Hub) touch(a *app.App, agent Connected) {
	sctx := tenant.WithWorkspace(context.Background(), agent.WorkspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE browser_agents SET last_seen_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, agent.WorkspaceID, agent.AgentID)
}

// haltWorkspace throws the kill switch on behalf of a browser that asked for it.
func (h *Hub) haltWorkspace(a *app.App, agent Connected) {
	ctx := context.Background()
	if err := Pause(ctx, a, agent.WorkspaceID, "", "stopped from "+agent.Name); err != nil {
		h.log.Error("kill switch from a browser failed", "workspace", agent.WorkspaceID, "err", err)
		return
	}
	CancelWorkspaceRuns(ctx, a, agent.WorkspaceID, "automation was stopped")
	h.DisconnectWorkspace(agent.WorkspaceID, "automation is stopped")
}

// lookupAgent resolves a token on the OWNER pool: the socket arrives with no
// workspace, so this one query is necessarily cross-tenant. It carries its own
// explicit predicate and returns only what identifies the browser.
func lookupAgent(ctx context.Context, sys *sql.DB, tokenHash string) (Connected, []string, error) {
	var c Connected
	var revoked sql.NullTime
	var hosts pgtext.Array
	err := sys.QueryRowContext(ctx, `
		SELECT b.id, b.workspace_id, b.name, b.platform, b.extension_version, b.revoked_at, b.allowed_hosts,
		       w.name, w.slug
		FROM browser_agents b JOIN workspaces w ON w.id = b.workspace_id
		WHERE b.token_hash = $1`, tokenHash,
	).Scan(&c.AgentID, &c.WorkspaceID, &c.Name, &c.Platform, &c.Version, &revoked, &hosts, &c.WorkspaceName, &c.WorkspaceSlug)
	if err != nil {
		return Connected{}, nil, err
	}
	if revoked.Valid {
		return Connected{}, nil, ErrRevoked
	}
	return c, hosts, nil
}

// saveRecording turns a pushed recording into a workflow and answers the
// browser with the result — or with the refusal, which the extension keeps so
// the operator can fix the allow-list and send it again rather than losing
// what they did.
func (h *Hub) saveRecording(a *app.App, c *conn, env Envelope) {
	reply := Envelope{ID: env.ID, Type: "recording.saved"}

	var rec Recording
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		reply.Error = "that recording could not be read"
		if frame, err := json.Marshal(reply); err == nil {
			c.trySend(frame)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	saved, err := SaveRecording(ctx, a, c.agent.WorkspaceID, c.agent.AgentID, rec)
	if err != nil {
		h.log.Warn("recording refused", "agent", c.agent.AgentID, "err", err)
		reply.Error = err.Error()
	} else {
		h.log.Info("recording saved as a workflow",
			"agent", c.agent.AgentID, "workflow", saved.WorkflowID, "steps", saved.Steps)
		reply.Payload, _ = json.Marshal(saved)
	}

	if frame, err := json.Marshal(reply); err == nil {
		c.trySend(frame)
	}
}
