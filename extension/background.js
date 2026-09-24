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
import { badgeFor, grantState, newerThan } from "./grants.js"
import { COMMANDS, closeRun, runStep, syncRuns } from "./driver.js"
import { fillMatched, matchProfileToFields, readForm } from "./driver/autofill.js"
import { tabCapacity } from "./driver/runs.js"
import { RUN_TABS_KEY } from "./driver/run-tabs-core.js"
import { hostAllowed } from "./hosts.js"
import { cancelPicks, pickElement } from "./pick.js"
import { addStep, discardRecording, recordingState, startRecording, stopRecording } from "./recorder.js"

// What this build tells the server at hello. "multi_tab": every run gets its
// own tab, so the server may run several at once here. "persistent_tabs":
// kept site tabs by key. "cdp": input goes through the debugger.
const CAPABILITIES = ["multi_tab", "persistent_tabs", "cdp"]

const OFFSCREEN_PATH = "offscreen.html"
const KEEPALIVE = "moh-keepalive"
const UPDATE_CHECK = "moh-update-check"

// Woken every minute so a dead socket is noticed while the browser is idle.
// Alarms are the only timer MV3 guarantees across worker restarts; a minute
// is Chrome's floor for one.
chrome.alarms.create(KEEPALIVE, { periodInMinutes: 1 })
// Whether a newer extension is out, every six hours and on every connect.
chrome.alarms.create(UPDATE_CHECK, { periodInMinutes: 360 })
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === UPDATE_CHECK) void checkForUpdate()
  if (a.name !== KEEPALIVE) return
  void applyUpdateWhenIdle()
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
      // The workspace profile (MH-478): what the Application autofill panel
      // fills a form with, and a preset's job-application inputs default to.
      profile: ack.profile && typeof ack.profile === "object" ? ack.profile : {},
      // How the runtime is to behave here (driver/runs.js, driver/tabs.js).
      tabsPerBrowser: ack.tabs_per_browser,
      persistentTabs: Array.isArray(ack.persistent_tabs) ? ack.persistent_tabs : undefined,
      automationWindow: ack.automation_window,
    })
    await setStatus({ state: "connected", error: "", retryInMs: 0 })
    // Told to the server on every connect: the page's "which mode" is only as
    // fresh as the last thing this browser said.
    lastSentState = ""
    void refreshGrants()
    void checkForUpdate()
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

/* ------------------------------------------------- Chrome's grant (MH-483) */

let lastSentState = ""

/*
Where this browser stands against its workspace's list: every site granted
("all"), or site by site with some still waiting for an OK. Kept in storage for
the popup and the Sites page, painted on the toolbar icon, and told to the
server so the Automation page can say it too.
*/
async function refreshGrants() {
  const agent = (await get(KEYS.agent)) || {}
  const state = await grantState(chrome.permissions, Array.isArray(agent.allowedHosts) ? agent.allowedHosts : [])
  const prev = (await get(KEYS.grants)) || {}
  if (prev.mode !== state.mode || JSON.stringify(prev.pending || []) !== JSON.stringify(state.pending)) {
    await set(KEYS.grants, { ...state, at: Date.now() })
  }
  await paintBadge(state)
  const wire = JSON.stringify(state)
  if (wire !== lastSentState && (await get(KEYS.token))) {
    lastSentState = wire
    await toOffscreen({ command: "agent-state", state: { site_access: state.mode, pending_hosts: state.pending } })
  }
  return state
}

async function paintBadge(state) {
  const badge = badgeFor(state || (await get(KEYS.grants)), await get(KEYS.update))
  await chrome.action.setBadgeText({ text: badge.text }).catch(() => {})
  await chrome.action.setTitle({ title: badge.title }).catch(() => {})
}

