/*
Which sites this browser may drive (MH-478, ported from fadymondy.com's
options page, FM-97; one gate since MH-483).

The workspace's list decides where a run may go (the driver and the server both
check it, MH-422). Chrome's grant only decides whether the extension can reach
a site at all, and since MH-483 the Connect click asks for every site once, so
a site added on the website works straight away. This page says which of the
two modes the browser is in, and is where the choice is changed:

  - "Allow on all sites": every http(s) site. On after Connect unless declined.
  - site by site: each site the workspace adds waits for an OK, given here or
    in the popup;
  - any other site, by hand.

And one honest list at the bottom: what Chrome has ACTUALLY granted right now,
whatever put it there, including changes made in chrome://extensions.

It also holds the one folder an upload step may read from (driver.js upload).
*/

import { KEYS, get, loadSettings, set } from "./config.js"
import { BROAD, grantState, originsFor } from "./grants.js"
import { normaliseHost } from "./hosts.js"

const el = (id) => document.getElementById(id)

// What someone typed, as the patterns for that host and its subdomains, or
// null when it is not a site address.
function patternsFor(input) {
  const host = normaliseHost(input)
  if (!host || !/^[a-z0-9.-]+$/.test(host) || (!host.includes(".") && host !== "localhost")) return null
  return originsFor(host)
}

async function granted() {
  const all = await chrome.permissions.getAll()
  return all.origins ?? []
}

let listed = []

async function render() {
  const origins = await granted()
  const settings = await loadSettings()
  const agent = (await get(KEYS.agent)) || {}
  const hosts = Array.isArray(agent.allowedHosts) ? agent.allowedHosts : []
  const state = await grantState(chrome.permissions, hosts)
  el("all-sites").checked = state.mode === "all"
  el("mode").dataset.mode = state.mode
  el("mode").textContent =
    state.mode === "all"
      ? "This browser allows every site. A site your workspace adds works straight away, and only the sites it lists are ever used."
      : state.pending.length
        ? `This browser allows sites one by one. ${state.pending.length} of your workspace's sites ${state.pending.length === 1 ? "waits" : "wait"} for your OK below.`
        : "This browser allows sites one by one. Each site your workspace adds will wait for your OK here or in the popup."

  el("site-hosts-empty").hidden = hosts.length > 0
  const list = el("site-hosts")
  list.replaceChildren()
  listed = []
  for (const host of hosts) {
    const patterns = originsFor(host)
    if (!patterns.length) continue
    const has = !state.pending.includes(normaliseHost(host))
    const li = document.createElement("li")
    const name = document.createElement("span")
    name.textContent = host
    li.append(name)
    if (has) {
      const state = document.createElement("span")
      state.className = "state state--ok"
      state.textContent = "allowed"
      li.append(state)
    } else {
      listed.push(...patterns)
      const button = document.createElement("button")
      button.className = "btn"
      button.type = "button"
      button.textContent = "Approve"
      button.addEventListener("click", () => request(patterns))
      li.append(button)
    }
    list.append(li)
  }
  el("allow-all-listed").hidden = listed.length < 4 // two patterns a site

  const grantedList = el("granted")
  grantedList.replaceChildren()
  for (const origin of origins) {
    const li = document.createElement("li")
    const name = document.createElement("span")
    name.textContent = BROAD.includes(origin) ? `every ${origin.slice(0, origin.indexOf(":"))} site` : origin
    li.append(name)
    if (origin.startsWith(settings.siteOrigin) || (settings.apiOrigin && origin.startsWith(settings.apiOrigin))) {
      // Required: the connection to the site runs on it.
      const state = document.createElement("span")
      state.className = "state"
      state.textContent = "needed to reach the site"
      li.append(state)
    } else {
      const button = document.createElement("button")
      button.className = "btn"
      button.type = "button"
      button.textContent = "Remove"
      button.addEventListener("click", async () => {
        await chrome.permissions.remove({ origins: [origin] }).catch(() => false)
        void render()
      })
      li.append(button)
    }
    grantedList.append(li)
  }

  el("upload-folder").value = (await get(KEYS.uploadFolder)) || ""
}

/*
Every request is made directly inside a click handler, with nothing awaited
before it: Chrome honours chrome.permissions.request only during the user
gesture, and an await in front of it spends the gesture and fails silently.
*/
function request(origins, note = "add-note") {
  chrome.permissions
    .request({ origins })
    .then((ok) => {
      el(note).textContent = ok ? "" : "Chrome did not grant it."
    })
    .catch((err) => {
      el(note).textContent = String(err?.message || err)
    })
    .finally(() => void render())
}

/*
The switch asks for, and gives back, exactly the manifest's optional hosts:
"http://*\/*" and "https://*\/*". It used to ask for "*://*\/*", which is not
among them, so Chrome refused it and the switch did nothing (MH-483).
*/
el("all-sites").addEventListener("change", (event) => {
  if (event.target.checked) request(BROAD, "all-note")
  else
    chrome.permissions
      .remove({ origins: BROAD })
      .then(async (ok) => {
        if (!ok) {
          el("all-note").textContent = "Chrome did not take it back."
          return
        }
        // Chrome takes back EVERY site "every site" covered — Moharrik's own
        // site too, which reconnecting and the update check need. It is asked
        // for again at once, while this click still counts, so turning the
        // switch off costs one more "Allow" and nothing else.
        const own = chrome.runtime.getManifest().host_permissions || []
        const kept = await chrome.permissions.contains({ origins: own }).catch(() => false)
        const back = kept || (await chrome.permissions.request({ origins: own }).catch(() => false))
        el("all-note").textContent = back
          ? "Every-site access is off. Sites now wait for your OK one by one."
          : "Every-site access is off. Chrome also took back Moharrik's own site: press Connect again in the extension to give it back."
      })
      .catch((err) => {
        el("all-note").textContent = String(err?.message || err)
      })
      .finally(() => void render())
})

el("allow-all-listed").addEventListener("click", () => request(listed))

el("add-host").addEventListener("click", () => {
  const patterns = patternsFor(el("new-host").value)
  if (!patterns) {
    el("add-note").textContent = "That does not look like a site address. Try example.com."
    return
  }
  el("new-host").value = ""
  request(patterns)
})
el("new-host").addEventListener("keydown", (event) => {
  if (event.key === "Enter") el("add-host").click()
})

el("save-folder").addEventListener("click", async () => {
  const folder = el("upload-folder").value.trim().replace(/[\\/]+$/, "")
  // An absolute path, and not a whole drive or the root: "everything" is not a folder.
  if (!/^([a-zA-Z]:[\\/].+|\/.+)$/.test(folder) || /^([a-zA-Z]:)?[\\/]?$/.test(folder)) {
    el("folder-note").textContent = "Name a folder by its full path, for example C:\\Users\\you\\Moharrik uploads."
    return
  }
  await set(KEYS.uploadFolder, folder)
  el("folder-note").textContent = "Saved. Upload steps may take files from this folder only."
})
el("clear-folder").addEventListener("click", async () => {
  await set(KEYS.uploadFolder, "")
  el("upload-folder").value = ""
  el("folder-note").textContent = "Uploads are off."
})

chrome.permissions.onAdded.addListener(() => void render())
chrome.permissions.onRemoved.addListener(() => void render())
chrome.storage.onChanged.addListener(() => void render())

void render()
