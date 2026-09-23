/*
What a step actually does in this browser.

THE RUNTIME (MH-478), ported from fadymondy.com's driver (FM-91, FM-267):

  ONE TAB PER RUN, in the Moharrik window (driver/tabs.js, run-tabs-core.js):
  a run takes a tab on its first navigation and only ever acts in it; at the
  end the tab is emptied and kept for the next run, or closed. A run may be
  bound to a KEPT site tab by key instead (step.tab from the server, or a
  step's own `persistent` argument), read without reloading while it is fresh.

  REAL INPUT, through Chrome's debugger (driver/cdp.js): a click is a curved
  pointer path, a hover pause, a press and a release; typing is per-character
  key events at a person's uneven pace (driver/human.js). Pages built for
  people behave for it — menus that open on a real pointerdown, React inputs
  that ignore a programmatic value, debounced search boxes. The debugger is
  attached when a run first acts and released 30 seconds after it goes idle,
  so an idle browser carries no banner. Where the debugger cannot attach
  (DevTools already owns the tab, or a harness without it) a step falls back
  to the script input this driver always had, and its result says which
  input it used — never silently.

  SELECTORS with a stated order (driver/locate.js): css=, text=, role= prefixes
  win; otherwise CSS, then visible text, then role.

  STRUCTURED RESULTS: every step answers { ok, command, ms, input, ... }, and
  a failure is thrown with the browser's own sentence — and a code when the
  server must act on it (left_allowed_sites).

It MUST cover everything the recorder (record-page.js) emits. A recorded step
this file does not know saves as a workflow without complaint and then fails on
every run, which is exactly what press, select and scroll did (MH-398).

On the script path every event is untrusted and the browser performs NO default
action for it: an Enter dispatched into a form field does not submit the form.
Where a person's key would have done something, that path does it itself
(pressInPage), and only when the page has not handled the key.
*/

import { KEYS, get } from "./config.js"
import { gotoHost, hostAllowed, pageVerdict } from "./hosts.js"
import { attach, cdpAvailable, detach, keepRendering, send, sendSoon } from "./driver/cdp.js"
import { cursorPath, jitter, keyDelay, moveDelay, pointWithin, randomBetween, sleep, wheelSteps } from "./driver/human.js"
import { findInPage } from "./driver/locate.js"
import { extractInPage, extractLegacyInPage } from "./driver/extract.js"
import { runTabs, signedOut } from "./driver/runs.js"

/* ------------------------------------------------ the allowed sites (MH-422)

The server checks a goto's URL against the browser's allowed sites before the
run is queued. That was the ONLY check, and it is not enough: a click on a
link, an Enter that submits a form, a dropdown that redirects, or a goto that
is answered with a redirect all move the tab without a goto — and every step
after that acted wherever the tab had landed.

So the tab is watched, and checked, from THIS side:

  BEFORE every step that touches the page (click, type, press, select, scroll,
  extract, wait), the run tab's URL — read with chrome.tabs.get and
  chrome.webNavigation.getFrame, never from anything the page reports — must
  be on the list. The script is then injected into THAT document by its
  documentId, so a navigation that commits between the check and the act
  makes the injection fail instead of landing on the new page.

  AFTER every step, once any navigation it started has settled, the landing
  page is checked again. The URL checked is the committed one, which is where
  a redirect chain ENDED; hops it passed through on the way are not pages
  anything acted on.

  AT COMMIT, webNavigation.onCommitted (and tabs.onUpdated's url, the same
  signal through the tabs permission) judges every top-level navigation of a
  run tab as it happens. It is the one check that catches a page moving by
  itself between steps — a script redirect, a meta refresh — and it ends the
  waiting of a step in flight, so an off-list page never gets a later step.

  Why not onBeforeNavigate: it cannot cancel anything. Neither can any other
  webNavigation event; only declarativeNetRequest can block a request, and it
  needs host permission for the very hosts this browser deliberately does not
  hold. A form POSTed to an off-list action has been sent by the time any of
  these fire. What this guarantees is that no STEP acts there — and the
  off-list page is blanked at once rather than left running in a signed-in
  browser until the run is closed.

Leaving the list STOPS the run. The error carries code "left_allowed_sites";
the server fails the run with it whatever continue_on_error says, and this
side refuses every later step of the run as well, so the two ends agree even
if one of them is out of date.

What counts as on the list is pageVerdict in hosts.js — the server's own rule,
tested against the server's own cases. about:blank is neither on nor off: it
is normal mid-navigation and for a new tab, so it does not stop a run, but no
step acts on it. data:, javascript:, blob:, file:, chrome: and extension pages
(this one's included) are OFF the list: the list names websites.
*/

export const LEFT_ALLOWED_SITES = "left_allowed_sites"

const runs = new Map() // run id -> run state (see runFor)

const DEFAULT_TIMEOUT = 20000

function runFor(runID) {
  let run = runs.get(runID)
  if (!run) {
    run = {
      id: runID,
      tabID: null,
      allowed: [],
      current: null, // the 1-based step number running now, when the server said
      last: null, // the last step that finished
      breach: null, // why this run stopped, once it has left the list
      wake: new Set(), // waiters to release the moment it does
      persistent: "", // the kept site tab this run is bound to, if any
      input: "", // "cdp" once the debugger is attached for this run, "script" when it cannot be
      inputWhy: "", // why not, when it cannot
    }
    runs.set(runID, run)
  }
  watchNavigations()
  return run
}

/** The list the SERVER sent with this step; the one it sent at connect, for an older server. */
async function allowedFor(step) {
  if (Array.isArray(step.allowed_hosts)) return step.allowed_hosts.map(String)
  try {
    const agent = (await get(KEYS.agent)) || {}
    return Array.isArray(agent.allowedHosts) ? agent.allowedHosts.map(String) : []
  } catch {
    return [] // cannot tell: an empty list allows nothing
  }
}

function breachError(run) {
  const err = new Error(run.breach)
  err.code = LEFT_ALLOWED_SITES
  return err
}

/**
 * The run has left its allowed sites. Recorded once — the first place it left
 * is the one worth naming — and it wakes anything waiting in a step.
 */
function leave(run, where, phase) {
  if (run.breach) return
  const tail = `${where}, which isn't on this browser's allowed sites`
  if (phase === "during" && run.current != null) run.breach = `step ${run.current} went to ${tail}`
  else if (run.last != null) run.breach = `after step ${run.last} the page went to ${tail}`
  else if (run.current != null) run.breach = `before step ${run.current} the page went to ${tail}`
  else run.breach = `this run's page went to ${tail}`
  // Nothing more of that page runs in this browser than already has.
  if (run.tabID != null) chrome.tabs.update(run.tabID, { url: "about:blank" }).catch(() => {})
  for (const fn of [...run.wake]) fn()
  run.wake.clear()
}

/** Judge a URL the run's tab is on. Throws when it is off the list. */
function judge(run, url, phase) {
  if (run.breach) throw breachError(run)
  const verdict = pageVerdict(url, run.allowed)
  if (verdict.state === "outside") {
    leave(run, verdict.where, phase)
    throw breachError(run)
  }
  return verdict.state
}