/*
Is a newer extension out? Asked of the site's public /latest (no session
needed), on connect and every six hours. A browser installed from the signed
package by policy updates itself; everyone else is told, with a badge and a
one-click download in the popup.
*/
async function checkForUpdate() {
  const settings = await loadSettings()
  const origin = (settings.apiOrigin || settings.siteOrigin).replace(/\/+$/, "")
  const current = chrome.runtime.getManifest().version
  try {
    const res = await fetch(`${origin}/api/automation/extension/latest`, { cache: "no-store" })
    if (!res.ok) return
    const body = await res.json()
    const latest = String(body.version || "")
    const available = !!latest && newerThan(latest, current)
    await set(KEYS.update, { latest, current, available, signed: body.signed === true, checkedAt: Date.now() })
    // Installed from the signed package, Chrome can fetch the new version now
    // rather than at its own next check, hours away; onUpdateAvailable then
    // applies it once no run holds a tab. Loaded unpacked, Chrome answers that
    // there is nothing to update, and the popup's download is the way.
    if (available && body.signed === true && chrome.runtime.requestUpdateCheck) {
      chrome.runtime.requestUpdateCheck().catch(() => {})
    }
  } catch {
    return // the site is unreachable; the next check tries again
  }
  await paintBadge()
}

/*
A browser installed from the signed package (a managed Chrome) is updated by
Chrome itself — but Chrome holds a new version back while the extension is
busy, and the offscreen document keeps it busy for good. So the update is
applied here, the moment no run holds a tab: a run is never cut off halfway,
and the browser does not sit on an old version until someone restarts Chrome.
*/
chrome.runtime.onUpdateAvailable.addListener((details) => {
  void set("moh.updateReady", details?.version || "?").then(applyUpdateWhenIdle)
})

async function applyUpdateWhenIdle() {
  if (!(await get("moh.updateReady"))) return
  const inFlight = Object.keys((await get(RUN_TABS_KEY)) || {}).length
  if (inFlight > 0) return // the keepalive alarm asks again in a minute
  await clear("moh.updateReady")
  chrome.runtime.reload()
}

chrome.permissions.onAdded.addListener(() => {
  void refreshGrants()
  void startFromIntent()
})
chrome.permissions.onRemoved.addListener(() => void refreshGrants())

/*
A Connect click asks Chrome for every site and connects in the same click. The
permission prompt can outlive the popup that asked (Chrome may close a popup
the moment its prompt takes focus), so the popup leaves an intent behind, and
the grant arriving here starts the connect if the popup could not.
*/
async function startFromIntent() {
  const intent = await get(KEYS.connectIntent)
  if (!intent || Date.now() - (intent.at || 0) > 5 * 60_000) return
  if (await get(KEYS.token)) return
  await startConnect(intent.languages || [])
}

let connectStarting = null

/** Start connecting once, however many clicks and grants ask for it. */
async function startConnect(languages) {
  await clear(KEYS.connectIntent)
  const pairing = await get(KEYS.pairing)
  if (pairing?.status === "pending" && Date.now() - (pairing.startedAt || 0) < 15_000) {
    return { code: pairing.code, expiresAt: pairing.expiresAt }
  }
  connectStarting ??= connector.start({ languages }).finally(() => {
    connectStarting = null
  })
  return connectStarting
}

/* ------------------------------------------------------- application autofill

Generalised from fadymondy.com's job-application autofill (FM-143/199): fills
the CURRENT tab's form from the workspace profile, on an allowed site only,
and never submits anything. Unlike a run, this touches no automation tab and
opens no page — see driver/autofill.js for the field reader and the rules.
*/

const AUTOFILL_KEY = "moh.autofill"

async function setAutofill(patch) {
  const current = (await get(AUTOFILL_KEY)) || {}
  await set(AUTOFILL_KEY, { ...current, ...patch, at: Date.now() })
}

/**
 * The tab the popup acts on: the real page the person had open, never this
 * extension's own popup, options or approval pages. The action popup is not
 * a tab in a normal browser — chrome.tabs.query never sees it — but a test
 * harness that drives it as one would otherwise shadow the actual page, so
 * anything on chrome-extension:// is filtered out here rather than trusted
 * blindly as "whatever is active".
 */
async function activeTab() {
  const tabs = await chrome.tabs.query({ lastFocusedWindow: true })
  const real = tabs.filter((t) => !String(t.url || "").startsWith("chrome-extension://"))
  return real.find((t) => t.active) || real[real.length - 1] || null
}

/**
 * May this tab be autofilled at all? The same two gates a run checks
 * (MH-422): the workspace's allowed sites, and Chrome's own host permission —
 * without the second, scripting.executeScript cannot reach the page.
 */
