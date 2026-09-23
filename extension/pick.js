/*
The element picker (MH-400).

Someone editing a workflow presses "Pick" next to a step, and this browser —
the one they paired, signed in to the site — opens the page, lets them click
the element they mean, and sends back a selector for it. Reading a selector out
of DevTools is not something the people who edit workflows should have to do.

It is a DIRECT command, not a run, and it touches as little as it can:

  - The click that picks is SWALLOWED. It never reaches the page, so picking
    the "Delete account" button deletes nothing.
  - The page opens in its own tab, which is closed again afterwards, and focus
    goes back to where it was (usually the editor).
  - Escape cancels, and the mode ends by itself after the timeout, so a
    browser cannot be left in it.

The allowed sites apply exactly as they do to a run (hosts.js is the server's
own rule): the page must be on the list before it is opened, and the page it
LANDS on — after any redirect — is checked again. A tab that leaves the list
while picking is blanked and the pick fails with the same code a run uses.
The kill switch drops the socket, and the service worker then closes any pick
in progress (cancelPicks).

The selector comes from selectors.js — the generator the recorder uses — so a
picked step and a recorded step are made the same way. When the page offers
nothing more durable than a position among siblings, the result is marked
fragile and the editor says so.
*/

import { gotoHost, hostAllowed, pageVerdict } from "./hosts.js"
import { installSelectors } from "./selectors.js"

export const PICK_TIMEOUT_MS = 90_000
const LOAD_TIMEOUT_MS = 30_000
export const LEFT_ALLOWED_SITES = "left_allowed_sites"

const active = new Set() // tab ids with a pick in progress

function left(message) {
  const err = new Error(message)
  err.code = LEFT_ALLOWED_SITES
  return err
}

function waitForComplete(tabId, timeoutMs) {
  return new Promise((resolve) => {
    const timer = setTimeout(done, timeoutMs)
    function done() {
      clearTimeout(timer)
      chrome.tabs.onUpdated.removeListener(listener)
      resolve()
    }
    function listener(id, info) {
      if (id === tabId && info.status === "complete") done()
    }
    chrome.tabs.onUpdated.addListener(listener)
    chrome.tabs.get(tabId).then(
      (tab) => tab?.status === "complete" && done(),
      done,
    )
  })
}

/** Run a pick. Resolves with {selector, fragile, tag, text, host}, or throws the browser's own words. */
export async function pickElement(payload) {
  const url = String(payload?.url || "").trim()
  const allowed = Array.isArray(payload?.allowed_hosts) ? payload.allowed_hosts.map(String) : []
  const timeoutMs = Math.max(5_000, Math.min(Number(payload?.timeout_ms) || PICK_TIMEOUT_MS, 120_000))

  if (!/^https?:\/\//i.test(url)) throw new Error("the picker needs the address of a web page")
  const host = gotoHost(url)
  if (!hostAllowed(allowed, host)) throw left(`${host || "that address"} isn't on this browser's allowed sites`)

  let before = null
  try {
    ;[before] = await chrome.tabs.query({ active: true, lastFocusedWindow: true })
  } catch {
    before = null
  }

  const tab = await chrome.tabs.create({ url, active: true })
  active.add(tab.id)

  // Where the tab goes WHILE the person is picking: a page that redirects or
  // moves by itself off the list is blanked at once, as a run's tab is.
  let breach = ""
  const watch = (tabId, info) => {
    if (tabId !== tab.id || !info?.url) return
    const verdict = pageVerdict(info.url, allowed)
    if (verdict.state === "outside" && !breach) {
      breach = verdict.where
      chrome.tabs.update(tab.id, { url: "about:blank" }).catch(() => {})
    }
  }
  chrome.tabs.onUpdated.addListener(watch)

  try {
    await waitForComplete(tab.id, LOAD_TIMEOUT_MS)
    let now
    try {
      now = await chrome.tabs.get(tab.id)
    } catch {
      throw new Error("the picking tab was closed")
    }
    const verdict = pageVerdict(now.url, allowed)
    if (breach || verdict.state === "outside") {
      throw left(`the page went to ${breach || verdict.where}, which isn't on this browser's allowed sites`)
    }
    if (verdict.state !== "allowed") throw new Error("the page did not load")

    // The person has to see the page to click on it.
    try {
      await chrome.windows.update(now.windowId, { focused: true })
    } catch {
      /* no windows API in this context — the tab is active anyway */
    }

    await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: installSelectors })
    let result
    try {
      ;[{ result } = {}] = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: pickInPage,
        args: [{ timeoutMs }],
      })
    } catch (err) {
      if (breach) throw left(`the page went to ${breach}, which isn't on this browser's allowed sites`)
      if (!active.has(tab.id)) throw new Error("the pick was stopped")
      throw err
    }
    if (breach) throw left(`the page went to ${breach}, which isn't on this browser's allowed sites`)
    if (!result) throw new Error("the page did not answer")
    if (!result.picked) {
      throw new Error(result.reason === "cancelled" ? "picking was cancelled" : "nothing was picked in time")
    }
    if (!result.selector) {
      throw new Error("nothing identifies that element on its own — pick something close to it, such as its label or container")
    }
    return {
      selector: result.selector,
      fragile: !!result.fragile,
      tag: result.tag || "",
      text: String(result.text || "").slice(0, 120),
      host: verdict.host || host,
    }
  } finally {
    chrome.tabs.onUpdated.removeListener(watch)
    active.delete(tab.id)
    chrome.tabs.remove(tab.id).catch(() => {})
    // Back to where they were — usually the editor — whatever happened.
    if (before?.id != null && before.id !== tab.id) {
      chrome.tabs.update(before.id, { active: true }).catch(() => {})
      if (before.windowId != null) chrome.windows?.update?.(before.windowId, { focused: true })?.catch?.(() => {})
    }
  }
}

