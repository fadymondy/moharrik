// MH-422: a run stops the moment its tab leaves the allowed sites, however it
// got there — a link, an Enter that submits a form, a redirect, a page that
// moves by itself.
//
// The allow-list used to be checked only for goto steps, on the server. These
// run the driver's real code against jsdom pages inside a fake chrome that
// behaves the way Chrome does where it matters here:
//
//   - a navigation is ASYNCHRONOUS: the tab reports "loading", then COMMITS
//     (webNavigation.onCommitted, tabs.onUpdated {url}), then "complete"
//   - a redirect chain commits only its last URL
//   - chrome.tabs.get and webNavigation.getFrame report the COMMITTED URL, from
//     the browser — nothing a page script can touch
//   - an injection pinned to a documentId fails once that document is gone
//   - a click on a link and a form submission navigate the tab (jsdom does not
//     navigate at all, so the fake does what the browser's default action is)
//
// web/e2e/allowed-sites-check.mjs runs the same cases in a real Chromium.

import { test } from "node:test"
import assert from "node:assert/strict"
import { JSDOM } from "jsdom"

import { closeRun, runStep } from "../driver.js"

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/* ------------------------------------------------------------ the fake web */

const SITES = {
  "https://allowed.test/start": `<!doctype html><title>Start</title>
    <a id="away" href="https://evil.test/landing">elsewhere</a>
    <a id="bounce" href="/out">a tracked link</a>
    <a id="sub" href="https://app.allowed.test:8443/inside">the app</a>
    <a id="lookalike" href="https://allowed.test.evil.io/">look-alike</a>
    <a id="sso" href="/sso">sign in</a>
    <form id="search" action="https://evil.test/search" method="get"><input id="q" name="q"></form>
    <form id="local" action="/results" method="get"><input id="q2" name="q"></form>
    <p id="here">allowed start</p>`,
  "https://allowed.test/results": `<!doctype html><title>Results</title><h1 id="result">results</h1>`,
  // A redirect chain that ENDS off the list, passing through an allowed host.
  "https://allowed.test/out": { redirect: "https://hop.allowed.test/next" },
  "https://hop.allowed.test/next": { redirect: "https://evil.test/landing" },
  // One that passes THROUGH an off-list host and ends back on the list: nothing
  // committed there, so nothing acted there.
  "https://allowed.test/sso": { redirect: "https://idp.evil.test/bounce" },
  "https://idp.evil.test/bounce": { redirect: "https://www.allowed.test/home" },
  "https://www.allowed.test/home": `<!doctype html><title>Home</title><p id="here">signed in</p><a id="away" href="https://evil.test/landing">x</a>`,
  "https://app.allowed.test/inside": `<!doctype html><title>App</title><p id="here">inside the app</p>`,
  "https://evil.test/landing": `<!doctype html><title>Evil</title><input id="field"><p id="here">evil</p>`,
  "https://evil.test/search": `<!doctype html><title>Evil search</title><input id="field"><p id="here">evil search</p>`,
  "https://allowed.test.evil.io/": `<!doctype html><title>Look-alike</title><input id="field">`,
  // A page that moves by itself, after it has loaded.
  "https://allowed.test/drifts": `<!doctype html><title>Drifts</title><p id="here">drifting</p>
    <script>setTimeout(() => window.__navigate("https://evil.test/landing"), 30)</script>`,
  // An off-list page that does its best to look like the allowed one.
  "https://evil.test/forger": `<!doctype html><title>allowed.test</title><input id="field">
    <script>
      Object.defineProperty(document, "URL", { get: () => "https://allowed.test/start" })
      Object.defineProperty(document, "documentURI", { get: () => "https://allowed.test/start" })
      Object.defineProperty(document, "domain", { get: () => "allowed.test" })
      history.replaceState(null, "", "/start")
      window.__mohAllowed = true
    </script>`,
  // And an allowed page that forges the other way.
  "https://allowed.test/forger": `<!doctype html><title>evil.test</title><p id="here">really allowed</p>
    <script>
      Object.defineProperty(document, "URL", { get: () => "https://evil.test/" })
      Object.defineProperty(document, "domain", { get: () => "evil.test" })
    </script>`,
}

