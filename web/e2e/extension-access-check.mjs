// MH-483 with the REAL extension in a REAL Chromium, Chrome's own permission
// prompts answered from outside the browser (uia-click.ps1), as a person would:
//
//   1. "Allow on all sites" asks for and gives back exactly the manifest's
//      optional hosts — the old "*://*/*" request is shown being refused.
//   2. The "Connect to the site" click asks for every site; a site added on the
//      website then works at the next run, with no second click. Declined, the
//      browser still connects, the icon shows a count, the popup lists
//      "Waiting for your OK: <host> [Approve]" and one click fixes it. The
//      Sites page and the Automation page say which mode the browser is in.
//   3. The extension checks the server's version and offers the update with a
//      badge and a one-click download; the Automation page flags the browser;
//      the API serves a signed CRX and its update manifest.
//
//   node e2e/extension-access-check.mjs [webBase] [apiBase] [--shots <card>]
//
// The API must offer a NEWER build than the source's manifest (EXTENSION_DIR
// with a higher build.json version) and hold EXTENSION_SIGNING_KEY.
import { chromium } from "playwright-core"
import { execFile } from "node:child_process"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const args = process.argv.slice(2)
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROME = process.env.EXT_CHROME ?? "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// The extension as a person installs it: the source, pointed at this stack,
// holding only the site itself and loopback — NOT every site.
const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-access-"))
cpSync(SRC, EXT, { recursive: true, filter: (p) => !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?|[\\/]store[\\/]?/.test(p.slice(SRC.length)) })
const sourceVersion = JSON.parse(readFileSync(path.join(SRC, "manifest.json"), "utf8")).version
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [`${base}/*`, `${apiBase}/*`, "http://localhost/*", "http://127.0.0.1/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
}

// Chrome's prompt, answered from outside. Started before the click that opens
// it, so the wait covers the prompt appearing.
function answerPrompt(button) {
  return new Promise((resolve) => {
    execFile("powershell", ["-NoProfile", "-File", path.join(here, "uia-click.ps1"), "-Button", button, "-TimeoutSec", "20"], { encoding: "utf8" }, (err, stdout) =>
      resolve(!err && /^pressed/.test(stdout.trim())),
    )
  })
}

// A site on a real hostname that is NOT granted up front: shop.test, resolved
// to loopback by the browser alone.
const PORT = 8795
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><title>Shop</title><h1 id="title">Coffee beans, 1 kg</h1><p id="price">420</p>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))
const SHOP = `http://shop.test:${PORT}/`

const launch = () =>
  chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ext-access-profile-")), {
    executablePath: CHROME,
    headless: false,
    viewport: { width: 1280, height: 860 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run", `--host-resolver-rules=MAP *.test 127.0.0.1`],
  })

const out = {}
let openRows = null
const ctx = await launch()
let ctx2 = null

