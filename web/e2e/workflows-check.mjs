// Saved workflows (MH-233), end to end with a stand-in browser.
//
// The assertion that matters most is the LAST one: a value marked secret must
// reach the browser and must NOT reach the run record, which a person reads and
// a model is handed.
import { chromium } from "playwright-core"
import { WebSocket } from "ws"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const apiBase = process.argv[3] ?? "http://localhost:8122"
const wsBase = apiBase.replace(/^http/, "ws")

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "wf-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `wf${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
  return { slug: page.url().split("/w/")[1].split("/")[0] }
}

const seen = []
function fakeBrowser(token) {
  return new Promise((resolve, reject) => {
    const sock = new WebSocket(`${wsBase}/api/agents/ws`)
    const timer = setTimeout(() => reject(new Error("no hello.ok")), 15000)
    sock.on("open", () => sock.send(JSON.stringify({ id: "h1", type: "hello", payload: { token, platform: "e2e" } })))
    sock.on("message", (raw) => {
      const env = JSON.parse(String(raw))
      if (env.type === "hello.ok") {
        clearTimeout(timer)
        resolve(sock)
        return
      }
      if (env.type === "run.step") {
        // What the BROWSER actually received, which is where the real secret
        // has to arrive.
        seen.push(env.payload ?? {})
        sock.send(JSON.stringify({ id: env.id, type: "run.step.result", payload: { summary: "ok" } }))
      }
    })
    sock.on("error", (e) => {
      clearTimeout(timer)
      reject(e)
    })
  })
}

const out = {}
const ctx = await newCtx()
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "WF " + Date.now())
out.slug = slug

// ---- pair a stand-in browser ----
const started = await fetch(`${base}/api/agents/pair/${slug}`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ name: "WF browser", platform: "e2e" }),
}).then((r) => r.json())
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="approve"]', { timeout: 20000 })
await page.fill("#pair-code", started.code)
await page.fill("#pair-name", "WF browser")
await page.fill("#pair-hosts", "example.com")
await page.click('form[data-form="approve"] button[type=submit]')
await page.waitForSelector("[data-browser]", { timeout: 15000 })
const claim = await fetch(`${base}/api/agents/pair/claim`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ poll_token: started.poll_token }),
}).then((r) => r.json())
const agentSock = await fakeBrowser(claim.token)

// ---- save a workflow with a placeholder and a secret ----
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="workflow"]', { timeout: 20000 })
out.startsEmpty = await page.evaluate(() => document.body.innerText.includes("Nothing saved yet"))

await page.fill("#wf-name", "Sign in and check")
await page.fill("#wf-desc", "Signs in and reads the dashboard")
await page.fill(
  "#wf-steps",
  '[{"command":"goto","args":{"url":"https://example.com/{{page}}"}},{"command":"type","args":{"selector":"#pass","text":"{{password}}"}}]',
)
await page.fill("#wf-secrets", "password")
await page.click('form[data-form="workflow"] button[type=submit]')
await page.waitForSelector('[data-workflow="Sign in and check"]', { timeout: 15000 })
out.saved = true
out.showsWhatItAsksFor = await page.evaluate(() => {
  const el = document.querySelector('[data-workflow="Sign in and check"]')
  return !!el && /page/.test(el.innerText) && /password/.test(el.innerText)
})

// ---- run it, supplying values ----
await page.click('[data-workflow="Sign in and check"] button:has-text("Use it")')
await page.waitForSelector('[data-form="run-workflow"]', { timeout: 10000 })
const secretFieldIsHidden = await page.evaluate(() => {
  const inputs = [...document.querySelectorAll('[data-form="run-workflow"] input')]
  const pass = inputs.find((i) => (i.id ?? "").endsWith("-password"))
  return pass?.type === "password"
})
out.secretFieldIsHidden = secretFieldIsHidden

await page.fill('[data-form="run-workflow"] input[id$="-page"]', "reports")
await page.fill('[data-form="run-workflow"] input[id$="-password"]', "hunter2-secret")
await page.click('[data-form="run-workflow"] button[type=submit]')
await page.waitForSelector('[data-run-status="ok"]', { timeout: 25000 })
  .then(() => (out.runFinished = true))
  .catch(() => (out.runFinished = false))

// ---- the browser got the REAL values ----
const urls = seen.map((s) => JSON.stringify(s))
out.browserGotTheFilledUrl = urls.some((u) => u.includes("https://example.com/reports"))
out.browserGotTheRealSecret = urls.some((u) => u.includes("hunter2-secret"))

// ---- the run record does NOT carry the secret ----
await page.click("[data-run] a")
await page.waitForSelector("[data-step]", { timeout: 15000 })
const runText = await page.evaluate(() => document.body.innerText)
out.runPageShowsTheFilledUrl = runText.includes("https://example.com/reports")
out.runPageHidesTheSecret = !runText.includes("hunter2-secret")
out.runPageShowsRedaction = /••••••/.test(await page.evaluate(() => document.body.innerText))

// ---- a run with a missing required value is refused ----
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-workflow="Sign in and check"]', { timeout: 15000 })
const runsBefore = await page.evaluate(() => document.querySelectorAll("[data-run]").length)
await page.click('[data-workflow="Sign in and check"] button:has-text("Use it")')
await page.waitForSelector('[data-form="run-workflow"]', { timeout: 10000 })
await page.fill('[data-form="run-workflow"] input[id$="-page"]', "")
await page.fill('[data-form="run-workflow"] input[id$="-password"]', "")
await page.click('[data-form="run-workflow"] button[type=submit]')
await page.waitForTimeout(2000)
out.missingValueRefused = await page.evaluate(() => /needs a value/i.test(document.body.innerText))
out.noRunCreatedOnRefusal = (await page.evaluate(() => document.querySelectorAll("[data-run]").length)) === runsBefore

await ctx.close()

// ---- Arabic at 390px ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "WF AR " + Date.now())
await pageAr.goto(`${base}/ar/w/${slugAr}/automation`, { waitUntil: "domcontentloaded" })
await pageAr.waitForTimeout(2000)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_rawKeys = await pageAr.evaluate(() => /automation\.[a-zA-Z]/.test(document.body.innerText))
await ctxAr.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))

// The stand-in browser's socket keeps Node alive: without this the check
// prints its result and then hangs until something kills it.
agentSock.close()
