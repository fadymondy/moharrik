/*
The debugger attachment: the only way to produce input a page believes.

`element.dispatchEvent(new MouseEvent(...))` produces events with
`isTrusted === false`. Plenty of applications check that, and plenty more break
in subtler ways — a React input whose value is set programmatically does not
fire the change the app listens for, a drag never starts, a menu that opens on
a real pointerdown never opens. Driving through Chrome's debugger produces the
same event stream a hand on a mouse produces, because it enters at the same
layer.

The cost is honest and visible: Chrome shows "… is debugging this browser" on
every tab this touches. There is no supported way to suppress it, and the popup
says so rather than pretending otherwise.

Attachment is per tab and released as soon as the work is done, so a browser
sitting idle carries no banner and no attached debugger.
*/

const ATTACHED = new Map() // tabId -> { since, idleTimer, keep }

/**
 * Whether this browser lets the extension drive through the debugger at all.
 * The manifest asks for it; a test harness, or a browser policy that blocks
 * the permission, does not have it — the driver then says which input it used
 * rather than pretending (driver.js).
 */
export function cdpAvailable() {
  return typeof globalThis.chrome?.debugger?.attach === "function"
}
const PROTOCOL = "1.3"

// A tab that has gone quiet for this long is released, so a run that dies
// half-way does not leave a banner up for ever.
const IDLE_RELEASE_MS = 30_000

/*
Background tabs (FM-267). With several runs at once, most run tabs are not the
active tab of their window, and the Automation window itself is usually behind
the operator's. Chrome then treats the page as hidden: no frames are drawn, and
mouse moves and wheel events — which Chrome delivers in step with frames — wait
for a frame that never comes. That was the 60–90 s click. So every attached tab
is told it is focused (Emulation.setFocusEmulationEnabled, which also keeps it
rendering while hidden) and active (Page.setWebLifecycleState, which undoes
Chrome's freezing of background pages).

`keep`: a run holds its tab for the whole run, including long waits, so the
attachment — and with it the rendering — is not dropped after IDLE_RELEASE_MS.
The run's end releases it (driver/run-tabs-core.js).
*/
export async function attach(tabId, { keep = false } = {}) {
  if (ATTACHED.has(tabId)) {
    if (keep) ATTACHED.get(tabId).keep = true
    touch(tabId)
    return
  }
  await new Promise((resolve, reject) => {
    chrome.debugger.attach({ tabId }, PROTOCOL, () => {
      const err = chrome.runtime.lastError
      // Another client (usually DevTools, open on that tab) already owns it.
      // Saying which is the difference between a fixable message and a shrug.
      if (err) reject(new Error(`cannot drive this tab: ${err.message}`))
      else resolve()
    })
  })
  ATTACHED.set(tabId, { since: Date.now(), idleTimer: null, keep })
  await keepRendering(tabId)
  touch(tabId)
}

/** Treat the page as focused and active, even in a background tab. Re-applied after a navigation. */
export async function keepRendering(tabId) {
  if (!ATTACHED.has(tabId)) return
  await raw(tabId, "Emulation.setFocusEmulationEnabled", { enabled: true }).catch(() => {})
  await raw(tabId, "Page.setWebLifecycleState", { state: "active" }).catch(() => {})
}

function raw(tabId, method, params) {
  return new Promise((resolve, reject) => {
    chrome.debugger.sendCommand({ tabId }, method, params, (result) => {
      const err = chrome.runtime.lastError
      if (err) reject(new Error(`${method}: ${err.message}`))
      else resolve(result)
    })
  })
}

/**
 * A pointer move or wheel notch, without waiting long for Chrome's
 * acknowledgement: those events are delivered in step with frames, and a page
 * that is not drawing would hold the whole click up. The event is still
 * delivered, in order, before the next press or release.
 */
export async function sendSoon(tabId, method, params, waitMs = 400) {
  const sent = send(tabId, method, params)
  sent.catch(() => {})
  let timer
  const late = new Promise((resolve) => {
    timer = setTimeout(() => resolve(undefined), waitMs)
  })
  try {
    return await Promise.race([sent, late])
  } finally {
    clearTimeout(timer)
  }
}

export async function send(tabId, method, params = {}) {
  await attach(tabId)
  touch(tabId)
  return new Promise((resolve, reject) => {
    chrome.debugger.sendCommand({ tabId }, method, params, (result) => {
      const err = chrome.runtime.lastError
      if (err) reject(new Error(`${method}: ${err.message}`))
      else resolve(result)
    })
  })
}

export async function detach(tabId) {
  // Detached whether or not this worker remembers attaching: a worker that was
  // stopped and started again has lost its map, and Chrome would otherwise
  // keep the attachment — and its banner — for good.
  const entry = ATTACHED.get(tabId)
  if (entry) clearTimeout(entry.idleTimer)
  ATTACHED.delete(tabId)
  await new Promise((resolve) => {
    chrome.debugger.detach({ tabId }, () => {
      void chrome.runtime.lastError // Already gone is a fine outcome.
      resolve()
    })
  })
}

export function attachedTabs() {
  return [...ATTACHED.keys()]
}

function touch(tabId) {
  const entry = ATTACHED.get(tabId)
  if (!entry) return
  clearTimeout(entry.idleTimer)
  if (entry.keep) return
  entry.idleTimer = setTimeout(() => void detach(tabId), IDLE_RELEASE_MS)
}

// Chrome detaches us when a tab closes, navigates cross-process, or DevTools
// takes over; forgetting the entry keeps a later attach from being skipped.
globalThis.chrome?.debugger?.onDetach?.addListener((source) => {
  const entry = ATTACHED.get(source.tabId)
  if (entry) clearTimeout(entry.idleTimer)
  ATTACHED.delete(source.tabId)
})