/** Stop every pick in progress: the kill switch, or the socket going away. */
export function cancelPicks() {
  for (const id of [...active]) {
    active.delete(id)
    chrome.tabs.remove(id).catch(() => {})
  }
}

/**
 * Injected into the page, so self-contained: it is serialised across the
 * boundary. Resolves with {picked, selector, fragile, tag, text} or
 * {picked: false, reason}.
 */
export function pickInPage(options) {
  const timeoutMs = Number(options?.timeoutMs) || 90000
  return new Promise((resolve) => {
    const HOST_ID = "__moh_pick_overlay__"
    document.getElementById(HOST_ID)?.remove()

    // A closed shadow root: the page's CSS cannot restyle the overlay and the
    // overlay's cannot leak into the page. Colours are fixed on purpose — this
    // is drawn on someone else's site, where the app's theme does not exist.
    const host = document.createElement("div")
    host.id = HOST_ID
    host.style.cssText = "position:fixed;inset:0;pointer-events:none;z-index:2147483647"
    const root = host.attachShadow ? host.attachShadow({ mode: "closed" }) : host
    root.innerHTML = `
      <style>
        .box{position:fixed;border:2px solid #4b00b6;background:rgba(75,0,182,.12);border-radius:2px;pointer-events:none}
        .tag{position:fixed;font:12px/1.4 ui-monospace,monospace;background:#4b00b6;color:#fff;padding:2px 6px;border-radius:2px;white-space:nowrap;max-width:60vw;overflow:hidden;text-overflow:ellipsis;pointer-events:none}
        .bar{position:fixed;left:50%;top:12px;transform:translateX(-50%);font:13px/1.4 system-ui,sans-serif;background:#111;color:#fff;padding:8px 14px;border-radius:4px;box-shadow:0 4px 18px rgba(0,0,0,.3);pointer-events:none}
      </style>
      <div class="bar" part="bar">Moharrik — click the element this step should use · Esc to cancel</div>
      <div class="box" hidden></div>
      <div class="tag" hidden></div>`
    const box = root.querySelector(".box")
    const tag = root.querySelector(".tag")
    document.documentElement.appendChild(host)

    const selectorFor = (el) => {
      try {
        return globalThis.__mohSelectors?.selectorFor(el) || ""
      } catch {
        return ""
      }
    }

    let current = null
    let done = false
    const finish = (result) => {
      if (done) return
      done = true
      clearTimeout(timer)
      document.removeEventListener("mousemove", onMove, true)
      document.removeEventListener("click", onClick, true)
      document.removeEventListener("mousedown", swallow, true)
      document.removeEventListener("mouseup", swallow, true)
      document.removeEventListener("pointerdown", swallow, true)
      document.removeEventListener("pointerup", swallow, true)
      document.removeEventListener("keydown", onKey, true)
      host.remove()
      resolve(result)
    }

    const targetOf = (event) => {
      const at = typeof document.elementFromPoint === "function" ? document.elementFromPoint(event.clientX, event.clientY) : null
      const el = at && at !== host ? at : event.target
      return el && el.nodeType === 1 && el !== host ? el : null
    }

    const onMove = (event) => {
      const el = targetOf(event)
      if (!el || el === current) return
      current = el
      const r = el.getBoundingClientRect()
      box.hidden = false
      tag.hidden = false
      Object.assign(box.style, { left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px` })
      tag.textContent = selectorFor(el) || el.tagName.toLowerCase()
      tag.style.left = `${Math.max(0, r.left)}px`
      tag.style.top = `${r.top > 24 ? r.top - 22 : r.bottom + 4}px`
    }
    // The picking click must not reach the page: it would press whatever it
    // is on. Every phase of it is stopped, before the page's own handlers.
    const swallow = (event) => {
      event.preventDefault()
      event.stopPropagation()
      event.stopImmediatePropagation()
    }
    const onClick = (event) => {
      swallow(event)
      const el = targetOf(event) || current
      if (!el) return
      const selector = selectorFor(el)
      finish({
        picked: true,
        selector,
        fragile: /:nth-of-type\(/.test(selector),
        tag: el.tagName.toLowerCase(),
        // Never el.value: on a password field that is the password, and this
        // text goes to the server and onto the editor's screen.
        text: String(el.innerText || el.textContent || el.getAttribute("aria-label") || el.getAttribute("placeholder") || "")
          .replace(/\s+/g, " ")
          .trim()
          .slice(0, 120),
      })
    }
    const onKey = (event) => {
      if (event.key !== "Escape") return
      swallow(event)
      finish({ picked: false, reason: "cancelled" })
    }
    const timer = setTimeout(() => finish({ picked: false, reason: "timeout" }), timeoutMs)

    document.addEventListener("mousemove", onMove, true)
    document.addEventListener("pointerdown", swallow, true)
    document.addEventListener("pointerup", swallow, true)
    document.addEventListener("mousedown", swallow, true)
    document.addEventListener("mouseup", swallow, true)
    document.addEventListener("click", onClick, true)
    document.addEventListener("keydown", onKey, true)
  })
}