const route = (url) => {
  const u = new URL(url)
  return SITES[u.origin.replace(/:\d+$/, "") + u.pathname] ?? SITES[u.origin + u.pathname]
}

let tabSeq = 100

/** A fake chrome with one run tab that really navigates, commits and redirects. */
function browser({ stored = ["allowed.test"] } = {}) {
  const TAB = ++tabSeq
  const updated = new Set()
  const committed = new Set()
  const state = { url: "", status: "complete", window: null, documentId: null, commits: [], injections: [], docSeq: 0, silent: false }

  const fire = (set, ...args) => [...set].forEach((fn) => fn(...args))

  function page(url, html) {
    const dom = new JSDOM(html, {
      url: url.startsWith("http") ? url : "about:blank",
      runScripts: "dangerously",
      pretendToBeVisual: true,
      beforeParse(w) {
        // The page moving itself (location.href = …), which jsdom cannot do.
        w.__navigate = (to) => navigate(String(new URL(to, url)))
        w.Element.prototype.scrollIntoView = () => {}
        // The browser's default actions: following a link, submitting a form.
        w.addEventListener("click", (e) => {
          const a = e.target.closest?.("a[href]")
          if (!a || e.defaultPrevented) return
          e.preventDefault()
          navigate(a.href)
        })
        w.addEventListener("submit", (e) => {
          if (e.defaultPrevented) return
          e.preventDefault()
          const form = e.target
          const to = new URL(form.getAttribute("action") || url, url)
          for (const [k, v] of new w.FormData(form)) to.searchParams.append(k, v)
          navigate(to.href)
        })
      },
    })
    return dom.window
  }

  function commit(url) {
    const site = url.startsWith("http") ? route(url) : null
    state.url = url
    state.documentId = `doc-${TAB}-${++state.docSeq}`
    state.window = page(url, typeof site === "string" ? site : "<!doctype html><title></title>")
    state.commits.push(url)
    // silent: the browser has committed, but its events have not reached the
    // extension yet — they are delivered asynchronously, and can lag.
    if (state.silent) return
    fire(committed, { tabId: TAB, frameId: 0, url, documentId: state.documentId })
    fire(updated, TAB, { status: "loading", url })
    setTimeout(() => {
      state.status = "complete"
      fire(updated, TAB, { status: "complete" })
    }, 5)
  }

  function navigate(url) {
    state.status = "loading"
    fire(updated, TAB, { status: "loading" })
    setTimeout(() => {
      let at = url
      for (let i = 0; i < 10; i++) {
        const site = at.startsWith("http") ? route(at) : null
        if (site && typeof site === "object" && site.redirect) at = new URL(site.redirect, at).href
        else break
      }
      commit(at)
    }, 15)
  }

  state.commitNow = commit

  const listenerSet = (set) => ({ addListener: (fn) => set.add(fn), removeListener: (fn) => set.delete(fn) })

  globalThis.chrome = {
    tabs: {
      create: async ({ url }) => (navigate(url), { id: TAB }),
      update: async (id, { url }) => (navigate(url), { id }),
      get: async (id) => ({ id, url: state.url, title: state.window?.document.title ?? "", status: state.status }),
      remove: async () => {},
      onUpdated: listenerSet(updated),
    },
    webNavigation: {
      getFrame: async ({ frameId }) => (frameId === 0 ? { frameId: 0, url: state.url, documentId: state.documentId } : null),
      onCommitted: listenerSet(committed),
    },
    scripting: {
      executeScript: async ({ target, func, args = [] }) => {
        if (target.documentIds && !target.documentIds.includes(state.documentId)) {
          throw new Error(`No document with id ${target.documentIds[0]} in tab with id ${target.tabId}`)
        }
        state.injections.push(state.url)
        const fn = state.window.eval(`(${func.toString()})`)
        const result = fn(...args)
        return [{ result: result === undefined ? undefined : JSON.parse(JSON.stringify(result)) }]
      },
    },
    storage: { local: { get: async () => ({ "moh.agent": { allowedHosts: stored } }) } },
  }
  return state
}

let runSeq = 0