/**
 * Where the run's tab is, from the browser — chrome.tabs.get and the top
 * frame's committed document — and whether a step may act there. Returns the
 * documentId to pin the injection to, when the browser gives one.
 */
async function checkPage(run, phase) {
  if (run.breach) throw breachError(run)
  if (run.tabID == null) throw new Error("this run has not opened a page yet — start with a goto step")
  let tab
  try {
    tab = await chrome.tabs.get(run.tabID)
  } catch {
    throw new Error("this run's tab has been closed")
  }
  let frame = null
  if (chrome.webNavigation?.getFrame) {
    try {
      frame = await chrome.webNavigation.getFrame({ tabId: run.tabID, frameId: 0 })
    } catch {
      frame = null
    }
  }
  const states = [judge(run, tab.url ?? "", phase)]
  if (frame?.url != null) states.push(judge(run, frame.url, phase))
  return { blank: states.includes("blank"), documentId: frame?.documentId || null, url: tab.url }
}

/** Resolve after ms, or as soon as the run leaves its sites. */
function pause(run, ms) {
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer)
      run.wake.delete(done)
      resolve()
    }
    const timer = setTimeout(done, ms)
    run.wake.add(done)
  })
}

// The commit-time watcher. Registered once per chrome object: at load in the
// extension, and again for each fake chrome a test installs.
const watching = new WeakSet()
function watchNavigations() {
  const c = globalThis.chrome
  if (!c) return
  const onCommit = (tabId, url) => {
    for (const run of runs.values()) {
      if (run.tabID !== tabId || run.breach) continue
      try {
        judge(run, url, run.current != null ? "during" : "before")
      } catch {
        // Recorded on the run; the step in flight, or the next one, reports it.
      }
    }
  }
  const nav = c.webNavigation?.onCommitted
  if (nav && !watching.has(nav)) {
    watching.add(nav)
    nav.addListener((d) => {
      if (d.frameId === 0) onCommit(d.tabId, d.url)
    })
  }
  const upd = c.tabs?.onUpdated
  if (upd && !watching.has(upd)) {
    watching.add(upd)
    upd.addListener((tabId, info) => {
      if (info?.url) onCommit(tabId, info.url)
    })
  }
}
watchNavigations()

/* ------------------------------------------------------------------ steps */

export async function runStep(runID, step) {
  const command = String(step.command || "").toLowerCase()
  const args = step.args || {}
  const started = Date.now()

  if (command === "close" || command === "run.end") return closeRun(runID)

  const run = runFor(runID)
  run.allowed = await allowedFor(step)
  // Once a run has left its sites, nothing more of it runs here.
  if (run.breach) throw breachError(run)
  // Which kept tab, if any: the server's word (on the step) wins, then the
  // step's own argument. Decided before the run has a tab, and then fixed.
  if (run.tabID == null && !run.persistent) {
    run.persistent = String(step.tab || args.persistent || "").trim()
  }

  const n = Number(step.step)
  run.current = Number.isInteger(n) && n > 0 ? n : null
  try {
    const result = await dispatch(run, command, args, step)
    return { ok: true, command, ms: Date.now() - started, ...(run.input ? { input: run.input } : {}), ...result }
  } catch (err) {
    // Whatever went wrong, if the run left its sites on the way, THAT is the
    // failure — "nothing matches #submit" on an off-list page is a symptom.
    if (run.breach) throw breachError(run)
    throw err
  } finally {
    if (run.current != null) run.last = run.current
    run.current = null
  }
}

/** The command table. Aliases are the vocabulary recorded workflows already use. */
function dispatch(run, command, args, step) {
  switch (command) {
    case "goto":
    case "navigate":
    case "open":
      return goto(run, args)
    case "click":
      return acting(run, click, args)
    case "type":
    case "fill":
      return acting(run, type, args)
    case "press":
    case "key":
      return acting(run, press, args)
    case "select":
      return acting(run, select, args)
    case "scroll":
      return acting(run, scroll, args)
    case "extract":
    case "read":
      return acting(run, extract, args)
    case "wait":
      return wait(run, args)
    case "wait_for":
      return waitFor(run, args)
    case "upload":
      return acting(run, upload, args)
    case "screenshot":
      return acting(run, screenshot, args)
    case "eval_readonly":
      return acting(run, evalReadonly, args)
    case "release":
      return release(run)
    default:
      throw new Error(`this browser does not know the step "${step.command}"`)
  }
}

/** Every command this driver knows, for agent.info and for the server's editor. */
export const COMMANDS = [
  "goto", "navigate", "open", "click", "type", "fill", "press", "key", "select", "scroll",
  "extract", "read", "wait", "wait_for", "upload", "screenshot", "eval_readonly", "release", "close",
]

/**
 * closeRun gives the run's tab back — emptied and kept idle, or closed; a kept
 * site tab stays where it is — and lets go of the debugger. Called when a run
 * finishes, however it ended (run.end), and safe for a run that never took a tab.
 */
export async function closeRun(runID) {
  const run = runs.get(runID)
  runs.delete(runID)
  if (run) {
    for (const fn of [...run.wake]) fn()
    run.wake.clear()
  }
  let released = { released: false }
  try {
    released = await runTabs.release(runID)
  } catch {
    // Storage or the tab went away under us; the next runs.sync tidies up.
  }
  return { summary: "closed", ...released }
}

/** The server's list of this browser's runs in flight: every other run's tab is released. */
export async function syncRuns(active) {
  const ids = new Set(Array.isArray(active) ? active : [])
  for (const id of [...runs.keys()]) if (!ids.has(id)) runs.delete(id)
  const result = await runTabs.sync([...ids])
  return { ...result, in_use: await runTabs.inUse() }
}

/** A step that touches the page: the page is checked before it, and where it landed after. */
async function acting(run, fn, args) {
  await checkPage(run, "before")
  const result = await fn(run, args)
  await checkPage(run, "during")
  return result
}

