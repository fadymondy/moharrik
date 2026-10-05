// MH-517 with the REAL extension in a REAL Chromium: a workflow's first run
// adds the sites it needs to the browser it runs in.
//
//   A. A browser holding every site (the Connect click allowed): an admin
//      presses Run on the Automation page; the site is added, pushed, and the
//      run goes through with no click, its first line saying what was added.
//   B. A browser that declined every site: the run's first step on the new
//      site waits on the extension's "Waiting for your OK"; one Approve in the
//      popup (Chrome's real prompt, answered from outside) and it carries on.
//   C. The canvas shows the sites on the trigger, and whether they are added.
//
//   node e2e/required-sites-check.mjs [webBase] [apiBase] [--shots <card>]
import { chromium } from "playwright-core"
import { execFile } from "node:child_process"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { connectExtension } from "./connect-helper.mjs"

const args = process.argv.slice(2)
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROME =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function extensionCopy(broad) {
  const SRC = path.resolve(here, "../../extension")
  const EXT = mkdtempSync(path.join(tmpdir(), "ext-sites-"))
  cpSync(SRC, EXT, {
    recursive: true,
    filter: (p) =>
      !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?|[\\/]store[\\/]?/.test(p.slice(SRC.length)),
  })
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [
    `${base}/*`,
    `${apiBase}/*`,
    ...(broad ? ["http://*/*", "https://*/*"] : []),
  ]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
  return EXT
}
const launch = (EXT) =>
  chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ext-sites-profile-")), {
    executablePath: CHROME,
    headless: false,
    viewport: { width: 1280, height: 900 },
    args: [
      `--disable-extensions-except=${EXT}`,
      `--load-extension=${EXT}`,
      "--no-first-run",
      "--host-resolver-rules=MAP *.test 127.0.0.1",
    ],
  })
function answerPrompt(button) {
  return new Promise((resolve) =>
    execFile(
      "powershell",
      [
        "-NoProfile",
        "-File",
        path.join(here, "uia-click.ps1"),
        "-Button",
        button,
        "-TimeoutSec",
        "25",
      ],
      { encoding: "utf8" },
      (err, o) => resolve(!err && /^pressed/.test(String(o).trim())),
    ),
  )
}

const PORT = 8796
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><title>Roaster</title><h1 id="price">Beans: 420</h1>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))

