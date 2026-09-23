/*
Tabs for runs (FM-267) — the pure part. No chrome.* in here, so node can test it
(extension/test/run-tabs.test.mjs); driver/runs.js binds it to Chrome.

The server runs several runs at once in a browser that advertises "multi_tab",
and every command of a run carries the run id. Two kinds of tab serve them:

THROWAWAY TABS — one per run.
  - The first command of a run takes a tab: an idle automation tab nobody
    holds, or a fresh one, up to a cap on open throwaway tabs.
  - Every later command of the run goes to that tab and no other.
  - If that tab is closed or crashes away, the run fails with a sentence saying
    so. Its commands are never redirected to another tab — that would be one
    run acting on another run's page.
  - At the end the tab is released: back to about:blank and kept idle, or
    closed once `maxIdle` idle tabs are already kept.

PERSISTENT TABS — one per key ("whatsapp", "gmail", …), kept open for good
(Fady: "keep the tabs of it open always to easy get data from it without
reload it").
  - The server names the key on the command (envelope `tab`). The first run on
    a key opens the tab; later runs reuse it.
  - One run at a time per key. The server already queues the second run; if
    one arrives anyway it is refused here rather than sharing the page.
  - A run's first navigate is skipped when the tab is already on that site and
    fresh (see planNavigation). It reloads when the page is stale, the last
    load failed, or Chrome discarded or froze the tab.
  - A persistent tab the operator closed is recreated by the next run.
  - At the end of the run the tab is left exactly where it is.

Maps live in storage because the service worker is killed when idle. Every
read-modify-write goes through one queue: commands of different runs arrive
concurrently, and two of them taking "the idle tab" at once is exactly the
cross-talk this exists to prevent.

deps:
  storage.get(key) -> any                  storage.set(key, value)
  tabs.exists(tabId) -> bool
  tabs.info(tabId) -> { url, discarded, frozen } | null
  tabs.owned() -> tabId[]                  automation tabs that still exist
  tabs.isIdle(tabId) -> bool               on about:blank / new tab
  tabs.open({ persistent }) -> tabId       a new tab in the Automation group
  tabs.blank(tabId)                        navigate to about:blank
  tabs.close(tabId)
  debugger.detach(tabId)                   that tab only
  limits() -> { maxTabs, maxIdle }
  persistentConfig(key) -> { match, maxAgeMs } | null
  now() -> ms                              optional, for tests
*/

export const RUN_TABS_KEY = "moh.runTabs" // { [runId]: { tab, at, key? } }
export const PERSISTENT_TABS_KEY = "moh.persistentTabs" // { [key]: { tab, loadedAt, url, failed } }

export class RunTabError extends Error {
  constructor(message, code = "") {
    super(message)
    this.code = code
  }
}

/*
0.12.1 (production fix): "all tabs in use" is CAPACITY, not a failure. It is
answered with code "tabs_busy" and the server puts the run back in the queue.
Only tabs held by a live run count against the cap — never a kept site tab,
never an idle tab, never a leftover tab no run holds (that one is reused).
*/
export const CODE_TABS_BUSY = "tabs_busy"

// A run's entry is dropped when the server says the run is not in flight
// (sync), at run.end, or when no command of the run has used it for this long:
// longer than any single step may take (90 s), so a live run is never evicted.
export const ORPHAN_TTL_MS = 10 * 60 * 1000
// A run the server has just claimed may reach here before the sync that lists
// it; entries younger than this are never released by a sync.
export const SYNC_GRACE_MS = 60 * 1000

// A run whose tab died keeps its entry, so its later commands fail rather than
// being handed a new tab — until release, sync, or ORPHAN_TTL_MS.
export const DEFAULT_MAX_AGE_MS = 30 * 60 * 1000

/** "web.whatsapp.com" or "www.linkedin.com/notifications" against a URL. */
export function urlMatches(match, url) {
  if (!match) return false
  let parsed
  try {
    parsed = new URL(url)
  } catch {
    return false
  }
  const [host, ...rest] = String(match).toLowerCase().trim().split("/")
  if (parsed.hostname.toLowerCase() !== host) return false
  const prefix = rest.filter(Boolean).join("/")
  if (!prefix) return true
  const path = parsed.pathname.replace(/\/+$/, "") + "/"
  return path.startsWith("/" + prefix.replace(/\/+$/, "") + "/")
}

