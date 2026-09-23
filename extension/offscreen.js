/*
The socket, in an offscreen document (MH-478, ported from fadymondy.com's
offscreen/offscreen.js).

WHY NOT THE SERVICE WORKER: MV3 stops an idle service worker after about thirty
seconds, and the socket goes with it. Moharrik used to hold the socket in the
worker and ping every 20 seconds to keep it awake — which held while pings
flowed and lost the browser whenever Chrome decided otherwise, which a
schedule at 3am found out. An offscreen document outlives the worker, so the
browser stays reachable while the worker comes and goes.

The wire protocol is the server's (internal/automation/ws.go):

    ->  { id, type: "hello", payload: { token, fingerprint, platform, … } }
    <-  { id, type: "hello.ok", payload: { agent_id, name, allowed_hosts, … } }
    <-  { id, type: "run.step" | "pick" | "runs.sync" | …, run?, payload }
    ->  { id, type: "<type>.result", payload }  or  { id, type, error, code? }
    <-  { type: "run.end" | "agent.update", run?, payload }   (no reply)
    <-> { type: "ping" } / { type: "pong" }

Authentication is the FIRST FRAME: a browser cannot set a header on a
WebSocket handshake, and a token in the query string would be written to every
access log between here and the API.

This document owns the socket and nothing else. The worker owns the token and
the pause, and executes every command (it is the context with tabs, scripting
and the debugger).
*/

const STATE = {
  ws: null,
  url: null,
  connected: false,
  authed: false,
  attempt: 0,
  reconnectTimer: null,
  pingTimer: null,
  token: null,
  hello: null,
  agent: null,
  lastError: "",
}

const PING_INTERVAL_MS = 25_000
const BACKOFF_BASE_MS = 1_000
const BACKOFF_MAX_MS = 60_000

/*
Backoff with jitter. Without the jitter every browser that lost the same
network — or was asleep when the API restarted — comes back at exactly the
same instants and keeps hitting the API in lockstep.
*/
export function backoffDelay(attempt, random = Math.random) {
  const capped = Math.min(BACKOFF_BASE_MS * 2 ** attempt, BACKOFF_MAX_MS)
  return capped / 2 + random() * (capped / 2)
}

function post(event, data) {
  chrome.runtime.sendMessage({ type: "OFFSCREEN_EVENT", event, data }).catch(() => {
    // The worker is asleep. It reads the stored status when it wakes.
  })
}

function report(patch) {
  post("STATUS", {
    connected: STATE.connected,
    authed: STATE.authed,
    attempt: STATE.attempt,
    agent: STATE.agent,
    lastError: STATE.lastError,
    at: Date.now(),
    ...patch,
  })
}

function connect(url, token, hello) {
  if (url) STATE.url = url
  if (token) STATE.token = token
  if (hello) STATE.hello = hello

  clearTimeout(STATE.reconnectTimer)
  STATE.reconnectTimer = null

  if (!STATE.url || !STATE.token) {
    STATE.lastError = "not connected to a workspace"
    report({})
    return
  }
  if (STATE.ws && (STATE.ws.readyState === WebSocket.CONNECTING || STATE.ws.readyState === WebSocket.OPEN)) return

  let socket
  try {
    socket = new WebSocket(STATE.url)
  } catch (err) {
    STATE.lastError = String(err?.message || err)
    scheduleReconnect()
    report({})
    return
  }

  STATE.ws = socket
  socket.onopen = () => {
    if (STATE.ws !== socket) return
    STATE.connected = true
    STATE.lastError = ""
    // Straight away: the server closes a socket that has not said hello
    // within ten seconds.
    send({ id: "hello", type: "hello", payload: { token: STATE.token, ...STATE.hello } })
    startPing()
    report({})
  }
  socket.onmessage = (event) => {
    if (STATE.ws === socket) handleFrame(event.data)
  }
  socket.onerror = () => {
    // The event carries no detail by design; the close handler records why.
    STATE.lastError = STATE.lastError || "connection error"
  }
  socket.onclose = (event) => {
    if (STATE.ws !== socket) return // already replaced by a newer socket
    const wasAuthed = STATE.authed
    STATE.connected = false
    STATE.authed = false
    stopPing()
    STATE.ws = null

    // 1008: the server refused the token — revoked, unknown. Retrying cannot
    // fix that; the worker forgets the token and the popup says why.
    if (event.code === 1008) {
      STATE.lastError = event.reason || "refused by the site"
      report({ fatal: true, closeCode: event.code })
      return
    }
    // 1000: we closed it ourselves (pause, forget), or the server replaced it
    // with a newer socket for this same browser. Nothing to come back for.
    if (event.code === 1000) {
      STATE.lastError = ""
      report({ wasAuthed, closeCode: event.code })
      return
    }
    // Everything else — the network, an API restart, the kill switch (4001),
    // a re-connect elsewhere (4003) — is retried with backoff.
    STATE.lastError = event.reason || `disconnected (${event.code})`
    scheduleReconnect()
    report({ wasAuthed, closeCode: event.code })
  }
}

