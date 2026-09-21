import { KEYS, clear, get, loadSettings, saveSettings, set, siteURL } from "./config.js"

const $ = (id) => document.getElementById(id)

const STATE_LABEL = {
  connected: "Connected",
  offline: "Not connected",
  unpaired: "Not paired",
  paused: "Automation stopped",
  error: "Problem",
  off: "Disconnected",
}

async function render() {
  const settings = await loadSettings()
  $("site").value = settings.siteOrigin
  $("api").value = settings.apiOrigin
  $("workspace").value = settings.workspace

  const status = (await get(KEYS.status)) || {}
  const agent = (await get(KEYS.agent)) || {}
  const token = await get(KEYS.token)

  $("state").textContent = STATE_LABEL[status.state] ?? "…"
  $("name").textContent = agent.name || ""
  $("error").hidden = !status.error
  $("error").textContent = status.error || ""

  const paired = !!token
  $("pair").hidden = paired
  $("paired").hidden = !paired

  const hosts = agent.allowedHosts || []
  $("hosts").textContent = hosts.length
    ? `This browser may be driven on: ${hosts.join(", ")}`
    : "No site is allowed yet. Add one on the workspace's Automation page."
}

$("start").addEventListener("click", async () => {
  const settings = await saveSettings({
    siteOrigin: $("site").value.trim().replace(/\/$/, ""),
    apiOrigin: $("api").value.trim().replace(/\/$/, ""),
    workspace: $("workspace").value.trim(),
  })
  if (!settings.workspace) {
    $("error").hidden = false
    $("error").textContent = "Which workspace? It is the part after /w/ in the address."
    return
  }

  // Chrome will not let this page fetch an origin it has no permission for,
  // and the click is the user gesture that lets us ask. The production site is
  // declared in the manifest, so this only prompts for a dev or staging host.
  const wanted = [`${settings.siteOrigin}/*`]
  if (settings.apiOrigin) wanted.push(`${settings.apiOrigin}/*`)
  try {
    const have = await chrome.permissions.contains({ origins: wanted })
    if (!have) {
      const granted = await chrome.permissions.request({ origins: wanted })
      if (!granted) {
        $("error").hidden = false
        $("error").textContent = "Without permission for that site this browser cannot pair with it."
        return
      }
    }
  } catch (e) {
    $("error").hidden = false
    $("error").textContent = String(e.message || e)
    return
  }

  $("start").disabled = true
  try {
    const res = await fetch(siteURL(settings, `/api/agents/pair/${encodeURIComponent(settings.workspace)}`), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: `${navigator.userAgent.includes("Windows") ? "Windows" : "This"} browser`,
        platform: navigator.userAgent.includes("Windows") ? "Windows" : navigator.platform || "",
        extension_version: chrome.runtime.getManifest().version,
      }),
    })
    if (!res.ok) throw new Error(res.status === 404 ? "No workspace with that name." : `The site answered ${res.status}.`)
    const started = await res.json()

    $("code").textContent = started.code.replace(/(\d{3})(\d{3})/, "$1 $2")
    $("codebox").hidden = false
    await set(KEYS.pairing, { pollToken: started.poll_token, expiresAt: started.expires_at })
    pollForApproval(settings, started.poll_token)
  } catch (e) {
    $("error").hidden = false
    $("error").textContent = String(e.message || e)
  } finally {
    $("start").disabled = false
  }
})

/*
Polling uses the POLL TOKEN, never the code.

The code is six digits, read off this screen and often read aloud. Whoever
overheard it could otherwise race this extension and collect the browser's
token first. The poll token never leaves here.
*/
async function pollForApproval(settings, pollToken) {
  const deadline = Date.now() + 10 * 60 * 1000
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 2000))
    let out
    try {
      const res = await fetch(siteURL(settings, "/api/agents/pair/claim"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ poll_token: pollToken }),
      })
      out = await res.json()
    } catch {
      continue // a blip while someone walks to the other screen
    }
    if (out.status === "expired") {
      $("error").hidden = false
      $("error").textContent = "That code expired. Press Connect for a new one."
      return
    }
    if (out.status === "approved" && out.token) {
      await set(KEYS.token, out.token)
      await clear(KEYS.pairing)
      chrome.runtime.sendMessage({ type: "connect" })
      $("codebox").hidden = true
      setTimeout(render, 500)
      return
    }
  }
}

$("grant").addEventListener("click", async () => {
  const agent = (await get(KEYS.agent)) || {}
  const hosts = agent.allowedHosts || []
  if (!hosts.length) return
  // Chrome requires a click for each host, and this is that click. Asking for
  // exactly the allow-list — rather than <all_urls> — means the extension can
  // only reach what the workspace already said it may.
  const origins = hosts.flatMap((h) => [`https://${h}/*`, `https://*.${h}/*`])
  const granted = await chrome.permissions.request({ origins })
  $("error").hidden = granted
  if (!granted) $("error").textContent = "Without those sites this browser cannot run anything on them."
})

$("halt").addEventListener("click", () => {
  chrome.runtime.sendMessage({ type: "halt" })
  $("error").hidden = false
  $("error").textContent = "Asked the workspace to stop everything."
})

$("forget").addEventListener("click", async () => {
  await clear(KEYS.token, KEYS.agent, KEYS.pairing)
  chrome.runtime.sendMessage({ type: "disconnect" })
  render()
})

render()
