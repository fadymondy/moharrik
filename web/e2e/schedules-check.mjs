// Automation schedules (MH-232), end to end on the real clock.
//
// The interesting behaviour is what happens when the browser ISN'T there, so
// this drives both: a schedule due within the minute with a stand-in browser
// connected, and the same schedule with the browser gone.
//
// The scheduler ticks every 30s, so this check takes a couple of minutes. It
// waits on the page rather than sleeping a fixed time.
import { chromium } from "playwright-core"
import { WebSocket } from "ws"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const apiBase = process.argv[3] ?? "http://localhost:8122"
const wsBase = apiBase.replace(/^http/, "ws")

async function newCtx() {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "sc-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `sc${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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

function fakeBrowser(token, onStep) {
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
        const step = env.payload ?? {}
        sock.send(JSON.stringify({ id: env.id, type: "run.step.result", payload: onStep ? onStep(step) : { summary: "ok" } }))
      }
    })
    sock.on("error", (e) => {
      clearTimeout(timer)
      reject(e)
    })
  })
}

/** A cron expression for the minute after next, in UTC, so it fires soon but not mid-setup. */
function soonCron() {
  const at = new Date(Date.now() + 75_000)
  return `${at.getUTCMinutes()} ${at.getUTCHours()} * * *`
}

const out = {}
const ctx = await newCtx()
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "SC " + Date.now())
out.slug = slug

// ---- pair a stand-in browser ----
const started = await fetch(`${base}/api/agents/pair/${slug}`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ name: "Sched browser", platform: "e2e" }),
}).then((r) => r.json())

await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="approve"]', { timeout: 20000 })
await page.fill("#pair-code", started.code)
await page.fill("#pair-name", "Sched browser")
await page.fill("#pair-hosts", "example.com")
await page.click('form[data-form="approve"] button[type=submit]')
await page.waitForSelector("[data-browser]", { timeout: 15000 })
const claim = await fetch(`${base}/api/agents/pair/claim`, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ poll_token: started.poll_token }),
}).then((r) => r.json())
let sock = await fakeBrowser(claim.token)
out.browserConnected = true

// ---- a schedule off the allow-list is refused when it is SAVED ----
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="schedule"]', { timeout: 20000 })
out.startsEmpty = await page.evaluate(() => document.body.innerText.includes("Nothing scheduled"))
await page.fill("#sc-name", "Off the allow-list")
await page.fill("#sc-cron", "0 7 * * *")
await page.fill("#sc-steps", '[{"command":"goto","args":{"url":"https://evil.test/"}}]')
await page.click('form[data-form="schedule"] button[type=submit]')
await page.waitForTimeout(1500)
out.offAllowlistRefusedAtSaveTime = await page.evaluate(() => /allow-list/i.test(document.body.innerText))
out.schedulesAfterRefusal = await page.evaluate(() => document.querySelectorAll("[data-schedule]").length)

// ---- a bad cron expression is refused too ----
await page.fill("#sc-name", "Bad cron")
await page.fill("#sc-cron", "every morning")
await page.fill("#sc-steps", '[{"command":"goto","args":{"url":"https://example.com/"}}]')
await page.click('form[data-form="schedule"] button[type=submit]')
await page.waitForTimeout(1500)
out.badCronRefused = await page.evaluate(() => /not a cron expression/i.test(document.body.innerText))

// ---- a real schedule, due in about a minute ----
const cron = soonCron()
await page.fill("#sc-name", "Morning check")
await page.fill("#sc-cron", cron)
await page.fill("#sc-zone", "UTC")
await page.fill("#sc-steps", '[{"command":"goto","args":{"url":"https://example.com/"}},{"command":"extract","args":{"selector":"h1"}}]')
await page.click('form[data-form="schedule"] button[type=submit]')
await page.waitForSelector('[data-schedule="Morning check"]', { timeout: 15000 })
out.saved = true
out.showsNextRun = await page.evaluate(() => /next /i.test(document.querySelector('[data-schedule="Morning check"]')?.innerText ?? ""))

// ---- the clock fires it, and the run finishes ----
const fired = await page
  .waitForFunction(
    () => {
      const el = document.querySelector('[data-schedule="Morning check"]')
      return el && /Finished|Running/i.test(el.innerText)
    },
    null,
    { timeout: 150000, polling: 3000 },
  )
  .then(() => true)
  .catch(() => false)
out.firedOnItsOwn = fired

await page.waitForFunction(() => document.querySelector('[data-run-status="ok"]') !== null, null, { timeout: 60000, polling: 2000 })
  .then(() => (out.runFinished = true))
  .catch(() => (out.runFinished = false))
out.runSaysScheduled = await page.evaluate(() => /Started here|schedule/i.test(document.querySelector("[data-run]")?.innerText ?? ""))

// ---- "run now" does not wait for the clock ----
await page.click('[data-schedule="Morning check"] button:has-text("Run now")')
await page.waitForTimeout(3000)
out.runCountAfterRunNow = await page.evaluate(() => document.querySelectorAll("[data-run]").length)

// ---- with the browser gone, the miss is RECORDED with a reason ----
sock.close()
await page.waitForTimeout(1000)
const cron2 = soonCron()
await page.fill("#sc-name", "Offline check")
await page.fill("#sc-cron", cron2)
await page.fill("#sc-zone", "UTC")
await page.fill("#sc-steps", '[{"command":"goto","args":{"url":"https://example.com/"}}]')
await page.click('form[data-form="schedule"] button[type=submit]')
await page.waitForSelector('[data-schedule="Offline check"]', { timeout: 15000 })

const missed = await page
  .waitForFunction(
    () => {
      const el = document.querySelector('[data-schedule="Offline check"]')
      return el && /Missed/i.test(el.innerText)
    },
    null,
    { timeout: 150000, polling: 3000 },
  )
  .then(() => true)
  .catch(() => false)
out.missRecorded = missed
out.missSaysWhy = await page.evaluate(() => /not connected/i.test(document.querySelector('[data-schedule="Offline check"]')?.innerText ?? ""))

// ---- pausing a schedule stops it ----
await page.click('[data-schedule="Offline check"] [role="switch"]')
await page.waitForTimeout(2000)
out.paused = await page.evaluate(() => /Paused/i.test(document.querySelector('[data-schedule="Offline check"]')?.innerText ?? ""))

await ctx.close()

// ---- Arabic at 390px ----
const ctxAr = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "sc-ar-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 390, height: 844 },
  colorScheme: "dark",
})
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "SC AR " + Date.now())
await pageAr.goto(`${base}/ar/w/${slugAr}/automation`, { waitUntil: "domcontentloaded" })
await pageAr.waitForTimeout(2000)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_rawKeys = await pageAr.evaluate(() => /automation\.[a-zA-Z]/.test(document.body.innerText))
await ctxAr.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
