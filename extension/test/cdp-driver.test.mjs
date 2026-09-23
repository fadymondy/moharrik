// MH-478: the runtime's real-input path — Chrome's debugger — with the MH-422
// guard still around every acting step.
//
// The fake chrome here has a debugger that behaves the way it matters: input
// is dispatched at a POINT, and the page under that point receives it; a
// mouse release is a click with the browser's default action (a link is
// followed); a key down with text types into whatever has focus, and Enter
// submits its form. Elements get boxes (jsdom lays nothing out), so the driver
// has something to aim at.
//
// The script-input path is exercised by allowed-sites.test.mjs and
// driver.test.mjs; web/e2e/runtime-check.mjs runs all of this in Chromium.

import { test } from "node:test"
import assert from "node:assert/strict"
import { JSDOM } from "jsdom"

import { closeRun, runStep } from "../driver.js"
import { fakeStorage } from "./fake-storage.mjs"

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const SITES = {
  "https://allowed.test/start": `<!doctype html><title>Start</title>
    <a id="away" href="https://evil.test/landing">elsewhere</a>
    <a id="inside" href="/next">next page</a>
    <button id="save" aria-label="Save changes">Save</button>
    <button id="cancel">Cancel</button>
    <form id="search" action="https://evil.test/search" method="get"><input id="q" name="q"></form>
    <form id="local" action="/results" method="get"><input id="q2" name="q" value="old"></form>
    <input id="file" type="file">
    <p id="out"></p>
    <script>
      document.getElementById("save").addEventListener("click", (e) => { document.getElementById("out").textContent = "saved:" + (window.__trusted === true) })
      document.getElementById("cancel").addEventListener("click", () => { document.getElementById("out").textContent = "cancelled" })
    </script>`,
  "https://allowed.test/next": `<!doctype html><title>Next</title><p id="here">next</p>`,
  "https://allowed.test/results": `<!doctype html><title>Results</title><h1 id="result">results</h1>`,
  "https://allowed.test/drifts": `<!doctype html><title>Drifts</title><p id="here">drifting</p>
    <script>setTimeout(() => window.__navigate("https://evil.test/landing"), 30)</script>`,
  "https://evil.test/landing": `<!doctype html><title>Evil</title><input id="field">`,
  "https://evil.test/search": `<!doctype html><title>Evil search</title>`,
}

let tabSeq = 500

