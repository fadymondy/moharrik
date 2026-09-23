// MH-398: the driver replays every step the recorder emits.
//
// The recorder (record-page.js) emits press, select and scroll; the driver used
// to throw "does not know the step" for all three, so a recording that used
// Enter, a dropdown or a scroll saved fine and then failed on every run.
//
// These run the driver's real code against jsdom. chrome.scripting is faked the
// way Chrome actually behaves: the injected function is SERIALISED and evaluated
// inside the page, and its result comes back through a structured clone — so a
// function that reached for module scope, or returned something that does not
// survive the trip, fails here as it would in the browser.
//
// What this cannot show is Chrome itself; web/e2e/replay-steps-check.mjs does.

import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { JSDOM } from "jsdom"

import { parseKey, pressInPage, runStep, scrollInPage, selectInPage } from "../driver.js"

const here = (p) => new URL(p, import.meta.url)
const FIXTURE = readFileSync(here("./fixtures/replay.html"), "utf8")
const PAGE_SCRIPT = FIXTURE.match(/<script>([\s\S]*?)<\/script>/)[1]

/** A page, with the fixture's own script run in it and scrolling made observable. */
function page(html = FIXTURE, url = "http://127.0.0.1/replay.html") {
  const dom = new JSDOM(html, { url, runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  if (html === FIXTURE) w.eval(PAGE_SCRIPT)
  // jsdom lays nothing out, so it cannot scroll; record what was asked instead.
  w.__scrolls = []
  let sx = 0
  let sy = 0
  Object.defineProperty(w, "scrollX", { get: () => sx, configurable: true })
  Object.defineProperty(w, "scrollY", { get: () => sy, configurable: true })
  w.scrollBy = (opts) => {
    w.__scrolls.push({ on: "window", ...opts })
    sx += opts.left || 0
    sy = Math.max(0, sy + (opts.top || 0))
  }
  w.Element.prototype.scrollIntoView = function (opts) {
    w.__scrolls.push({ on: this.id || this.tagName, intoView: opts })
  }
  w.Element.prototype.scrollBy = function (opts) {
    w.__scrolls.push({ on: this.id || this.tagName, ...opts })
  }
  return w
}

/** Run an injected function the way chrome.scripting does: source in, clone out. */
function inject(w, func, args) {
  const fn = w.eval(`(${func.toString()})`)
  const result = fn(...args)
  return result === undefined ? undefined : JSON.parse(JSON.stringify(result))
}

/** A fake chrome with one tab that loads the fixture. */
function browser(html = FIXTURE) {
  const listeners = new Set()
  const state = { window: null, loads: 0 }
  const TAB = 7
  const load = (url) => {
    state.window = page(html, url)
    state.loads++
    setTimeout(() => [...listeners].forEach((fn) => fn(TAB, { status: "complete" })), 5)
  }
  globalThis.chrome = {
    tabs: {
      create: async ({ url }) => (load(url), { id: TAB }),
      update: async (id, { url }) => (load(url), { id }),
      get: async (id) => ({ id, url: state.window.location.href, title: state.window.document.title, status: "complete" }),
      remove: async () => {},
      onUpdated: { addListener: (fn) => listeners.add(fn), removeListener: (fn) => listeners.delete(fn) },
    },
    scripting: {
      executeScript: async ({ func, args = [] }) => [{ result: inject(state.window, func, args) }],
    },
  }
  return state
}

let runSeq = 0
const newRun = () => `run-${++runSeq}`

/* ------------------------------------------------ the recorder's vocabulary */

test("every command the recorder can emit is one the driver knows", async () => {
  // Read from the recorder's own source, so a step added there and forgotten
  // here fails this test instead of every run that uses it.
  const emitted = new Set()
  for (const file of ["../record-page.js", "../recorder.js"]) {
    for (const [, command] of readFileSync(here(file), "utf8").matchAll(/command:\s*"([a-z_]+)"/g)) emitted.add(command)
  }
  assert.ok(emitted.has("press") && emitted.has("select") && emitted.has("scroll"), `found ${[...emitted]}`)

  browser()
  const run = newRun()
  await runStep(run, { command: "goto", args: { url: "http://127.0.0.1/replay.html" } })
  for (const command of emitted) {
    try {
      await runStep(run, { command, args: {} })
    } catch (err) {
      assert.doesNotMatch(String(err.message), /does not know the step/, `${command} is not replayable`)
    }
  }
})

test("an unknown step is still refused, by name", async () => {
  browser()
  await assert.rejects(runStep(newRun(), { command: "teleport", args: {} }), /does not know the step "teleport"/)
})

test("press, select and scroll need the run's page, like click and type", async () => {
  browser()
  for (const command of ["press", "select", "scroll"]) {
    await assert.rejects(runStep(newRun(), { command, args: { key: "Enter", selector: "#country", value: "x", dy: 300 } }), /start with a goto step/)
  }
})

/* ------------------------------------------ a recorded workflow, replayed */

test("a recording that types, presses Enter, chooses from a dropdown and scrolls replays", async () => {
  const tab = browser()
  const run = newRun()
  // Exactly the shapes record-page.js sends.
  const steps = [
    { command: "navigate", args: { url: "http://127.0.0.1/replay.html" } },
    { command: "type", args: { selector: "#q", text: "moharrik", clear: true } },
    { command: "press", args: { key: "Enter" } },
    { command: "select", args: { selector: "#country", value: "United Kingdom" } },
    { command: "scroll", args: { dy: 2100 } },
    { command: "click", args: { selector: "#deep" } },
  ]
  const results = []
  for (const step of steps) results.push(await runStep(run, step))
  const doc = tab.window.document

  assert.equal(doc.getElementById("searched").textContent, "searched: moharrik")
  assert.match(results[2].summary, /pressed Enter — submitted the form/)
  assert.equal(doc.getElementById("chosen").textContent, "chosen: uk")
  assert.deepEqual([results[3].value, results[3].label], ["uk", "United Kingdom"])
  assert.equal(tab.window.scrollY, 2100)
  assert.match(results[4].summary, /scrolled by 2100px down/)
  assert.equal(doc.getElementById("clicked").textContent, "clicked at scrollY 2100")
  // Nothing re-navigated: the Enter was handled by the page's submit handler.
  assert.equal(tab.loads, 1)
})

/* ---------------------------------------------------------------- press */

test("Enter goes to the focused field and submits its form when nothing handles it", () => {
  const w = page()
  const seen = []
  const q = w.document.getElementById("q")
  for (const type of ["keydown", "keypress", "keyup"]) q.addEventListener(type, (e) => seen.push([type, e.key, e.keyCode, e.which]))
  q.focus()
  q.value = "hello"
  const result = inject(w, pressInPage, ["", "Enter", {}])
  assert.equal(result.action, "submitted")
  assert.equal(result.target, "#q")
  assert.equal(w.document.getElementById("searched").textContent, "searched: hello")
  // Legacy handlers test keyCode === 13; they must see it.
  assert.deepEqual(seen, [
    ["keydown", "Enter", 13, 13],
    ["keypress", "Enter", 13, 13],
    ["keyup", "Enter", 13, 13],
  ])
})

test("Enter the page handles itself does not also submit", () => {
  const w = page()
  const q = w.document.getElementById("q")
  q.addEventListener("keydown", (e) => {
    if (e.key === "Enter") e.preventDefault()
  })
  q.focus()
  const result = inject(w, pressInPage, ["", "Enter", {}])
  assert.equal(result.handled, true)
  assert.equal(w.document.getElementById("searched").textContent, "not searched")
})

test("Enter clicks the form's default button, so the button's own handler runs", () => {
  const w = page(`<form id=f><input id=a><button type=button id=other>x</button><button id=go>Go</button></form><p id=out></p>`)
  const out = w.document.getElementById("out")
  w.document.getElementById("go").addEventListener("click", () => (out.textContent += "click;"))
  w.document.getElementById("f").addEventListener("submit", (e) => {
    e.preventDefault()
    out.textContent += `submit:${e.submitter?.id};`
  })
  w.document.getElementById("a").focus()
  assert.equal(inject(w, pressInPage, ["", "Enter", {}]).action, "submitted")
  assert.equal(out.textContent, "click;submit:go;")
})

test("Enter follows the HTML rules for implicit submission", () => {
  // Two text fields and no submit button: a browser does not submit.
  let w = page(`<form id=f><input id=a><input id=b></form>`)
  let submitted = false
  w.document.getElementById("f").addEventListener("submit", (e) => (e.preventDefault(), (submitted = true)))
  w.document.getElementById("a").focus()
  assert.equal(inject(w, pressInPage, ["", "Enter", {}]).action, undefined)
  assert.equal(submitted, false)

  // A disabled default button blocks it too.
  w = page(`<form id=f><input id=a><button id=go disabled>Go</button></form>`)
  submitted = false
  w.document.getElementById("f").addEventListener("submit", (e) => (e.preventDefault(), (submitted = true)))
  w.document.getElementById("a").focus()
  inject(w, pressInPage, ["", "Enter", {}])
  assert.equal(submitted, false)
})

test("Enter on a link or a button activates it; in a textarea it adds a line", () => {
  const w = page(`<a id=l href="#x">x</a><button id=b>b</button><textarea id=t>ab</textarea><p id=out></p>`)
  const out = w.document.getElementById("out")
  w.document.getElementById("l").addEventListener("click", (e) => (e.preventDefault(), (out.textContent += "link;")))
  w.document.getElementById("b").addEventListener("click", () => (out.textContent += "button;"))
  assert.equal(inject(w, pressInPage, ["#l", "Enter", {}]).action, "clicked")
  assert.equal(inject(w, pressInPage, ["#b", "Enter", {}]).action, "clicked")
  assert.equal(out.textContent, "link;button;")

  const t = w.document.getElementById("t")
  let inputs = 0
  t.addEventListener("input", () => inputs++)
  t.focus()
  t.setSelectionRange(1, 1)
  assert.equal(inject(w, pressInPage, ["", "Enter", {}]).action, "newline")
  assert.equal(t.value, "a\nb")
  assert.equal(inputs, 1)
})

test("a selector focuses its element first; a missing one says so", () => {
  const w = page()
  inject(w, pressInPage, ["#q", "Escape", {}])
  assert.equal(w.document.activeElement.id, "q")
  const missing = inject(w, pressInPage, ["#nope", "Enter", {}])
  assert.equal(missing.ok, false)
  assert.match(missing.reason, /nothing on the page matches #nope/)
})

test("Escape is dispatched and does nothing else", () => {
  const w = page()
  const seen = []
  const q = w.document.getElementById("q")
  q.addEventListener("keydown", (e) => seen.push(e.key + ":" + e.keyCode))
  q.addEventListener("keypress", () => seen.push("keypress"))
  q.focus()
  const result = inject(w, pressInPage, ["", "Escape", {}])
  assert.deepEqual(seen, ["Escape:27"]) // Escape produces no keypress
  assert.equal(result.action, undefined)
  assert.equal(w.document.getElementById("searched").textContent, "not searched")
})

test("Tab and Shift+Tab move focus, skipping what a person could not tab to", () => {
  const w = page(`<input id=a><input id=hid type=hidden><input id=off disabled><button id=b>b</button><a id=nolink>x</a><div hidden><input id=gone></div><select id=c></select>`)
  w.document.getElementById("a").focus()
  assert.equal(inject(w, pressInPage, ["", "Tab", {}]).target, "#b")
  assert.equal(w.document.activeElement.id, "b")
  inject(w, pressInPage, ["", "Tab", {}])
  assert.equal(w.document.activeElement.id, "c")
  inject(w, pressInPage, ["", "Tab", { shift: true }])
  assert.equal(w.document.activeElement.id, "b")
})

test("modifiers reach the events, and a shortcut does not submit", () => {
  const w = page()
  const q = w.document.getElementById("q")
  let flags = null
  q.addEventListener("keydown", (e) => (flags = [e.ctrlKey, e.shiftKey, e.altKey, e.metaKey]))
  q.focus()
  inject(w, pressInPage, ["", "Enter", { control: true, shift: true }])
  assert.deepEqual(flags, [true, true, false, false])
  assert.equal(w.document.getElementById("searched").textContent, "not searched")
})

test("keys and modifiers are read from every shape a step is written in", () => {
  const none = { alt: false, control: false, meta: false, shift: false }
  assert.deepEqual(parseKey({ key: "Enter" }), { key: "Enter", modifiers: none })
  assert.deepEqual(parseKey({ key: "Shift+Tab" }), { key: "Tab", modifiers: { ...none, shift: true } })
  assert.deepEqual(parseKey({ key: "a", modifiers: ["Ctrl", "Meta"] }), { key: "a", modifiers: { ...none, control: true, meta: true } })
  assert.deepEqual(parseKey({ key: "a", modifiers: { control: true, alt: false } }), { key: "a", modifiers: { ...none, control: true } })
  assert.deepEqual(parseKey({ key: "+" }), { key: "+", modifiers: none })
  assert.deepEqual(parseKey({ key: "Esc" }).key, "Escape")
  assert.throws(() => parseKey({ key: "a", modifiers: ["Hyper"] }), /not a modifier key/)
})

test("press without a key is refused", async () => {
  browser()
  const run = newRun()
  await runStep(run, { command: "goto", args: { url: "http://127.0.0.1/replay.html" } })
  await assert.rejects(runStep(run, { command: "press", args: {} }), /this step has no key/)
})

/* --------------------------------------------------------------- select */

test("select takes the recorder's visible label and fires input then change", () => {
  const w = page()
  const el = w.document.getElementById("country")
  const events = []
  el.addEventListener("input", () => events.push("input:" + el.value))
  el.addEventListener("change", () => events.push("change:" + el.value))
  const result = inject(w, selectInPage, ["#country", "United Kingdom", ""])
  assert.deepEqual(result, { ok: true, value: "uk", label: "United Kingdom" })
  assert.deepEqual(events, ["input:uk", "change:uk"])
  assert.equal(w.document.activeElement, el)
})

test("select also takes a real option value, and `by` settles an ambiguity", () => {
  const w = page(`<select id=s><option value="2">1</option><option value="1">2</option><option value="fr">  France  </option></select>`)
  assert.equal(inject(w, selectInPage, ["#s", "fr", ""]).label, "France")
  // "1" is both one option's label and another's value: the label wins,
  // because a label is what a recording holds...
  assert.equal(inject(w, selectInPage, ["#s", "1", ""]).value, "2")
  // ...and `by: value` says otherwise when that is what was meant.
  assert.equal(inject(w, selectInPage, ["#s", "1", "value"]).value, "1")
  assert.equal(inject(w, selectInPage, ["#s", "fr", "label"]).ok, false)
  // Whitespace and case in a label are forgiven.
  assert.equal(inject(w, selectInPage, ["#s", "france", ""]).value, "fr")
})

test("select sets the value through the native setter, past a framework's own", () => {
  // A controlled-field library wraps the element's value property. A plain
  // `el.value = x` would go to the wrapper and be swallowed.
  const w = page()
  const el = w.document.getElementById("country")
  let swallowed = null
  Object.defineProperty(el, "value", {
    configurable: true,
    get: () => w.HTMLSelectElement.prototype.__lookupGetter__("value").call(el),
    set: (v) => (swallowed = v),
  })
  inject(w, selectInPage, ["#country", "France", ""])
  assert.equal(swallowed, null)
  assert.equal(el.selectedIndex, 3)
  assert.equal(w.document.getElementById("chosen").textContent, "chosen: fr")
})

test("select explains what it could not do", () => {
  const w = page()
  assert.match(inject(w, selectInPage, ["#country", "Atlantis", ""]).reason, /has no option "Atlantis"/)
  assert.match(inject(w, selectInPage, ["#country", "Nowhere", ""]).reason, /"Nowhere" is disabled/)
  assert.match(inject(w, selectInPage, ["#q", "x", ""]).reason, /not a dropdown/)
  assert.match(inject(w, selectInPage, ["#missing", "x", ""]).reason, /nothing on the page matches/)
})

test("a select step may name the label explicitly, and must name something", async () => {
  const tab = browser()
  const run = newRun()
  await runStep(run, { command: "goto", args: { url: "http://127.0.0.1/replay.html" } })
  const r = await runStep(run, { command: "select", args: { selector: "#country", label: "Egypt" } })
  assert.equal(r.summary, 'chose "Egypt" in #country')
  assert.equal(tab.window.document.getElementById("country").value, "eg")
  await assert.rejects(runStep(run, { command: "select", args: { selector: "#country" } }), /does not say which option/)
  await assert.rejects(runStep(run, { command: "select", args: { value: "Egypt" } }), /no selector/)
  await assert.rejects(runStep(run, { command: "select", args: { selector: "#country", value: "Atlantis" } }), /has no option/)
})

/* --------------------------------------------------------------- scroll */

test("scroll moves the window by the recorded distance", () => {
  const w = page()
  const result = inject(w, scrollInPage, ["", 0, 900])
  assert.deepEqual(w.__scrolls, [{ on: "window", left: 0, top: 900, behavior: "instant" }])
  assert.deepEqual(result, { ok: true, mode: "by", x: 0, y: 900, moved: true })
})

test("scroll with only a selector brings that element into view", () => {
  const w = page()
  const result = inject(w, scrollInPage, ["#deep", 0, 0])
  assert.equal(result.mode, "into-view")
  assert.deepEqual(JSON.parse(JSON.stringify(w.__scrolls)), [{ on: "deep", intoView: { block: "center", inline: "nearest" } }])
})

test("scroll with a selector and a distance scrolls that element", () => {
  const w = page()
  inject(w, scrollInPage, ["#spacer", 40, 300])
  assert.deepEqual(w.__scrolls, [{ on: "spacer", left: 40, top: 300, behavior: "instant" }])
  // <html> and <body> mean the page itself.
  inject(w, scrollInPage, ["body", 0, 200])
  assert.equal(w.__scrolls[1].on, "window")
})

test("a scroll step needs a distance or an element, in numbers", async () => {
  browser()
  const run = newRun()
  await runStep(run, { command: "goto", args: { url: "http://127.0.0.1/replay.html" } })
  await assert.rejects(runStep(run, { command: "scroll", args: {} }), /no distance to scroll/)
  await assert.rejects(runStep(run, { command: "scroll", args: { dy: "lots" } }), /dy has to be a number/)
  await assert.rejects(runStep(run, { command: "scroll", args: { selector: "#nope" } }), /nothing on the page matches #nope/)
  const r = await runStep(run, { command: "scroll", args: { deltaY: -50 } })
  assert.match(r.summary, /already at the end/)
})
