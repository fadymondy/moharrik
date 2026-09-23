/*
The popup (MH-478), section by section after fadymondy.com's: this browser,
its workspace, the connection, the last command, the pause switch, the
recorder, and the actions.

Its one rule, carried over: it states the truth about the connection. Every
line is read from state the worker and the socket wrote to storage, and "I do
not know" is rendered as such, never as "fine" — a popup that says connected
because it once was sends people to schedule work that will never run.
*/

import { DEFAULTS, KEYS, clear, get, loadSettings, saveSettings } from "./config.js"

const $ = (id) => document.getElementById(id)

// The site origins this popup last rendered. Kept so the Connect click can ask
// Chrome for permission as its FIRST await: Chrome honours a permission request
// only inside the click's user gesture.
let wanted = []

function hostOf(origin) {
  try {
    return new URL(origin).host
  } catch {
    return origin
  }
}

function ago(at) {
  if (!at) return ""
  const s = Math.max(0, Math.round((Date.now() - at) / 1000))
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  return `${Math.round(s / 86400)}d ago`
}

function newerThan(a, b) {
  const pa = String(a).split(".").map(Number)
  const pb = String(b).split(".").map(Number)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] || 0
    const y = pb[i] || 0
    if (x !== y) return x > y
  }
  return false
}

async function read() {
  const [settings, status, agent, token, pairing, paused, recording] = await Promise.all([
    loadSettings(),
    get(KEYS.status),
    get(KEYS.agent),
    get(KEYS.token),
    get(KEYS.pairing),
    get(KEYS.paused),
    get("moh.recording"),
  ])
  return {
    settings,
    status: status || {},
    agent: agent || {},
    paired: !!token,
    pairing: pairing || null,
    paused: paused === true,
    recording: recording || null,
  }
}

function render(view) {
  const { settings, status, agent, paired, pairing, paused } = view
  const host = hostOf(settings.siteOrigin)
  $("site").value = settings.siteOrigin
  $("api").value = settings.apiOrigin
  wanted = [`${settings.siteOrigin}/*`]
  if (settings.apiOrigin) wanted.push(`${settings.apiOrigin}/*`)

  $("agent-name").textContent = paired ? agent.name || "connected, name not known yet" : "—"
  $("workspace-row").hidden = !(paired && agent.workspaceName)
  $("workspace-name").textContent = agent.workspaceName || ""

  const waiting = !paired && pairing?.status === "pending"
  let state = "not connected"
  let dot = ""
  let detail = ""
  let bad = false

  if (waiting) {
    state = "waiting for approval"
    dot = "dot--wait"
    const mins = Math.max(0, Math.round((new Date(pairing.expiresAt).getTime() - Date.now()) / 60000))
    detail = `Approve it on ${host} — the page opened in a new tab. Check it shows the same code. It expires in ${mins} minute${mins === 1 ? "" : "s"}.`
  } else if (!paired && pairing?.status === "denied") {
    state = "refused"
    dot = "dot--off"
    bad = true
    detail = "The request was refused on the site. Connect again to ask a second time."
  } else if (!paired && (pairing?.status === "expired" || pairing?.status === "claimed")) {
    state = "not connected"
    dot = "dot--off"
    bad = true
    detail = "Nobody approved it in time. Connect again for a new code."
  } else if (!paired) {
    detail = status.error
      ? `Cannot reach ${host}: ${status.error}.`
      : `This browser is not connected to ${host} yet. Connecting opens the site, where you approve it into one of your workspaces.`
    bad = !!status.error
    if (status.error) dot = "dot--off"
  } else if (paused) {
    state = "paused"
    dot = "dot--off"
    detail = "You paused this browser. Nothing runs in it until you switch it back."
  } else if (status.state === "connected") {
    state = "connected"
    dot = "dot--on"
    detail = `Signed in to ${host} as this browser.`
  } else if (status.state === "stopped") {
    state = "automation stopped"
    dot = "dot--off"
    bad = true
    detail = `Automation is stopped for this workspace on ${host}${status.error ? ` (${status.error})` : ""}. It reconnects by itself when someone resumes it there.`
  } else {
    state = "disconnected"
    dot = "dot--off"
    bad = true
    detail = status.error ? `Cannot reach ${host}: ${status.error}.` : `Cannot reach ${host}. Retrying.`
  }

  $("state").textContent = state
  $("dot").className = `dot ${dot}`
  $("detail").textContent = detail
  $("detail").classList.toggle("detail--bad", bad)

  $("waiting").hidden = !waiting
  if (waiting) $("code").textContent = pairing.code

  const last = status.lastCommand
  $("last-row").hidden = !paired
  $("last-command").textContent = last
    ? `${last.type} · ${last.ok ? `${last.ms ?? 0}ms` : `failed: ${last.error ?? "error"}`} · ${ago(last.at)}`
    : "none yet"

  const latest = agent.latestVersion
  const current = chrome.runtime.getManifest().version
  const outdated = paired && latest && newerThan(latest, current)
  $("update").hidden = !outdated
  if (outdated) {
    $("update").textContent = `Version ${latest} is available (this is ${current}). Download it from the Install the extension page on ${host} and load it in place of this one.`
  }

  $("pause-row").hidden = !paired
  $("pause-hint").hidden = !paired
  $("pause").checked = paused

  const hosts = agent.allowedHosts || []
  $("hosts").hidden = !paired
  $("hosts").textContent = hosts.length
    ? `Allowed here: ${hosts.join(", ")}.`
    : "No site is allowed for this browser yet. Add one on the workspace's Automation page."

  $("connect").hidden = paired || waiting
  $("connect").textContent = pairing && !waiting ? "Connect again" : "Connect to the site"
  $("reconnect").hidden = !paired || paused || status.state === "connected"
  $("grant").hidden = !paired || hosts.length === 0
  $("forget").hidden = !paired
  // Travels over the socket, so offered only while connected.
  $("halt").hidden = !(paired && status.state === "connected" && !paused)

  renderRecording(view)
}

