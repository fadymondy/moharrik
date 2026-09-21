// Automation smoke check (MH-61), end to end through a REAL socket.
//
// The peer here is a stand-in for the Chrome extension: it starts a pairing,
// waits for an admin to approve it in the browser, collects its token, opens
// /api/agents/ws, says hello, and answers run.step frames. That is the entire
// contract the extension implements, so exercising it proves the server half
// without Chrome — and it is the only way to see a run actually finish.
//
// NOTE: any page holding an open stream never reaches Playwright's
// "networkidle"; these navigations use domcontentloaded and wait on selectors.
import { chromium } from "playwright-core"
import { WebSocket } from "ws"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
// The socket goes STRAIGHT to the Go API, not through the web app: /api/* is a
// Next rewrite, and a Next rewrite cannot carry a protocol upgrade (it answers
// 500). In production the proxy routes this one path to the API for the same
// reason — see deploy/lxc/docker-compose.yml.
const apiBase = process.argv[3] ?? "http://localhost:8122"
const wsBase = apiBase.replace(/^http/, "ws")

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "au-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `au${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
  // networkidle here: the register page holds no stream, and the submit button
  // stays disabled until React has hydrated the form state.
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", email)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", wsName)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  return { email, slug: page.url().split("/w/")[1].split("/")[0] }
}

/** The fake extension: connects, says hello, answers steps. */
function fakeBrowser(token, { onStep } = {}) {
  return new Promise((resolve, reject) => {
    const sock = new WebSocket(`${wsBase}/api/agents/ws`)
    const timer = setTimeout(() => reject(new Error("socket never said hello.ok")), 15000)
    sock.on("open", () => {
      sock.send(JSON.stringify({ id: "h1", type: "hello", payload: { token, platform: "e2e", extension_version: "0.0.1-test" } }))
    })
    sock.on("message", (raw) => {
      const env = JSON.parse(String(raw))
      // payload is raw JSON on the wire, so it arrives already parsed here.
      if (env.type === "hello.ok") {
        clearTimeout(timer)
        resolve({ sock, ack: env.payload ?? {} })
        return
      }
      if (env.type === "run.step") {
        const step = env.payload ?? {}
        const answer = onStep ? onStep(step) : { summary: `did ${step.command}` }
        // An answer with an `error` is how a browser refuses a step.
        sock.send(JSON.stringify({ id: env.id, type: "run.step.result", ...answer }))
      }
    })
    sock.on("error", (e) => {
      clearTimeout(timer)
      reject(e)
    })
    sock.on("close", (code, reason) => {
      clearTimeout(timer)
      reject(new Error(`closed ${code} ${reason}`))
    })
  })
}

const out = {}
const ctx = await newCtx()
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "AU " + Date.now())
out.slug = slug

// ---- the page loads and says there is nothing yet ----
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="approve"]', { timeout: 20000 })
out.navHasAutomation = await page.evaluate(() => [...document.querySelectorAll("nav a")].some((a) => /\/automation$/.test(new URL(a.href).pathname)))
out.startsEmpty = await page.evaluate(() => document.body.innerText.includes("No browser paired yet"))

// ---- a wrong code is refused ----
await page.fill("#pair-code", "000000")
await page.click('form[data-form="approve"] button[type=submit]')
await page.waitForTimeout(1500)
out.wrongCodeRefused = await page.evaluate(() => /not valid/i.test(document.body.innerText))

// ---- the extension asks to pair, an admin approves it ----
const started = await fetch(`${base}/api/agents/pair/${slug}`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ name: "E2E browser", platform: "e2e", extension_version: "0.0.1-test" }),
})
out.pairStartStatus = started.status
const { code, poll_token: pollToken } = await started.json()
out.gotCode = /^\d{6}$/.test(code ?? "")

// Before approval there is nothing to collect.
const early = await fetch(`${base}/api/agents/pair/claim`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ poll_token: pollToken }),
}).then((r) => r.json())
out.claimBeforeApproval = early.status // expect "pending", and no token
out.claimBeforeApprovalHadToken = !!early.token

// A pairing waiting for approval is visible on the page.
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-pairing]", { timeout: 15000 })
out.pairingShown = await page.evaluate(() => document.body.innerText.includes("E2E browser"))

await page.fill("#pair-code", code)
await page.fill("#pair-name", "Work laptop")
await page.fill("#pair-hosts", "example.com")
await page.click('form[data-form="approve"] button[type=submit]')
await page.waitForSelector("[data-browser]", { timeout: 15000 })
out.browserRowName = await page.evaluate(() => document.querySelector("[data-browser]")?.getAttribute("data-browser"))
out.browserStartsOffline = await page.evaluate(() => document.querySelector("[data-browser]")?.getAttribute("data-browser-status"))

// ---- the token is handed over exactly once ----
const claim = await fetch(`${base}/api/agents/pair/claim`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ poll_token: pollToken }),
}).then((r) => r.json())
out.claimStatus = claim.status
const token = claim.token
out.gotToken = !!token && token.startsWith("moh_agt_")