async function goto(run, args) {
  const url = String(args.url || "")
  if (!url) throw new Error("this step has no url")
  // The server refused anything off the list before queueing; this is the same
  // rule again, for a server that has not (an older one, or a list changed
  // since the run was accepted).
  const host = gotoHost(url)
  if (!hostAllowed(run.allowed, host)) {
    const who = run.current != null ? `step ${run.current}` : "this step"
    const err = new Error(`${who} would go to ${host || "an address that is not a website"}, which isn't on this browser's allowed sites`)
    err.code = LEFT_ALLOWED_SITES
    throw err
  }

  if (run.tabID == null) {
    // The run's own tab — or its kept site tab — in the Moharrik window. A
    // browser whose tabs are all taken answers with code "tabs_busy", which
    // the server reads as "try again shortly", not as a failed run.
    try {
      const { tabId } = await runTabs.tabFor(run.id, run.persistent)
      run.tabID = tabId
    } catch (err) {
      const e = new Error(String(err?.message || err))
      if (err?.code) e.code = err.code
      throw e
    }
  }

  // A kept tab already on this site, and fresh, is read as it is.
  const plan = run.persistent ? await runTabs.planFor(run.id, url) : { navigate: true, reason: "" }
  if (plan.navigate) {
    // A tab the Moharrik window was just created with is still committing its
    // own about:blank; a navigation issued before that lands is overtaken by
    // it and the tab stays blank (seen in Chromium: a 20-second "navigate" to
    // about:blank). Let the blank settle first, and try once more if it wins.
    await blankSettled(run.tabID)
    await chrome.tabs.update(run.tabID, { url })
    await waitForLoad(run, url)
    if (!/^about:blank/i.test(url) && /^about:blank/i.test((await chrome.tabs.get(run.tabID)).url || "") && !run.breach) {
      await chrome.tabs.update(run.tabID, { url })
      await waitForLoad(run, url)
    }
    CURSOR.delete(run.tabID) // a fresh document: the old pointer means nothing
    if (run.input === "cdp") await keepRendering(run.tabID).catch(() => {})
  }
  // Where it LANDED: a redirect chain ends wherever it ends.
  const page = await checkPage(run, "during")
  const tab = await chrome.tabs.get(run.tabID)
  if (run.persistent) {
    await runTabs.markLoaded(run.id, url, !/^chrome-error:/.test(tab.url || "")).catch(() => {})
    const out = await signedOut(run.persistent, run.tabID)
    if (out) throw new Error(`${out} — sign in again in the "${run.persistent}" tab of the Moharrik window; this run was not retried`)
  }
  return {
    url: page.url ?? tab.url,
    title: tab.title,
    summary: tab.title || tab.url,
    ...(run.persistent ? { persistent: run.persistent, reloaded: plan.navigate, reason: plan.reason } : {}),
  }
}

/** Until a blank tab has finished loading its blank page (at most 3 seconds). */
async function blankSettled(tabId) {
  for (let i = 0; i < 30; i++) {
    let tab
    try {
      tab = await chrome.tabs.get(tabId)
    } catch {
      return
    }
    const blank = !tab.url || /^about:blank/i.test(tab.url)
    if (!blank || tab.status !== "loading") return
    await sleep(100)
  }
}

/**
 * Wait for the navigation to `target` to finish. A brand-new tab can report
 * its about:blank load finishing AFTER the run's own navigation has started,
 * so a "complete" while the tab is still blank does not count.
 */
function waitForLoad(run, target, timeout = DEFAULT_TIMEOUT) {
  const tabID = run.tabID
  const wantsBlank = /^about:blank/i.test(String(target || ""))
  return new Promise((resolve) => {
    // Resolved, not rejected, on the timeout: a page that never reaches
    // "complete" (a long poll, a stuck tracker) is still usable, and failing
    // the step here would make half the web unautomatable.
    const timer = setTimeout(done, timeout)
    function done() {
      clearTimeout(timer)
      chrome.tabs.onUpdated.removeListener(listener)
      run.wake.delete(done)
      resolve()
    }
    function listener(id, info) {
      if (id !== tabID || info.status !== "complete") return
      chrome.tabs.get(tabID).then((tab) => {
        if (wantsBlank || !/^about:blank/i.test(tab.url || "")) done()
      }, done)
    }
    chrome.tabs.onUpdated.addListener(listener)
    run.wake.add(done)
  })
}

/**
 * Run a function in the run's page — the page that was just checked, and no
 * other. With a documentId the injection is pinned to that document: if the tab
 * navigated in between, Chrome refuses it, and the new page is checked before
 * one more attempt.
 */
async function inPage(run, func, args) {
  for (let attempt = 0; ; attempt++) {
    const page = await checkPage(run, "during")
    if (page.blank) throw new Error("the page is blank — there is nothing on it to act on")
    const target = page.documentId ? { tabId: run.tabID, documentIds: [page.documentId] } : { tabId: run.tabID }
    try {
      const [{ result } = {}] = await chrome.scripting.executeScript({ target, args, func })
      run.lastDocumentId = page.documentId
      return result
    } catch (err) {
      if (run.breach) throw breachError(run)
      if (attempt === 0 && page.documentId) continue // the document changed under us
      throw err
    }
  }
}

/* ------------------------------------------------------------ real input

The debugger for this run's tab, attached once and held for the run (keep), so
a long wait does not drop it and a background tab keeps rendering. Released
at run.end by runTabs.release, or after 30 seconds idle once the run is over.
*/

// Where the pointer "is" in each tab. A page cannot be asked, so it is tracked,
// and consecutive clicks trace one continuous path.
const CURSOR = new Map()

async function useCdp(run) {
  if (run.input === "script") return false
  if (!cdpAvailable()) {
    run.input = "script"
    run.inputWhy = "this browser does not let the extension use its debugger"
    return false
  }
  try {
    await attach(run.tabID, { keep: true })
    run.input = "cdp"
    return true
  } catch (err) {
    // Usually DevTools, open on that tab: it owns the debugger. The run carries
    // on with script input and says so in every result.
    run.input = "script"
    run.inputWhy = String(err?.message || err)
    return false
  }
}

let markSeq = 0
const newMark = () => `m${Date.now().toString(36)}${(++markSeq).toString(36)}`

/**
 * Find the element a step names, in the page that was just checked, and tag it
 * for the acting injection. Returns what locate saw plus the documentId it saw
 * it in, so real input can be refused if the page changed before it lands.
 */
async function locateIn(run, selector, mark = newMark()) {
  if (!selector) throw new Error("this step has no selector")
  const found = await inPage(run, findInPage, [selector, mark])
  if (!found?.found) throw new Error(found?.reason || `nothing on the page matches ${selector}`)
  return { ...found, mark, documentId: run.lastDocumentId }
}

/** Real input must land on the document it was aimed at. */
async function stillThere(run, located) {
  const page = await checkPage(run, "during")
  if (located.documentId && page.documentId && page.documentId !== located.documentId) {
    throw new Error("the page changed before the step could act on it")
  }
}

function cursorOf(tabId, viewport) {
  return (
    CURSOR.get(tabId) || {
      x: randomBetween(40, Math.max(60, (viewport?.width ?? 800) * 0.4)),
      y: randomBetween(40, Math.max(60, (viewport?.height ?? 600) * 0.4)),
    }
  )
}

async function glideTo(tabId, target, viewport) {
  const path = cursorPath(cursorOf(tabId, viewport), target)
  for (let i = 0; i < path.length; i++) {
    await sendSoon(tabId, "Input.dispatchMouseEvent", {
      type: "mouseMoved",
      x: Math.round(path[i].x),
      y: Math.round(path[i].y),
      button: "none",
      buttons: 0,
      pointerType: "mouse",
    })
    await sleep(moveDelay(i, path.length))
  }
  CURSOR.set(tabId, path[path.length - 1])
  return path
}

/** Bring an element into view the way a person does: wheel notches, then look again. */
async function ensureVisible(run, selector, found) {
  if (found.inViewport) return found
  const centre = found.rect.y + found.rect.height / 2
  for (const step of wheelSteps(centre - found.viewport.height / 2)) {
    await sendSoon(run.tabID, "Input.dispatchMouseEvent", {
      type: "mouseWheel",
      x: Math.round(found.viewport.width / 2),
      y: Math.round(found.viewport.height / 2),
      deltaX: 0,
      deltaY: step,
      pointerType: "mouse",
    })
    await sleep(jitter(60, 30))
  }
  await sleep(jitter(180, 90))
  return locateIn(run, selector, found.mark)
}