/** Steps the way the server sends them: numbered, with the list attached. */
function runner(list = ["allowed.test"]) {
  const run = `run-${++runSeq}`
  let n = 0
  return {
    run,
    step: (command, args = {}) => runStep(run, { command, args, step: ++n, allowed_hosts: list }),
  }
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

const evilInjections = (tab) => tab.injections.filter((u) => !/^https:\/\/([a-z]+\.)*allowed\.test\//.test(u))

/* ------------------------------------------------------------------ cases */

test("a click on a link to an off-list host stops the run before the next step", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assertStops(r.step("click", { selector: "#away" }), /^step 2 went to evil\.test,/)
  // The next step never acts: it is refused with the same reason, and nothing
  // was injected into the off-list page at any point.
  await assertStops(r.step("type", { selector: "#field", text: "a password" }), /^step 2 went to evil\.test,/)
  await assertStops(r.step("extract", { selector: "body" }), /^step 2 went to evil\.test,/)
  assert.deepEqual(evilInjections(tab), [])
  // And the off-list page is not left running: the tab is blanked.
  await sleep(40)
  assert.equal(tab.commits.at(-1), "about:blank")
  await closeRun(r.run)
})

test("an Enter that submits to an off-list form action stops the run", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await r.step("type", { selector: "#q", text: "moharrik" })
  await assertStops(r.step("press", { key: "Enter" }), /^step 3 went to evil\.test,/)
  assert.ok(tab.commits.includes("https://evil.test/search?q=moharrik"), tab.commits.join(" "))
  await assertStops(r.step("type", { selector: "#field", text: "x" }), /step 3 went to evil\.test/)
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("a click whose redirect chain ends off the list stops, naming where it ended", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assertStops(r.step("click", { selector: "#bounce" }), /^step 2 went to evil\.test,/)
  // Only the end of the chain committed.
  assert.ok(!tab.commits.includes("https://hop.allowed.test/next"))
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("a goto answered with a redirect off the list stops at the goto", async () => {
  const tab = browser()
  const r = runner()
  await assertStops(r.step("goto", { url: "https://allowed.test/out" }), /^step 1 went to evil\.test,/)
  await assertStops(r.step("extract", {}), /step 1 went to evil\.test/)
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("a look-alike host that merely ends in the allowed name is off the list", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assertStops(r.step("click", { selector: "#lookalike" }), /^step 2 went to allowed\.test\.evil\.io,/)
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("an allowed subdomain, port and redirect continue — the same rule as goto", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  // A subdomain, on another port.
  await r.step("click", { selector: "#sub" })
  assert.equal((await r.step("extract", { selector: "#here" })).value, "inside the app")
  // A goto whose chain bounces through an off-list host and ENDS on www.allowed.test.
  const landed = await r.step("goto", { url: "https://allowed.test/sso" })
  assert.equal(landed.url, "https://www.allowed.test/home")
  assert.equal((await r.step("extract", { selector: "#here" })).value, "signed in")
  // Enter submitting to an on-list action.
  await r.step("goto", { url: "https://allowed.test/start" })
  await r.step("type", { selector: "#q2", text: "fine" })
  await r.step("press", { key: "Enter", selector: "#q2" })
  assert.equal((await r.step("extract", { selector: "#result" })).value, "results")
  assert.ok(tab.commits.includes("https://allowed.test/results?q=fine"))
  await closeRun(r.run)
})

test("a page that moves off the list by itself between steps stops the next step", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/drifts" })
  await sleep(80) // the page's own timer fires and it navigates away
  await assertStops(r.step("click", { selector: "#here" }), /^after step 1 the page went to evil\.test,/)
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("a wait is cut short, and fails, when the page leaves during it", async () => {
  browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/drifts" })
  const started = Date.now()
  await assertStops(r.step("wait", { ms: 5000 }), /^step 2 went to evil\.test,/)
  assert.ok(Date.now() - started < 1000, "the wait should end when the page leaves, not run its course")
  await closeRun(r.run)
})

test("the page's host comes from the browser, which a page script cannot forge", async () => {
  // Off the list, dressed up as allowed.test: document.URL, documentURI,
  // document.domain, the title and the path all say allowed.test. The run
  // still stops, and no script of the driver's ever ran in that page.
  let tab = browser()
  let r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  // A link the page rewrote to point at the forger.
  tab.window.document.getElementById("away").href = "https://evil.test/forger"
  await assertStops(r.step("click", { selector: "#away" }), /^step 2 went to evil\.test,/)
  assert.ok(tab.commits.includes("https://evil.test/forger"), tab.commits.join(" "))
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)

  // And a goto straight at it is refused before the tab moves at all.
  tab = browser()
  r = runner()
  await assertStops(r.step("goto", { url: "https://evil.test/forger" }), /^step 1 would go to evil\.test,/)
  assert.equal(tab.commits.length, 0)
  await closeRun(r.run)

  // The other way round: an allowed page claiming to be evil.test is not stopped.
  tab = browser()
  r = runner()
  await r.step("goto", { url: "https://allowed.test/forger" })
  assert.equal((await r.step("extract", { selector: "#here" })).value, "really allowed")
  await closeRun(r.run)
})

test("an injection aimed at a page that has since navigated never lands on the new one", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  // Between the driver's check and its injection, the tab commits an off-list
  // page — and the commit events have not arrived yet, so the watcher cannot
  // have seen it. The injection is pinned to the checked document, so Chrome
  // refuses it; the recheck then reads where the tab really is.
  const realGetFrame = chrome.webNavigation.getFrame
  let calls = 0
  chrome.webNavigation.getFrame = async (q) => {
    const frame = await realGetFrame(q)
    if (++calls === 2) {
      tab.silent = true
      tab.commitNow("https://evil.test/landing")
      tab.silent = false
    }
    return frame
  }
  await assertStops(r.step("extract", { selector: "#here" }), /^step 2 went to evil\.test,/)
  assert.deepEqual(evilInjections(tab), [])
  await closeRun(r.run)
})

test("about:blank is not leaving the list, but nothing is acted on there", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  tab.window.__navigate("about:blank")
  await sleep(40)
  await assert.rejects(r.step("click", { selector: "#here" }), (err) => {
    assert.notEqual(err.code, "left_allowed_sites")
    assert.match(err.message, /the page is blank/)
    return true
  })
  // …and the run can carry on from there.
  await r.step("goto", { url: "https://allowed.test/start" })
  assert.equal((await r.step("extract", { selector: "#here" })).value, "allowed start")
  await closeRun(r.run)
})

test("a data: URL in the run tab is off the list", async () => {
  const tab = browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  tab.window.__navigate("data:text/html,<input id=field>")
  await sleep(40)
  await assertStops(r.step("type", { selector: "#field", text: "x" }), /^after step 1 the page went to a data: URL,/)
  await closeRun(r.run)
})

test("the list the server sends with the step is the one enforced", async () => {
  // Stored at connect: allowed.test. Sent with the step: only other.test — the
  // list was edited since. The newer list wins, for a goto and for a landing.
  browser({ stored: ["allowed.test"] })
  let r = runner(["other.test"])
  await assertStops(r.step("goto", { url: "https://allowed.test/start" }), /^step 1 would go to allowed\.test,/)
  await closeRun(r.run)

  // An older server sends no list: the one from connect applies.
  browser({ stored: ["allowed.test"] })
  const run = `run-${++runSeq}`
  await runStep(run, { command: "goto", args: { url: "https://allowed.test/start" } })
  await assertStops(runStep(run, { command: "click", args: { selector: "#away" } }), /went to evil\.test/)
  await closeRun(run)

  // No list anywhere allows nothing.
  browser({ stored: [] })
  await assertStops(
    runStep(`run-${++runSeq}`, { command: "goto", args: { url: "https://allowed.test/start" } }),
    /would go to allowed\.test/,
  )
})

test("closing a run clears it, so the kill switch's run.end leaves nothing behind", async () => {
  browser()
  const r = runner()
  await r.step("goto", { url: "https://allowed.test/start" })
  await assertStops(r.step("click", { selector: "#away" }), /evil\.test/)
  await closeRun(r.run)
  // The same run id after run.end starts from nothing: no tab, no breach.
  await assert.rejects(r.step("click", { selector: "#here" }), /start with a goto step/)
})
