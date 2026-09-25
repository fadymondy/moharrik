// The browser runtime (MH-478, phase 2), with the REAL extension in a REAL
// Chromium: the offscreen socket, real input through the debugger, a tab of
// the run's own in the Moharrik window, locate prefixes, structured
// extraction, a screenshot kept on the server, and the MH-422 guard around
// real input.
//
//   node e2e/runtime-check.mjs [webBase] [apiBase] [--shots <card>] [--idle <seconds>]
import { chromium } from "playwright-core"
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
const idle = Number(opt("idle", "40"))
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const here = path.dirname(fileURLToPath(import.meta.url))

const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-runtime-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) => !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?/.test(p.slice(SRC.length)),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [`${base}/*`, `${apiBase}/*`, "http://localhost/*", "http://127.0.0.1/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
}

// The pages a run drives. 127.0.0.1 is allowed; localhost is a different host
// and is not — so a link there leaves the allowed sites.
const PORT = 8794
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  if (req.url.startsWith("/results")) {
    res.end(
      `<!doctype html><title>Results</title><h1 id="result">results for ${new URL(req.url, "http://x").searchParams.get("q")}</h1>`,
    )
    return
  }
  res.end(`<!doctype html><html><head><title>Orders</title></head><body>
    <h1 id="title">Orders for today</h1>
    <ul id="orders"><li class="order"><a href="/o/1">Order 1</a><span class="total">120</span></li><li class="order"><a href="/o/2">Order 2</a><span class="total">80</span></li></ul>
    <button id="menu" aria-label="Open the menu">Menu</button><p id="log">nothing yet</p>
    <form action="/results"><input id="q" name="q" value="old"></form>
    <a id="away" href="http://localhost:${PORT}/elsewhere">elsewhere</a>
    <script>
      // A page that only believes a person: an untrusted click is ignored.
      document.getElementById("menu").addEventListener("click", (e) => {
        document.getElementById("log").textContent = e.isTrusted ? "opened by a real click" : "ignored an untrusted click"
      })
    </script></body></html>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))

const out = {}
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-rt-profile-")),
  {
    executablePath:
      process.env.EXT_CHROME ??
      "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe",
    headless: false,
    viewport: { width: 1280, height: 860 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
  },
)

try {
  let worker = ctx.serviceWorkers()[0]
  if (!worker) worker = await ctx.waitForEvent("serviceworker", { timeout: 30000 })
  const extensionID = new URL(worker.url()).host
  out.extensionVersion = await worker.evaluate(() => chrome.runtime.getManifest().version)

  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", `runtime-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Runtime ${Date.now().toString(36)}`)
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
    name: "Runtime Chromium",
    hosts: ["127.0.0.1"],
  })
  out.connected = online

  // ---- the socket lives in the offscreen document ----
  out.offscreenDocumentOwnsTheSocket = await worker.evaluate(
    async () =>
      (await chrome.runtime.getContexts({ contextTypes: ["OFFSCREEN_DOCUMENT"] })).length === 1,
  )
  out.helloToldTheRuntime = await worker.evaluate(async () => {
    const a = (await chrome.storage.local.get("moh.agent"))["moh.agent"] || {}
    return a.tabsPerBrowser > 0 && a.automationWindow?.width >= 800
  })

  // ---- idle long enough for Chrome to stop an idle worker; the browser stays reachable ----
  if (idle > 0) await new Promise((r) => setTimeout(r, idle * 1000))
  const runOnce = async (steps) => {
    const { run_id } = await call("POST", "/api/w/runs", { agent_id: browser.id, steps }, wsID)
    for (let i = 0; i < 120; i++) {
      const d = await call("GET", `/api/w/runs/${run_id}`, undefined, wsID)
      if (!["queued", "running"].includes(d.run.status)) return d
      await new Promise((r) => setTimeout(r, 1000))
    }
    throw new Error("the run never finished")
  }
  const u = `http://127.0.0.1:${PORT}/`
  const d = await runOnce([
    { command: "goto", args: { url: u } },
    { command: "click", args: { selector: 'role=button[name="Open the menu"]' } },
    { command: "extract", args: { selector: "#log" } },
    { command: "type", args: { selector: "#q", text: "moharrik" } },
    { command: "press", args: { key: "Enter", selector: "#q" } },
    { command: "extract", args: { selector: "#result" } },
    { command: "goto", args: { url: u } },
    {
      command: "extract",
      args: { list: "li.order", fields: { name: "a", href: "a|@href", total: ".total" } },
    },
    { command: "wait_for", args: { selector: "text=Orders for today" } },
    {
      command: "eval_readonly",
      args: { expression: "document.querySelectorAll('li.order').length" },
    },
    { command: "screenshot" },
  ])
  out.runAfterIdleFinished = d.run.status === "ok"
  const results = d.steps.map((s) => (s.result ? JSON.parse(s.result) : {}))
  out.stepsAnswered = d.steps.map((s) => `${s.command}:${s.status}`).join(" ")
  out.realInputUsed =
    results.slice(1).every((r) => !r.input || r.input === "cdp") && results[1]?.input === "cdp"
  out.pageBelievedTheClick = results[2]?.value === "opened by a real click"
  out.roleSelectorResolved = results[1]?.strategy === "role"
  out.typedWithoutEcho = results[3]?.characters === 8 && !d.steps[3].result.includes("moharrik")
  out.trustedEnterSubmitted = results[5]?.value === "results for moharrik"
  // Compared field by field: the server keeps a step's arguments as JSON
  // objects, so the order the fields were written in is not preserved.
  const rows = results[7]?.rows || []
  out.structuredRows =
    rows.length === 2 &&
    rows[0].name === "Order 1" &&
    rows[0].href === "/o/1" &&
    rows[0].total === "120" &&
    rows[1].name === "Order 2" &&
    rows[1].href === "/o/2" &&
    rows[1].total === "80"
  out.textSelectorWaited = results[8]?.strategy === "text"
  out.evalReturned = results[9]?.value === 2
  out.screenshotKept = d.steps[10].has_screenshot === true && !d.steps[10].result.includes("base64")
  const shot = await page.request.get(`${base}/api/w/runs/${d.run.id}/steps/10/screenshot`, {
    headers: { "X-Workspace-ID": wsID },
  })
  const png = await shot.body()
  out.screenshotIsAPng =
    shot.status() === 200 && png.subarray(1, 4).toString() === "PNG" && png.length > 1000

  // ---- the run's page shows the screenshot, in every locale and scheme ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({
      executablePath:
        process.env.EXT_CHROME ??
        "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe",
    })
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({
            storageState: state,
            viewport: { width, height: 900 },
            colorScheme: theme,
          })
          const p = await c.newPage()
          await p.goto(`${base}/${locale}/w/${slug}/automation/runs/${d.run.id}`, {
            waitUntil: "load",
          })
          await p.waitForSelector("[data-step-shot] img", { timeout: 60000 })
          await p.waitForTimeout(800)
          await p.screenshot({
            path: `${SHOTS}/${card}-run-${locale}-${theme}-${width}.png`,
            fullPage: true,
          })
          out[`runPage-${locale}-${theme}-${width}-overflow`] = await p.evaluate(
            () => document.documentElement.scrollWidth - innerWidth === 0,
          )
          await c.close()
        }
      }
    }
    await shooter.close()
  }

  // ---- the run had a tab of its own, in the Moharrik window, and gave it back ----
  const look = () =>
    worker.evaluate(async () => {
      const win = (await chrome.storage.local.get("moh.automationWindow"))["moh.automationWindow"]
      const groups = await chrome.tabGroups.query({ title: "Moharrik" })
      // getTargets() cannot answer this under Playwright, which is itself
      // attached to every tab. Asking Chrome to detach THIS extension can: it
      // refuses when the extension is not attached.
      const owned =
        (await chrome.storage.local.get("moh.automationTabs"))["moh.automationTabs"] || []
      let stillAttached = 0
      for (const tabId of owned) {
        await new Promise((resolve) =>
          chrome.debugger.detach({ tabId }, () => {
            if (!chrome.runtime.lastError) stillAttached++
            resolve()
          }),
        )
      }
      return {
        window: typeof win === "number",
        grouped: groups.length > 0,
        stillAttached,
        runTabs: Object.keys((await chrome.storage.local.get("moh.runTabs"))["moh.runTabs"] || {})
          .length,
      }
    })
  let where = await look()
  for (let i = 0; i < 10 && (where.runTabs || where.stillAttached); i++) {
    await new Promise((r) => setTimeout(r, 500)) // run.end is pushed as the run finishes
    where = await look()
  }
  out.ranInTheMoharrikWindow = where.window && where.grouped
  out.tabGivenBackAtRunEnd = where.runTabs === 0
  out.debuggerReleasedAtRunEnd = where.stillAttached === 0

  // ---- MH-422 around real input: a real click off the allowed sites stops the run ----
  const breach = await runOnce([
    { command: "goto", args: { url: u } },
    { command: "click", args: { selector: "#away" } },
    { command: "extract", args: { selector: "body" }, continue_on_error: true },
  ])
  out.realClickOffTheListStopped =
    breach.run.status === "failed" &&
    /step 2 went to localhost, which isn't on this browser's allowed sites/.test(
      breach.run.error,
    ) &&
    breach.steps[2].status === "skipped"

  // ---- the popup, now with the debugging-banner note, and the Sites page ----
  const popup = await ctx.newPage()
  await popup.setViewportSize({ width: 360, height: 640 })
  await popup.goto(`chrome-extension://${extensionID}/popup.html`)
  await popup.waitForFunction(
    () => document.getElementById("state").textContent === "connected",
    null,
    { timeout: 15000 },
  )
  out.popupShowsBannerNote = !(await popup.$eval("#banner-note", (e) => e.hidden))
  if (card) {
    for (const scheme of ["light", "dark"]) {
      await popup.emulateMedia({ colorScheme: scheme })
      await popup.waitForTimeout(200)
      await popup.screenshot({ path: `${SHOTS}/${card}-popup-connected-${scheme}.png` })
    }
  }
  const options = await ctx.newPage()
  await options.goto(`chrome-extension://${extensionID}/options.html`)
  await options.waitForSelector("#site-hosts li", { timeout: 10000 })
  out.sitesPageListsTheWorkspacesSites = (await options.textContent("#site-hosts")).includes(
    "127.0.0.1",
  )
  if (card) {
    for (const scheme of ["light", "dark"]) {
      await options.emulateMedia({ colorScheme: scheme })
      await options.setViewportSize({ width: 1024, height: 900 })
      await options.waitForTimeout(200)
      await options.screenshot({ path: `${SHOTS}/${card}-options-${scheme}.png`, fullPage: true })
    }
  }

  // ---- a name change on the site reaches the popup without a reconnect ----
  await call("PATCH", `/api/w/browsers/${browser.id}`, { name: "Renamed Chromium" }, wsID)
  await popup.bringToFront()
  await popup.waitForFunction(
    () => document.getElementById("agent-name").textContent === "Renamed Chromium",
    null,
    { timeout: 15000 },
  )
  out.renamePushedToTheBrowser = true
} catch (e) {
  out.error = String(e?.stack || e).slice(0, 900)
} finally {
  await ctx.close()
  site.close()
  console.log(JSON.stringify(out, null, 1))
  const bad = Object.entries(out).filter(([k, v]) => v === false || k === "error")
  process.exit(bad.length ? 1 : 0)
}