/** A real click: glide, hover, press, dwell, release. */
async function cdpClick(run, selector, { button = "left" } = {}) {
  let found = await locateIn(run, selector)
  if (!found.visible) throw new Error(`${selector} is on the page but not visible, so it cannot be clicked`)
  found = await ensureVisible(run, selector, found)
  if (!found.visible) throw new Error(`${selector} left the page while scrolling to it`)
  await stillThere(run, found)
  const target = pointWithin(found.rect)
  const path = await glideTo(run.tabID, target, found.viewport)
  await sleep(jitter(140, 70)) // the hover pause: menus and tooltips need it
  const point = { x: Math.round(target.x), y: Math.round(target.y) }
  const btn = button === "right" ? "right" : "left"
  await send(run.tabID, "Input.dispatchMouseEvent", { type: "mousePressed", ...point, button: btn, buttons: btn === "right" ? 2 : 1, clickCount: 1, pointerType: "mouse" })
  await sleep(jitter(78, 34))
  await send(run.tabID, "Input.dispatchMouseEvent", { type: "mouseReleased", ...point, button: btn, buttons: 0, clickCount: 1, pointerType: "mouse" })
  return { found, point, path }
}

/* ------------------------------------------------------------------ click */

async function click(run, args) {
  const selector = String(args.selector || "")
  if (!selector) throw new Error("this step has no selector")
  if (await useCdp(run)) {
    const { found, point, path } = await cdpClick(run, selector, { button: args.button })
    // A click on a link or a submit button starts a navigation; the next step
    // has to run on the page it lands on, not the one it is leaving.
    await settle(run)
    return { summary: `clicked ${selector}`, text: found.text, strategy: found.strategy, target: point, path_points: path.length }
  }
  const found = await locateIn(run, selector)
  const result = await inPage(run, clickMarked, [found.mark])
  if (!result?.ok) throw new Error(`nothing on the page matches ${selector}`)
  await settle(run)
  return { summary: `clicked ${selector}`, text: result.text, strategy: found.strategy }
}

/** Injected into the page, so self-contained. */
function clickMarked(mark) {
  const el = document.querySelector(`[data-moh-t="${mark}"]`)
  if (!el) return { ok: false }
  el.scrollIntoView({ block: "center" })
  el.click()
  return { ok: true, text: (el.textContent || "").trim().slice(0, 120) }
}

/* ------------------------------------------------------------------- type */

async function type(run, args) {
  const selector = String(args.selector || "")
  const text = String(args.text ?? "")
  if (!selector) throw new Error("this step has no selector")
  if (await useCdp(run)) {
    // Clicked first, because focusing by script skips everything a page does
    // on focus. Then the field is emptied — a type step REPLACES the value, as
    // it always has here — unless the step says clear: false.
    const { found } = await cdpClick(run, selector)
    await sleep(jitter(120, 60))
    if (args.clear !== false) {
      await inPage(run, selectAllMarked, [found.mark])
      await pressKey(run.tabID, "Backspace", 8)
      await sleep(jitter(90, 40))
    }
    let previous = ""
    let typed = 0
    for (const character of [...text]) {
      // Latin through key events so keydown handlers see it; anything an IME
      // would compose (Arabic, CJK, emoji) through insertText, because a
      // keystroke stream is the wrong model for it.
      if (isLatinTypable(character)) await pressCharacter(run.tabID, character)
      else await send(run.tabID, "Input.insertText", { text: character })
      typed++
      await sleep(keyDelay(character, previous))
      previous = character
    }
    // The value itself is NOT echoed back: a "type" step is how a password
    // reaches a page, and the result travels to the server and into a run record.
    return { summary: `typed into ${selector}`, characters: typed }
  }
  const found = await locateIn(run, selector)
  const result = await inPage(run, typeMarked, [found.mark, text])
  if (!result?.ok) throw new Error(`nothing on the page matches ${selector}`)
  return { summary: `typed into ${selector}`, characters: [...text].length }
}

/** Injected into the page, so self-contained. */
function typeMarked(mark, value) {
  const el = document.querySelector(`[data-moh-t="${mark}"]`)
  if (!el) return { ok: false }
  el.focus()
  // Set through the native setter and fire input/change, or a React-style
  // field ignores the value entirely.
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set
  if (setter) setter.call(el, value)
  else el.value = value
  el.dispatchEvent(new Event("input", { bubbles: true }))
  el.dispatchEvent(new Event("change", { bubbles: true }))
  return { ok: true }
}

/** Injected into the page, so self-contained: select everything in the field, for Backspace to clear. */
function selectAllMarked(mark) {
  const el = document.querySelector(`[data-moh-t="${mark}"]`)
  if (!el) return false
  el.focus()
  if (typeof el.select === "function") el.select()
  else if (el.isContentEditable) {
    const range = document.createRange()
    range.selectNodeContents(el)
    const sel = getSelection()
    sel.removeAllRanges()
    sel.addRange(range)
  }
  return true
}

/* ---------------------------------------------------------------- extract */

async function extract(run, args) {
  // The structured shape (fadymondy.com's): fields, optionally per row.
  if (args.fields && typeof args.fields === "object") {
    const data = await inPage(run, extractInPage, [{ list: args.list ?? null, fields: args.fields, limit: args.limit ?? 200 }])
    if (data == null) throw new Error("the page did not answer")
    const page = await chrome.tabs.get(run.tabID)
    const summary = Array.isArray(data.rows)
      ? `${data.rows.length} row${data.rows.length === 1 ? "" : "s"}${data.total > data.rows.length ? ` of ${data.total}` : ""}`
      : `${Object.keys(data.fields || {}).length} field(s)`
    return { url: page.url, title: page.title, ...data, summary }
  }
  // The legacy shape every recorded workflow uses.
  const selector = String(args.selector || "body")
  const result = await inPage(run, extractLegacyInPage, [selector, !!args.all, args.attribute ? String(args.attribute) : ""])
  if (result == null) throw new Error(`nothing on the page matches ${selector}`)
  if (result.values) {
    return { values: result.values, count: result.values.length, summary: `${result.values.length} matches for ${selector}` }
  }
  const value = String(result.value ?? "")
  return { value, summary: value.slice(0, 120) }
}

/* ------------------------------------------------------------------ waits */

async function wait(run, args) {
  const ms = Math.min(Number(args.ms) || 1000, 30000)
  const selector = args.selector ? String(args.selector) : ""
  if (!selector) {
    // A plain pause touches nothing — but a page can move by itself while it
    // waits, so a run that has a page has it checked either side of the pause.
    if (run.tabID != null) await checkPage(run, "before")
    await pause(run, ms)
    if (run.tabID != null) await checkPage(run, "during")
    return { summary: `waited ${ms}ms` }
  }
  return waitFor(run, { selector, timeout_ms: ms })
}

/** Until a selector is on the page (visible, for text and role), or give up with a reason. */
async function waitFor(run, args) {
  const selector = String(args.selector || "")
  if (!selector) throw new Error("this step has no selector")
  const ms = Math.min(Number(args.timeout_ms ?? args.ms) || 15000, 60000)
  await checkPage(run, "before")
  const deadline = Date.now() + ms
  for (;;) {
    // Every look is a fresh, checked injection: the page this polls may be the
    // one a previous step's navigation is still arriving at.
    const found = await inPage(run, findInPage, [selector, ""])
    if (found?.found) return { summary: `${selector} appeared`, strategy: found.strategy, text: found.text }
    if (Date.now() > deadline) throw new Error(`${selector} did not appear`)
    await pause(run, 250)
  }
}

