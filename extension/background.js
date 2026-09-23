/*
The service worker (MH-478): custody of the token, connecting by approval, the
offscreen document that holds the socket, this browser's pause, and command
dispatch. Ported in shape from fadymondy.com's background/service-worker.js.

Division of labour:
  offscreen.js   owns the WebSocket and nothing else: hello-frame auth,
                 reconnect with jittered backoff, keepalive.
  this file      owns the token, connecting, pause, and executes every command
                 — it is the context with tabs, scripting and the debugger.
  driver.js      does the work a step asks for, inside the allowed sites
                 (MH-422) and through real input (driver/cdp.js).

The token is in chrome.storage.local, the only storage an extension has that
survives a restart. It is not encrypted there and cannot usefully be: whatever
would decrypt it would sit beside it. What it can do is bounded at the other
end — revocable from the site the moment a browser looks wrong, and revoking
drops the socket.

State the popup shows lives in storage (moh.status, moh.agent), never in a
worker variable: the worker is stopped constantly, and a popup opening against
a fresh worker would otherwise say "unknown" every time.
*/

import { KEYS, clear, get, loadSettings, set, socketURL } from "./config.js"
import { createConnect } from "./connect.js"
import { COMMANDS, closeRun, runStep, syncRuns } from "./driver.js"
import { tabCapacity } from "./driver/runs.js"
import { cancelPicks, pickElement } from "./pick.js"
import { addStep, discardRecording, recordingState, startRecording, stopRecording } from "./recorder.js"

// What this build tells the server at hello. "multi_tab": every run gets its
// own tab, so the server may run several at once here. "persistent_tabs":
// kept site tabs by key. "cdp": input goes through the debugger.
const CAPABILITIES = ["multi_tab", "persistent_tabs", "cdp"]

const OFFSCREEN_PATH = "offscreen.html"
const KEEPALIVE = "moh-keepalive"

// Woken every minute so a dead socket is noticed while the browser is idle.
// Alarms are the only timer MV3 guarantees across worker restarts; a minute
// is Chrome's floor for one.
chrome.alarms.create(KEEPALIVE, { periodInMinutes: 1 })
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name !== KEEPALIVE) return
  void ensureConnected()
  // A worker stopped while waiting for an approval picks the wait up again.
  void connector.resume()
})
chrome.runtime.onStartup.addListener(() => void ensureConnected())
chrome.runtime.onInstalled.addListener(() => void ensureConnected())

/* ------------------------------------------------------------ connecting */

/*
Connecting by approval (MH-478). The popup's "Connect to the site" lands in
connect.js, which opens the site's approval page and waits here for the
decision — the popup is gone the moment the new tab opens.
*/
const connector = createConnect({
  settings: loadSettings,
  get,
  set,
  clear,
  fetch: (url, init) => fetch(url, init),
  openTab: async (url) => {
    await chrome.tabs.create({ url, active: true })
  },
  describe: async () => ({
    name: await suggestedName(),
    fingerprint: await fingerprint(),
    platform: await platformName(),
    browser_version: browserVersion(),
    extension_version: chrome.runtime.getManifest().version,
  }),
  sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
  keys: { pairing: KEYS.pairing, token: KEYS.token, agent: KEYS.agent },
})

/* ------------------------------------------------------------- the socket */

async function hello() {
  return {
    fingerprint: await fingerprint(),
    platform: await platformName(),
    browser_version: browserVersion(),
    extension_version: chrome.runtime.getManifest().version,
    capabilities: CAPABILITIES,
    tab_capacity: tabCapacity(),
  }
}

async function ensureConnected() {
  const token = await get(KEYS.token)
  if (!token) {
    await setStatus({ state: "unpaired" })
    return
  }
  if ((await get(KEYS.paused)) === true) {
    await toOffscreen({ command: "disconnect", reason: "paused" })
    await setStatus({ state: "held", error: "" })
    return
  }
  const settings = await loadSettings()
  await openOffscreen()
  await toOffscreen({ command: "connect", url: socketURL(settings), token, hello: await hello() })
}

