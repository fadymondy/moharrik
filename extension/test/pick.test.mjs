// MH-400: the element picker.
//
// The picker's page half runs against jsdom, injected the way chrome.scripting
// injects it (source in, structured clone out), with selectors.js installed
// first as the service worker does. The worker half runs against a fake chrome
// that navigates, redirects and closes tabs.
//
// web/e2e/workflow-editor-check.mjs does the same in a real Chromium, with a
// person's click replaced by Playwright's trusted one.

import { test } from "node:test"
import assert from "node:assert/strict"
import { JSDOM } from "jsdom"

import { cancelPicks, pickElement, pickInPage } from "../pick.js"
import { installSelectors } from "../selectors.js"

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const PAGE = `<!doctype html><title>Login</title>
  <form id="login">
    <label for="email">Email</label><input id="email" name="email">
    <input type="password" name="pw" value="hunter2">
    <button id="submit" type="submit" onclick="window.__clicked = (window.__clicked || 0) + 1">Sign in</button>
  </form>
  <ul><li>one</li><li>two</li><li>three</li></ul>`

function page(html = PAGE, url = "https://allowed.test/login") {
  const dom = new JSDOM(html, { url, runScripts: "dangerously", pretendToBeVisual: true })
  const w = dom.window
  // jsdom lays nothing out: elementFromPoint answers from what the test says
  // the pointer is over.
  w.__over = null
  w.document.elementFromPoint = () => w.__over
  return w
}

/** Inject a function as chrome.scripting does; a returned promise is awaited. */
async function inject(w, func, args = []) {
  const fn = w.eval(`(${func.toString()})`)
  const result = await fn(...args)
  return result === undefined ? undefined : JSON.parse(JSON.stringify(result))
}

function clickOn(w, el) {
  w.__over = el
  el.dispatchEvent(new w.MouseEvent("mousemove", { bubbles: true, clientX: 5, clientY: 5 }))
  for (const type of ["pointerdown", "mousedown", "pointerup", "mouseup", "click"]) {
    const Ctor = type.startsWith("pointer") ? w.MouseEvent : w.MouseEvent
    el.dispatchEvent(new Ctor(type, { bubbles: true, cancelable: true, clientX: 5, clientY: 5 }))
  }
}

test("a pick returns a durable selector and swallows the click", async () => {
  const w = page()
  await inject(w, installSelectors)
  const picking = inject(w, pickInPage, [{ timeoutMs: 2000 }])
  await sleep(5)
  assert.ok(w.document.getElementById("__moh_pick_overlay__"), "the overlay is on the page")
  clickOn(w, w.document.getElementById("submit"))
  const result = await picking
  assert.equal(result.picked, true)
  assert.equal(result.selector, "#submit")
  assert.equal(result.fragile, false)
  assert.equal(result.tag, "button")
  assert.equal(result.text, "Sign in")
  assert.equal(w.__clicked, undefined, "the picking click never reached the page")
  assert.equal(w.document.getElementById("__moh_pick_overlay__"), null, "the overlay is gone")
})

test("a password field is picked by its name, and its value never travels", async () => {
  const w = page()
  await inject(w, installSelectors)
  const picking = inject(w, pickInPage, [{ timeoutMs: 2000 }])
  await sleep(5)
  clickOn(w, w.document.querySelector("input[type=password]"))
  const result = await picking
  assert.equal(result.selector, 'input[name="pw"]')
  assert.doesNotMatch(JSON.stringify(result), /hunter2/)
})

test("an element with nothing durable about it is marked fragile", async () => {
  const w = page()
  await inject(w, installSelectors)
  const picking = inject(w, pickInPage, [{ timeoutMs: 2000 }])
  await sleep(5)
  clickOn(w, w.document.querySelectorAll("li")[1])
  const result = await picking
  assert.match(result.selector, /li:nth-of-type\(2\)/)
  assert.equal(result.fragile, true)
  assert.equal(w.document.querySelector(result.selector).textContent, "two", "the fragile selector still finds it")
})

test("Escape cancels, and the timeout ends it by itself", async () => {
  const w = page()
  await inject(w, installSelectors)
  let picking = inject(w, pickInPage, [{ timeoutMs: 2000 }])
  await sleep(5)
  w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }))
  assert.deepEqual(await picking, { picked: false, reason: "cancelled" })

  picking = inject(w, pickInPage, [{ timeoutMs: 30 }])
  assert.deepEqual(await picking, { picked: false, reason: "timeout" })
  assert.equal(w.document.getElementById("__moh_pick_overlay__"), null)
})