/* ---------------------------------------------- upload, screenshot, eval */

/**
 * Put a file into a file input. Needs the debugger (a page will not accept a
 * file an extension fabricates), and a folder this browser's owner has named
 * in the extension's options: without one, nothing on this computer can be
 * sent anywhere. The workspace chooses the file; the person at the browser
 * chooses what the workspace may choose from.
 */
async function upload(run, args) {
  const files = (Array.isArray(args.files) ? args.files : [args.file]).filter(Boolean).map(String)
  if (files.length === 0) throw new Error("upload needs at least one file path")
  const allowed = String((await get(KEYS.uploadFolder)) || "").trim()
  if (!allowed) {
    throw new Error("uploads are off in this browser — its owner can name a folder for them on the extension's Sites page")
  }
  const norm = (p) => p.replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase()
  const root = norm(allowed) + "/"
  for (const f of files) {
    if (f.includes("..") || !norm(f).startsWith(root)) {
      throw new Error(`${f.split(/[\\/]/).pop()} is not in the folder this browser allows uploads from`)
    }
  }
  if (!(await useCdp(run))) throw new Error(`uploading a file needs the debugger: ${run.inputWhy || "it is not available"}`)
  const selector = String(args.selector || "input[type=file]")
  const found = await locateIn(run, selector.startsWith("css=") ? selector : `css=${selector}`)
  await stillThere(run, found)
  const { root: doc } = await send(run.tabID, "DOM.getDocument", { depth: 0 })
  const { nodeId } = await send(run.tabID, "DOM.querySelector", { nodeId: doc.nodeId, selector: `[data-moh-t="${found.mark}"]` })
  if (!nodeId) throw new Error(`no file input at ${selector}`)
  await send(run.tabID, "DOM.setFileInputFiles", { nodeId, files })
  return { summary: `put ${files.length} file${files.length === 1 ? "" : "s"} into ${selector}`, files: files.length }
}

/** A PNG of the run's tab, for the run record. Through the debugger: this tab is usually not the active one. */
async function screenshot(run, args) {
  if (!(await useCdp(run))) throw new Error(`a screenshot needs the debugger: ${run.inputWhy || "it is not available"}`)
  const capture = () => send(run.tabID, "Page.captureScreenshot", { format: "png", captureBeyondViewport: args.full_page === true })
  let shot = await withDeadline(capture(), 8000)
  if (!shot) {
    // Chrome can stop painting a background tab: make it the active tab of the
    // (unfocused) Moharrik window and try once more. The operator's own window
    // is never touched.
    await chrome.tabs.update(run.tabID, { active: true }).catch(() => {})
    shot = await withDeadline(capture(), 15000)
    if (!shot) throw new Error("the page did not paint a screenshot in time")
  }
  return { summary: `${Math.round((shot.data.length * 3) / 4 / 1024)} kB screenshot`, format: "png", base64: shot.data, bytes: shot.data.length }
}

/**
 * Evaluate an expression in the page and return its value. "readonly" is a
 * statement of intent, not a sandbox — what is enforced is the allowed sites
 * (checked either side, like every acting step) and the size of the answer.
 */
async function evalReadonly(run, args) {
  const expression = String(args.expression || "")
  if (!expression) throw new Error("eval_readonly needs an expression")
  if (!(await useCdp(run))) throw new Error(`eval_readonly needs the debugger: ${run.inputWhy || "it is not available"}`)
  const result = await send(run.tabID, "Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
    timeout: Math.min(Number(args.timeout_ms) || 5000, 30000),
  })
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || "the expression threw")
  const value = result.result?.value
  const encoded = JSON.stringify(value ?? null)
  if (encoded.length > 256_000) throw new Error("the result is too large to return")
  return { value, summary: encoded.slice(0, 120) }
}

/** Let go of the run's tab: detaches the debugger and drops its banner. */
async function release(run) {
  if (run.tabID != null) await detach(run.tabID)
  run.input = ""
  return { summary: "released the debugger" }
}

/** The promise's value, or null if it has not settled in time. */
function withDeadline(promise, ms) {
  let timer
  return Promise.race([
    promise.catch(() => null),
    new Promise((resolve) => {
      timer = setTimeout(() => resolve(null), ms)
    }),
  ]).finally(() => clearTimeout(timer))
}

/* --------------------------------------------------------------- keyboard */

const NAMED_KEYS = {
  Enter: { code: "Enter", keyCode: 13, text: "\r" },
  Tab: { code: "Tab", keyCode: 9, text: "\t" },
  Escape: { code: "Escape", keyCode: 27 },
  Backspace: { code: "Backspace", keyCode: 8 },
  Delete: { code: "Delete", keyCode: 46 },
  " ": { code: "Space", keyCode: 32, text: " " },
  ArrowUp: { code: "ArrowUp", keyCode: 38 },
  ArrowDown: { code: "ArrowDown", keyCode: 40 },
  ArrowLeft: { code: "ArrowLeft", keyCode: 37 },
  ArrowRight: { code: "ArrowRight", keyCode: 39 },
  Home: { code: "Home", keyCode: 36 },
  End: { code: "End", keyCode: 35 },
  PageDown: { code: "PageDown", keyCode: 34 },
  PageUp: { code: "PageUp", keyCode: 33 },
}

function modifierMask({ alt, control, meta, shift } = {}) {
  return (alt ? 1 : 0) | (control ? 2 : 0) | (meta ? 4 : 0) | (shift ? 8 : 0)
}

// US-layout virtual-key codes; a shifted symbol shares its base key's code.
// (fadymondy.com FM-143: sending "." as 46 — VK_DELETE — deleted a character
// for every period typed.)
const PUNCTUATION_VK = {
  " ": 32, ";": 186, ":": 186, "=": 187, "+": 187, ",": 188, "<": 188, "-": 189, "_": 189,
  ".": 190, ">": 190, "/": 191, "?": 191, "`": 192, "~": 192, "[": 219, "{": 219,
  "\\": 220, "|": 220, "]": 221, "}": 221, "'": 222, '"': 222,
  "!": 49, "@": 50, "#": 51, "$": 52, "%": 53, "^": 54, "&": 55, "*": 56, "(": 57, ")": 48,
}

function isLatinTypable(character) {
  return character.length === 1 && character >= " " && character <= "~"
}

async function pressCharacter(tabId, character) {
  const upper = character.toUpperCase()
  const isLetter = /[a-zA-Z]/.test(character)
  const isDigit = /[0-9]/.test(character)
  const code = isLetter ? `Key${upper}` : isDigit ? `Digit${character}` : undefined
  const keyCode = isLetter || isDigit ? upper.charCodeAt(0) : (PUNCTUATION_VK[character] ?? 0)
  await send(tabId, "Input.dispatchKeyEvent", {
    type: "keyDown",
    key: character,
    code,
    text: character,
    unmodifiedText: character,
    windowsVirtualKeyCode: keyCode,
    nativeVirtualKeyCode: keyCode,
  })
  await sleep(jitter(22, 12))
  await send(tabId, "Input.dispatchKeyEvent", { type: "keyUp", key: character, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode })
}