async function autofillAllowed(tab) {
  if (!tab?.url) return "this tab has no page to fill"
  let url
  try {
    url = new URL(tab.url)
  } catch {
    return "this tab has no page to fill"
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return `cannot fill a ${url.protocol}// page`
  const agent = (await get(KEYS.agent)) || {}
  const hosts = Array.isArray(agent.allowedHosts) ? agent.allowedHosts : []
  if (!hostAllowed(hosts, url.hostname)) {
    return `${url.hostname} is not on this browser's allowed sites — add it on the workspace's Automation page`
  }
  if (!(await chrome.permissions.contains({ origins: [`${url.origin}/*`] }))) {
    return `Chrome has not allowed this extension on ${url.hostname} yet — allow it on the Sites page`
  }
  return ""
}

/** Reads the current tab's form and matches it against the workspace profile: the preview. */
async function autofillPreview() {
  const tab = await activeTab()
  const refused = await autofillAllowed(tab)
  if (refused) return { error: refused }
  const profile = ((await get(KEYS.agent)) || {}).profile || {}
  if (Object.keys(profile).length === 0) {
    return { error: "the workspace profile is empty — an admin fills it in on Settings → Application profile" }
  }
  let form
  try {
    ;[{ result: form } = {}] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: readForm })
  } catch (err) {
    return { error: String(err?.message || err) }
  }
  if (!form) return { error: "could not read this page" }
  const matches = matchProfileToFields(form.fields, profile)
  await setAutofill({ state: "previewed", tabId: tab.id, url: form.url, matches, filled: null })
  return { ok: true, tabId: tab.id, url: form.url, fieldCount: form.fields.length, matches }
}

/**
 * Fills exactly the matches given (a subset the popup's checkboxes chose from
 * the last preview) — never recomputed, and only on the SAME tab the preview
 * read. Nothing here can click, press a key or navigate.
 */
async function autofillFill(matches) {
  const tab = await activeTab()
  const refused = await autofillAllowed(tab)
  if (refused) return { error: refused }
  const current = await get(AUTOFILL_KEY)
  if (!current || current.tabId !== tab.id) {
    return { error: "the page changed since Preview — press Preview again" }
  }
  const chosen = (Array.isArray(matches) ? matches : current.matches || []).filter((m) => m?.sel)
  if (chosen.length === 0) return { error: "nothing was chosen to fill" }
  let results
  try {
    ;[{ result: results } = {}] = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: fillMatched,
      args: [chosen],
    })
  } catch (err) {
    return { error: String(err?.message || err) }
  }
  const filled = (results || []).filter((r) => r.ok).length
  const skipped = (results || []).filter((r) => !r.ok)
  await setAutofill({ state: "filled", filled, skipped })
  return { ok: true, filled, skipped }
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
      // The workspace profile changed (Settings → Application profile): the
      // popup's Application panel reads it fresh next time it opens.
      ...(payload.profile && typeof payload.profile === "object" ? { profile: payload.profile } : {}),
    })
    // With every site granted, a site added on the website is usable from the
    // next step; without, it joins the "waiting for your OK" list at once.
    await refreshGrants()
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
    startConnect(msg.languages || [])
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
  if (msg?.type === "grants.refresh") {
    refreshGrants().then(reply)
    return true
  }
  if (msg?.type === "update.check") {
    checkForUpdate().then(async () => reply((await get(KEYS.update)) || {}))
    return true
  }
  if (msg?.type === "disconnect") {
    toOffscreen({ command: "disconnect", reason: "forgotten" }).then(async () => {
      await setStatus({ state: "unpaired", error: "" })
      reply({ ok: true })
    })
    return true
  }
  if (msg?.type === "autofill.preview") {
    autofillPreview().then(reply)
    return true
  }
  if (msg?.type === "autofill.fill") {
    autofillFill(msg.matches).then(reply)
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
void refreshGrants()
// A worker that has not asked for a while asks now, so a browser that was off
// for days learns about a new version when it wakes, not six hours later.
void get(KEYS.update).then((u) => {
  if (!u?.checkedAt || Date.now() - u.checkedAt > 15 * 60_000) void checkForUpdate()
})