async function reconnect() {
  const token = await get(KEYS.token)
  if (!token || (await get(KEYS.paused)) === true) return ensureConnected()
  const settings = await loadSettings()
  await openOffscreen()
  await toOffscreen({ command: "reconnect", url: socketURL(settings), token, hello: await hello() })
}

let offscreenReady = null

async function openOffscreen() {
  if (await hasOffscreen()) return
  // Two callers at once must not both create it — Chrome throws on the second.
  offscreenReady ??= chrome.offscreen
    .createDocument({
      url: OFFSCREEN_PATH,
      reasons: ["WORKERS"],
      justification: "Holds the WebSocket to the Moharrik site so this browser stays reachable while the service worker is idle.",
    })
    .catch((err) => {
      if (!String(err?.message || err).includes("Only a single offscreen")) throw err
    })
    .finally(() => {
      offscreenReady = null
    })
  await offscreenReady
}

async function hasOffscreen() {
  const contexts = await chrome.runtime.getContexts({ contextTypes: ["OFFSCREEN_DOCUMENT"] })
  return contexts.length > 0
}

async function toOffscreen(message) {
  try {
    await chrome.runtime.sendMessage({ target: "offscreen", ...message })
  } catch {
    // No document yet, or it is being recreated; the next alarm tries again.
  }
}

/* ----------------------------------------------------------------- status */

async function setStatus(patch) {
  const current = (await get(KEYS.status)) || {}
  await set(KEYS.status, { ...current, ...patch, at: Date.now() })
}

/** What the offscreen document reports, turned into what the popup reads. */
async function onSocketStatus(s) {
  if (s.agent && s.authed) {
    const ack = s.agent
    const prev = (await get(KEYS.agent)) || {}
    await set(KEYS.agent, {
      ...prev,
      id: ack.agent_id,
      name: ack.name,
      allowedHosts: Array.isArray(ack.allowed_hosts) ? ack.allowed_hosts : [],
      // Which workspace this browser answers to: a browser connected by
      // approval did not know it when it asked.
      workspaceName: ack.workspace_name || "",
      workspaceSlug: ack.workspace_slug || "",
      latestVersion: ack.latest_version || "",
      // How the runtime is to behave here (driver/runs.js, driver/tabs.js).
      tabsPerBrowser: ack.tabs_per_browser,
      persistentTabs: Array.isArray(ack.persistent_tabs) ? ack.persistent_tabs : undefined,
      automationWindow: ack.automation_window,
    })
    await setStatus({ state: "connected", error: "", retryInMs: 0 })
    return
  }
  if (s.fatal) {
    // The site refused this browser's token: it was revoked, or replaced when
    // the browser was connected again. Retrying cannot fix that.
    await set(KEYS.token, "")
    await setStatus({ state: "unpaired", error: s.lastError || "this browser is no longer connected" })
    cancelPicks()
    return
  }
  if ((await get(KEYS.paused)) === true) {
    await setStatus({ state: "held", error: "" })
    return
  }
  if (!(await get(KEYS.token))) return
  if (s.connected && !s.authed) {
    await setStatus({ state: "connecting" })
    return
  }
  if (s.closeCode === 1000) {
    await setStatus({ state: "offline", error: "" })
    return
  }
  // 4001 is the kill switch: temporary by design, retried until someone
  // resumes automation on the site.
  await setStatus({ state: s.closeCode === 4001 ? "stopped" : "offline", error: s.lastError || "", retryInMs: s.retryInMs || 0 })
  // Nothing waits for a pick's answer any more — and a kill switch must not
  // leave a picking tab open on someone's signed-in page.
  cancelPicks()
}

/* --------------------------------------------------------------- commands */

