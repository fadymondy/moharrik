/*
The popup (MH-478), section by section after fadymondy.com's: this browser,
its workspace, the connection, the last command, the pause switch, the
recorder, and the actions.

Its one rule, carried over: it states the truth about the connection. Every
line is read from state the worker and the socket wrote to storage, and "I do
not know" is rendered as such, never as "fine" — a popup that says connected
because it once was sends people to schedule work that will never run.
*/

import { DEFAULTS, KEYS, clear, get, loadSettings, saveSettings, set } from "./config.js"
import { BROAD, newerThan, originsFor } from "./grants.js"

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

async function read() {
  const [settings, status, agent, token, pairing, paused, recording, grants, update] = await Promise.all([
    loadSettings(),
    get(KEYS.status),
    get(KEYS.agent),
    get(KEYS.token),
    get(KEYS.pairing),
    get(KEYS.paused),
    get("moh.recording"),
    get(KEYS.grants),
    get(KEYS.update),
  ])
  return {
    settings,
    status: status || {},
    agent: agent || {},
    paired: !!token,
    pairing: pairing || null,
    paused: paused === true,
    recording: recording || null,
    grants: grants || null,
    update: update || null,
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
  } else if (status.state === "connecting") {
    state = "authenticating"
    dot = "dot--wait"
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
    detail = status.error ? `Cannot reach ${host}: ${status.error}.` : `Cannot reach ${host}.`
    if (status.retryInMs) detail += ` Retrying in ${Math.max(1, Math.round(status.retryInMs / 1000))}s.`
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

  // The newest of what the socket said at connect and what the six-hourly
  // check found (MH-483).
  const current = chrome.runtime.getManifest().version
  const latest = [agent.latestVersion, view.update?.latest].filter(Boolean).reduce((a, b) => (newerThan(b, a) ? b : a), "")
  const outdated = !!latest && newerThan(latest, current)
  $("update-box").hidden = !outdated
  if (outdated) {
    $("update-version").textContent = `${current} → ${latest}`
    $("update-download").textContent = `Download ${latest}`
  }

  // Sites the workspace added that Chrome has not allowed yet (MH-483).
  const pending = paired ? view.grants?.pending || [] : []
  $("waiting-ok").hidden = pending.length === 0
  renderPending(pending)
  $("connect-note").hidden = paired || waiting

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
  $("banner-note").hidden = !paired
  $("forget").hidden = !paired
  // Travels over the socket, so offered only while connected.
  $("halt").hidden = !(paired && status.state === "connected" && !paused)

  // Application autofill (MH-478): offered whenever this browser could act at
  // all. Whether the CURRENT tab is on an allowed site is decided by Preview
  // itself, in words, rather than a check here that could go stale between
  // opening the popup and pressing the button.
  $("autofill-box").hidden = !(paired && !paused && status.state === "connected")

  renderRecording(view)
}

function renderPending(pending) {
  const list = $("pending-list")
  if (list.dataset.hosts === pending.join(",")) return // do not rebuild under the pointer
  list.dataset.hosts = pending.join(",")
  list.replaceChildren()
  for (const host of pending) {
    const li = document.createElement("li")
    const name = document.createElement("span")
    name.textContent = host
    const button = document.createElement("button")
    button.className = "btn"
    button.type = "button"
    button.textContent = "Approve"
    button.dataset.approve = host
    // The request is the click's FIRST call: Chrome honours it only inside the gesture.
    button.addEventListener("click", () => ask(originsFor(host)))
    li.append(name, button)
    list.append(li)
  }
}

function ask(origins) {
  chrome.permissions
    .request({ origins })
    .then((ok) => {
      if (!ok) say("Chrome did not allow it. The site stays waiting until you approve it.")
    })
    .catch((e) => say(String(e?.message || e)))
    .finally(() => chrome.runtime.sendMessage({ type: "grants.refresh" }).catch(() => {}))
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
  const languages = navigator.languages || [navigator.language]
  // The permission request goes FIRST, inside the click (MH-483): every
  // http(s) site, once, so a site the workspace adds later works without a
  // second click here. The workspace's list still decides where a run may go.
  const asked = chrome.permissions.request({ origins: [...BROAD, ...wanted] }).catch(() => false)
  // Left for the worker in case Chrome closes this popup while its prompt is
  // up: the grant arriving there starts the connect instead (background.js).
  void set(KEYS.connectIntent, { at: Date.now(), languages })
  $("connect").disabled = true
  asked
    .then(async (granted) => {
      if (!granted) {
        void clear(KEYS.connectIntent)
        // Declined: connect anyway, site by site, when the site itself is
        // reachable (it always is for the site this build was made for).
        const siteOk = await chrome.permissions.contains({ origins: wanted }).catch(() => false)
        if (!siteOk) {
          say("Without permission for that site this browser cannot connect to it.")
          return
        }
      }
      const out = await chrome.runtime.sendMessage({ type: "connect.start", languages })
      if (!out?.ok) say(out?.error || "The site could not be reached.")
      else if (!granted) say("Connecting site by site: each site your workspace adds will wait for your OK here.", false)
    })
    .catch((e) => say(String(e?.message || e)))
    .finally(() => {
      $("connect").disabled = false
      refresh()
    })
})

