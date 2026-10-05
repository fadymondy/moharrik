// Read-only production check for the intent sidebar (MH-1060/MH-1073; was MH-526's
// drill-down) and MH-527 (the presets marketplace). Signs in as the demo user; nothing is added, saved or
// run — the preset detail sheet is opened and closed with Escape.
// en/ar × light/dark × 1440/390 for the gallery and a nested page; the full
// row/sub sweep runs once at en-dark-1440.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/nav-presets-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "https://app.moharrik.com").replace(/\/$/, "")
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
// Rows with subs (nav-top.ts). Each sub loads and lights up its own row. Footer rows (Settings)
// do not nest: their sub-pages are cards on the page.
const ROWS = ["customers", "inbox", "research", "automations", "campaigns", "insights"]
// The current row (not a sub) and current subs in the visible sidebar or the mobile sheet.
const readNav = (page, scope) =>
  page.evaluate((scope) => {
    const root = scope ? document.querySelector(scope) : document
    if (!root) return null
    const cur = [...root.querySelectorAll("a[data-nav-item][aria-current=page]")].filter((a) => a.offsetParent !== null)
    return {
      row: cur.filter((a) => !a.closest("[data-nav-subs]")).map((a) => a.dataset.navItem).join(),
      subs: cur.filter((a) => a.closest("[data-nav-subs]")).map((a) => a.dataset.navItem).join(),
    }
  }, scope)

for (const scheme of ["dark", "light"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "navpre-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  const w = (loc, p = "") => `${base}/${loc}/w/${slug}${p ? "/" + p : ""}`

  if (scheme === "dark") {
    // Full sweep: each row lists its subs while active, and every sub loads under that row.
    for (const r of ROWS) {
      await page.goto(w("en"), { waitUntil: "load" })
      const row = await page.waitForSelector(`a[data-nav-item="${r}"]`, { timeout: 20000 }).catch(() => null)
      if (!row) {
        out[`row:${r}`] = "hidden for this role"
        continue
      }
      await row.click()
      await page.waitForSelector(`[data-nav-subs="${r}"] a[data-nav-item]`, { timeout: 15000 }).catch(() => {})
      const hrefs = await page.$$eval(`[data-nav-subs="${r}"] a[data-nav-item]`, (as) => as.map((a) => a.getAttribute("href")))
      check(`row:${r}:subs`, hrefs.length > 0, hrefs)
      for (const h of hrefs) {
        const resp = await page.goto(new URL(h, base).toString(), { waitUntil: "load" })
        await page.waitForSelector("a[data-nav-item][aria-current=page]", { timeout: 20000 }).catch(() => {})
        await page.waitForTimeout(400)
        const nav = await readNav(page, null)
        const item = h.split(`/w/${slug}/`)[1]
        check(`page:${item}`, (resp?.status() ?? 0) < 400 && nav?.row === r, { status: resp?.status(), nav })
        const ov = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
        check(`overflow:${item}:1440`, ov <= 1, ov)
      }
    }
  }

  for (const loc of ["en", "ar"])
    for (const vw of [1440, 390]) {
      const k = `${loc}-${scheme}-${vw}`
      await page.setViewportSize({ width: vw, height: 900 })

      // Gallery home: brand tiles; search narrows to presets.
      await page.goto(w(loc, "automation/presets"), { waitUntil: "load" })
      await page.waitForSelector("[data-brand-tile]", { timeout: 30000 }).catch(() => {})
      const tiles = (await page.$$("[data-brand-tile]")).length
      check(`${k}-tiles`, tiles >= 10, tiles)
      await page.fill("[data-preset-search]", "trends")
      const narrowed = await page
        .waitForFunction(
          () => {
            const n = document.querySelectorAll("[data-preset]").length
            return n > 0 && n < 10 ? n : false
          },
          null,
          { timeout: 10000 },
        )
        .then((h) => h.jsonValue())
        .catch(() => 0)
      check(`${k}-search`, narrowed > 0, narrowed)
      let ov = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-gallery-overflow`, ov <= 1, ov)
      if (shots) await page.screenshot({ path: path.join(shots, `mh527-live-${k}.png`) })

      // Brand page → detail sheet, technical notes collapsed. Escape closes it; nothing is added.
      await page.goto(w(loc, "automation/presets/google-news"), { waitUntil: "load" })
      await page
        .waitForSelector("[data-preset-open='google-news-search']", { timeout: 30000 })
        .catch(() => {})
      await page.click("[data-preset-open='google-news-search']").catch(() => {})
      const sheet = await page
        .waitForSelector("[data-preset-sheet='google-news-search']", { timeout: 10000 })
        .then(() => true)
        .catch(() => false)
      const collapsed =
        sheet &&
        !(await page.isVisible(
          "[data-preset-sheet='google-news-search'] [data-preset-technical] [data-preset-inputs]",
        ))
      check(`${k}-sheet`, sheet && collapsed, { sheet, collapsed })
      await page.keyboard.press("Escape")

      // A nested page at this size: its row and sub are current (at 390, inside the mobile sheet).
      await page.goto(w(loc, "automation/browsers"), { waitUntil: "load" })
      await page.waitForSelector("a[data-nav-item][aria-current=page]", { timeout: 20000 }).catch(() => {})
      let scope = null
      if (vw === 390) {
        await page.locator("[data-nav-toggle]").filter({ visible: true }).first().click().catch(() => {})
        scope = (await page.waitForSelector("[data-slot=app-shell-sheet]", { timeout: 10000 }).then(() => true, () => false))
          ? "[data-slot=app-shell-sheet]"
          : null
        check(`${k}-mobile-sheet`, scope !== null)
        await page.waitForTimeout(400)
      }
      const nav = await readNav(page, scope)
      check(`${k}-nav-current`, nav?.row === "automations" && nav.subs === "automation-browsers", nav)
      if (shots) await page.screenshot({ path: path.join(shots, `mh526-live-${k}.png`) })
      await page.keyboard.press("Escape")
      ov = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-nav-overflow`, ov <= 1, ov)
    }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