/*
The recorder (MH-235). Its state lives in the worker and in storage: the popup
is closed most of the time and a recording outlives it.
*/
function renderRecording({ paired, status, recording, paused }) {
  const rec = recording
  $("record-box").hidden = !(paired && !paused && (status.state === "connected" || rec))
  const steps = rec?.steps?.length ?? 0
  $("rec").hidden = !!rec
  $("recstop").hidden = !rec?.active
  $("recdiscard").hidden = !rec
  $("record-state").textContent = rec ? `${rec.active ? "on" : "stopped"} · ${steps} step${steps === 1 ? "" : "s"}` : "off"
  $("record-note").classList.toggle("detail--bad", !!(rec?.error || rec?.blocked))
  $("record-note").textContent = rec?.error
    ? `${steps} steps not saved — ${rec.error}`
    : rec?.blocked
      ? `Not recording on this page: ${rec.blocked}`
      : rec?.active
        ? "Use the page as normal. Password characters are never recorded."
        : rec
          ? `${steps} steps waiting to be saved.`
          : "Clicks, typing, keys and page changes in this tab become a workflow you can edit, schedule and run from the AI."
  $("recstop").textContent = rec && !rec.active ? "Send again" : "Stop and save"
  $("recstop").hidden = !rec
}

let lastView = null
async function refresh() {
  lastView = await read()
  render(lastView)
}

function say(text, isBad = true) {
  $("detail").textContent = text
  $("detail").classList.toggle("detail--bad", isBad)
}

/* ---------------------------------------------------------------- actions */

$("connect").addEventListener("click", () => {
  // The permission request goes FIRST, inside the click. For the production
  // site the manifest grants it and this returns at once without a prompt.
  const asked = chrome.permissions.request({ origins: wanted }).catch(() => false)
  $("connect").disabled = true
  asked
    .then(async (granted) => {
      if (!granted) {
        say("Without permission for that site this browser cannot connect to it.")
        return
      }
      const out = await chrome.runtime.sendMessage({ type: "connect.start", languages: navigator.languages || [navigator.language] })
      if (!out?.ok) say(out?.error || "The site could not be reached.")
    })
    .catch((e) => say(String(e?.message || e)))
    .finally(() => {
      $("connect").disabled = false
      refresh()
    })
})

$("reopen").addEventListener("click", () => chrome.runtime.sendMessage({ type: "connect.reopen" }))