function sameOrigin(a, b) {
  try {
    return new URL(a).origin === new URL(b).origin
  } catch {
    return false
  }
}

/**
 * Whether a persistent tab must load `url`, or can be read as it is.
 * Returns { navigate: bool, reason }.
 */
export function planNavigation({ entry, tab, url, config, now }) {
  if (!tab) return { navigate: true, reason: "new tab" }
  if (tab.discarded) return { navigate: true, reason: "Chrome discarded the tab" }
  if (tab.frozen) return { navigate: true, reason: "Chrome froze the tab" }
  if (!entry || !entry.loadedAt || !tab.url) return { navigate: true, reason: "never loaded" }
  if (entry.failed) return { navigate: true, reason: "the last load failed" }
  const maxAge = config?.maxAgeMs ?? DEFAULT_MAX_AGE_MS
  if (now - entry.loadedAt > maxAge) return { navigate: true, reason: "stale" }
  if (!sameOrigin(tab.url, url)) return { navigate: true, reason: "another site" }
  // Same site — but a keyed page (linkedin.com/notifications) only counts if
  // the tab is still on it: the operator may have clicked away to the feed.
  if (config?.match && urlMatches(config.match, url) && !urlMatches(config.match, tab.url)) {
    return { navigate: true, reason: "the tab left its page" }
  }
  return { navigate: false, reason: "reused without reloading" }
}

