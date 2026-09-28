// Connecting a browser by approval (MH-478), with the REAL extension in a REAL
// Chromium: install, press "Connect to the site" in its popup, approve it on
// the page that opens, and watch the browser come online by itself. Then the
// refusal, a run through the new token, rename, and revoke.
//
//   node e2e/connect-check.mjs [webBase] [apiBase] [--shots <card>]
//
// Playwright's Chromium, headed: Chrome 137+ ignores --load-extension, and a
// browser that starts without its extension looks exactly like an extension
// that failed to start (see extension-check.mjs).
import { chromium } from "playwright-core"
import { execFile } from "node:child_process"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const args = process.argv.slice(2)
const shotsIdx = args.indexOf("--shots")
const card = shotsIdx >= 0 ? args.splice(shotsIdx, 2)[1] : ""
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const here = path.dirname(fileURLToPath(import.meta.url))

// A copy of THIS worktree's extension with the dev site baked in, exactly as
// `build.mjs --dev` bakes it: the site into config.js, loopback into the
// manifest's host permissions (the check cannot click Chrome's permission
// prompt; everything else is the real extension).
const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-connect-"))
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

// The page a run drives, on loopback — which is what the browser will be allowed.
const PAGE_PORT = 8793
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(
    `<!doctype html><html><body><h1 id="title">Orders for today</h1><p class="row">first</p><p class="row">second</p></body></html>`,
  )
})
await new Promise((r) => site.listen(PAGE_PORT, "127.0.0.1", r))