function scheduleReconnect() {
  if (STATE.reconnectTimer) return
  const delay = backoffDelay(STATE.attempt)
  STATE.attempt += 1
  report({ retryInMs: Math.round(delay) })
  STATE.reconnectTimer = setTimeout(() => {
    STATE.reconnectTimer = null
    connect()
  }, delay)
}

function handleFrame(raw) {
  let env
  try {
    env = JSON.parse(raw)
  } catch {
    return // an unreadable frame is dropped, not fatal
  }

  if (env.type === "hello.ok") {
    STATE.authed = true
    STATE.attempt = 0
    STATE.agent = env.payload || null
    report({})
    return
  }
  if (env.type === "pong") return
  if (env.type === "ping") {
    send({ id: env.id, type: "pong" })
    return
  }
  // The site's answer to a recording this browser sent: a reply, not a command.
  if (env.type === "recording.saved") {
    post("RECORDING_SAVED", { id: env.id, payload: env.payload ?? null, error: env.error || "" })
    return
  }
  // Pushed without an id, expecting nothing back: a run is over, or this
  // browser's name or allowed sites changed on the site.
  if (!env.id) {
    post("PUSH", { type: env.type, run: env.run || "", payload: env.payload ?? null })
    return
  }
  // Anything else with an id is a command, executed by the worker.
  void execute(env)
}

async function execute(env) {
  let reply
  try {
    const result = await chrome.runtime.sendMessage({
      type: "EXECUTE_COMMAND",
      command: env.type,
      payload: env.payload ?? null,
      run: typeof env.run === "string" ? env.run : "",
      id: env.id,
    })
    reply = result?.error
      ? { id: env.id, type: `${env.type}.result`, error: String(result.error), ...(result.code ? { code: String(result.code) } : {}) }
      : { id: env.id, type: `${env.type}.result`, payload: result?.payload ?? null }
  } catch (err) {
    // The worker did not answer. The server is waiting on this id, so it gets
    // an answer either way.
    reply = { id: env.id, type: `${env.type}.result`, error: String(err?.message || err) }
  }
  send(reply)
}

function send(frame) {
  if (STATE.ws && STATE.ws.readyState === WebSocket.OPEN) {
    STATE.ws.send(JSON.stringify(frame))
    return true
  }
  return false
}

/*
Application-level keepalive. Deliberately NOT a queue: nothing outbound is
buffered while the socket is down. A command that arrived before a disconnect
has been answered or lost, and replaying it minutes later against a page that
has moved on is worse than not answering.
*/
function startPing() {
  stopPing()
  STATE.pingTimer = setInterval(() => {
    if (!send({ type: "ping" })) stopPing()
  }, PING_INTERVAL_MS)
}

function stopPing() {
  clearInterval(STATE.pingTimer)
  STATE.pingTimer = null
}

function disconnect(reason = "paused") {
  clearTimeout(STATE.reconnectTimer)
  STATE.reconnectTimer = null
  STATE.attempt = 0
  stopPing()
  const ws = STATE.ws
  STATE.ws = null
  STATE.connected = false
  STATE.authed = false
  if (ws) {
    try {
      ws.close(1000, reason)
    } catch {
      /* already gone */
    }
  }
  report({ closeCode: 1000 })
}

if (globalThis.chrome?.runtime?.onMessage) {
  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (message?.target !== "offscreen") return false
    switch (message.command) {
      case "connect":
        connect(message.url, message.token, message.hello)
        break
      case "disconnect":
        disconnect(message.reason)
        break
      case "reconnect":
        // An explicit retry: drop any pending backoff so nobody waits out a
        // minute-long delay they can see counting.
        disconnect("reconnect")
        connect(message.url, message.token, message.hello)
        break
      case "status":
        report({})
        break
      case "send-recording":
        if (!send({ id: message.id, type: "recording", payload: message.recording })) {
          post("RECORDING_SAVED", { id: message.id, error: "this browser is not connected to the workspace" })
        }
        break
      case "halt":
        // "Stop all automation" from the popup: over the socket, because the
        // extension holds no session for the site's REST API.
        send({ type: "halt" })
        break
      default:
        break
    }
    sendResponse({ ok: true })
    return false
  })

  // The document may be recreated after a restart with the socket still
  // wanted; ask the worker what to do rather than assuming.
  post("READY", {})
}
