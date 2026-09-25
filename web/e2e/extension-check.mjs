// The Chrome extension (MH-245), loaded FOR REAL.
//
// Everything before this verified the server against a stand-in that spoke the
// socket protocol. That proves the contract and nothing about the extension.
// This loads extension/ unpacked into a real Chrome, pairs it through its own
// popup, and drives a run against a local page — so what is exercised is the
// extension's own pairing, its socket, and its driver.
//
// It runs HEADED, and against PLAYWRIGHT'S CHROMIUM rather than the installed
// Chrome: Chrome 137+ dropped --load-extension, and 153 ignores it even with
// the feature flag — the browser starts perfectly and simply has no extension,
// which looks exactly like an extension that failed to start. Playwright's
// bundled build still loads one, and it is the same engine.
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const apiBase = process.argv[3] ?? "http://localhost:8122"
// A COPY of the extension whose manifest declares the dev hosts. The shipped
// manifest declares only app.moharrik.com and asks for anything else with a
// click — which is right for a person and impossible for a check, because that
// click is a native Chrome dialog. Everything else here is the real extension.
const SRC = path.resolve("E:/Sites/moharrik/extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-src-"))
cpSync(SRC, EXT, { recursive: true })
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = ["http://localhost/*", "http://127.0.0.1/*", "https://app.moharrik.com/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
}

// The page the run will be driven against. localhost is what the allow-list
// will be set to, so nothing here touches the internet.
const PAGE_PORT = 8791
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><html><body>
    <h1 id="title">Orders for today</h1>
    <p class="row">first row</p><p class="row">second row</p>
    <input id="q" /><button id="go" onclick="document.getElementById('title').textContent='Filtered orders'">Go</button>
  </body></html>`)
})
await new Promise((r) => site.listen(PAGE_PORT, "127.0.0.1", r))

const out = {}
process.on("exit", () => console.log(JSON.stringify(out, null, 1)))
const userDir = mkdtempSync(path.join(tmpdir(), "ext-"))
const ctx = await chromium.launchPersistentContext(userDir, {
  executablePath:
    process.env.EXT_CHROME ??
    "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe",
  headless: false,
  viewport: { width: 1280, height: 860 },
  args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
})

// The service worker is the extension: waiting for it is how we know Chrome
// actually loaded it rather than starting a plain browser.
let worker = ctx.serviceWorkers()[0]
if (!worker) worker = await ctx.waitForEvent("serviceworker", { timeout: 30000 })
const extensionID = new URL(worker.url()).host
const workerErrors = []
worker.on("console", (m) => {
  if (m.type() === "error") workerErrors.push(m.text().slice(0, 200))
})
out.extensionLoaded = !!extensionID

const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- a workspace, from the app ----
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", `ex${Date.now()}@x.test`)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "EXT " + Date.now())
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]
out.slug = slug

// ---- pair THROUGH THE POPUP ----
const popup = await ctx.newPage()
await popup.goto(`chrome-extension://${extensionID}/popup.html`)
await popup.fill("#site", base)
await popup.fill("#api", apiBase)
await popup.fill("#workspace", slug)
await popup.click("#start")
await popup.waitForSelector("#codebox:not([hidden])", { timeout: 20000 })
const code = (await popup.textContent("#code")).replace(/\s/g, "")
out.popupShowedACode = /^\d{6}$/.test(code)

// ---- approve it in the app, with localhost allowed ----
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="approve"]', { timeout: 20000 })
await page.fill("#pair-code", code)
await page.fill("#pair-name", "This Chrome")
await page.fill("#pair-hosts", "127.0.0.1")
await page.click('form[data-form="approve"] button[type=submit]')
await page.waitForSelector("[data-browser]", { timeout: 20000 })

// What the extension itself believes, so a failure here says WHERE it stopped
// rather than only "the browser never came online".
const extState = async () =>
  worker.evaluate(async () => {
    const all = await chrome.storage.local.get(["moh.token", "moh.status", "moh.agent"])
    return { hasToken: !!all["moh.token"], status: all["moh.status"], agent: all["moh.agent"] }
  })

// The extension polls for approval, collects its token and connects on its own.
const online = await page
  .waitForFunction(
    () =>
      document.querySelector("[data-browser]")?.getAttribute("data-browser-status") === "online",
    null,
    {
      timeout: 60000,
      polling: 2000,
    },
  )
  .then(() => true)
  .catch(() => false)
out.extensionConnectedByItself = online
out.extensionState = await extState()

// ---- drive a run at the local page ----
await page.fill(
  "#run-steps",
  JSON.stringify([
    { command: "goto", args: { url: `http://127.0.0.1:${PAGE_PORT}/` } },
    { command: "extract", args: { selector: "#title" } },
    { command: "type", args: { selector: "#q", text: "today" } },
    { command: "click", args: { selector: "#go" } },
    { command: "extract", args: { selector: "#title" } },
    { command: "extract", args: { selector: ".row", all: true } },
  ]),
)
await page.click('form[data-form="run"] button[type=submit]')
await page
  .waitForFunction(() => document.querySelector('[data-run-status="ok"]') !== null, null, {
    timeout: 90000,
    polling: 2000,
  })
  .then(() => (out.runFinished = true))
  .catch(() => (out.runFinished = false))

await page.click("[data-run] a")
await page.waitForSelector("[data-step]", { timeout: 20000 })
const runText = await page.evaluate(() => document.body.innerText)
out.extractedTheTitle = runText.includes("Orders for today")
out.clickChangedThePage = runText.includes("Filtered orders")
out.extractedAllRows = runText.includes("first row") && runText.includes("second row")
// A "type" step's RESULT must not echo what was typed: the result is what the
// browser chooses to send back, and echoing there would put a password in the
// record for every run. The step's ARGS are a different matter — a raw run
// stores what it was asked to do, verbatim and on purpose; a value that must
// not be stored belongs in a workflow's secret parameter (MH-233), which is
// redacted before the args are written.
out.typeResultSaysOnlyWhatItDid = runText.includes("typed into #q")

// ---- a step that cannot work says why, in the browser's own words ----
await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="run"]', { timeout: 20000 })
await page.fill(
  "#run-steps",
  JSON.stringify([
    { command: "goto", args: { url: `http://127.0.0.1:${PAGE_PORT}/` } },
    { command: "click", args: { selector: "#not-there" } },
  ]),
)
await page.click('form[data-form="run"] button[type=submit]')
await page
  .waitForFunction(
    () => document.querySelector('[data-run][data-run-status="failed"]') !== null,
    null,
    { timeout: 90000, polling: 2000 },
  )
  .then(() => (out.failingStepFailedTheRun = true))
  .catch(() => (out.failingStepFailedTheRun = false))
await page.click('[data-run][data-run-status="failed"] a')
await page.waitForSelector("[data-step]", { timeout: 20000 })
out.saysWhatWasWrong = await page.evaluate(() =>
  /nothing on the page matches/i.test(document.body.innerText),
)

// ---- the popup reflects the connection ----
await popup.reload()
await popup.waitForTimeout(1000)
out.popupState = await popup.evaluate(() => document.getElementById("state")?.textContent ?? "")
out.popupSaysConnected = out.popupState === "Connected"
out.extensionErrors = workerErrors
out.popupShowsTheAllowList = await popup.evaluate(() =>
  /127\.0\.0\.1/.test(document.getElementById("hosts")?.textContent ?? ""),
)

await ctx.close()
site.close()