const out = {}
// The popup in both schemes: the owner looks at it on a dark toolbar.
async function shots(pg, name) {
  if (!card) return
  for (const scheme of ["light", "dark"]) {
    await pg.emulateMedia({ colorScheme: scheme })
    await pg.waitForTimeout(150)
    await pg.screenshot({ path: `${SHOTS}/${card}-${name}-${scheme}.png` })
  }
  await pg.emulateMedia({ colorScheme: null })
}
const fail = (why) => {
  out.failed = why
  throw new Error(why)
}
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-profile-")),
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
  out.extensionLoaded = !!extensionID
  out.extensionVersion = await worker.evaluate(() => chrome.runtime.getManifest().version)
  const workerErrors = []
  worker.on("console", (m) => m.type() === "error" && workerErrors.push(m.text().slice(0, 200)))

  // ---- a member with a workspace, signed in IN THIS BROWSER ----
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  const email = `connect-${Date.now()}@x.test`
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", email)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Nile Coffee Co ${Date.now().toString(36).slice(-4)}`)
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
    return { status: res.status(), body: text ? JSON.parse(text) : null }
  }
  const wsID = (await call("GET", "/api/me/workspaces")).body.workspaces.find(
    (w) => w.slug === slug,
  ).id

  // ---- the popup: one button ----
  const popup = await ctx.newPage()
  await popup.setViewportSize({ width: 360, height: 560 })
  const popupURL = `chrome-extension://${extensionID}/popup.html`
  await popup.goto(popupURL)
  await popup.waitForSelector("#connect:not([hidden])")
  out.popupOffersConnect = (await popup.textContent("#connect")).trim() === "Connect to the site"
  out.popupHasNoWorkspaceField = (await popup.$("#workspace-field, input#workspace")) === null
  await shots(popup, "popup-not-connected")

  const opened = ctx.waitForEvent("page", { timeout: 30000 })
  // Since MH-483 the click first asks Chrome for every site; Chrome's prompt
  // is browser UI, answered from outside as a person would (uia-click.ps1).
  const answered = new Promise((resolve) =>
    execFile(
      "powershell",
      [
        "-NoProfile",
        "-File",
        path.join(path.dirname(fileURLToPath(import.meta.url)), "uia-click.ps1"),
        "-Button",
        "Allow",
        "-TimeoutSec",
        "20",
      ],
      { encoding: "utf8" },
      (err, o) => resolve(!err && /^pressed/.test(String(o).trim())),
    ),
  )
  await popup.bringToFront()
  await popup.click("#connect")
  out.everySitePromptAnswered = await answered
  const approval = await opened
  await approval.waitForLoadState("domcontentloaded")
  const approvalURL = approval.url()
  out.approvalPageOpened = /\/automation\/connect\/[A-Z0-9]{4}-[A-Z0-9]{4}#k=/.test(approvalURL)
  out.keyOnlyInFragment =
    !new URL(approvalURL).search.includes("k=") && new URL(approvalURL).hash.startsWith("#k=")

  await popup.bringToFront()
  await popup.waitForSelector("#waiting:not([hidden])", { timeout: 15000 })
  const popupCode = (await popup.textContent("#code")).trim()
  await shots(popup, "popup-waiting")

  // ---- approve it on the page the extension opened ----
  await approval.bringToFront()
  await approval.waitForSelector("[data-connect-form]", { timeout: 30000 })
  const pageCode = (await approval.textContent("[data-connect-code]")).trim()
  out.codesMatch = popupCode === pageCode && /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(pageCode)
  out.claimsShown = (await approval.textContent("[data-connect-claims]")).includes("browser")
  await approval.fill("#connect-name", "This Chromium")
  await approval.fill("#connect-hosts", "127.0.0.1")
  if (card)
    await approval.screenshot({ path: `${SHOTS}/${card}-approval-live.png`, fullPage: true })
  await approval.click("[data-approve]")
  await approval.waitForSelector('[data-connect-done="approved"]', { timeout: 20000 })
  out.approvedOnThePage = true

  // ---- the extension collects its token and connects by itself ----
  const extState = () =>
    worker.evaluate(async () => {
      const all = await chrome.storage.local.get([
        "moh.token",
        "moh.status",
        "moh.agent",
        "moh.pairing",
      ])
      return {
        hasToken: !!all["moh.token"],
        status: all["moh.status"],
        agent: all["moh.agent"],
        pairing: all["moh.pairing"] ?? null,
      }
    })
  let state
  for (let i = 0; i < 60; i++) {
    state = await extState()
    if (state.hasToken && state.status?.state === "connected") break
    await new Promise((r) => setTimeout(r, 1000))
  }
  out.extensionConnectedByItself = state.hasToken && state.status?.state === "connected"
  out.extensionKnowsItsWorkspace = (state.agent?.workspaceName || "").startsWith("Nile Coffee Co")
  out.extensionKnowsItsSites =
    JSON.stringify(state.agent?.allowedHosts) === JSON.stringify(["127.0.0.1"])
  out.requestForgotten = state.pairing === null

  await popup.reload()
  await popup.waitForSelector("#forget:not([hidden])", { timeout: 10000 })
  out.popupSaysConnected = (await popup.textContent("#state")).trim() === "connected"
  out.popupNamesTheWorkspace = (await popup.textContent("#workspace-name")).includes(
    state.agent?.workspaceName || "?",
  )
  out.popupNamesTheBrowser = (await popup.textContent("#agent-name")).trim() === "This Chromium"
  out.popupShowsTheLastCommandLineAndPause =
    !(await popup.$eval("#last-row", (e) => e.hidden)) &&
    !(await popup.$eval("#pause-row", (e) => e.hidden))

  // ---- the site sees it online, in this workspace ----
  let browsers = (await call("GET", "/api/w/browsers", undefined, wsID)).body.browsers
  const mine = browsers.find((b) => b.name === "This Chromium")
  out.siteShowsItOnline = mine?.status === "online"
  out.siteHasItsSites = JSON.stringify(mine?.allowed_hosts) === JSON.stringify(["127.0.0.1"])

  // ---- the token drives a run ----
  const run = await call(
    "POST",
    "/api/w/runs",
    {
      agent_id: mine.id,
      steps: [
        { command: "goto", args: { url: `http://127.0.0.1:${PAGE_PORT}/` } },
        { command: "extract", args: { selector: "#title" } },
      ],
    },
    wsID,
  )
  let runState
  for (let i = 0; i < 60 && run.status === 201; i++) {
    runState = (await call("GET", `/api/w/runs/${run.body.run_id}`, undefined, wsID)).body
    if (!["queued", "running"].includes(runState.run.status)) break
    await new Promise((r) => setTimeout(r, 1000))
  }
  out.runThroughTheNewToken =
    runState?.run?.status === "ok" && JSON.stringify(runState.steps).includes("Orders for today")
  await popup.bringToFront()
  await popup.reload()
  await popup.waitForFunction(
    () => /extract · \d+ms/.test(document.getElementById("last-command").textContent),
    null,
    { timeout: 10000 },
  )
  out.popupShowsTheLastCommand = true
  await shots(popup, "popup-connected")

  // ---- this browser's own pause: disconnects, and the site sees it go ----
  await popup.click("#pause")
  await popup.waitForFunction(
    () => document.getElementById("state").textContent === "paused",
    null,
    { timeout: 10000 },
  )
  await shots(popup, "popup-paused")
  let offline = false
  for (let i = 0; i < 20 && !offline; i++) {
    offline =
      (await call("GET", "/api/w/browsers", undefined, wsID)).body.browsers.find(
        (b) => b.id === mine.id,
      )?.status === "offline"
    if (!offline) await new Promise((r) => setTimeout(r, 500))
  }
  out.pauseDisconnects = offline
  await popup.click("#pause")
  let back = false
  for (let i = 0; i < 30 && !back; i++) {
    back =
      (await call("GET", "/api/w/browsers", undefined, wsID)).body.browsers.find(
        (b) => b.id === mine.id,
      )?.status === "online"
    if (!back) await new Promise((r) => setTimeout(r, 500))
  }
  out.unpauseReconnects = back

  // ---- recording state, on an allowed page ----
  for (let i = 0; i < 20; i++) {
    state = await extState()
    if (state.status?.state === "connected") break
    await new Promise((r) => setTimeout(r, 500))
  }
  const recPage = await ctx.newPage()
  await recPage.goto(`http://127.0.0.1:${PAGE_PORT}/`)
  await recPage.bringToFront()
  const recStarted = await popup.evaluate(() =>
    chrome.runtime.sendMessage({ type: "record.start" }),
  )
  await popup.bringToFront()
  await popup.reload()
  await popup
    .waitForFunction(() => /^on/.test(document.getElementById("record-state").textContent), null, {
      timeout: 10000,
    })
    .catch(() => {})
  out.popupShowsRecording =
    !recStarted?.error && /^on/.test(await popup.textContent("#record-state"))
  await shots(popup, "popup-recording")
  await popup.evaluate(() => chrome.runtime.sendMessage({ type: "record.discard" }))
  await recPage.close()

  // ---- rename on the site ----
  await page.goto(`${base}/en/w/${slug}/automation/browsers`, { waitUntil: "load" })
  await page.waitForSelector("[data-manage-browsers], [data-browser]", { timeout: 60000 })
  const manage = await page.$("[data-manage-browsers]")
  if (manage) await manage.click()
  await page.click(`[data-browser="This Chromium"] [data-rename-browser]`)
  await page.fill(`#name-${mine.id}`, "Work Chromium")
  await page.click('form[data-form="rename"] button[type=submit]')
  await page.waitForSelector('[data-browser="Work Chromium"]', { timeout: 15000 })
  browsers = (await call("GET", "/api/w/browsers", undefined, wsID)).body.browsers
  out.renamedOnTheSite = browsers.some((b) => b.id === mine.id && b.name === "Work Chromium")
  if (card)
    await page.screenshot({
      path: `${SHOTS}/${card}-automation-renamed-en-light-1440.png`,
      fullPage: true,
    })

  // ---- a refusal reaches the popup ----
  await popup.bringToFront()
  await popup.click("#forget")
  await popup.waitForSelector("#connect:not([hidden])", { timeout: 10000 })
  const opened2 = ctx.waitForEvent("page", { timeout: 20000 })
  await popup.click("#connect")
  const approval2 = await opened2
  await approval2.waitForSelector("[data-connect-form]", { timeout: 30000 })
  await approval2.click("[data-deny]")
  await approval2.waitForSelector('[data-connect-done="denied"]', { timeout: 15000 })
  for (let i = 0; i < 20; i++) {
    state = await extState()
    if (state.pairing?.status === "denied") break
    await new Promise((r) => setTimeout(r, 1000))
  }
  out.refusalReachedTheExtension = state.pairing?.status === "denied" && !state.hasToken
  await popup.bringToFront()
  await popup.reload()
  await popup.waitForFunction(
    () => document.getElementById("state").textContent === "refused",
    null,
    { timeout: 10000 },
  )
  out.popupSaysRefused = (await popup.textContent("#detail")).includes("refused")
  await shots(popup, "popup-refused")

  // ---- connect again, then revoke on the site: the browser is dropped ----
  const opened3 = ctx.waitForEvent("page", { timeout: 20000 })
  await popup.click("#connect")
  const approval3 = await opened3
  await approval3.waitForSelector("[data-connect-form]", { timeout: 30000 })
  await approval3.fill("#connect-name", "Work Chromium")
  await approval3.fill("#connect-hosts", "127.0.0.1")
  await approval3.click("[data-approve]")
  await approval3.waitForSelector('[data-connect-done="approved"]', { timeout: 20000 })
  out.sameBrowserReconnectedNotDuplicated = (
    await approval3.textContent("[data-connect-done]")
  ).includes("already connected")
  for (let i = 0; i < 40; i++) {
    state = await extState()
    if (state.hasToken && state.status?.state === "connected") break
    await new Promise((r) => setTimeout(r, 1000))
  }
  browsers = (await call("GET", "/api/w/browsers", undefined, wsID)).body.browsers
  out.stillOneBrowser =
    browsers.filter((b) => !b.revoked_at).length === 1 && browsers[0].id === mine.id
  const revoked = await call("DELETE", `/api/w/browsers/${mine.id}`, undefined, wsID)
  out.revokeAccepted = revoked.status === 204
  for (let i = 0; i < 40; i++) {
    state = await extState()
    if (!state.hasToken) break
    await new Promise((r) => setTimeout(r, 1000))
  }
  out.revokedBrowserForgetsItsToken = !state.hasToken
  await popup.bringToFront()
  await popup.reload()
  await popup.waitForSelector("#connect:not([hidden])", { timeout: 10000 })
  out.popupSaysWhyAfterRevoke = /token revoked/.test(await popup.textContent("#detail"))
  await shots(popup, "popup-revoked")
  out.workerErrors = workerErrors
} catch (e) {
  out.error = String(e?.stack || e).slice(0, 800)
} finally {
  await ctx.close()
  site.close()
  console.log(JSON.stringify(out, null, 1))
  const bad = Object.entries(out).filter(([k, v]) => v === false || k === "error" || k === "failed")
  process.exit(bad.length ? 1 : 0)
}
