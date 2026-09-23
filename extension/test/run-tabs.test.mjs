// FM-267: one tab per run, and persistent site tabs. Pure logic, no Chrome.
//
//   node --test extension/test/
import { test } from "node:test"
import assert from "node:assert/strict"

import { createRunTabs, planNavigation, urlMatches, RunTabError } from "../driver/run-tabs-core.js"

/** A fake browser: tabs with urls, a storage area, and a log of what was done. */
function fakeBrowser({ maxTabs = 3, maxIdle = 1, config = {} } = {}) {
  const store = new Map()
  const tabs = new Map() // id -> { url, discarded, frozen }
  const log = []
  let nextId = 100
  let clock = 1_000_000
  const b = {
    tabs,
    log,
    store,
    advance: (ms) => (clock += ms),
    close: (id) => tabs.delete(id),
    deps: {
      now: () => clock,
      storage: {
        get: async (k) => (store.has(k) ? structuredClone(store.get(k)) : undefined),
        set: async (k, v) => void store.set(k, structuredClone(v)),
      },
      tabs: {
        exists: async (id) => tabs.has(id),
        info: async (id) => (tabs.has(id) ? { ...tabs.get(id) } : null),
        owned: async () => [...tabs.keys()],
        isIdle: async (id) => tabs.get(id)?.url === "about:blank",
        open: async ({ persistent } = {}) => {
          const id = nextId++
          tabs.set(id, { url: "about:blank" })
          log.push(["open", id, persistent || ""])
          return id
        },
        blank: async (id) => {
          tabs.get(id).url = "about:blank"
          log.push(["blank", id])
        },
        close: async (id) => {
          tabs.delete(id)
          log.push(["close", id])
        },
      },
      debugger: { detach: async (id) => void log.push(["detach", id]) },
      limits: () => ({ maxTabs, maxIdle }),
      persistentConfig: (key) => config[key] ?? null,
    },
  }
  return b
}

/* ------------------------------------------------------- throwaway tabs */

test("each run gets its own tab, and keeps it for every command", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  // Concurrent first commands of three runs: all different tabs.
  const [a, c, d] = await Promise.all([rt.tabFor("run-a"), rt.tabFor("run-c"), rt.tabFor("run-d")])
  assert.equal(new Set([a.tabId, c.tabId, d.tabId]).size, 3, "two runs were given the same tab")
  // Later commands: the same tab each time.
  for (let i = 0; i < 3; i++) {
    assert.equal((await rt.tabFor("run-a")).tabId, a.tabId)
    assert.equal((await rt.tabFor("run-c")).tabId, c.tabId)
  }
})

test("two runs racing for one idle tab do not share it", async () => {
  const b = fakeBrowser()
  b.tabs.set(1, { url: "about:blank" }) // the one idle automation tab
  const rt = createRunTabs(b.deps)
  const got = await Promise.all(["r1", "r2", "r3"].map((r) => rt.tabFor(r)))
  assert.equal(new Set(got.map((g) => g.tabId)).size, 3)
  assert.ok(got.some((g) => g.tabId === 1), "the idle tab was not reused")
})

test("the tab cap refuses a run past it, with a reason", async () => {
  const b = fakeBrowser({ maxTabs: 2 })
  const rt = createRunTabs(b.deps)
  await rt.tabFor("r1")
  await rt.tabFor("r2")
  await assert.rejects(rt.tabFor("r3"), (err) => err instanceof RunTabError && /all 2 automation tabs are in use/.test(err.message))
})

test("a closed tab fails only its own run, and never gets a replacement mid-run", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("run-a")
  const c = await rt.tabFor("run-c")
  b.close(a.tabId)
  await assert.rejects(rt.tabFor("run-a"), /closed or crashed/)
  await assert.rejects(rt.tabFor("run-a"), /closed or crashed/, "a second command of the dead run got a tab")
  assert.equal((await rt.tabFor("run-c")).tabId, c.tabId, "the other run was affected")
  // A new run is unaffected too.
  const d = await rt.tabFor("run-d")
  assert.notEqual(d.tabId, c.tabId)
})

test("release blanks the tab and keeps it idle, closing it past maxIdle", async () => {
  const b = fakeBrowser({ maxIdle: 1 })
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("run-a")
  const c = await rt.tabFor("run-c")
  b.tabs.get(a.tabId).url = "https://example.com/"
  b.tabs.get(c.tabId).url = "https://example.org/"

  assert.deepEqual(await rt.release("run-a"), { released: true, tab_id: a.tabId, closed: false })
  assert.equal(b.tabs.get(a.tabId).url, "about:blank")
  // One idle already kept: the next released tab is closed.
  assert.deepEqual(await rt.release("run-c"), { released: true, tab_id: c.tabId, closed: true })
  assert.equal(b.tabs.has(c.tabId), false)
  // Released twice: harmless.
  assert.deepEqual(await rt.release("run-a"), { released: false })
  // The idle tab is reused by the next run.
  assert.equal((await rt.tabFor("run-e")).tabId, a.tabId)
})