export function createRunTabs(deps) {
  let queue = Promise.resolve()
  const serial = (work) => {
    const next = queue.then(work, work)
    queue = next.catch(() => {})
    return next
  }
  const now = () => (deps.now ? deps.now() : Date.now())

  const readMap = async (key) => {
    const stored = await deps.storage.get(key)
    return stored && typeof stored === "object" && !Array.isArray(stored) ? { ...stored } : {}
  }
  const readRuns = async () => {
    const out = {}
    for (const [run, e] of Object.entries(await readMap(RUN_TABS_KEY))) {
      if (e && typeof e.tab === "number") {
        const at = Number(e.at) || 0
        out[run] = { tab: e.tab, at, since: Number(e.since) || at, ...(e.key ? { key: e.key } : {}) }
      }
    }
    return out
  }
  const readPersistent = async () => {
    const out = {}
    for (const [key, e] of Object.entries(await readMap(PERSISTENT_TABS_KEY))) {
      if (e && typeof e.tab === "number") out[key] = { ...e }
    }
    return out
  }

  /** Tabs held by a live run; drops orphaned run entries from `runs` in place. */
  async function liveHeld(runs) {
    const held = new Set()
    for (const [run, entry] of Object.entries(runs)) {
      if (now() - entry.at > ORPHAN_TTL_MS) delete runs[run]
      else if (await deps.tabs.exists(entry.tab)) held.add(entry.tab)
    }
    return held
  }

  async function persistentTabIds() {
    const ids = new Set()
    for (const e of Object.values(await readPersistent())) ids.add(e.tab)
    return ids
  }

  async function takePersistent(runs, runId, key) {
    const persistent = await readPersistent()
    const entry = persistent[key]
    if (entry && (await deps.tabs.exists(entry.tab))) {
      await liveHeld(runs)
      for (const [other, e] of Object.entries(runs)) {
        if (other !== runId && e.tab === entry.tab) {
          throw new RunTabError(`the ${key} tab is busy with another run — runs on it take turns`)
        }
      }
      runs[runId] = { tab: entry.tab, at: now(), since: now(), key }
      return { tabId: entry.tab, created: false }
    }
    // First use, or the operator closed it: (re)create it.
    const tabId = await deps.tabs.open({ persistent: key })
    persistent[key] = { tab: tabId, loadedAt: 0, url: "", failed: false }
    await deps.storage.set(PERSISTENT_TABS_KEY, persistent)
    runs[runId] = { tab: tabId, at: now(), since: now(), key }
    return { tabId, created: entry ? "recreated" : "opened" }
  }

  async function takeThrowaway(runs, runId) {
    const held = await liveHeld(runs)
    const keep = await persistentTabIds()
    const { maxTabs } = deps.limits()

    // Only live runs' own tabs count against the cap.
    const busy = [...held].filter((id) => !keep.has(id)).length
    if (busy >= maxTabs) {
      throw new RunTabError(`all ${maxTabs} automation tabs are in use — this run waits for one`, CODE_TABS_BUSY)
    }
    let tabId
    const free = (await deps.tabs.owned()).filter((id) => !keep.has(id) && !held.has(id))
    for (const id of free) {
      if (await deps.tabs.isIdle(id)) {
        tabId = id
        break
      }
    }
    // No idle tab: a leftover no run holds (a release that never came, a
    // builder tab) is reused rather than counted — up to the cap, a new one.
    if (tabId === undefined && busy + free.length >= maxTabs && free.length > 0) tabId = free[0]
    if (tabId === undefined) tabId = await deps.tabs.open({})
    runs[runId] = { tab: tabId, at: now(), since: now() }
    return { tabId, created: false }
  }

  /**
   * The tab for a run: its own, or one taken on its first command.
   * Returns { tabId, key, created }.
   */
  function tabFor(runId, key = "") {
    if (!runId) return Promise.reject(new RunTabError("a run command needs a run id"))
    return serial(async () => {
      const runs = await readRuns()
      const mine = runs[runId]
      if (mine) {
        if (await deps.tabs.exists(mine.tab)) {
          // Used: not an orphan. Written at most every 30 s.
          if (now() - mine.at > 30_000) {
            runs[runId] = { ...mine, at: now() }
            await deps.storage.set(RUN_TABS_KEY, runs)
          }
          return { tabId: mine.tab, key: mine.key || "", created: false }
        }
        // Closed under the run. Fail THIS run; keep the entry so every later
        // command of the run fails the same way instead of quietly opening a
        // new tab half-way through a workflow.
        throw new RunTabError(
          mine.key
            ? `the ${mine.key} tab was closed or crashed during this run — this run stops here; the next run reopens it`
            : "the automation tab for this run was closed or crashed — this run stops here; other runs are unaffected",
        )
      }
      const taken = key ? await takePersistent(runs, runId, key) : await takeThrowaway(runs, runId)
      await deps.storage.set(RUN_TABS_KEY, runs)
      return { tabId: taken.tabId, key: key || "", created: taken.created }
    })
  }

  /** Should this run's navigate to `url` actually load it? */
  async function planFor(runId, url) {
    const mine = (await readRuns())[runId]
    if (!mine?.key) return { navigate: true, reason: "throwaway tab" }
    const entry = (await readPersistent())[mine.key]
    const tab = await deps.tabs.info(mine.tab)
    return planNavigation({ entry, tab, url, config: deps.persistentConfig(mine.key), now: now() })
  }

  /** Record how a persistent tab's load went, for the next run's freshness check. */
  function markLoaded(runId, url, ok) {
    return serial(async () => {
      const mine = (await readRuns())[runId]
      if (!mine?.key) return
      const persistent = await readPersistent()
      const entry = persistent[mine.key]
      if (!entry || entry.tab !== mine.tab) return
      persistent[mine.key] = { ...entry, url, loadedAt: ok ? now() : entry.loadedAt, failed: !ok }
      await deps.storage.set(PERSISTENT_TABS_KEY, persistent)
    })
  }

  /** Whether a run holds this tab, or it is a persistent tab — non-run commands leave both alone. */
  async function isReserved(tabId) {
    const runs = await readRuns()
    if (Object.values(runs).some((e) => e.tab === tabId)) return true
    return (await persistentTabIds()).has(tabId)
  }

  /** The persistent tab for a key, if open — for a future watcher (FM-275). */
  async function persistentTab(key) {
    const entry = (await readPersistent())[key]
    return entry && (await deps.tabs.exists(entry.tab)) ? entry.tab : null
  }

  /** The run is over: give its tab back. Safe twice, or for a run that never took a tab. */
  function release(runId) {
    return serial(async () => {
      const runs = await readRuns()
      const mine = runs[runId]
      if (!mine) return { released: false }
      delete runs[runId]
      await deps.storage.set(RUN_TABS_KEY, runs)

      // Only this tab's debugger: another run's attachment is untouched.
      await deps.debugger.detach(mine.tab)
      if (!(await deps.tabs.exists(mine.tab))) return { released: true, tab_id: mine.tab, closed: true }
      // A persistent tab stays exactly where the run left it.
      if (mine.key) return { released: true, tab_id: mine.tab, closed: false, kept: mine.key }

      const held = await liveHeld(runs)
      const keep = await persistentTabIds()
      let idle = 0
      for (const id of await deps.tabs.owned()) {
        if (id !== mine.tab && !held.has(id) && !keep.has(id) && (await deps.tabs.isIdle(id))) idle++
      }
      if (idle >= deps.limits().maxIdle) {
        await deps.tabs.close(mine.tab)
        return { released: true, tab_id: mine.tab, closed: true }
      }
      await deps.tabs.blank(mine.tab)
      return { released: true, tab_id: mine.tab, closed: false }
    })
  }

  /**
   * The server's list of runs in flight: every other run's tab is released
   * (a run.end that never arrived). Entries newer than SYNC_GRACE_MS stay.
   */
  async function sync(activeIds) {
    const active = new Set(Array.isArray(activeIds) ? activeIds : [])
    const runs = await serial(() => readRuns())
    const stale = Object.entries(runs)
      .filter(([run, e]) => !active.has(run) && now() - e.since > SYNC_GRACE_MS)
      .map(([run]) => run)
    const released = []
    for (const run of stale) {
      if ((await release(run)).released) released.push(run)
    }
    return { released, held: Object.keys(runs).length - released.length }
  }

  /** How many tabs live runs hold right now (kept tabs and idle tabs excluded). */
  async function inUse() {
    return serial(async () => {
      const runs = await readRuns()
      const held = await liveHeld(runs)
      const keep = await persistentTabIds()
      return [...held].filter((id) => !keep.has(id)).length
    })
  }

  /** If this run's tab is gone, the sentence saying so; otherwise "". Changes nothing. */
  async function tabGone(runId) {
    const mine = (await readRuns())[runId]
    if (!mine || (await deps.tabs.exists(mine.tab))) return ""
    return mine.key
      ? `the ${mine.key} tab was closed or crashed during this run — this run stops here; the next run reopens it`
      : "the automation tab for this run was closed or crashed — this run stops here; other runs are unaffected"
  }

  return { tabFor, planFor, markLoaded, release, isReserved, persistentTab, tabGone, sync, inUse }
}

/*
Signed out? A persistent tab exists to read a session the operator already has.
When the site has signed him out, the run must fail and SAY so — a retry would
only load the sign-in page again. This is the URL half; the WhatsApp QR code,
which appears without a URL change, is checked in the page (driver/runs.js).
*/
const SIGN_IN_PATHS = /^\/(login|signin|sign-in|authwall|uas\/login|checkpoint|i\/flow\/login|accounts\/login)(\/|$)/i
const SIGN_IN_HOSTS = new Set(["accounts.google.com", "login.live.com", "login.microsoftonline.com"])

export function signedOutAt(key, url, config) {
  let parsed
  try {
    parsed = new URL(url)
  } catch {
    return ""
  }
  if (!/^https?:$/.test(parsed.protocol)) return ""
  if (SIGN_IN_HOSTS.has(parsed.hostname)) return `signed out of ${key}`
  if (SIGN_IN_PATHS.test(parsed.pathname)) return `signed out of ${key}`
  // Gmail's signed-out landing is on another Google host entirely.
  if (key === "gmail" && /(^|\.)workspace\.google\.com$|^www\.google\.com$/.test(parsed.hostname)) return `signed out of ${key}`
  void config
  return ""
}