const second = await fetch(`${base}/api/agents/pair/claim`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ poll_token: pollToken }),
}).then((r) => r.json())
out.secondClaimHadToken = !!second.token // must be false: once means once

// ---- an unknown token cannot open the socket ----
out.unknownTokenRejected = await fakeBrowser("moh_agt_definitely-not-a-real-token")
  .then(() => false)
  .catch(() => true)

// ---- the real browser connects ----
const { sock, ack } = await fakeBrowser(token, {
  onStep: (step) => {
    if (step.command === "extract") return { payload: { title: "Example Domain", count: 3 } }
    if (step.command === "explode") return { error: "the page was not what I expected" }
    return { payload: { summary: `did ${step.command}` } }
  },
})
out.helloAckHasHosts = Array.isArray(ack.allowed_hosts) && ack.allowed_hosts.includes("example.com")

await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForFunction(() => document.querySelector("[data-browser]")?.getAttribute("data-browser-status") === "online", { timeout: 15000 })
  .then(() => (out.showsOnline = true))
  .catch(() => (out.showsOnline = false))

// ---- a run outside the allow-list is refused BEFORE anything is queued ----
await page.fill("#run-steps", '[{"command":"goto","args":{"url":"https://evil.test/"}}]')
await page.click('form[data-form="run"] button[type=submit]')
await page.waitForTimeout(1500)
out.offAllowlistRefused = await page.evaluate(() => /allow-list/i.test(document.body.innerText))
out.runsAfterRefusal = await page.evaluate(() => document.querySelectorAll("[data-run]").length)

// ---- a run within it goes through, and the steps come back ----
await page.fill("#run-steps", '[{"command":"goto","args":{"url":"https://example.com/"}},{"command":"extract","args":{"selector":"h1"}}]')
await page.click('form[data-form="run"] button[type=submit]')
await page.waitForSelector('[data-run-status="ok"]', { timeout: 20000 })
  .then(() => (out.runFinishedOk = true))
  .catch(() => (out.runFinishedOk = false))
out.runSummary = await page.evaluate(() => document.querySelector("[data-run]")?.innerText.match(/(\d+ of \d+ steps ok)/)?.[1])

// the detail page shows what the browser actually sent
await page.click("[data-run] a")
await page.waitForSelector("[data-step]", { timeout: 15000 })
out.stepCount = await page.evaluate(() => document.querySelectorAll("[data-step]").length)
out.resultVerbatim = await page.evaluate(() => document.body.innerText.includes('"title":"Example Domain"'))

// ---- a failing step stops the run and the rest are skipped ----
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="run"]', { timeout: 15000 })
await page.fill("#run-steps", '[{"command":"explode"},{"command":"goto","args":{"url":"https://example.com/"}}]')
await page.click('form[data-form="run"] button[type=submit]')
await page.waitForFunction(() => document.querySelector('[data-run][data-run-status="failed"]') !== null, { timeout: 20000 })
  .then(() => (out.failingRunMarkedFailed = true))
  .catch(() => (out.failingRunMarkedFailed = false))
await page.click('[data-run][data-run-status="failed"] a')
await page.waitForSelector("[data-step]", { timeout: 15000 })
out.skippedAfterFailure = await page.evaluate(() => document.querySelector('[data-step="1"]')?.getAttribute("data-step-status"))

// ---- the kill switch: stops runs, drops sockets, refuses new ones ----
const closed = new Promise((resolve) => sock.on("close", (code) => resolve(code)))
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="run"]', { timeout: 15000 })
await page.click('button:has-text("Stop everything")')
await page.waitForSelector('[role="alertdialog"] button:has-text("Stop everything")', { timeout: 10000 })
await page.click('[role="alertdialog"] button:has-text("Stop everything")')
await page.waitForSelector('[data-paused="true"]', { timeout: 15000 })
out.pausedBannerShown = true
out.socketDroppedCode = await Promise.race([closed, new Promise((r) => setTimeout(() => r(0), 8000))])

// a paused workspace refuses a new run even through the API
const refused = await page.evaluate(async (ws) => {
  const csrf = await (await fetch("/api/auth/csrf")).json()
  const r = await fetch("/api/w/runs", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf.csrf_token, "X-Workspace-ID": ws },
    body: JSON.stringify({ agent_id: "whatever", steps: [{ command: "goto", args: { url: "https://example.com" } }] }),
  })
  return { status: r.status, body: (await r.text()).slice(0, 200) }
}, slug)
out.pausedRunStatus = refused.status
out.pausedRunSaysWhy = /paused|stopped/i.test(refused.body)

// and a browser cannot even connect while it is paused
out.connectWhilePausedRejected = await fakeBrowser(token)
  .then(() => false)
  .catch(() => true)

await ctx.close()

// ---- Arabic at 390px ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "AU AR " + Date.now())
await pageAr.goto(`${base}/ar/w/${slugAr}/automation`, { waitUntil: "domcontentloaded" })
await pageAr.waitForTimeout(2000)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_rawKeys = await pageAr.evaluate(() => /automation\.[a-zA-Z]|nav\.[a-zA-Z]/.test(document.body.innerText))
await ctxAr.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