test("release detaches the debugger of that tab only", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("run-a")
  await rt.tabFor("run-c")
  await rt.release("run-a")
  assert.deepEqual(b.log.filter((l) => l[0] === "detach"), [["detach", a.tabId]])
})

test("a tab held by a run is reserved from non-run commands", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("run-a")
  assert.equal(await rt.isReserved(a.tabId), true)
  await rt.release("run-a")
  assert.equal(await rt.isReserved(a.tabId), false)
})

test("the map survives the service worker being killed", async () => {
  const b = fakeBrowser()
  const a = await createRunTabs(b.deps).tabFor("run-a")
  // A fresh instance over the same storage: what a restarted worker sees.
  const again = createRunTabs(b.deps)
  assert.equal((await again.tabFor("run-a")).tabId, a.tabId)
  assert.notEqual((await again.tabFor("run-b")).tabId, a.tabId)
})

test("an abandoned run (no run.end ever came) stops holding its tab after an hour", async () => {
  const b = fakeBrowser({ maxTabs: 1 })
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("run-a")
  b.tabs.get(a.tabId).url = "about:blank"
  await assert.rejects(rt.tabFor("run-b"), /in use/)
  b.advance(61 * 60 * 1000)
  assert.equal((await rt.tabFor("run-b")).tabId, a.tabId)
})

test("a command without a run id is refused", async () => {
  const rt = createRunTabs(fakeBrowser().deps)
  await assert.rejects(rt.tabFor(""), /needs a run id/)
})

/* ------------------------------------------------------ persistent tabs */

const WA = { whatsapp: { match: "web.whatsapp.com", maxAgeMs: 30 * 60 * 1000 } }

test("a persistent tab is opened once and reused by later runs, and kept at release", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const first = await rt.tabFor("run-1", "whatsapp")
  assert.equal(first.created, "opened")
  b.tabs.get(first.tabId).url = "https://web.whatsapp.com/"
  const released = await rt.release("run-1")
  assert.equal(released.kept, "whatsapp")
  assert.equal(b.tabs.get(first.tabId).url, "https://web.whatsapp.com/", "release navigated the persistent tab")

  const second = await rt.tabFor("run-2", "whatsapp")
  assert.equal(second.tabId, first.tabId)
  assert.equal(second.created, false)
})

test("reuse without navigation: same site, fresh, loaded", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const { tabId } = await rt.tabFor("run-1", "whatsapp")
  assert.equal((await rt.planFor("run-1", "https://web.whatsapp.com/")).navigate, true, "a brand-new tab must load")
  b.tabs.get(tabId).url = "https://web.whatsapp.com/"
  await rt.markLoaded("run-1", "https://web.whatsapp.com/", true)
  await rt.release("run-1")

  await rt.tabFor("run-2", "whatsapp")
  b.advance(5 * 60 * 1000)
  assert.deepEqual(await rt.planFor("run-2", "https://web.whatsapp.com/"), { navigate: false, reason: "reused without reloading" })
})

test("a second run on the same key waits its turn", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  await rt.tabFor("run-1", "whatsapp")
  await assert.rejects(rt.tabFor("run-2", "whatsapp"), /busy with another run/)
  // …and a run on no key is not held up by it.
  assert.ok((await rt.tabFor("run-3")).tabId)
  await rt.release("run-1")
  assert.ok((await rt.tabFor("run-2", "whatsapp")).tabId)
})

test("a persistent tab is never taken as a throwaway tab, even when blank", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const wa = await rt.tabFor("run-1", "whatsapp") // a new tab, still about:blank
  await rt.release("run-1")
  const other = await rt.tabFor("run-2")
  assert.notEqual(other.tabId, wa.tabId)
  assert.equal(await rt.isReserved(wa.tabId), true)
})

test("a closed persistent tab is recreated by the next run", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const first = await rt.tabFor("run-1", "whatsapp")
  await rt.release("run-1")
  b.close(first.tabId)
  const next = await rt.tabFor("run-2", "whatsapp")
  assert.notEqual(next.tabId, first.tabId)
  assert.equal(next.created, "recreated")
  assert.equal(await rt.persistentTab("whatsapp"), next.tabId)
  assert.equal((await rt.planFor("run-2", "https://web.whatsapp.com/")).navigate, true, "a recreated tab must load")
})