/** Every command the server can send, answered by the worker. */
async function execute({ command, payload, run }) {
  if ((await get(KEYS.paused)) === true) return { error: "this browser is paused" }
  const started = Date.now()
  try {
    const out = await handle(command, payload || {}, run)
    if (command === "run.step") {
      await setStatus({ lastCommand: { type: String(payload?.command || ""), at: started, ok: true, ms: Date.now() - started } })
    }
    return { payload: out }
  } catch (err) {
    const reason = String(err?.message || err).slice(0, 500)
    if (command === "run.step") {
      await setStatus({ lastCommand: { type: String(payload?.command || ""), at: started, ok: false, error: reason.slice(0, 120) } })
    }
    // A code travels with the words when the failure is one the server must
    // act on rather than only record: left_allowed_sites (MH-422) stops the
    // run even where the step said to continue on error.
    return { error: reason, ...(err?.code ? { code: String(err.code) } : {}) }
  }
}

async function handle(command, payload, run) {
  switch (command) {
    case "run.step":
      return runStep(run, payload)
    case "pick":
      // The element picker (MH-400): someone editing a workflow clicks the
      // element on the live page.
      return pickElement(payload)
    case "runs.sync":
      // The server's list of this browser's runs in flight: tabs held for any
      // other run are given back (their run.end was lost).
      return syncRuns(payload.active)
    case "agent.ping":
      return { ok: true, pong: true, at: new Date().toISOString() }
    case "agent.info": {
      const granted = await chrome.permissions.getAll()
      return {
        ok: true,
        extension_version: chrome.runtime.getManifest().version,
        platform: await platformName(),
        allowed_origins: granted.origins ?? [],
        commands: COMMANDS,
        capabilities: CAPABILITIES,
      }
    }
    default:
      // Named, so the site can tell "this browser is old" from "it refused".
      throw new Error(`unsupported command: ${command}`)
  }
}

/** Frames the server pushes and expects no answer to. */
async function onPush({ type, run, payload }) {
  if (type === "run.end" && run) {
    await closeRun(run)
    return
  }
  if (type === "agent.update" && payload) {
    // A name or the allowed sites changed on the site: the popup, the options
    // page and the recorder read them from here.
    const prev = (await get(KEYS.agent)) || {}
    await set(KEYS.agent, {
      ...prev,
      ...(typeof payload.name === "string" ? { name: payload.name } : {}),
      ...(Array.isArray(payload.allowed_hosts) ? { allowedHosts: payload.allowed_hosts } : {}),
    })
  }
}

/* --------------------------------------------------------------- messages */

chrome.runtime.onMessage.addListener((msg, sender, reply) => {
  if (msg?.type === "OFFSCREEN_EVENT") {
    const data = msg.data || {}
    if (msg.event === "READY") void ensureConnected()
    else if (msg.event === "STATUS") void onSocketStatus(data)
    else if (msg.event === "PUSH") void onPush(data)
    else if (msg.event === "RECORDING_SAVED") settleRecording(data)
    reply({ ok: true })
    return false
  }
  if (msg?.type === "EXECUTE_COMMAND") {
    execute(msg).then(reply)
    return true // answered asynchronously
  }

  // From the popup.
  if (msg?.type === "connect") {
    reconnect().then(() => reply({ ok: true }))
    return true
  }
  if (msg?.type === "connect.start") {
    connector
      .start({ languages: msg.languages || [] })
      .then((out) => reply({ ok: true, ...out }))
      .catch((e) => reply({ ok: false, error: String(e?.message || e) }))
    return true
  }
  if (msg?.type === "connect.reopen") {
    connector.reopen().then((ok) => reply({ ok }))
    return true
  }
  if (msg?.type === "connect.cancel") {
    connector.cancel().then(() => reply({ ok: true }))
    return true
  }
  if (msg?.type === "disconnect") {
    toOffscreen({ command: "disconnect", reason: "forgotten" }).then(async () => {
      await setStatus({ state: "unpaired", error: "" })
      reply({ ok: true })
    })
    return true
  }
  if (msg?.type === "halt") {
    // "Stop everything", from this browser. Honoured by the server for the
    // whole workspace: a kill switch only the web app can throw is not there
    // when the web app is not open.
    toOffscreen({ command: "halt" }).then(() => reply({ ok: true }))
    return true
  }

  // The recorder (MH-235). The page half sends record.step on every action and
  // record.stop when the operator presses Stop on the in-page bar.
  if (msg?.type === "record.step") {
    addStep(sender?.tab?.id, msg.step)
      .then((count) => reply({ count }))
      .catch(() => reply({}))
    return true
  }
  if (msg?.type === "record.start") {
    startRecording()
      .then(reply)
      .catch((e) => reply({ error: String(e?.message || e) }))
    return true
  }
  if (msg?.type === "record.stop") {
    stopRecording({ sendToSite: sendRecording })
      .then(reply)
      .catch((e) => reply({ error: String(e?.message || e) }))
    return true
  }
  if (msg?.type === "record.discard") {
    discardRecording().then(() => reply({ ok: true }))
    return true
  }
  if (msg?.type === "record.state") {
    recordingState().then(reply)
    return true
  }
  return false
})

