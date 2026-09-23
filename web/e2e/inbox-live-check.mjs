// Read-only production check for MH-379 / MH-383 on the demo workspace: the thread's CRM
// panel (link / create / deal), a ?c= deep link, canned replies, snooze presets, stats.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-live-check.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ilc-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const W = `${base}/en/w/${slug}`
await page.goto(`${W}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
out.conversations = await page.locator("[data-conversation]").count()
await page.locator("[data-conversation]").first().click()
await page.waitForSelector("[data-thread-crm]", { timeout: 20000 })
check("deepLinkInUrl", /[?&]c=/.test(page.url()), page.url())
const deep = page.url()
const crmActions = await page.$$eval("[data-crm-action]", (els) => els.map((e) => e.getAttribute("data-crm-action")))
out.crmActions = crmActions
check("crmPanel", crmActions.includes("deal") && (crmActions.includes("link") || crmActions.includes("change")))
check("crmCreateOrLinked", crmActions.includes("create") || crmActions.includes("change"))
if (shots) await page.screenshot({ path: path.join(shots, "thread.png") })
// canned
await page.click('[data-action="canned"]')
await page.waitForSelector("[data-canned]", { timeout: 10000 }).catch(() => {})
out.canned = await page.$$eval("[data-canned]", (els) => els.map((e) => e.getAttribute("data-canned")))
check("cannedReplies", out.canned.length > 0, out.canned)
if (shots) await page.screenshot({ path: path.join(shots, "canned.png") })
await page.keyboard.press("Escape")
// snooze presets (open the menu only)
const sn = page.locator('[data-action="snooze"]')
if (await sn.count()) {
  await sn.first().click()
  await page.waitForSelector("[data-snooze-preset]", { timeout: 10000 }).catch(() => {})
  out.snoozePresets = await page.$$eval("[data-snooze-preset]", (els) => els.map((e) => e.getAttribute("data-snooze-preset")))
  check("snoozePresets", out.snoozePresets.length >= 3, out.snoozePresets)
  await page.keyboard.press("Escape")
} else check("snoozePresets", (await page.locator('[data-action="unsnooze"]').count()) > 0, "no snooze button")
// deep link in a fresh tab
const p2 = await ctx.newPage(); await p2.goto(deep, { waitUntil: "domcontentloaded" })
await p2.waitForSelector("[data-thread-crm]", { timeout: 20000 }).catch(() => {})
check("deepLinkOpensThread", (await p2.locator("[data-thread-crm]").count()) === 1)
await p2.close()
// a linked contact's timeline shows the conversation
const linked = page.locator("[data-crm-contact] a[href*='/contacts/']")
out.threadLinkedToContact = await linked.count()
// stats
await page.goto(`${W}/inbox/stats`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-stats-tiles]", { timeout: 20000 }).catch(() => {})
out.statTiles = await page.$$eval("[data-stat]", (els) => els.map((e) => e.getAttribute("data-stat") + "=" + e.innerText.replace(/\s+/g, " ").slice(0, 40)))
check("statsTiles", out.statTiles.length >= 3, out.statTiles)
check("statsTable", (await page.locator("[data-stats-row]").count()) > 0)
if (shots) await page.screenshot({ path: path.join(shots, "stats.png"), fullPage: true })
// canned management page
await page.goto(`${W}/inbox/canned`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-canned-row]", { timeout: 20000 }).catch(() => {})
out.cannedVariants = await page.$$eval("[data-canned-variant]", (els) => [...new Set(els.map((e) => e.getAttribute("data-canned-variant")))])
check("cannedEnAr", out.cannedVariants.includes("en") && out.cannedVariants.includes("ar"), out.cannedVariants)
out.errors = errs
check("noPageErrors", errs.length === 0, errs)
console.log(JSON.stringify(out, null, 1)); console.log(fail.length ? "FAILED: " + fail.join(", ") : "ALL CHECKS PASSED")
await ctx.close(); process.exit(fail.length ? 1 : 0)