test("a persistent tab closed during a run fails that run only", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const wa = await rt.tabFor("run-1", "whatsapp")
  const other = await rt.tabFor("run-2")
  b.close(wa.tabId)
  await assert.rejects(rt.tabFor("run-1", "whatsapp"), /whatsapp tab was closed or crashed during this run/)
  assert.equal((await rt.tabFor("run-2")).tabId, other.tabId)
})

test("planNavigation: discarded, frozen, stale, failed, another site, left its page", () => {
  const now = 10_000_000
  const entry = { loadedAt: now - 60_000, failed: false }
  const tab = { url: "https://web.whatsapp.com/" }
  const cfg = { match: "web.whatsapp.com", maxAgeMs: 30 * 60 * 1000 }
  const plan = (over = {}) => planNavigation({ entry, tab, url: "https://web.whatsapp.com/", config: cfg, now, ...over })

  assert.equal(plan().navigate, false)
  assert.deepEqual(plan({ tab: { ...tab, discarded: true } }), { navigate: true, reason: "Chrome discarded the tab" })
  assert.deepEqual(plan({ tab: { ...tab, frozen: true } }), { navigate: true, reason: "Chrome froze the tab" })
  assert.deepEqual(plan({ now: now + 31 * 60 * 1000 }), { navigate: true, reason: "stale" })
  assert.deepEqual(plan({ entry: { ...entry, failed: true } }), { navigate: true, reason: "the last load failed" })
  assert.deepEqual(plan({ url: "https://example.com/" }), { navigate: true, reason: "another site" })
  assert.deepEqual(plan({ entry: { loadedAt: 0 } }), { navigate: true, reason: "never loaded" })

  const li = { match: "www.linkedin.com/notifications", maxAgeMs: cfg.maxAgeMs }
  const onFeed = planNavigation({ entry, tab: { url: "https://www.linkedin.com/feed/" }, url: "https://www.linkedin.com/notifications/", config: li, now })
  assert.deepEqual(onFeed, { navigate: true, reason: "the tab left its page" })
  const onPage = planNavigation({ entry, tab: { url: "https://www.linkedin.com/notifications/?filter=all" }, url: "https://www.linkedin.com/notifications/", config: li, now })
  assert.equal(onPage.navigate, false)
})

test("a stale persistent tab is reloaded, through the whole flow", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const { tabId } = await rt.tabFor("run-1", "whatsapp")
  b.tabs.get(tabId).url = "https://web.whatsapp.com/"
  await rt.markLoaded("run-1", "https://web.whatsapp.com/", true)
  await rt.release("run-1")
  b.advance(31 * 60 * 1000)
  await rt.tabFor("run-2", "whatsapp")
  assert.deepEqual(await rt.planFor("run-2", "https://web.whatsapp.com/"), { navigate: true, reason: "stale" })
})

test("a discarded persistent tab is reloaded, through the whole flow", async () => {
  const b = fakeBrowser({ config: WA })
  const rt = createRunTabs(b.deps)
  const { tabId } = await rt.tabFor("run-1", "whatsapp")
  b.tabs.get(tabId).url = "https://web.whatsapp.com/"
  await rt.markLoaded("run-1", "https://web.whatsapp.com/", true)
  await rt.release("run-1")
  b.tabs.get(tabId).discarded = true
  await rt.tabFor("run-2", "whatsapp")
  assert.deepEqual(await rt.planFor("run-2", "https://web.whatsapp.com/"), { navigate: true, reason: "Chrome discarded the tab" })
})

test("a throwaway run always navigates", async () => {
  const rt = createRunTabs(fakeBrowser().deps)
  await rt.tabFor("run-1")
  assert.equal((await rt.planFor("run-1", "https://web.whatsapp.com/")).navigate, true)
})

test("urlMatches is host-exact and path-segment aware", () => {
  assert.equal(urlMatches("web.whatsapp.com", "https://web.whatsapp.com/"), true)
  assert.equal(urlMatches("web.whatsapp.com", "https://web.whatsapp.com.evil.io/"), false)
  assert.equal(urlMatches("x.com/notifications", "https://x.com/notifications"), true)
  assert.equal(urlMatches("x.com/notifications", "https://x.com/notificationsettings"), false)
  assert.equal(urlMatches("x.com/notifications", "https://x.com/home"), false)
})