const out = {}
const ctxA = await launch(extensionCopy(true))
let ctxB = null
try {
  const workerA =
    ctxA.serviceWorkers()[0] || (await ctxA.waitForEvent("serviceworker", { timeout: 30000 }))
  const idA = new URL(workerA.url()).host
  const page = ctxA.pages()[0] ?? (await ctxA.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "load" })
  await page.fill("#email", `sites-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Sites ${Date.now().toString(36)}`)
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
  const a = await connectExtension({
    ctx: ctxA,
    extensionID: idA,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "Office Chrome",
    hosts: [],
  })
  out.connectedA = a.online
  const hostsOf = async (id) =>
    (await call("GET", "/api/w/browsers", undefined, wsID)).browsers.find((b) => b.id === id)
      .allowed_hosts

  const wf = (
    await call(
      "POST",
      "/api/w/workflows",
      {
        name: "Roaster price",
        steps: [
          { command: "goto", args: { url: `http://roaster.test:${PORT}/` } },
          { command: "extract", args: { selector: "#price" } },
        ],
      },
      wsID,
    )
  ).workflow
  out.workflowKnowsItsSites = (wf.required_sites || []).join(",") === "roaster.test"

  // ---- A: an admin presses Run; the site is added and it goes through ----
  await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "load" })
  const row = page.locator("li[data-workflow='Roaster price']")
  await row.waitFor({ timeout: 20000 })
  await row.getByRole("button", { name: /use it/i }).click()
  await row.locator("form[data-form='run-workflow'] button[type=submit]").click()
  let runA
  for (let i = 0; i < 60; i++) {
    const runs = (await call("GET", "/api/w/runs", undefined, wsID)).runs
    runA = runs[0]
    if (runA && !["queued", "running"].includes(runA.status)) break
    await sleep(1000)
  }
  out.runAOk = runA?.status === "ok"
  out.siteAddedToTheBrowser = (await hostsOf(a.browser.id)).includes("roaster.test")
  out.runSaysWhatItAdded =
    (runA?.notes || [])[0] === "Added roaster.test to Office Chrome's allowed sites"
  const extHosts = await workerA.evaluate(
    async () => (await chrome.storage.local.get("moh.agent"))["moh.agent"]?.allowedHosts || [],
  )
  out.pushedToTheExtension = extHosts.includes("roaster.test")
  await page.goto(`${base}/en/w/${slug}/automation/runs/${runA.id}`, { waitUntil: "load" })
  await page.waitForSelector("[data-run-note='sites-added']", { timeout: 20000 })
  out.runPageShowsIt = /Added roaster\.test to Office Chrome's allowed sites/.test(
    await page.textContent("[data-run-notes]"),
  )
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-run-note.png` })

  // ---- C: the canvas trigger shows the sites ----
  await page.goto(`${base}/en/w/${slug}/automation/workflows/${wf.id}?view=editor`, { waitUntil: "load" })
  await page.waitForSelector("[data-trigger-sites]", { timeout: 30000 })
  out.triggerNodeShowsSites = /roaster\.test · added when it runs/.test(
    await page.textContent("[data-trigger-sites]"),
  )
  await page.click("[data-canvas-node='trigger']")
  await page.waitForSelector("[data-trigger-required-sites='roaster.test'] [data-auto-add='on']", {
    timeout: 15000,
  })
  out.triggerPanelShowsStatus = true
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-canvas-trigger.png` })

  // ---- C2: "Test workflow" on the canvas is a first run too ----
  const wf2 = (
    await call(
      "POST",
      "/api/w/workflows",
      {
        name: "Beans page",
        steps: [
          { command: "goto", args: { url: `http://beans.test:${PORT}/` } },
          { command: "extract", args: { selector: "#price" } },
        ],
      },
      wsID,
    )
  ).workflow
  await page.goto(`${base}/en/w/${slug}/automation/workflows/${wf2.id}?view=editor`, { waitUntil: "load" })
  await page.waitForSelector("[data-test-run]:not([disabled])", { timeout: 30000 })
  await page.click("[data-test-run]")
  for (let i = 0; i < 30 && !(await hostsOf(a.browser.id)).includes("beans.test"); i++)
    await sleep(500)
  out.testRunAddsTheSite = (await hostsOf(a.browser.id)).includes("beans.test")
  let testRun
  for (let i = 0; i < 60; i++) {
    testRun = (await call("GET", "/api/w/runs?trigger=test", undefined, wsID)).runs[0]
    if (testRun && !["queued", "running"].includes(testRun.status)) break
    await sleep(1000)
  }
  out.testRunOk =
    testRun?.status === "ok" &&
    (testRun.notes || [])[0] === "Added beans.test to Office Chrome's allowed sites"

  // ---- B: a browser that declined every site waits for the OK, then carries on ----
  ctxB = await launch(extensionCopy(false))
  const workerB =
    ctxB.serviceWorkers()[0] || (await ctxB.waitForEvent("serviceworker", { timeout: 30000 }))
  const idB = new URL(workerB.url()).host
  const b = await connectExtension({
    ctx: ctxB,
    extensionID: idB,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "Laptop Chrome",
    hosts: [],
  })
  out.connectedB = b.online
  for (let i = 0; i < 20; i++) {
    if (
      (await call("GET", "/api/w/browsers", undefined, wsID)).browsers.find(
        (x) => x.id === b.browser.id,
      ).site_access === "sites"
    )
      break
    await sleep(500)
  }
  const { run_id } = await call(
    "POST",
    `/api/w/workflows/${wf.id}/run`,
    { agent_id: b.browser.id, values: {} },
    wsID,
  )
  // The site is on the browser's list and waiting for its OK.
  const popup = await ctxB.newPage()
  await popup.setViewportSize({ width: 360, height: 700 })
  await popup.goto(`chrome-extension://${idB}/popup.html`)
  await popup.waitForSelector("[data-approve='roaster.test']", { timeout: 20000 })
  await sleep(4000)
  let mid = await call("GET", `/api/w/runs/${run_id}`, undefined, wsID)
  out.runWaitsInsteadOfFailing = mid.run.status === "running"
  out.runBNote = (mid.run.notes || [])[0]
  if (card) await popup.screenshot({ path: `${SHOTS}/${card}-popup-waiting-run.png` })
  const allow = answerPrompt("Allow")
  await popup.bringToFront()
  await popup.click("[data-approve='roaster.test']")
  out.approvePromptAnswered = await allow
  for (let i = 0; i < 60; i++) {
    mid = await call("GET", `/api/w/runs/${run_id}`, undefined, wsID)
    if (!["queued", "running"].includes(mid.run.status)) break
    await sleep(1000)
  }
  out.runBOkAfterTheOK = mid.run.status === "ok"
  out.runBExtracted = mid.steps.some(
    (s) => s.command === "extract" && s.status === "ok" && s.result.includes("Beans: 420"),
  )

  if (card) {
    const state = await ctxA.storageState()
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
          await p.goto(`${base}/${locale}/w/${slug}/automation`, { waitUntil: "load" })
          await p.waitForSelector("[data-sites-settings]", { timeout: 30000 })
          await p.locator("[data-sites-settings]").scrollIntoViewIfNeeded()
          await p.waitForTimeout(600)
          await p.screenshot({ path: `${SHOTS}/${card}-settings-${locale}-${theme}-${width}.png` })
          out[`settings-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(
            () => document.documentElement.scrollWidth - innerWidth === 0,
          )
          await p.goto(`${base}/${locale}/w/${slug}/automation/runs/${runA.id}`, {
            waitUntil: "load",
          })
          await p.waitForSelector("[data-run-notes]", { timeout: 30000 })
          await p.waitForTimeout(500)
          await p.screenshot({ path: `${SHOTS}/${card}-run-${locale}-${theme}-${width}.png` })
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  if (ctxB) await ctxB.close().catch(() => {})
  await ctxA.close().catch(() => {})
  site.close()
}

console.log(JSON.stringify(out, null, 2))
const failed = Object.entries(out).filter(([k, v]) => v === false || k === "error")
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