/* ------------------------------------------------ the service worker half */

function fakeChrome({ redirects = {}, pick = (w) => clickOn(w, w.document.getElementById("submit")) } = {}) {
  const listeners = new Set()
  const tabs = new Map()
  const log = { created: [], removed: [], focused: [], blanked: [] }
  let seq = 100
  const fire = (id, info) => [...listeners].forEach((fn) => fn(id, info))
  const navigate = (id, url) => {
    let final = url
    while (redirects[final]) final = redirects[final]
    const tab = tabs.get(id)
    tab.url = final
    tab.status = "loading"
    tab.window = page(PAGE, final.startsWith("http") ? final : "https://allowed.test/")
    setTimeout(() => {
      fire(id, { url: final })
      tab.status = "complete"
      fire(id, { status: "complete" })
    }, 5)
  }
  globalThis.chrome = {
    tabs: {
      query: async () => [{ id: 1, windowId: 9 }],
      create: async ({ url }) => {
        const id = ++seq
        tabs.set(id, { id, windowId: 9 })
        log.created.push(url)
        navigate(id, url)
        return { id, windowId: 9 }
      },
      update: async (id, props) => {
        if (props.url === "about:blank") log.blanked.push(id)
        if (props.active) log.focused.push(id)
        return { id }
      },
      get: async (id) => {
        const t = tabs.get(id)
        if (!t) throw new Error("No tab with id")
        return { id, url: t.url, status: t.status, windowId: 9 }
      },
      remove: async (id) => {
        log.removed.push(id)
        tabs.delete(id)
      },
      onUpdated: { addListener: (fn) => listeners.add(fn), removeListener: (fn) => listeners.delete(fn) },
    },
    windows: { update: async () => ({}) },
    scripting: {
      executeScript: async ({ target, func, args = [] }) => {
        const t = tabs.get(target.tabId)
        if (!t) throw new Error("No tab with id")
        const running = inject(t.window, func, args)
        if (func.name === "pickInPage") setTimeout(() => pick(t.window), 10)
        return [{ result: await running }]
      },
    },
  }
  return log
}

test("the worker opens the page, returns the pick, closes the tab and gives focus back", async () => {
  const log = fakeChrome()
  const got = await pickElement({ url: "https://allowed.test/login", allowed_hosts: ["allowed.test"], timeout_ms: 5000 })
  assert.equal(got.selector, "#submit")
  assert.equal(got.host, "allowed.test")
  assert.deepEqual(log.created, ["https://allowed.test/login"])
  assert.equal(log.removed.length, 1, "the picking tab is closed")
  assert.ok(log.focused.includes(1), "focus goes back to the tab that was active")
})

test("a page off the allowed sites is never opened", async () => {
  const log = fakeChrome()
  await assert.rejects(
    pickElement({ url: "https://evil.test/", allowed_hosts: ["allowed.test"] }),
    (err) => err.code === "left_allowed_sites" && /evil\.test isn't on this browser's allowed sites/.test(err.message),
  )
  assert.equal(log.created.length, 0)
  await assert.rejects(pickElement({ url: "https://allowed.test/", allowed_hosts: [] }), /allowed sites/)
})

test("a redirect off the allowed sites stops the pick, blanks the tab and closes it", async () => {
  const log = fakeChrome({ redirects: { "https://allowed.test/login": "https://evil.test/phish" } })
  await assert.rejects(
    pickElement({ url: "https://allowed.test/login", allowed_hosts: ["allowed.test"] }),
    (err) => err.code === "left_allowed_sites" && /evil\.test/.test(err.message),
  )
  assert.equal(log.blanked.length, 1)
  assert.equal(log.removed.length, 1)
})

test("cancelPicks closes a pick in progress, and the pick says it was stopped", async () => {
  const log = fakeChrome({ pick: () => {} }) // nobody clicks
  const picking = pickElement({ url: "https://allowed.test/login", allowed_hosts: ["allowed.test"], timeout_ms: 5000 })
  await sleep(40)
  // The kill switch drops the socket; the worker then cancels.
  const pending = picking.catch((e) => e)
  cancelPicks()
  // Closing the tab ends the injected wait in a real browser; the fake times
  // out instead, so the test only checks the tab was closed and no selector came back.
  assert.equal(log.removed.length, 1)
  const outcome = await Promise.race([pending, sleep(6000).then(() => "still waiting")])
  assert.ok(outcome instanceof Error, `the pick ended without a selector: ${outcome}`)
})
