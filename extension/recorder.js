/*
The recorder's service-worker half (MH-235).

The worker owns the recording, not the page, for two reasons that both bite in
practice:

  - Every navigation throws the page half away, and the steps recorded before
    it have to survive that.
  - An MV3 service worker is KILLED after 30 seconds idle, so a variable in
    this module is not storage. The recording lives in chrome.storage.local.

Stopping sends the steps over the authenticated socket, where the server turns
them into a workflow (MH-233) after re-checking every rule — this half is
convenience, not security.
*/

import { KEYS, get } from "./config.js"
import { hostAllowed, normaliseHost } from "./hosts.js"
import { installSelectors } from "./selectors.js"
import { recordInPage, stopRecordingInPage } from "./record-page.js"

export const RECORDING_KEY = "moh.recording"

// A navigation this soon after a click or a key is that action's CONSEQUENCE.
// Replaying the click already performs it, so recording the navigation too
// would open the page twice.
const CAUSED_BY_ACTION_MS = 4000

async function state() {
  return (await chrome.storage.local.get(RECORDING_KEY))[RECORDING_KEY] || null
}

async function save(next) {
  if (next) await chrome.storage.local.set({ [RECORDING_KEY]: next })
  else await chrome.storage.local.remove(RECORDING_KEY)
}

/** Is this URL one this browser is allowed to be driven on? */
async function allowedHere(url) {
  const agent = (await get(KEYS.agent)) || {}
  const hosts = agent.allowedHosts || []
  let host
  try {
    const parsed = new URL(url)
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return ""
    host = normaliseHost(parsed.hostname)
  } catch {
    return ""
  }
  // The server's own rule (hosts.js, MH-422): an entry matches the host and
  // any subdomain of it, never a mere text suffix, and "www." is not special.
  return hostAllowed(hosts, host) ? "" : `${host} is not on this browser's allow-list`
}

async function inject(tabId, count) {
  await chrome.scripting.executeScript({ target: { tabId }, func: installSelectors })
  await chrome.scripting.executeScript({ target: { tabId }, func: recordInPage, args: [count] })
}

/** Start recording in the active tab. The allow-list applies before anything is watched. */
export async function startRecording() {
  const current = await state()
  if (current?.active) throw new Error("already recording")

  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true })
  if (!tab) throw new Error("there is no tab to record in")

  const blocked = await allowedHere(tab.url)
  if (blocked) throw new Error(blocked)

  await save({
    active: true,
    tabId: tab.id,
    startUrl: tab.url,
    lastUrl: tab.url,
    steps: [],
    startedAt: Date.now(),
    lastActionAt: 0,
    blocked: "",
    error: "",
  })
  await inject(tab.id, 0)
  await chrome.action.setBadgeText({ text: "REC" })
  await chrome.action.setBadgeBackgroundColor({ color: "#e5484d" })
  return { ok: true }
}

/**
 * Stop, and hand what was recorded to `sendToSite`.
 *
 * A failure KEEPS the recording rather than discarding it: a dropped socket
 * must not cost the operator the task they just performed by hand. The popup
 * offers to send it again or throw it away.
 */
export async function stopRecording({ sendToSite }) {
  const current = await state()
  if (!current) throw new Error("not recording")

  if (current.active) {
    try {
      await chrome.scripting.executeScript({ target: { tabId: current.tabId }, func: stopRecordingInPage })
      // The page flushes the field being typed into as it stops; let that land.
      await new Promise((resolve) => setTimeout(resolve, 400))
    } catch {
      /* the tab was closed; what was recorded still stands */
    }
  }
  await chrome.action.setBadgeText({ text: "" })

  const finished = { ...(await state()), active: false }
  if (!finished.steps?.length) {
    await save(null)
    throw new Error("nothing was recorded")
  }
  await save(finished)

  try {
    const saved = await sendToSite({
      name: finished.name || "",
      start_url: finished.startUrl,
      steps: finished.steps,
    })
    await save(null)
    return { ok: true, saved }
  } catch (err) {
    await save({ ...finished, error: String(err?.message || err) })
    throw err
  }
}

export async function discardRecording() {
  const current = await state()
  if (current?.active) {
    try {
      await chrome.scripting.executeScript({ target: { tabId: current.tabId }, func: stopRecordingInPage })
    } catch {
      /* gone */
    }
  }
  await chrome.action.setBadgeText({ text: "" })
  await save(null)
}

/** What the popup renders. */
export async function recordingState() {
  const current = await state()
  if (!current) return { active: false, steps: 0 }
  return {
    active: !!current.active,
    steps: current.steps?.length || 0,
    startUrl: current.startUrl || "",
    blocked: current.blocked || "",
    error: current.error || "",
  }
}

/*
Every change to the recording goes through ONE queue.

Each change is a read-modify-write of the whole record in storage, and two
steps sent back to back — a field's value, then the Enter pressed in it —
otherwise both read the same list and the second write silently drops the
first. That is not hypothetical: in fadymondy's end-to-end run it ate the
password step, behind its own Enter.
*/
let queue = Promise.resolve()
function serial(work) {
  const next = queue.then(work, work)
  queue = next.catch(() => {})
  return next
}

/** A step from the page. Consecutive typing into one field collapses into one step. */
export function addStep(tabId, step) {
  return serial(() => addStepNow(tabId, step))
}

async function addStepNow(tabId, step) {
  const current = await state()
  if (!current?.active || current.tabId !== tabId || !step?.command) return null

  const steps = [...current.steps]
  const last = steps[steps.length - 1]
  if (step.command === "type" && last?.command === "type" && last.args?.selector === step.args?.selector) {
    steps[steps.length - 1] = step
  } else {
    steps.push(step)
  }
  const actionLike = step.command === "click" || step.command === "press" || step.command === "select"
  await save({ ...current, steps, lastActionAt: actionLike ? Date.now() : current.lastActionAt })
  return steps.length
}

/** Follow the recorded tab across navigations: record typed-in URLs, re-attach the page half. */
chrome.tabs.onUpdated.addListener((tabId, info, tab) => {
  if (info.status !== "complete") return
  void serial(async () => {
    const current = await state()
    if (!current?.active || current.tabId !== tabId) return

    let next = { ...current, lastUrl: tab.url, blocked: "" }

    const blocked = await allowedHere(tab.url)
    if (blocked) {
      // Off the allowed sites: nothing is watched here, and the popup says why.
      // Recording is not stopped — the operator may navigate back.
      await save({ ...next, blocked })
      return
    }

    const moved = tab.url !== current.lastUrl
    if (moved && Date.now() - (current.lastActionAt || 0) > CAUSED_BY_ACTION_MS) {
      next = { ...next, steps: [...current.steps, { command: "navigate", args: { url: tab.url } }] }
    }
    await save(next)
    try {
      await inject(tabId, next.steps.length)
    } catch {
      /* a page that refuses scripts (the web store, a PDF); the popup shows the count */
    }
  })
})

chrome.tabs.onRemoved.addListener((tabId) => {
  void (async () => {
    const current = await state()
    if (current?.active && current.tabId === tabId) {
      await chrome.action.setBadgeText({ text: "" })
      await save({
        ...current,
        active: false,
        error: "the tab being recorded was closed — send what was recorded, or discard it",
      })
    }
  })()
})