/** A fake chrome with real-input debugger semantics, any number of tabs. */
function browser({ allowed = ["allowed.test"], debuggerWorks = true, extra = {} } = {}) {
  const tabs = new Map() // id -> state
  const updated = new Set()
  const committed = new Set()
  const cdp = [] // every debugger command: [tabId, method, params]
  const fire = (set, ...a) => [...set].forEach((fn) => fn(...a))

  function page(tab, url, html) {
    const dom = new JSDOM(html, {
      url: url.startsWith("http") ? url : "about:blank",
      runScripts: "dangerously",
      pretendToBeVisual: true,
      beforeParse(w) {
        w.__navigate = (to) => navigate(tab, String(new URL(to, url)))
        w.Element.prototype.scrollIntoView = () => {}
        // Every element gets a box of its own, in document order, so a point
        // names exactly one of them.
        const order = new WeakMap()
        let n = 0
        w.Element.prototype.getBoundingClientRect = function () {
          if (!order.has(this)) order.set(this, ++n)
          const i = order.get(this)
          return { x: 10, y: i * 30, left: 10, top: i * 30, width: 200, height: 20, right: 210, bottom: i * 30 + 20 }
        }
        w.document.elementFromPoint = (x, y) => {
          let best = null
          for (const el of w.document.querySelectorAll("body *")) {
            const r = el.getBoundingClientRect()
            if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) best = el
          }
          return best
        }
        w.addEventListener("click", (e) => {
          const a = e.target.closest?.("a[href]")
          if (!a || e.defaultPrevented) return
          e.preventDefault()
          navigate(tab, a.href)
        })
        w.addEventListener("submit", (e) => {
          if (e.defaultPrevented) return
          e.preventDefault()
          const form = e.target
          const to = new URL(form.getAttribute("action") || url, url)
          for (const [k, v] of new w.FormData(form)) to.searchParams.append(k, v)
          navigate(tab, to.href)
        })
        Object.defineProperty(w, "innerHeight", { value: 2000, configurable: true })
        Object.defineProperty(w, "innerWidth", { value: 1440, configurable: true })
      },
    })
    return dom.window
  }

  function commit(tab, url) {
    const site = url.startsWith("http") ? SITES[new URL(url).origin + new URL(url).pathname] : null
    tab.url = url
    tab.documentId = `doc-${tab.id}-${++tab.docSeq}`
    tab.window = page(tab, url, site || "<!doctype html><title></title>")
    tab.commits.push(url)
    fire(committed, { tabId: tab.id, frameId: 0, url, documentId: tab.documentId })
    fire(updated, tab.id, { status: "loading", url })
    setTimeout(() => {
      tab.status = "complete"
      fire(updated, tab.id, { status: "complete" })
    }, 5)
  }

  function navigate(tab, url) {
    tab.status = "loading"
    fire(updated, tab.id, { status: "loading" })
    setTimeout(() => commit(tab, url), 15)
  }

  function create(url) {
    const tab = { id: ++tabSeq, url: "", status: "complete", window: null, documentId: null, docSeq: 0, commits: [], injections: [] }
    tabs.set(tab.id, tab)
    navigate(tab, url)
    return tab
  }

  // What real input does to the page.
  function input(tab, method, params) {
    const w = tab.window
    const doc = w.document
    if (method === "Input.dispatchMouseEvent" && params.type === "mouseReleased") {
      const el = doc.elementFromPoint(params.x, params.y)
      if (el) {
        el.focus?.()
        // A trusted click (jsdom cannot mint isTrusted, so the page reads a
        // flag set only around real input), and its default action happens.
        w.__trusted = true
        el.dispatchEvent(new w.MouseEvent("click", { bubbles: true, cancelable: true, clientX: params.x, clientY: params.y }))
        w.__trusted = false
      }
    }
    if (method === "Input.dispatchKeyEvent" && params.type === "keyDown" && params.text) {
      const el = doc.activeElement
      if (params.key === "Enter" && el?.form) el.form.requestSubmit()
      else if (el && "value" in el) {
        el.value += params.text
        el.dispatchEvent(new w.Event("input", { bubbles: true }))
      }
    }
    if (method === "Input.dispatchKeyEvent" && params.type === "rawKeyDown" && params.key === "Backspace") {
      const el = doc.activeElement
      if (el && "value" in el) el.value = ""
    }
    if (method === "Input.insertText") {
      const el = doc.activeElement
      if (el && "value" in el) el.value += params.text
    }
  }

  const attached = new Set()
  const listenerSet = (set) => ({ addListener: (fn) => set.add(fn), removeListener: (fn) => set.delete(fn) })
  globalThis.chrome = {
    runtime: { lastError: undefined },
    tabs: {
      create: async ({ url }) => ({ id: create(url).id }),
      update: async (id, { url }) => (url ? navigate(tabs.get(id), url) : null, { id }),
      get: async (id) => {
        const t = tabs.get(id)
        if (!t) throw new Error(`No tab with id: ${id}`)
        return { id, url: t.url, title: t.window?.document.title ?? "", status: t.status }
      },
      remove: async (id) => void tabs.delete(id),
      onUpdated: listenerSet(updated),
    },
    webNavigation: {
      getFrame: async ({ tabId, frameId }) => {
        const t = tabs.get(tabId)
        return frameId === 0 && t ? { frameId: 0, url: t.url, documentId: t.documentId } : null
      },
      onCommitted: listenerSet(committed),
    },
    scripting: {
      executeScript: async ({ target, func, args = [] }) => {
        const t = tabs.get(target.tabId)
        if (target.documentIds && !target.documentIds.includes(t.documentId)) throw new Error("No document with that id")
        t.injections.push(t.url)
        const result = t.window.eval(`(${func.toString()})`)(...args)
        return [{ result: result === undefined ? undefined : JSON.parse(JSON.stringify(result)) }]
      },
    },
    debugger: {
      attach: (target, _v, cb) => {
        if (!debuggerWorks) {
          chrome.runtime.lastError = { message: "Another debugger is already attached to the tab" }
          cb()
          chrome.runtime.lastError = undefined
          return
        }
        attached.add(target.tabId)
        cb()
      },
      detach: (target, cb) => {
        attached.delete(target.tabId)
        cb?.()
      },
      sendCommand: (target, method, params, cb) => {
        const t = tabs.get(target.tabId)
        cdp.push([target.tabId, method, params])
        let result = {}
        if (method.startsWith("Input.")) input(t, method, params)
        if (method === "Page.captureScreenshot") result = { data: Buffer.from(`png of ${t.url}`).toString("base64") }
        if (method === "Runtime.evaluate") result = { result: { value: t.window.eval(params.expression) } }
        if (method === "DOM.getDocument") result = { root: { nodeId: 1 } }
        if (method === "DOM.querySelector") result = { nodeId: t.window.document.querySelector(params.selector) ? 2 : 0 }
        setTimeout(() => cb(result), 0)
      },
      onDetach: { addListener: () => {} },
    },
    storage: fakeStorage({ "moh.agent": { allowedHosts: allowed }, ...extra }),
  }
  return { tabs, cdp, attached, only: () => [...tabs.values()].find((t) => t.commits.some((u) => u.startsWith("http"))) }
}

