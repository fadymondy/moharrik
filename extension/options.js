/*
Which sites this browser may drive (MH-478, ported from fadymondy.com's
options page, FM-97).

Chrome grants host access only in response to a click in the browser, so this
page exists to BE that click. From narrowest to widest:

  - the sites the workspace allowed for this browser (from hello.ok), each
    with its own Allow, and one button for all of them;
  - any other site, by hand;
  - "Allow on all sites", off by default and labelled for what it is.

And one honest list at the bottom: what Chrome has ACTUALLY granted right now,
whatever put it there — including changes made in chrome://extensions.

The workspace's list and Chrome's grant are two separate gates. A site Chrome
allows but the workspace does not list is still refused by every run (the
driver and the server both check the workspace's list, MH-422).

It also holds the one folder an upload step may read from (driver.js upload).
*/

import { KEYS, get, loadSettings, set } from "./config.js"

const ALL = "*://*/*"
const el = (id) => document.getElementById(id)

// "https://app.example.com/path" or "example.com" → the pattern for that host,
// any scheme. (A line comment: the pattern itself would end a block comment.)
function patternFor(input) {
  let host = String(input || "").trim().toLowerCase()
  if (!host) return null
  if (host.includes("://")) {
    try {
      host = new URL(host).hostname
    } catch {
      return null
    }
  }
  host = host.replace(/\/.*$/, "").replace(/:\d+$/, "")
  if (!/^[a-z0-9.-]+$/.test(host) || (!host.includes(".") && host !== "localhost")) return null
  return `*://${host}/*`
}

async function granted() {
  const all = await chrome.permissions.getAll()
  return all.origins ?? []
}

let listed = []

async function render() {
  const origins = await granted()
  const settings = await loadSettings()
  el("all-sites").checked = origins.includes(ALL)

  const agent = (await get(KEYS.agent)) || {}
  const hosts = Array.isArray(agent.allowedHosts) ? agent.allowedHosts : []
  el("site-hosts-empty").hidden = hosts.length > 0
  const list = el("site-hosts")
  list.replaceChildren()
  listed = []
  for (const host of hosts) {
    const pattern = patternFor(host)
    if (!pattern) continue
    const has = origins.includes(ALL) || (await chrome.permissions.contains({ origins: [pattern] }).catch(() => false))
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
      listed.push(pattern)
      const button = document.createElement("button")
      button.className = "btn"
      button.type = "button"
      button.textContent = "Allow"
      button.addEventListener("click", () => request([pattern]))
      li.append(button)
    }
    list.append(li)
  }
  el("allow-all-listed").hidden = listed.length < 2

  const grantedList = el("granted")
  grantedList.replaceChildren()
  for (const origin of origins) {
    const li = document.createElement("li")
    const name = document.createElement("span")
    name.textContent = origin === ALL ? "all sites" : origin
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
function request(origins) {
  chrome.permissions
    .request({ origins })
    .then((ok) => {
      el("add-note").textContent = ok ? "" : "Chrome did not grant it."
    })
    .catch((err) => {
      el("add-note").textContent = String(err?.message || err)
    })
    .finally(() => void render())
}

el("all-sites").addEventListener("change", (event) => {
  if (event.target.checked) request([ALL])
  else
    chrome.permissions
      .remove({ origins: [ALL] })
      .catch(() => false)
      .finally(() => void render())
})

el("allow-all-listed").addEventListener("click", () => request(listed))

el("add-host").addEventListener("click", () => {
  const pattern = patternFor(el("new-host").value)
  if (!pattern) {
    el("add-note").textContent = "That does not look like a site address. Try example.com."
    return
  }
  el("new-host").value = ""
  request([pattern])
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