$("approve-all-sites").addEventListener("click", () => ask(BROAD))

$("update-download").addEventListener("click", async () => {
  // The signed-in download on the site (its cookie is on the site, not the
  // API origin): one click, and the zip lands in Downloads.
  const settings = await loadSettings()
  await chrome.tabs.create({ url: `${settings.siteOrigin.replace(/\/+$/, "")}/api/automation/extension/download` })
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

$("sites").addEventListener("click", () => {
  void chrome.runtime.openOptionsPage()
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

/* ------------------------------------------------------- application autofill

fadymondy.com's Autofill, generalised: reads the CURRENT tab's form, matches it
against the workspace profile, and shows the mapping — nothing is filled until
each field is reviewed and Fill is pressed. Nothing here can submit anything.
*/
let autofillMatches = []

function renderAutofillNote(text, bad) {
  $("autofill-note").textContent = text
  $("autofill-note").classList.toggle("detail--bad", !!bad)
}

function renderAutofillMatches(matches) {
  autofillMatches = matches || []
  const list = $("autofill-list")
  list.replaceChildren()
  if (autofillMatches.length === 0) {
    list.hidden = true
    $("autofill-run").hidden = true
    return
  }
  autofillMatches.forEach((m, i) => {
    const li = document.createElement("li")
    li.dataset.autofillMatch = m.profileKey
    const label = document.createElement("label")
    label.style.display = "flex"
    label.style.gap = "6px"
    label.style.alignItems = "center"
    const cb = document.createElement("input")
    cb.type = "checkbox"
    cb.checked = true
    cb.dataset.i = String(i)
    const span = document.createElement("span")
    span.dir = "auto"
    span.textContent = `${m.label || m.profileKey} → ${m.value}`
    label.append(cb, span)
    li.append(label)
    list.append(li)
  })
  list.hidden = false
  $("autofill-run").hidden = false
  $("autofill-run").textContent = `Fill ${autofillMatches.length} field(s)`
}

$("autofill-preview").addEventListener("click", async () => {
  $("autofill-preview").disabled = true
  renderAutofillNote("Reading the page…", false)
  const res = await chrome.runtime.sendMessage({ type: "autofill.preview" }).catch((e) => ({ error: String(e?.message || e) }))
  $("autofill-preview").disabled = false
  if (res?.error) {
    renderAutofillNote(res.error, true)
    renderAutofillMatches([])
    return
  }
  renderAutofillMatches(res.matches)
  renderAutofillNote(
    res.matches.length > 0
      ? `${res.matches.length} of ${res.fieldCount} field(s) on this page matched the workspace profile. Nothing is filled yet — review below, then press Fill. It never submits.`
      : "Nothing on this page matched the workspace profile.",
    false,
  )
})

$("autofill-run").addEventListener("click", async () => {
  const chosen = [...document.querySelectorAll("#autofill-list input[type=checkbox]:checked")].map((cb) => autofillMatches[Number(cb.dataset.i)])
  if (chosen.length === 0) return
  $("autofill-run").disabled = true
  const res = await chrome.runtime.sendMessage({ type: "autofill.fill", matches: chosen }).catch((e) => ({ error: String(e?.message || e) }))
  $("autofill-run").disabled = false
  if (res?.error) {
    renderAutofillNote(res.error, true)
    return
  }
  const leftAlone = res.skipped?.length ? `; ${res.skipped.length} left as they were` : ""
  renderAutofillNote(`${res.filled} field(s) filled${leftAlone}. Review the page, then submit it yourself — this never submits anything.`, false)
  $("autofill-list").hidden = true
  $("autofill-run").hidden = true
})

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
