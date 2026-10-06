// MH-558: before MH-480's canvas editor replaced the old /automation page, a
// publishing workflow (workflow.writes=true) opened a confirm dialog before
// it ran. The canvas run control and the schedules "run now" button had no
// equivalent. This check drives the REAL extension in a REAL Chromium and
// asserts:
//
//   - the workflows list still flags a publishing workflow with a Publishes
//     badge, and has no run button of its own to gate (the row only opens
//     the canvas and deletes; MH-526 already established this)
//   - the canvas run control ("Test workflow") on a writes workflow opens
//     "Publish with {name}?" before it runs, Cancel starts nothing, and the
//     confirm actually starts a run
//   - "run to here" on a step of a writes workflow asks the same question
//   - the canvas run control on a non-writing workflow runs with no dialog
//
//   node e2e/workflow-write-confirm-check.mjs [webBase] [apiBase]
import { chromium } from "playwright-core"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { connectExtension } from "./connect-helper.mjs"

const base = (process.argv[2] ?? "http://localhost:3141").replace(/\/$/, "")
const apiBase = (process.argv[3] ?? "http://localhost:8141").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/moharrik-wt-ao-write-confirm/.setup/shots"
// System Chrome on this machine has extension-loading policy-disabled — its
// service worker never registers and the whole run times out — so this
// follows presets-check.mjs's own default of the Playwright-bundled
// Chromium, overridable with EXT_CHROME on a machine where real Chrome allows
// unpacked extensions.
const CHROME =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-wfconfirm-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) =>
    !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?|[\\/]store[\\/]?/.test(p.slice(SRC.length)),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [`${base}/*`, `${apiBase}/*`, "http://*/*", "https://*/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
}

const out = {}
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-wfconfirm-profile-")),
  {
    executablePath: CHROME,
    // Chrome only registers an extension's service worker in a context that is
    // NOT headless (the same reason presets-check.mjs and the other extension
    // checks in this directory launch headless:false); a headless context here
    // times out waiting for it.
    headless: false,
    viewport: { width: 1280, height: 900 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
  },
)