try {
  let worker = ctx.serviceWorkers()[0] || (await ctx.waitForEvent("serviceworker", { timeout: 30000 }))
  const extensionID = new URL(worker.url()).host
  const sw = () => {
    const w = ctx.serviceWorkers().find((x) => x.url().includes(extensionID))
    return w || worker
  }

  // ---- a member and a workspace ----
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", `access-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Access ${Date.now().toString(36)}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  const call = async (method, p, data, wsID) => {
    const csrf = (await (await page.request.get(`${base}/api/auth/csrf`)).json()).csrf_token
    const res = await page.request.fetch(`${base}${p}`, { method, headers: { "X-CSRF-Token": csrf, ...(wsID ? { "X-Workspace-ID": wsID } : {}) }, data })
    const text = await res.text()
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${text}`)
    return text ? JSON.parse(text) : null
  }
  const wsID = (await call("GET", "/api/me/workspaces")).workspaces.find((w) => w.slug === slug).id

  // ---- 2. the REAL Connect click asks for every site; the prompt is answered Allow ----
  const popup = await ctx.newPage()
  await popup.setViewportSize({ width: 360, height: 700 })
  await popup.goto(`chrome-extension://${extensionID}/popup.html`)
  await popup.waitForSelector("#connect:not([hidden])", { timeout: 20000 })
  out.connectNoteSaysWhatItAsks = /use websites for you/.test(await popup.textContent("#connect-note")) && !(await popup.$eval("#connect-note", (e) => e.hidden))
  if (card) await popup.screenshot({ path: `${SHOTS}/${card}-popup-connect.png` })
  const opened = ctx.waitForEvent("page", { timeout: 30000 })
  const allowConnect = answerPrompt("Allow")
  await popup.bringToFront()
  await popup.click("#connect")
  out.connectPromptAnswered = await allowConnect
  const approval = await opened
  const u = new URL(approval.url())
  const { browser } = await call(
    "POST",
    "/api/agents/connect/approve",
    { code: u.pathname.split("/").pop(), key: new URLSearchParams(u.hash.slice(1)).get("k"), workspace: wsID, name: "Access Chromium", allowed_hosts: ["127.0.0.1"] },
    wsID,
  )
  await approval.close()
  const browserRow = async () => (await call("GET", "/api/w/browsers", undefined, wsID)).browsers.find((b) => b.id === browser.id)
  for (let i = 0; i < 40 && (await browserRow()).status !== "online"; i++) await sleep(1000)
  out.connected = (await browserRow()).status === "online"
  out.connectGrantedEverySite = await popup.evaluate(() => chrome.permissions.contains({ origins: ["http://*/*", "https://*/*"] }))

  const runOnce = async (steps) => {
    const { run_id } = await call("POST", "/api/w/runs", { agent_id: browser.id, steps }, wsID)
    for (let i = 0; i < 90; i++) {
      const d = await call("GET", `/api/w/runs/${run_id}`, undefined, wsID)
      if (!["queued", "running"].includes(d.run.status)) return d
      await sleep(1000)
    }
    throw new Error("the run never finished")
  }
  const shopRun = [{ command: "goto", args: { url: SHOP } }, { command: "extract", args: { selector: "#price" } }]

  // The server hears the mode, and the Automation page says it.
  for (let i = 0; i < 20 && (await browserRow()).site_access !== "all"; i++) await sleep(500)
  out.serverKnowsEverySite = (await browserRow()).site_access === "all"

  // ---- a site added ON THE WEBSITE works at the next run, no click in the extension ----
  await call("PATCH", `/api/w/browsers/${browser.id}`, { allowed_hosts: ["127.0.0.1", "shop.test"] }, wsID)
  const atOnce = await runOnce(shopRun)
  out.siteAddedOnTheWebsiteWorksAtOnce = atOnce.run.status === "ok" && JSON.parse(atOnce.steps[1].result).value === "420"

  await page.goto(`${base}/en/w/${slug}/automation`, { waitUntil: "load" })
  openRows = async (p) => {
    // Set up, the browsers fold into one status row; "Manage" opens them.
    const manage = await p.waitForSelector("[data-manage-browsers], [data-browser-list]", { timeout: 20000 })
    if (await manage.getAttribute("data-manage-browsers") !== null) await manage.click()
  }
  await openRows(page)
  await page.waitForSelector(`[data-site-access="all"]`, { timeout: 20000 })
  out.automationPageSaysEverySite = /Every site is allowed in this browser/.test(await page.textContent(`[data-site-access="all"]`))

  // ---- 1. the Sites page: states the mode; the switch gives back and asks again ----
  const options = await ctx.newPage()
  await options.goto(`chrome-extension://${extensionID}/options.html`)
  await options.waitForFunction(() => document.getElementById("mode").dataset.mode === "all", null, { timeout: 10000 })
  out.sitesPageSaysEverySite = (await options.isChecked("#all-sites")) && /allows every site/.test(await options.textContent("#mode"))
  if (card) await options.screenshot({ path: `${SHOTS}/${card}-sites-all.png`, fullPage: true })

  // What the old switch asked for, refused by Chrome itself. Asked inside a
  // real click, as the old switch did.
  await options.evaluate(() => {
    const b = document.createElement("button")
    b.id = "old-ask"
    b.textContent = "old"
    b.onclick = () =>
      chrome.permissions
        .request({ origins: ["*://*/*"] })
        .then((ok) => (b.dataset.result = `granted:${ok}`))
        .catch((e) => (b.dataset.result = `error:${e.message}`))
    document.body.prepend(b)
  })
  const denyOld = answerPrompt("Deny")
  await options.bringToFront()
  await options.click("#old-ask")
  await options.waitForFunction(() => document.getElementById("old-ask").dataset.result, null, { timeout: 25000 })
  const oldResult = await options.$eval("#old-ask", (b) => b.dataset.result)
  await denyOld
  out.oldAllSitesRequestWasBroken = oldResult
  await options.evaluate(() => document.getElementById("old-ask").remove())

  // Off: taken back at once. Chrome withholds every site the broad grant
  // covered, Moharrik's own site included, so the switch asks for that one
  // back in the same click — one more "Allow".
  const allowOwn = answerPrompt("Allow")
  await options.bringToFront()
  await options.click("#all-sites")
  out.ownSitePromptAnswered = await allowOwn
  await options.waitForFunction(() => document.getElementById("mode").dataset.mode === "sites", null, { timeout: 10000 })
  out.switchOffTakesItBack = !(await options.evaluate(() => chrome.permissions.contains({ origins: ["https://*/*"] })))
  await options.waitForFunction(() => /one by one\./.test(document.getElementById("all-note").textContent), null, { timeout: 10000 })
  out.ownSiteKeptAfterSwitchOff = await options.evaluate((o) => chrome.permissions.contains({ origins: [`${o}/*`] }), base)
  out.sitesPageSaysOneByOne = /one by one\. 1 of your workspace's sites waits/.test(await options.textContent("#mode"))
  if (card) await options.screenshot({ path: `${SHOTS}/${card}-sites-one-by-one.png`, fullPage: true })

  // Declined mode: the icon counts, the popup lists it, the server hears it.
  for (let i = 0; i < 20; i++) {
    if ((await sw().evaluate(() => chrome.action.getBadgeText({}))) === "1") break
    await sleep(300)
  }
  out.badgeCountsTheWaitingSite = (await sw().evaluate(() => chrome.action.getBadgeText({}))) === "1"
  for (let i = 0; i < 20 && (await browserRow()).pending_hosts?.join(",") !== "shop.test"; i++) await sleep(500)
  const row = await browserRow()
  out.serverKnowsWhatWaits = row.site_access === "sites" && row.pending_hosts.join(",") === "shop.test"
  await page.reload({ waitUntil: "load" })
  await page.waitForSelector(`[data-status-pending="shop.test"]`, { timeout: 20000 })
  out.foldedRowSaysWhatWaits = true
  await openRows(page)
  await page.waitForSelector(`[data-pending="shop.test"]`, { timeout: 20000 })
  out.automationPageListsWhatWaits = /Waiting for an OK in the browser: shop\.test/.test(await page.textContent(`[data-pending="shop.test"]`))

  const refused = await runOnce(shopRun)
  out.ungrantedSiteFailsInPlainWords =
    refused.run.status === "failed" && /Chrome has not allowed Moharrik on shop\.test yet: approve it under \\"Waiting for your OK\\"/.test(JSON.stringify(refused))
  if (!out.ungrantedSiteFailsInPlainWords) out.refusedRun = refused.steps.map((x) => `${x.command}:${x.status}:${x.error}`)

  await popup.reload()
  await popup.waitForSelector("#waiting-ok:not([hidden]) [data-approve='shop.test']", { timeout: 10000 })
  out.popupListsWaitingSite = (await popup.textContent("#pending-list")).includes("shop.test")
  if (card) {
    for (const scheme of ["light", "dark"]) {
      await popup.emulateMedia({ colorScheme: scheme })
      await popup.waitForTimeout(200)
      await popup.screenshot({ path: `${SHOTS}/${card}-popup-waiting-${scheme}.png` })
    }
    await popup.emulateMedia({ colorScheme: "light" })
  }
  // One click and it works. (Chrome may not prompt again here: this profile
  // granted every site once, and Chrome remembers that. The fresh profile
  // below, which never did, shows the real prompt.)
  const allowOne = answerPrompt("Allow")
  await popup.bringToFront()
  await popup.click("[data-approve='shop.test']")
  out.approvePromptInThisProfile = (await allowOne) ? "shown and answered" : "not shown (every site was granted here before)"
  await popup.waitForSelector("#waiting-ok[hidden]", { state: "attached", timeout: 15000 })
  out.approvedSiteLeavesTheList = true
  const approved = await runOnce(shopRun)
  out.approvedSiteWorks = approved.run.status === "ok"
  out.approvalWasOneSiteOnly = !(await popup.evaluate(() => chrome.permissions.contains({ origins: ["https://*/*"] })))

  // ---- 3. the update: badge, popup, download, the Automation page's flag ----
  await sw().evaluate(() => new Promise((r) => chrome.runtime.sendMessage({ type: "update.check" }, r)))
  for (let i = 0; i < 20 && (await sw().evaluate(() => chrome.action.getBadgeText({}))) !== "NEW"; i++) await sleep(300)
  out.badgeSaysNewOnceNothingWaits = (await sw().evaluate(() => chrome.action.getBadgeText({}))) === "NEW"
  await popup.reload()
  await popup.waitForSelector("#update-box:not([hidden])", { timeout: 10000 })
  const latest = (await (await page.request.get(`${apiBase}/api/automation/extension/latest`)).json()).version
  out.popupOffersTheUpdate = (await popup.textContent("#update-version")).includes(`${sourceVersion} → ${latest}`)
  if (card) {
    for (const scheme of ["light", "dark"]) {
      await popup.emulateMedia({ colorScheme: scheme })
      await popup.waitForTimeout(200)
      await popup.screenshot({ path: `${SHOTS}/${card}-popup-update-${scheme}.png` })
    }
    await popup.emulateMedia({ colorScheme: "light" })
  }
  // The tab the button opens turns into a download at once, so it is caught
  // where it is created: in the extension's own worker.
  await sw().evaluate(() => {
    globalThis.__opened = []
    chrome.tabs.onCreated.addListener((tab) => globalThis.__opened.push(tab.pendingUrl || tab.url))
  })
  await popup.click("#update-download")
  await sleep(1500)
  const openedUrls = await sw().evaluate(() => globalThis.__opened)
  out.downloadIsOneClick = openedUrls.includes(`${base}/api/automation/extension/download`)
  if (!out.downloadIsOneClick) out.openedUrls = openedUrls
  const dlRes = await page.request.get(`${base}/api/automation/extension/download`)
  out.downloadServesTheZip = dlRes.status() === 200 && dlRes.headers()["x-extension-version"] === latest
  await page.reload({ waitUntil: "load" })
  out.automationPageFlagsTheOldVersion = !!(await page.waitForSelector(`[data-update-available]`, { timeout: 20000 }).catch(() => null))

  // The signed package and its manifest, public.
  const xml = await (await fetch(`${apiBase}/api/automation/extension/updates.xml`)).text()
  const crx = await fetch(`${apiBase}/api/automation/extension/moharrik.crx`)
  const crxBody = Buffer.from(await crx.arrayBuffer())
  out.updateManifestNamesTheCRX = /<updatecheck codebase='[^']+\/api\/automation\/extension\/moharrik\.crx' version='[^']+' \/>/.test(xml)
  out.crxIsSigned = crx.status === 200 && crx.headers.get("content-type") === "application/x-chrome-extension" && crxBody.subarray(0, 4).toString() === "Cr24" && crxBody.readUInt32LE(4) === 3

  // ---- 2b. declined at Connect: still connects, site by site ----
  ctx2 = await launch()
  const w2 = ctx2.serviceWorkers()[0] || (await ctx2.waitForEvent("serviceworker", { timeout: 30000 }))
  const id2 = new URL(w2.url()).host
  const pop2 = await ctx2.newPage()
  await pop2.setViewportSize({ width: 360, height: 700 })
  await pop2.goto(`chrome-extension://${id2}/popup.html`)
  await pop2.waitForSelector("#connect:not([hidden])", { timeout: 20000 })
  const opened2 = ctx2.waitForEvent("page", { timeout: 30000 })
  const deny = answerPrompt("Deny")
  await pop2.bringToFront()
  await pop2.click("#connect")
  out.declinePromptAnswered = await deny
  const approval2 = await opened2
  const u2 = new URL(approval2.url())
  const second = await call(
    "POST",
    "/api/agents/connect/approve",
    { code: u2.pathname.split("/").pop(), key: new URLSearchParams(u2.hash.slice(1)).get("k"), workspace: wsID, name: "Declined Chromium", allowed_hosts: ["127.0.0.1", "shop.test"] },
    wsID,
  )
  await approval2.close()
  let row2
  for (let i = 0; i < 40; i++) {
    row2 = (await call("GET", "/api/w/browsers", undefined, wsID)).browsers.find((b) => b.id === second.browser.id)
    if (row2.status === "online" && row2.site_access) break
    await sleep(1000)
  }
  out.declinedStillConnects = row2.status === "online"
  out.declinedWaitsForTheSite = row2.site_access === "sites" && row2.pending_hosts.join(",") === "shop.test"
  const w2now = ctx2.serviceWorkers().find((x) => x.url().includes(id2)) || w2
  out.declinedBadgeCounts = (await w2now.evaluate(() => chrome.action.getBadgeText({}))) === "1"
  // The popup's Approve, in a profile that never granted every site: Chrome's
  // real prompt for that one site, answered Allow, and the site stops waiting.
  await pop2.reload()
  await pop2.waitForSelector("[data-approve='shop.test']", { timeout: 15000 })
  if (card) await pop2.screenshot({ path: `${SHOTS}/${card}-popup-declined.png` })
  const allow2 = answerPrompt("Allow")
  await pop2.bringToFront()
  await pop2.click("[data-approve='shop.test']")
  out.approvePromptAnswered = await allow2
  await pop2.waitForSelector("#waiting-ok[hidden]", { state: "attached", timeout: 15000 })
  out.approveGrantsOnlyThatSite =
    (await pop2.evaluate(() => chrome.permissions.contains({ origins: ["http://*.shop.test/*"] }))) &&
    !(await pop2.evaluate(() => chrome.permissions.contains({ origins: ["https://*/*"] })))
  await ctx2.close()
  ctx2 = null

  // ---- the Automation page and the extension page, in every locale and scheme ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({ executablePath: CHROME })
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({ storageState: state, viewport: { width, height: 900 }, colorScheme: theme })
          const p = await c.newPage()
          await p.goto(`${base}/${locale}/w/${slug}/automation`, { waitUntil: "load" })
          await openRows(p)
          await p.waitForSelector("[data-site-access]", { timeout: 30000 })
          await p.locator("[data-site-access]").first().scrollIntoViewIfNeeded()
          await p.waitForTimeout(600)
          await p.screenshot({ path: `${SHOTS}/${card}-automation-${locale}-${theme}-${width}.png`, fullPage: true })
          out[`automation-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(() => document.documentElement.scrollWidth - innerWidth === 0)
          await p.goto(`${base}/${locale}/w/${slug}/automation/extension`, { waitUntil: "load" })
          await p.waitForSelector("[data-managed]", { timeout: 30000 })
          await p.waitForTimeout(600)
          await p.screenshot({ path: `${SHOTS}/${card}-extension-${locale}-${theme}-${width}.png`, fullPage: true })
          out[`extension-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(() => document.documentElement.scrollWidth - innerWidth === 0)
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  if (ctx2) await ctx2.close().catch(() => {})
  await ctx.close().catch(() => {})
  site.close()
}

console.log(JSON.stringify(out, null, 2))
const failed = Object.entries(out).filter(([k, v]) => v === false || k === "error")
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