test("signedOutAt spots each monitored site's sign-in page, and nothing else", async () => {
  const { signedOutAt } = await import("../driver/run-tabs-core.js")
  const out = [
    ["gmail", "https://accounts.google.com/v3/signin/identifier?continue=https://mail.google.com"],
    ["gmail", "https://workspace.google.com/intl/en/gmail/"],
    ["linkedin-notifications", "https://www.linkedin.com/login?session_redirect=%2Fnotifications"],
    ["linkedin-notifications", "https://www.linkedin.com/authwall?trk=x"],
    ["linkedin-notifications", "https://www.linkedin.com/checkpoint/challenge/abc"],
    ["x-notifications", "https://x.com/i/flow/login?redirect_after_login=%2Fnotifications"],
    ["x-notifications", "https://x.com/login"],
  ]
  for (const [key, url] of out) assert.match(signedOutAt(key, url), /signed out of/, url)
  const inside = [
    ["gmail", "https://mail.google.com/mail/u/0/#inbox"],
    ["linkedin-notifications", "https://www.linkedin.com/notifications/"],
    ["x-notifications", "https://x.com/notifications"],
    ["x-notifications", "https://x.com/loginhelp-is-a-profile"],
    ["whatsapp", "https://web.whatsapp.com/"],
  ]
  for (const [key, url] of inside) assert.equal(signedOutAt(key, url), "", url)
})

test("tabGone names a closed run tab without changing anything", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  assert.equal(await rt.tabGone("never-started"), "")
  const a = await rt.tabFor("run-a")
  assert.equal(await rt.tabGone("run-a"), "")
  b.close(a.tabId)
  assert.match(await rt.tabGone("run-a"), /closed or crashed/)
  assert.equal(b.log.filter((l) => l[0] === "open").length, 1, "tabGone opened a tab")
})

/* ------------------------------------ 0.12.1: leaks, capacity, reconciliation */

test("a full browser answers with the typed busy code", async () => {
  const b = fakeBrowser({ maxTabs: 1 })
  const rt = createRunTabs(b.deps)
  await rt.tabFor("r1")
  await assert.rejects(rt.tabFor("r2"), (err) => err.code === "tabs_busy" && /in use/.test(err.message))
})

test("tabs of runs whose run.end was lost are released by the server's sync, after a worker restart too", async () => {
  const b = fakeBrowser({ maxTabs: 3 })
  let rt = createRunTabs(b.deps)
  for (const r of ["dead-1", "dead-2", "dead-3"]) await rt.tabFor(r)
  await assert.rejects(rt.tabFor("new-1"), /in use/)
  b.advance(2 * 60 * 1000)
  // The service worker restarted and the socket reconnected: a new instance
  // over the same storage, and the server says only dead-2 is still going.
  rt = createRunTabs(b.deps)
  const out = await rt.sync(["dead-2"])
  assert.deepEqual(out.released.sort(), ["dead-1", "dead-3"])
  assert.equal(await rt.inUse(), 1)
  assert.ok((await rt.tabFor("new-1")).tabId)
  assert.ok((await rt.tabFor("new-2")).tabId)
  await assert.rejects(rt.tabFor("new-3"), /in use/)
})

test("a sync never releases a run claimed moments ago", async () => {
  const b = fakeBrowser()
  const rt = createRunTabs(b.deps)
  const a = await rt.tabFor("just-claimed")
  const out = await rt.sync([])
  assert.deepEqual(out.released, [])
  assert.equal((await rt.tabFor("just-claimed")).tabId, a.tabId)
})

test("an orphaned run stops holding its tab after the TTL, but a run in use does not", async () => {
  const b = fakeBrowser({ maxTabs: 2 })
  const rt = createRunTabs(b.deps)
  await rt.tabFor("orphan")
  await rt.tabFor("busy-run")
  for (let i = 0; i < 12; i++) {
    b.advance(60 * 1000)
    await rt.tabFor("busy-run") // a live run keeps using its tab
  }
  assert.equal(await rt.inUse(), 1)
  assert.ok((await rt.tabFor("next")).tabId)
  assert.ok((await rt.tabFor("busy-run")).tabId, "the live run lost its tab")
})

test("kept site tabs and idle tabs do not count against the cap", async () => {
  const b = fakeBrowser({ maxTabs: 2, config: WA })
  const rt = createRunTabs(b.deps)
  await rt.tabFor("wa", "whatsapp")
  await rt.tabFor("gm", "gmail")
  b.tabs.set(900, { url: "about:blank" }) // an idle tab
  assert.equal(await rt.inUse(), 0)
  await rt.tabFor("r1")
  await rt.tabFor("r2")
  await assert.rejects(rt.tabFor("r3"), /in use/)
})

test("a leftover tab no run holds is reused instead of counting as busy", async () => {
  const b = fakeBrowser({ maxTabs: 2 })
  b.tabs.set(1, { url: "https://example.com/old" })
  b.tabs.set(2, { url: "https://example.com/older" })
  const rt = createRunTabs(b.deps)
  const got = [await rt.tabFor("r1"), await rt.tabFor("r2")].map((g) => g.tabId).sort()
  assert.deepEqual(got, [1, 2])
  assert.equal(b.log.filter((l) => l[0] === "open").length, 0)
})