async function pressKey(tabId, key, keyCode, modifiers = {}) {
  const named = NAMED_KEYS[key]
  const mask = modifierMask(modifiers)
  const base = {
    key,
    code: named?.code,
    windowsVirtualKeyCode: named?.keyCode ?? keyCode,
    nativeVirtualKeyCode: named?.keyCode ?? keyCode,
    modifiers: mask,
  }
  await send(tabId, "Input.dispatchKeyEvent", {
    type: named?.text && !mask ? "keyDown" : "rawKeyDown",
    ...base,
    text: mask ? undefined : named?.text,
  })
  await sleep(jitter(24, 10))
  await send(tabId, "Input.dispatchKeyEvent", { type: "keyUp", ...base })
}

async function pressCombo(tabId, key, modifiers = {}) {
  if (NAMED_KEYS[key]) return pressKey(tabId, key, NAMED_KEYS[key].keyCode, modifiers)
  if (key.length === 1 && !modifiers.control && !modifiers.meta && !modifiers.alt && isLatinTypable(key)) {
    return pressCharacter(tabId, key)
  }
  return pressKey(tabId, key, key.toUpperCase().charCodeAt(0), modifiers)
}

/* ------------------------------------------------ press, select, scroll (MH-398)

The recorder's shapes, which these accept exactly (record-page.js):

  press   {key: "Enter" | "Escape"}              — no selector: the key goes to
                                                   whatever has focus, which is
                                                   the field the preceding type
                                                   step just filled
  select  {selector, value: "<visible label>"}   — `value` holds the LABEL
  scroll  {dy: <px>}                             — the window, vertically

and, for steps written by hand or over MCP, a little more: press takes an
optional selector and modifiers; select takes a real option value, or `label`
/ `by` to say which one is meant; scroll takes dx, an element to scroll, or a
selector alone to bring into view.
*/

/**
 * Let an action's consequence land. If the step started a navigation (a
 * submitted form, a followed link, a dropdown that redirects), wait for the new
 * page; otherwise return after a beat. The listener goes on BEFORE the status
 * is read, so a load that finishes in between is not missed. Leaving the
 * allowed sites ends the wait at once.
 */
async function settle(run, beat = 200) {
  await pause(run, beat)
  if (run.breach) return
  const tabID = run.tabID
  await new Promise((resolve) => {
    const timer = setTimeout(done, DEFAULT_TIMEOUT)
    function done() {
      clearTimeout(timer)
      chrome.tabs.onUpdated.removeListener(listener)
      run.wake.delete(done)
      resolve()
    }
    function listener(id, info) {
      if (id === tabID && info.status === "complete") done()
    }
    chrome.tabs.onUpdated.addListener(listener)
    run.wake.add(done)
    chrome.tabs.get(tabID).then(
      (tab) => {
        if (tab?.status !== "loading") done()
      },
      done, // the tab is gone; the next step will say so
    )
  })
}

const MODIFIER_NAMES = {
  alt: "alt",
  option: "alt",
  control: "control",
  ctrl: "control",
  meta: "meta",
  cmd: "meta",
  command: "meta",
  super: "meta",
  shift: "shift",
}

/**
 * The key and its modifiers, from any of the shapes people write:
 * {key: "Enter"}, {key: "Shift+Tab"}, {key: "a", modifiers: ["Control"]},
 * {key: "a", modifiers: {control: true}}.
 */
export function parseKey(args) {
  let key = String(args.key ?? "")
  const mods = { alt: false, control: false, meta: false, shift: false }
  const add = (name) => {
    const m = MODIFIER_NAMES[String(name).trim().toLowerCase()]
    if (!m) throw new Error(`"${name}" is not a modifier key`)
    mods[m] = true
  }
  // "Shift+Tab" — but "+" on its own is the plus key.
  if (key.length > 1 && key.includes("+")) {
    const parts = key.split("+")
    key = parts.pop() || "+"
    parts.filter(Boolean).forEach(add)
  }
  const given = args.modifiers
  if (Array.isArray(given)) given.forEach(add)
  else if (given && typeof given === "object") {
    for (const [name, on] of Object.entries(given)) if (on) add(name)
  } else if (typeof given === "string" && given) given.split(/[+,\s]+/).filter(Boolean).forEach(add)
  if (key === "Space" || key === "Spacebar") key = " "
  if (key === "Esc") key = "Escape"
  if (key === "Return") key = "Enter"
  return { key, modifiers: mods }
}

async function press(run, args) {
  const { key, modifiers } = parseKey(args)
  if (!key) throw new Error("this step has no key")
  const selector = args.selector ? String(args.selector) : ""
  const combo = [
    ...Object.entries(modifiers)
      .filter(([, on]) => on)
      .map(([m]) => m[0].toUpperCase() + m.slice(1)),
    key === " " ? "Space" : key,
  ].join("+")

  if (await useCdp(run)) {
    // A real key: the browser itself does what the key does — Enter submits
    // the form, Tab moves focus — because the event is trusted. With a
    // selector, that element gets focus first (focused, not clicked: a click
    // on a button and then Enter would press it twice).
    if (selector) {
      const found = await locateIn(run, selector)
      await inPage(run, focusMarked, [found.mark])
      await stillThere(run, found)
    }
    await pressCombo(run.tabID, key, modifiers)
    await settle(run)
    return { summary: `pressed ${combo}`, key: combo }
  }

  let target = ""
  if (selector) {
    const found = await locateIn(run, selector)
    target = `[data-moh-t="${found.mark}"]`
  }
  const result = await inPage(run, pressInPage, [target, key, modifiers])
  if (!result?.ok) throw new Error(result?.reason?.replace(target, selector) || `could not press ${key}`)
  // Enter in a form submits it, and the next step belongs on the result.
  await settle(run)
  const what = {
    submitted: "submitted the form",
    clicked: "activated it",
    focused: "moved focus",
    newline: "new line",
  }[result.action]
  return {
    summary: `pressed ${combo}${result.handled ? " (the page handled it)" : what ? ` — ${what}` : ""}`,
    key: combo,
    target: result.target,
    ...(result.action ? { action: result.action } : {}),
  }
}

/** Injected into the page, so self-contained. */
function focusMarked(mark) {
  const el = document.querySelector(`[data-moh-t="${mark}"]`)
  if (!el) return false
  if (typeof el.focus === "function") el.focus()
  return true
}

/**
 * Injected into the page, so self-contained. Dispatches keydown/keypress/keyup
 * to the target and, because those events are untrusted and so have no default
 * action, then does what the browser would have done for a real key — unless
 * the page prevented it, in which case the page's own handler is the whole of
 * the key's effect.
 */