$("cancel").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "connect.cancel" })
  refresh()
})

$("reconnect").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "connect" })
  refresh()
})

$("pause").addEventListener("change", async (event) => {
  // The worker watches this key: the socket closes now, and reopens only when
  // the switch goes back.
  await chrome.storage.local.set({ [KEYS.paused]: event.target.checked })
  refresh()
})

$("grant").addEventListener("click", () => {
  // Chrome requires a click for each site, and this is that click. Asking for
  // exactly the allowed sites — never every site — keeps the extension to
  // what the workspace already said it may reach.
  const hosts = (lastView?.agent?.allowedHosts || []).filter(Boolean)
  if (!hosts.length) return
  const origins = hosts.flatMap((h) => [`https://${h}/*`, `http://${h}/*`, `https://*.${h}/*`])
  chrome.permissions
    .request({ origins })
    .then((granted) => {
      if (!granted) say("Without those sites this browser cannot run anything on them.")
    })
    .catch((e) => say(String(e?.message || e)))
})

$("forget").addEventListener("click", async () => {
  // Forgets the token HERE only. The browser stays listed on the site until an
  // admin revokes it there — which is what makes its token useless anywhere.
  await clear(KEYS.token, KEYS.agent, KEYS.pairing)
  await chrome.runtime.sendMessage({ type: "disconnect" }).catch(() => {})
  refresh()
})

$("halt").addEventListener("click", async () => {
  if (
    !window.confirm(
      "Stop ALL automation?\n\nEvery running task in this workspace is cancelled and every connected browser — not only this one — is disconnected. Nothing runs again until someone resumes it on the site.",
    )
  )
    return
  await chrome.runtime.sendMessage({ type: "halt" })
  say("Asked the workspace to stop everything.", false)
})

async function recordAction(type) {
  const out = await chrome.runtime.sendMessage({ type }).catch(() => ({ error: "the extension did not answer" }))
  if (out?.error) {
    $("record-note").textContent = out.error
    $("record-note").classList.add("detail--bad")
  } else if (type === "record.start") {
    window.close() // out of the way of the page being recorded
  } else if (out?.saved) {
    say(`Saved as "${out.saved.name}" — ${out.saved.steps} steps. Review it on the site before scheduling it.`, false)
  }
  refresh()
}
$("rec").addEventListener("click", () => recordAction("record.start"))
$("recstop").addEventListener("click", () => recordAction("record.stop"))
$("recdiscard").addEventListener("click", () => recordAction("record.discard"))

$("save-site").addEventListener("click", () => {
  const norm = (v) => {
    const t = String(v || "").trim().replace(/\/+$/, "")
    if (!t) return ""
    try {
      return new URL(t).origin
    } catch {
      return null
    }
  }
  const siteOrigin = norm($("site").value) || DEFAULTS.siteOrigin
  const apiOrigin = norm($("api").value)
  if (siteOrigin === null || apiOrigin === null) {
    $("site-note").textContent = "That is not a web address."
    return
  }
  const origins = [`${siteOrigin}/*`]
  if (apiOrigin) origins.push(`${apiOrigin}/*`)
  chrome.permissions
    .request({ origins })
    .then(async (granted) => {
      if (!granted) {
        $("site-note").textContent = "Not saved: this browser did not grant access to that address."
        return
      }
      await saveSettings({ siteOrigin, apiOrigin })
      $("site-note").textContent = "Saved. Reconnect to use it."
      refresh()
    })
    .catch((e) => ($("site-note").textContent = String(e?.message || e)))
})

/* ----------------------------------------------------------------- follow */


// The toolbar icon follows the browser's theme: the lifted teal the app uses on
// dark grounds reads on a dark toolbar where the brand teal sinks. A worker
// cannot ask for the colour scheme, so the popup sets it when it opens.
if (window.matchMedia?.("(prefers-color-scheme: dark)").matches) {
  chrome.action.setIcon({ path: { 16: "icons/icon-dark-16.png", 32: "icons/icon-dark-32.png" } }).catch(() => {})
}

chrome.storage.onChanged.addListener((_changes, area) => {
  if (area === "local") refresh()
})
// "3s ago" keeps counting while the popup is open.
setInterval(refresh, 5000)
refresh()
