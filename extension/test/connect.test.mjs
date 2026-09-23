// Connecting by approval (MH-478): the extension's half, against a fake site.
import { test } from "node:test"
import assert from "node:assert/strict"

import { connectLocale, createConnect } from "../connect.js"

const KEYS = { pairing: "moh.pairing", token: "moh.token", agent: "moh.agent" }

function harness(answers) {
  const store = new Map()
  const calls = []
  const opened = []
  let polls = 0
  const deps = {
    settings: async () => ({ siteOrigin: "https://app.example.test/", apiOrigin: "" }),
    get: async (k) => store.get(k),
    set: async (k, v) => void store.set(k, v),
    clear: async (...ks) => ks.forEach((k) => store.delete(k)),
    fetch: async (url, init) => {
      calls.push({ url, body: init?.body ? JSON.parse(init.body) : null })
      if (url.endsWith("/api/agents/connect/start")) {
        return json(201, {
          code: "K7QF-3MTX",
          poll_token: "moh_poll_secret-poll-token",
          browser_key: "the-key",
          verify_path: "/ar/automation/connect/K7QF-3MTX#k=the-key",
          expires_at: "2030-01-01T00:00:00Z",
        })
      }
      if (url.endsWith("/api/agents/connect/poll")) {
        const next = answers[Math.min(polls, answers.length - 1)]
        polls++
        return typeof next === "number" ? json(next, { error: "x" }) : json(200, next)
      }
      throw new Error("unexpected " + url)
    },
    openTab: async (url) => void opened.push(url),
    describe: async () => ({ name: "Windows browser", fingerprint: "fp-1", platform: "Windows x86-64" }),
    sleep: () => new Promise((r) => setImmediate(r)),
    keys: KEYS,
  }
  return { store, calls, opened, deps, polls: () => polls }
}

function json(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body }
}

async function settled(h, until) {
  for (let i = 0; i < 200; i++) {
    if (until()) return
    await new Promise((r) => setImmediate(r))
  }
  throw new Error("never settled")
}

test("start asks the site, opens the approval page with the key in the fragment, and remembers only what the popup needs", async () => {
  const h = harness([{ status: "pending" }])
  const c = createConnect(h.deps)
  const out = await c.start({ languages: ["ar-EG", "en"] })
  assert.deepEqual(out, { code: "K7QF-3MTX", expiresAt: "2030-01-01T00:00:00Z" })
  assert.equal(h.calls[0].url, "https://app.example.test/api/agents/connect/start")
  assert.equal(h.calls[0].body.locale, "ar")
  assert.equal(h.calls[0].body.fingerprint, "fp-1")
  assert.deepEqual(h.opened, ["https://app.example.test/ar/automation/connect/K7QF-3MTX#k=the-key"])
  const p = h.store.get(KEYS.pairing)
  assert.equal(p.status, "pending")
  assert.equal(p.pollToken, "moh_poll_secret-poll-token")
  await c.cancel()
})

test("approval stores the agent, then the token, and forgets the request", async () => {
  const h = harness([
    { status: "pending" },
    { status: "approved", token: "moh_agt_new", agent_id: "a1", name: "Work laptop", workspace_name: "Nile Coffee", workspace_slug: "nile" },
  ])
  const c = createConnect(h.deps)
  await c.start()
  await settled(h, () => h.store.has(KEYS.token))
  assert.equal(h.store.get(KEYS.token), "moh_agt_new")
  assert.deepEqual(h.store.get(KEYS.agent), {
    id: "a1",
    name: "Work laptop",
    workspaceName: "Nile Coffee",
    workspaceSlug: "nile",
    allowedHosts: [],
  })
  assert.equal(h.store.has(KEYS.pairing), false)
  // The poll carried the poll token and nothing else.
  const polled = h.calls.filter((c) => c.url.endsWith("/poll"))
  assert.ok(polled.every((c) => c.body.poll_token === "moh_poll_secret-poll-token" && Object.keys(c.body).length === 1))
})

test("a refusal, an expiry and an unknown request each end the wait and say so", async () => {
  for (const [answer, status] of [
    [{ status: "denied" }, "denied"],
    [{ status: "expired" }, "expired"],
    [404, "expired"],
    [{ status: "claimed" }, "claimed"],
  ]) {
    const h = harness([answer])
    const c = createConnect(h.deps)
    await c.start()
    await settled(h, () => h.store.get(KEYS.pairing)?.status !== "pending")
    assert.equal(h.store.get(KEYS.pairing).status, status)
    assert.equal(h.store.has(KEYS.token), false)
  }
})

test("a second wait does not start a second loop, and cancel stops the one there is", async () => {
  const h = harness([{ status: "pending" }])
  const c = createConnect(h.deps)
  await c.start()
  void c.wait()
  void c.resume()
  await settled(h, () => h.polls() > 5)
  await c.cancel()
  const after = h.polls()
  await new Promise((r) => setTimeout(r, 20))
  assert.ok(h.polls() <= after + 1, "polling carried on after cancel")
  assert.equal(h.store.has(KEYS.pairing), false)
})

test("a site that refuses to start says why, rather than an HTML page", async () => {
  const h = harness([])
  h.deps.fetch = async () => ({ ok: false, status: 429, json: async () => ({}) })
  await assert.rejects(createConnect(h.deps).start(), /too many connect requests/)
  h.deps.fetch = async () => ({ ok: false, status: 500, json: async () => ({ detail: "database is down" }) })
  await assert.rejects(createConnect(h.deps).start(), /database is down/)
})

test("the approval page speaks the browser's language when the site does", () => {
  assert.equal(connectLocale(["ar-EG"]), "ar")
  assert.equal(connectLocale(["fr-FR", "en-GB"]), "en")
  assert.equal(connectLocale(["de"]), "en")
  assert.equal(connectLocale([]), "en")
})