export function pressInPage(selector, key, mods) {
  let el
  if (selector) {
    el = document.querySelector(selector)
    if (!el) return { ok: false, reason: `nothing on the page matches ${selector}` }
    if (typeof el.focus === "function") el.focus()
  } else {
    el = document.activeElement
    if (!el || el === document.documentElement) el = document.body
  }
  if (!el) return { ok: false, reason: "the page has nothing to press a key in" }

  const NAMED = {
    Enter: ["Enter", 13],
    Tab: ["Tab", 9],
    Escape: ["Escape", 27],
    Backspace: ["Backspace", 8],
    Delete: ["Delete", 46],
    " ": ["Space", 32],
    ArrowUp: ["ArrowUp", 38],
    ArrowDown: ["ArrowDown", 40],
    ArrowLeft: ["ArrowLeft", 37],
    ArrowRight: ["ArrowRight", 39],
    Home: ["Home", 36],
    End: ["End", 35],
    PageUp: ["PageUp", 33],
    PageDown: ["PageDown", 34],
  }
  let code = ""
  let keyCode = 0
  if (NAMED[key]) [code, keyCode] = NAMED[key]
  else if (key.length === 1 && /[a-z]/i.test(key)) [code, keyCode] = ["Key" + key.toUpperCase(), key.toUpperCase().charCodeAt(0)]
  else if (key.length === 1 && /[0-9]/.test(key)) [code, keyCode] = ["Digit" + key, key.charCodeAt(0)]
  else if (/^F([1-9]|1[0-2])$/.test(key)) [code, keyCode] = [key, 111 + Number(key.slice(1))]

  const producesChar = key === "Enter" || key.length === 1
  const charCode = key === "Enter" ? 13 : key.length === 1 ? key.charCodeAt(0) : 0
  const flags = {
    altKey: !!mods.alt,
    ctrlKey: !!mods.control,
    metaKey: !!mods.meta,
    shiftKey: !!mods.shift,
  }

  const fire = (target, type) => {
    const legacy = type === "keypress" ? charCode : keyCode
    const charValue = type === "keypress" ? charCode : 0
    const event = new KeyboardEvent(type, {
      key,
      code,
      bubbles: true,
      cancelable: true,
      composed: true,
      view: window,
      ...flags,
      keyCode: legacy,
      which: legacy,
      charCode: charValue,
    })
    // Plenty of pages still test e.keyCode === 13. Not every engine takes the
    // legacy fields from the init dictionary, so they are pinned on the event.
    for (const [prop, value] of [
      ["keyCode", legacy],
      ["which", legacy],
      ["charCode", charValue],
    ]) {
      if (event[prop] !== value) {
        try {
          Object.defineProperty(event, prop, { get: () => value })
        } catch {
          /* read-only here; key and code still say what was pressed */
        }
      }
    }
    return target.dispatchEvent(event) // false when a handler called preventDefault
  }

  const describe = (node) =>
    node.id
      ? `#${node.id}`
      : node.getAttribute?.("name")
        ? `${node.tagName.toLowerCase()}[name="${node.getAttribute("name")}"]`
        : node.tagName.toLowerCase()
  const target = describe(el)

  let allowed = fire(el, "keydown")
  // A cancelled keydown produces no keypress, as in a browser; nor does a shortcut.
  if (allowed && producesChar && !mods.control && !mods.meta && !mods.alt) allowed = fire(el, "keypress")
  // A handler may have replaced the element; keyup goes to what has focus now.
  fire(el.isConnected ? el : document.activeElement || document.body, "keyup")

  if (!allowed) return { ok: true, handled: true, target }
  // With Ctrl, Alt or Cmd held a key is a shortcut, and a shortcut's default
  // belongs to the browser, not the page. Shift+Tab and Shift+Enter still act.
  if (mods.control || mods.meta || mods.alt) return { ok: true, handled: false, target }

  const tag = el.tagName
  const inputType = tag === "INPUT" ? (el.type || "text").toLowerCase() : ""
  const activatable =
    (tag === "A" && el.hasAttribute("href")) ||
    tag === "BUTTON" ||
    tag === "SUMMARY" ||
    ["submit", "button", "reset", "image"].includes(inputType)

  if (key === "Enter") {
    if (activatable) {
      el.click()
      return { ok: true, handled: false, action: "clicked", target }
    }
    if (tag === "TEXTAREA") {
      const start = el.selectionStart ?? el.value.length
      const end = el.selectionEnd ?? start
      el.setRangeText("\n", start, end, "end")
      el.dispatchEvent(new Event("input", { bubbles: true }))
      return { ok: true, handled: false, action: "newline", target }
    }
    if (tag === "INPUT" && el.form) {
      // Implicit submission, as the HTML spec defines it: the form's default
      // button is clicked, so its handlers run as they would for a person; with
      // no default button the form submits only if this is its one text-like
      // field. requestSubmit, not submit(): it fires the submit event and runs
      // validation, which is what a page's own handler listens for.
      const form = el.form
      const controls = [...form.elements]
      const defaultButton = controls.find(
        (c) =>
          (c.tagName === "BUTTON" && (c.getAttribute("type") || "submit").toLowerCase() === "submit") ||
          (c.tagName === "INPUT" && ["submit", "image"].includes((c.type || "").toLowerCase())),
      )
      if (defaultButton) {
        if (defaultButton.disabled) return { ok: true, handled: false, target }
        defaultButton.click()
        return { ok: true, handled: false, action: "submitted", target }
      }
      const BLOCKING = [
        "text", "search", "url", "tel", "email", "password", "date",
        "month", "week", "time", "datetime-local", "number",
      ]
      const blocking = controls.filter(
        (c) => c.tagName === "INPUT" && BLOCKING.includes((c.type || "text").toLowerCase()),
      )
      if (blocking.length > 1) return { ok: true, handled: false, target }
      if (typeof form.requestSubmit === "function") form.requestSubmit()
      else form.submit()
      return { ok: true, handled: false, action: "submitted", target }
    }
    return { ok: true, handled: false, target }
  }

  if (key === " " && tag !== "A" && (activatable || inputType === "checkbox" || inputType === "radio")) {
    el.click()
    return { ok: true, handled: false, action: "clicked", target }
  }

  if (key === "Tab") {
    const visible = (n) => {
      if (n.closest("[hidden],[inert]")) return false
      const style = getComputedStyle(n)
      return style.display !== "none" && style.visibility !== "hidden"
    }
    const focusable = [
      ...document.querySelectorAll(
        'a[href],button,input,select,textarea,summary,iframe,[tabindex],[contenteditable=""],[contenteditable="true"]',
      ),
    ].filter((n) => n.tabIndex >= 0 && !n.disabled && !(n.tagName === "INPUT" && n.type === "hidden") && visible(n))
    // Positive tabindex first, in value order; then the rest in document order.
    const order = [
      ...focusable.filter((n) => n.tabIndex > 0).sort((a, b) => a.tabIndex - b.tabIndex),
      ...focusable.filter((n) => n.tabIndex === 0),
    ]
    if (order.length === 0) return { ok: true, handled: false, target }
    const at = order.indexOf(el)
    const next = mods.shift
      ? order[at <= 0 ? order.length - 1 : at - 1]
      : order[at < 0 || at === order.length - 1 ? 0 : at + 1]
    next.focus()
    return { ok: true, handled: false, action: "focused", target: describe(next) }
  }

  return { ok: true, handled: false, target }
}