try {
  const worker =
    ctx.serviceWorkers()[0] || (await ctx.waitForEvent("serviceworker", { timeout: 30000 }))
  const extensionID = new URL(worker.url()).host

  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "load" })
  await page.fill("#email", `wfconfirm-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `WF Confirm ${Date.now().toString(36)}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  const call = async (method, p, data, wsID) => {
    const csrf = (await (await page.request.get(`${base}/api/auth/csrf`)).json()).csrf_token
    const res = await page.request.fetch(`${base}${p}`, {
      method,
      headers: { "X-CSRF-Token": csrf, ...(wsID ? { "X-Workspace-ID": wsID } : {}) },
      data,
    })
    const text = await res.text()
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${text}`)
    return text ? JSON.parse(text) : null
  }
  const wsID = (await call("GET", "/api/me/workspaces")).workspaces.find((w) => w.slug === slug).id
  const { browser, online } = await connectExtension({
    ctx,
    extensionID,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "WF Confirm Chromium",
    hosts: [],
  })
  out.connected = online

  // ---- add a publishing preset and a non-writing one, both paired to the browser ----
  await call("POST", "/api/w/automation/presets/linkedin-post/add", { agent_id: browser.id }, wsID)
  await call(
    "POST",
    "/api/w/automation/presets/google-news-search/add",
    { agent_id: browser.id },
    wsID,
  )
  const wfs = (await call("GET", "/api/w/workflows", undefined, wsID)).workflows
  const writes = wfs.find((w) => w.preset === "linkedin-post")
  const plain = wfs.find((w) => w.preset === "google-news-search")
  out.presetsAdded = !!writes && !!plain
  out.writesFlagged = writes?.writes === true
  out.plainNotFlagged = plain?.writes === false || plain?.writes === undefined

  // ---- the workflows list: Publishes badge, no run button to gate ----
  await page.goto(`${base}/en/w/${slug}/automation/workflows`, { waitUntil: "load" })
  await page.waitForSelector(`li[data-workflow-row="${writes.name}"]`, { timeout: 20000 })
  out.listFlagsPublishing =
    (await page.$(`li[data-workflow-row="${writes.name}"] [data-workflow-writes]`)) !== null
  out.listHasNoRunButton =
    (await page.$(`li[data-workflow-row="${writes.name}"] [data-run-workflow]`)) === null

  const canvasUrl = (id, locale = "en") =>
    `${base}/${locale}/w/${slug}/automation/workflows/${id}?view=editor`
  const runsFor = async (id) =>
    (await call("GET", "/api/w/runs", undefined, wsID)).runs.filter((r) => r.workflow_id === id)

  // ---- canvas run control on a WRITES workflow: dialog first, Cancel starts nothing ----
  await page.goto(canvasUrl(writes.id), { waitUntil: "load" })
  await page.waitForSelector("[data-test-run]:not([disabled])", { timeout: 30000 })
  // linkedin-post's one param ("text") is required: fill it via the trigger
  // panel's test-values field so the confirmed run actually starts rather
  // than stopping on canvas.needValues.
  await page.click("[data-canvas-node='trigger']")
  await page.waitForSelector("#val-text", { timeout: 10000 })
  await page.fill("#val-text", "hello from the write-confirm check")
  const before = await runsFor(writes.id)
  await page.click("[data-test-run]")
  await page.waitForSelector("[data-confirm-run-dialog]", { timeout: 10000 })
  out.canvasDialogTitle = await page.textContent(
    "[data-confirm-run-dialog] [data-slot='alert-dialog-title'], [data-confirm-run-dialog] h2",
  )
  await page.click("[data-confirm-run-dialog] button:has-text('Cancel')")
  await page.waitForSelector("[data-confirm-run-dialog]", { state: "hidden", timeout: 10000 })
  await sleep(1000)
  out.cancelStartedNothing = (await runsFor(writes.id)).length === before.length

  // ---- "run to here" on a step asks the same question ----
  // The step toolbar (with "run to here") shows on a hovered or selected step.
  await page.locator("[data-canvas-node]:not([data-canvas-node='trigger'])").first().click()
  await page.locator("[data-run-to]:not([disabled])").first().click()
  await page.waitForSelector("[data-confirm-run-dialog]", { timeout: 10000 })
  out.runToAsksFirst = true
  await page.click("[data-confirm-run-dialog] button:has-text('Cancel')")
  await page.waitForSelector("[data-confirm-run-dialog]", { state: "hidden", timeout: 10000 })
  await sleep(1000)
  out.runToCancelStartedNothing = (await runsFor(writes.id)).length === before.length

  // ---- confirming actually starts the run ----
  await page.click("[data-test-run]")
  await page.waitForSelector("[data-confirm-run-dialog]", { timeout: 10000 })
  await page.click("[data-confirm-run]")
  await page.waitForSelector("[data-confirm-run-dialog]", { state: "hidden", timeout: 15000 })
  let after = []
  for (let i = 0; i < 20; i++) {
    after = await runsFor(writes.id)
    if (after.length > before.length) break
    await sleep(500)
  }
  out.confirmStartedARun = after.length > before.length

  // ---- canvas run control on a NON-WRITING workflow: no dialog ----
  await page.goto(canvasUrl(plain.id), { waitUntil: "load" })
  await page.waitForSelector("[data-test-run]:not([disabled])", { timeout: 30000 })
  await page.click("[data-test-run]")
  await sleep(1500)
  out.plainNoDialog = (await page.$("[data-confirm-run-dialog]")) === null

  // ---- shots: the confirm dialog, en/ar × light/dark × 1440/390, no overflow ----
  const state = await ctx.storageState()
  const shooter = await chromium.launch({ executablePath: CHROME })
  for (const locale of ["en", "ar"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const c = await shooter.newContext({
          storageState: state,
          viewport: { width, height: 900 },
          colorScheme: theme,
        })
        const p = await c.newPage()
        await p.goto(canvasUrl(writes.id, locale), { waitUntil: "load" })
        await p.waitForSelector("[data-test-run]:not([disabled])", { timeout: 30000 })
        await p.click("[data-test-run]")
        await p.waitForSelector("[data-confirm-run-dialog]", { timeout: 10000 })
        await p.waitForTimeout(500)
        await p.screenshot({ path: `${SHOTS}/wf-confirm-dialog-${locale}-${theme}-${width}.png` })
        out[`dialog-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(
          () => document.documentElement.scrollWidth - innerWidth === 0,
        )
        await c.close()
      }
    }
  }
  await shooter.close()
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  await ctx.close().catch(() => {})
}

console.log(JSON.stringify(out, null, 2))
const failed = Object.entries(out).filter(([k, v]) => v === false || k === "error")
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