let runSeq = 0
function runner(list = ["allowed.test"]) {
  const run = `cdp-run-${++runSeq}`
  let n = 0
  return { run, step: (command, args = {}) => runStep(run, { command, args, step: ++n, allowed_hosts: list }) }
}

const LEFT = /which isn't on this browser's allowed sites/
async function assertStops(promise, pattern) {
  await assert.rejects(promise, (err) => {
    assert.equal(err.code, "left_allowed_sites", `code on: ${err.message}`)
    assert.match(err.message, pattern)
    assert.match(err.message, LEFT)
    return true
  })
}

test("a click is real input — a curved path, a press and a release — and the page sees a trusted click", async () => {
  const b = browser()
  const r = runner()
  const went = await r.step("goto", { url: "https://allowed.test/start" })
  assert.equal(went.ok, true)
  assert.equal(went.command, "goto")
  const res = await r.step("click", { selector: "#save" })
  assert.equal(res.input, "cdp")
  assert.equal(res.strategy, "css")
  assert.ok(res.path_points >= 8, `path points: ${res.path_points}`)
  const tab = b.only()
  assert.equal(tab.window.document.getElementById("out").textContent, "saved:true")
  const kinds = b.cdp.filter(([, m]) => m === "Input.dispatchMouseEvent").map(([, , p]) => p.type)
  assert.ok(kinds.filter((k) => k === "mouseMoved").length >= 8)
  assert.deepEqual(kinds.slice(-2), ["mousePressed", "mouseReleased"])
  await closeRun(r.run)
})

test("text= and role= selectors find what a person would call them", async () => {
  const b = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  assert.equal((await r.step("click", { selector: "text=Cancel" })).strategy, "text")
  assert.equal(b.only().window.document.getElementById("out").textContent, "cancelled")
  assert.equal((await r.step("click", { selector: 'role=button[name="Save changes"]' })).strategy, "role")
  assert.equal(b.only().window.document.getElementById("out").textContent, "saved:true")
  // Without a prefix, CSS first, then visible text.
  assert.equal((await r.step("click", { selector: "Cancel" })).strategy, "text")
  await assert.rejects(r.step("click", { selector: "text=Nothing like this" }), /nothing on the page matches text=Nothing like this/)
  await closeRun(r.run)
})

test("a real click on a link off the list stops the run — the guard wraps real input too", async () => {
  const b = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assertStops(r.step("click", { selector: "#away" }), /^step 2 went to evil\.test,/)
  await assertStops(r.step("type", { selector: "#field", text: "a password" }), /^step 2 went to evil\.test,/)
  // No input and no injection ever reached the off-list page.
  const tab = b.only()
  assert.deepEqual(tab.injections.filter((u) => u.startsWith("https://evil.test")), [])
  await closeRun(r.run)
})