async function select(run, args) {
  const selector = String(args.selector || "")
  if (!selector) throw new Error("this step has no selector")
  // The recorder puts the option's visible label in `value`; a hand-written
  // step may put a real option value there, or say which with `label` / `by`.
  let wanted = args.value
  let by = String(args.by || "").toLowerCase()
  if (args.label != null && args.label !== "") {
    wanted = args.label
    by = "label"
  }
  if (wanted == null) throw new Error("this step does not say which option to choose")
  if (by && by !== "label" && by !== "value") {
    throw new Error(`"${args.by}" is not a way to choose an option (label or value)`)
  }
  const result = await inPage(run, selectInPage, [selector, String(wanted), by])
  if (!result?.ok) throw new Error(result?.reason || `could not choose an option in ${selector}`)
  // A dropdown that navigates on change (a language or sort picker) is common.
  await settle(run)
  return { summary: `chose "${result.label}" in ${selector}`, value: result.value, label: result.label }
}

/** Injected into the page, so self-contained. */
export function selectInPage(selector, wanted, by) {
  const el = document.querySelector(selector)
  if (!el) return { ok: false, reason: `nothing on the page matches ${selector}` }
  if (el.tagName !== "SELECT") return { ok: false, reason: `${selector} is not a dropdown (<select>)` }
  if (el.disabled) return { ok: false, reason: `${selector} is disabled` }

  const clean = (s) => String(s ?? "").replace(/\s+/g, " ").trim()
  const labelOf = (o) => clean(o.label || o.text)
  const options = [...el.options]
  const want = clean(wanted)

  // Label first: it is what the recorder stores, and what a person sees.
  let option
  if (by !== "value") option = options.find((o) => labelOf(o) === want)
  if (!option && by !== "label") option = options.find((o) => o.value === wanted) || options.find((o) => o.value === want)
  if (!option && by !== "value") option = options.find((o) => labelOf(o).toLowerCase() === want.toLowerCase())
  if (!option) return { ok: false, reason: `${selector} has no option "${wanted}"` }
  if (option.disabled || option.parentElement?.disabled) {
    return { ok: false, reason: `the option "${labelOf(option)}" is disabled` }
  }

  if (typeof el.focus === "function") el.focus()
  // Through the native setter, as for typing: a framework that wraps the
  // element's own value property would otherwise swallow the change and
  // re-render the old choice.
  const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set
  if (setter) setter.call(el, option.value)
  else el.value = option.value
  // Two options can share a value and the setter picks the first; pin the one meant.
  if (el.selectedIndex !== option.index) el.selectedIndex = option.index
  el.dispatchEvent(new Event("input", { bubbles: true }))
  el.dispatchEvent(new Event("change", { bubbles: true }))
  return { ok: true, value: option.value, label: labelOf(option) }
}

async function scroll(run, args) {
  const num = (...names) => {
    for (const n of names) {
      if (args[n] == null || args[n] === "") continue
      const v = Number(args[n])
      if (!Number.isFinite(v)) throw new Error(`${n} has to be a number of pixels`)
      return Math.max(-100000, Math.min(100000, Math.round(v)))
    }
    return 0
  }
  const dx = num("dx", "deltaX")
  const dy = num("dy", "deltaY")
  const selector = args.selector ? String(args.selector) : ""
  if (!selector && dx === 0 && dy === 0) {
    throw new Error("this step has no distance to scroll and no element to scroll to")
  }

  if (await useCdp(run)) {
    // Wheel notches, never a jump: a page that loads more on scroll sees the
    // scroll as it really arrives (driver/human.js).
    let at
    if (selector) {
      let found = await locateIn(run, selector)
      if (dx === 0 && dy === 0) {
        found = await ensureVisible(run, selector, found)
        await settle(run, 400)
        return { summary: `scrolled ${selector} into view`, moved: true }
      }
      at = { x: found.rect.x + found.rect.width / 2, y: found.rect.y + found.rect.height / 2 }
    } else {
      const viewport = await inPage(run, () => ({ width: innerWidth, height: innerHeight }), [])
      at = cursorOf(run.tabID, viewport)
    }
    const before = await inPage(run, () => [scrollX, scrollY], [])
    const steps = wheelSteps(dy || 0)
    const across = dx ? wheelSteps(dx) : []
    for (let i = 0; i < Math.max(steps.length, across.length); i++) {
      await sendSoon(run.tabID, "Input.dispatchMouseEvent", {
        type: "mouseWheel",
        x: Math.round(at.x),
        y: Math.round(at.y),
        deltaX: across[i] || 0,
        deltaY: steps[i] || 0,
        pointerType: "mouse",
      })
      await sleep(jitter(64, 28))
    }
    await settle(run, 400)
    const after = await inPage(run, () => [scrollX, scrollY], [])
    const moved = after[0] !== before[0] || after[1] !== before[1]
    const distance = [dy && `${dy}px down`, dx && `${dx}px across`].filter(Boolean).join(", ")
    return { summary: `scrolled ${selector ? selector + " " : ""}by ${distance}${moved ? "" : " (already at the end)"}`, x: Math.round(after[0]), y: Math.round(after[1]), moved, steps: steps.length }
  }

  const result = await inPage(run, scrollInPage, [selector, dx, dy])
  if (!result?.ok) throw new Error(result?.reason || "could not scroll")
  // A page that loads more on scroll needs a moment to do it — the recorder
  // keeps scroll steps precisely so that those pages replay.
  await settle(run, 400)
  const distance = [dy && `${dy}px down`, dx && `${dx}px across`].filter(Boolean).join(", ")
  const summary =
    result.mode === "into-view"
      ? `scrolled ${selector} into view`
      : `scrolled ${selector ? selector + " " : ""}by ${distance}${result.moved ? "" : " (already at the end)"}`
  return { summary, x: result.x, y: result.y, moved: result.moved }
}

/** Injected into the page, so self-contained. */
export function scrollInPage(selector, dx, dy) {
  let el = null
  if (selector) {
    el = document.querySelector(selector)
    if (!el) return { ok: false, reason: `nothing on the page matches ${selector}` }
    if (dx === 0 && dy === 0) {
      el.scrollIntoView({ block: "center", inline: "nearest" })
      return { ok: true, mode: "into-view", x: Math.round(window.scrollX), y: Math.round(window.scrollY), moved: true }
    }
  }
  // Scrolling <html> or <body> by selector means the page's own scroller.
  const onWindow = !el || el === document.documentElement || el === document.body
  const at = () => (onWindow ? [window.scrollX, window.scrollY] : [el.scrollLeft, el.scrollTop])
  const [x0, y0] = at()
  // "instant", not "smooth": the step reports where the page ended up, and a
  // smooth scroll has arrived nowhere yet when the call returns.
  if (onWindow) window.scrollBy({ left: dx, top: dy, behavior: "instant" })
  else if (typeof el.scrollBy === "function") el.scrollBy({ left: dx, top: dy, behavior: "instant" })
  else {
    el.scrollLeft += dx
    el.scrollTop += dy
  }
  const [x1, y1] = at()
  // Not moving is not a failure: a page shorter than the recorded distance has
  // reached its end, as it would have for a person.
  return { ok: true, mode: "by", x: Math.round(x1), y: Math.round(y1), moved: x1 !== x0 || y1 !== y0 }
}