// The token appearing IS the signal to connect, and the pause switch is
// watched the same way: a popup that closes a moment too early must not leave
// a paired browser silent until the next alarm.
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local") return
  if (KEYS.token in changes && changes[KEYS.token].newValue) void ensureConnected()
  if (KEYS.paused in changes) void ensureConnected()
})

/*
Sending a recording, and waiting for the site's answer (MH-235). It REJECTS
rather than resolving silently on failure, because the caller keeps the
recording when it fails: losing a task the operator just performed by hand is
the one outcome worth engineering against here.
*/
const pendingRecordings = new Map()

async function sendRecording(payload) {
  const status = (await get(KEYS.status)) || {}
  if (status.state !== "connected") throw new Error("this browser is not connected to the workspace — reconnect, then send it again")
  const id = `rec-${crypto.randomUUID()}`
  const reply = new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pendingRecordings.delete(id)
      reject(new Error("the workspace did not answer in time — send it again"))
    }, 20000)
    pendingRecordings.set(id, { resolve, reject, timer })
  })
  await toOffscreen({ command: "send-recording", id, recording: payload })
  return reply
}

function settleRecording(data) {
  const waiter = pendingRecordings.get(data.id)
  if (!waiter) return
  pendingRecordings.delete(data.id)
  clearTimeout(waiter.timer)
  if (data.error) waiter.reject(new Error(data.error))
  else waiter.resolve(data.payload || {})
}

/* ---------------------------------------------------------------- helpers */

/**
 * A stable id for this browser profile, so a reinstall is recognised as the
 * same browser rather than a second one. Random and stored — NOT derived from
 * anything about the machine. (chrome.storage, not localStorage: a service
 * worker has none.)
 */
async function fingerprint() {
  let id = await get("moh.fp")
  if (!id) {
    id = crypto.randomUUID()
    await set("moh.fp", id)
  }
  return id
}

async function platformName() {
  try {
    const info = await chrome.runtime.getPlatformInfo()
    const os = { win: "Windows", mac: "macOS", linux: "Linux", cros: "ChromeOS" }[info.os] || info.os
    return `${os} ${info.arch}`
  } catch {
    return navigator.platform || "unknown"
  }
}

function browserVersion() {
  const match = navigator.userAgent.match(/Chrom(?:e|ium)\/([\d.]+)/)
  return match ? `Chrome ${match[1]}` : navigator.userAgent.slice(0, 60)
}

/** A first guess at a name; whoever approves renames it. */
async function suggestedName() {
  try {
    const info = await chrome.runtime.getPlatformInfo()
    return `${{ win: "Windows", mac: "Mac", linux: "Linux", cros: "ChromeOS" }[info.os] || info.os} browser`
  } catch {
    return "Browser"
  }
}

// A worker that starts for any reason should get the socket back up.
void ensureConnected()
void connector.resume()