test("typing is per-key input that replaces the field, and the result never echoes it", async () => {
  const b = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  const res = await r.step("type", { selector: "#q2", text: "fine" })
  assert.equal(res.characters, 4)
  assert.ok(!JSON.stringify(res).includes("fine"))
  assert.equal(b.only().window.document.getElementById("q2").value, "fine")
  const keys = b.cdp.filter(([, m, p]) => m === "Input.dispatchKeyEvent" && p.type === "keyDown").map(([, , p]) => p.key)
  assert.deepEqual(keys.slice(-4), ["f", "i", "n", "e"])
  // Arabic goes in as text, not as invented key codes.
  await r.step("type", { selector: "#q2", text: "مرحبا" })
  assert.equal(b.only().window.document.getElementById("q2").value, "مرحبا")
  assert.ok(b.cdp.some(([, m, p]) => m === "Input.insertText" && p.text === "م"))
  // A trusted Enter submits the form — to an allowed action here.
  await r.step("press", { key: "Enter", selector: "#q2" })
  assert.equal((await r.step("extract", { selector: "#result" })).value, "results")
  await closeRun(r.run)
})

test("a real Enter that submits to an off-list action stops the run", async () => {
  browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await r.step("type", { selector: "#q", text: "moharrik" })
  await assertStops(r.step("press", { key: "Enter" }), /^step 3 went to evil\.test,/)
  await closeRun(r.run)
})

test("a screenshot and a read-only eval come back, and neither is taken of an off-list page", async () => {
  browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  const shot = await r.step("screenshot")
  assert.equal(shot.format, "png")
  assert.equal(Buffer.from(shot.base64, "base64").toString(), "png of https://allowed.test/start")
  const ev = await r.step("eval_readonly", { expression: "document.title" })
  assert.equal(ev.value, "Start")
  await r.step("goto", { url: "https://allowed.test/drifts" })
  await sleep(80)
  await assertStops(r.step("screenshot"), /went to evil\.test/)
  await closeRun(r.run)
})

test("structured extraction: fields, and fields per row", async () => {
  browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  const one = await r.step("extract", { fields: { title: "title", save: "#save|@aria-label" } })
  assert.deepEqual(one.fields, { title: "Start", save: "Save changes" })
  const rows = await r.step("extract", { list: "button", fields: { label: ":scope", id: "@id" } })
  assert.deepEqual(rows.rows, [
    { label: "Save", id: "save" },
    { label: "Cancel", id: "cancel" },
  ])
  assert.equal(rows.summary, "2 rows")
  await closeRun(r.run)
})

test("where the debugger cannot attach, a step still runs with script input and says so", async () => {
  const b = browser({ debuggerWorks: false })
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  const res = await r.step("click", { selector: "#save" })
  assert.equal(res.input, "script")
  assert.equal(b.only().window.document.getElementById("out").textContent, "saved:false")
  // What needs the debugger says so, rather than half-working.
  await assert.rejects(r.step("screenshot"), /a screenshot needs the debugger: cannot drive this tab: Another debugger/)
  await closeRun(r.run)
})

test("uploads are off until the browser's owner names a folder, and stay inside it", async () => {
  let b = browser()
  let r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assert.rejects(r.step("upload", { selector: "#file", file: "C:/Users/me/.ssh/id_rsa" }), /uploads are off in this browser/)
  await closeRun(r.run)

  b = browser({ extra: { "moh.uploadFolder": "C:\\Users\\me\\Uploads" } })
  r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assert.rejects(r.step("upload", { selector: "#file", file: "C:/Users/me/.ssh/id_rsa" }), /is not in the folder/)
  await assert.rejects(r.step("upload", { selector: "#file", file: "C:/Users/me/Uploads/../.ssh/id_rsa" }), /is not in the folder/)
  const ok = await r.step("upload", { selector: "#file", file: "C:/Users/me/Uploads/invoice.pdf" })
  assert.equal(ok.files, 1)
  const set = b.cdp.find(([, m]) => m === "DOM.setFileInputFiles")
  assert.deepEqual(set[2].files, ["C:/Users/me/Uploads/invoice.pdf"])
  await closeRun(r.run)
})

test("each run drives its own tab, and run.end gives it back", async () => {
  const b = browser()
  const one = runner()
  const two = runner()
  await one.step("goto", { url: "https://allowed.test/start" })
  await two.step("goto", { url: "https://allowed.test/next" })
  const opened = [...b.tabs.values()].filter((t) => t.commits.some((u) => u.startsWith("http")))
  assert.equal(opened.length, 2, "two runs, two tabs")
  // Run one acting does not touch run two's page.
  await one.step("click", { selector: "#save" })
  assert.equal((await two.step("extract", { selector: "#here" })).value, "next")
  const released = await closeRun(one.run)
  assert.equal(released.released, true)
  await closeRun(two.run)
})
