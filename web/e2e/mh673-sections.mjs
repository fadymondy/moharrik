// MH-673, reworked for the intent sidebar (MH-1060/MH-1073): a section's dashboard is still a page.
// Each section home renders its tiles, the sidebar row that owns it is current (with the section's
// "Overview" sub current where the row has one), a deep link lights up its owner and sub, and
// /research keeps its old ?tab= links. Runs en/ar × light/dark × 1440/390 (390 through the mobile
// sheet, which must close after a row navigates).
// Read-only: fails on any API write other than marking a thread read.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh673-sections.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:5800").replace(/\/$/, "")
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

// Section home (nav-config.ts) → the sidebar row that owns it (nav-top.ts) and its overview sub, if any.
const HOMES = [
  { key: "crm", path: "crm", row: "customers", sub: "crm-overview" },
  { key: "marketing", path: "marketing", row: "campaigns", sub: "marketing-overview" },
  { key: "inbox", path: "inbox/overview", row: "inbox", sub: "inbox-overview" },
  { key: "automation", path: "automation", row: "automations", sub: null },
  { key: "research", path: "research", row: "research", sub: null },
  { key: "insights", path: "insights", row: "insights", sub: null },
  { key: "notifications", path: "notifications/overview", row: null, sub: null },
  // Footer rows do not nest: Settings' sub-pages are the cards on its page.
  { key: "settings", path: "settings", row: "settings", sub: null },
]

async function settle(page) {
  // The workspace event stream stays open, so the page never goes fully idle: cap the wait.
  await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => {})
  await page.waitForTimeout(800)
}

// The current row (not a sub) and the current subs, read from the visible sidebar or sheet.
const readNav = (page, scope) =>
  page.evaluate((scope) => {
    const root = scope ? document.querySelector(scope) : document
    if (!root) return null
    const cur = [...root.querySelectorAll("a[data-nav-item][aria-current=page]")].filter((a) => a.offsetParent !== null)
    return {
      row: cur.filter((a) => !a.closest("[data-nav-subs]")).map((a) => a.dataset.navItem),
      subs: cur.filter((a) => a.closest("[data-nav-subs]")).map((a) => a.dataset.navItem),
    }
  }, scope)

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh673-"))
  const ctx = await chromium.launchPersistentContext(dir, {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = [],
    writes = []
  page.on("pageerror", (e) => errs.push(String(e)))
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/") && !/\/read$/.test(u.pathname))
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await settle(page)
  await page.waitForTimeout(700)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, { timeout: 30000 })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of (process.env.LOCS ?? "en,ar").split(","))
    for (const w of (process.env.WIDTHS ?? "1440,390").split(",").map(Number)) {
      const k = `${loc}-${scheme}-${w}`
      const ws = `${base}/${loc}/w/${slug}`
      const mobile = w < 768
      const sheet = "[data-slot=app-shell-sheet]"
      const scope = mobile ? sheet : null
      const openSheet = async () => {
        if (!mobile) return
        // A dev server can still be hydrating when the toggle first renders: retry the click.
        for (let i = 0; i < 4; i++) {
          await page.locator("[data-nav-toggle]").filter({ visible: true }).first().click().catch(() => {})
          if (await page.waitForSelector(sheet, { timeout: 5000 }).then(() => true, () => false)) {
            await page.waitForTimeout(400)
            return
          }
        }
        check(`${k}-sheet-opens@${page.url()}`, false)
      }
      const closeSheet = async () => {
        if (mobile) await page.keyboard.press("Escape").catch(() => {})
        await page.waitForTimeout(300)
      }
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(ws, { waitUntil: "domcontentloaded" })
      await settle(page)
      const env = await page.evaluate(() => ({ dir: document.documentElement.dir, dark: document.documentElement.classList.contains("dark") }))
      check(`${k}-dir`, env.dir === (loc === "ar" ? "rtl" : "ltr"), env.dir)
      check(`${k}-theme`, env.dark === (scheme === "dark"), env.dark)

      for (const h of HOMES) {
        const hk = `${k}-${h.key}`
        await page.goto(`${ws}/${h.path}`, { waitUntil: "domcontentloaded" })
        const dash = await page.waitForSelector(`[data-section-home="${h.key}"] [data-section-page]`, { timeout: 20000 }).then(() => true, () => false)
        check(`${hk}-dashboard`, dash)
        await settle(page)
        const body = await page.evaluate((key) => ({
          tiles: document.querySelectorAll(`[data-section-home="${key}"] [data-section-page]`).length,
          blankDesc: [...document.querySelectorAll("[data-section-page] span span:last-child")].filter((s) => !s.textContent.trim() || s.textContent.startsWith("section.")).length,
          title: document.title,
          h1: document.querySelector("main h1, h1")?.textContent,
          overflow: document.documentElement.scrollWidth > innerWidth + 1,
        }), h.key)
        check(`${hk}-tiles`, body.tiles > 0, body.tiles)
        check(`${hk}-descriptions`, body.blankDesc === 0, body.blankDesc)
        check(`${hk}-title`, body.title.includes(body.h1 ?? "\u0000"), { title: body.title, h1: body.h1 })
        check(`${hk}-no-overflow`, !body.overflow)
        if (h.row) {
          await openSheet()
          const nav = await readNav(page, scope)
          check(`${hk}-row`, nav?.row.join() === h.row, nav)
          if (h.sub) check(`${hk}-overview-sub`, nav?.subs.join() === h.sub, nav)
          await closeSheet()
        }
        if (shots) await page.screenshot({ path: `${shots}/mh673-${hk}.png` })
      }

      // A page inside a section lights up its owner and its own sub.
      await page.goto(`${ws}/leads`, { waitUntil: "domcontentloaded" })
      await settle(page)
      await openSheet()
      // The sidebar renders after the page's first paint on a cold dev compile: wait for a current link.
      await page.waitForSelector(`${scope ?? "body"} a[data-nav-item][aria-current=page]`, { timeout: 20000 }).catch(() => {})
      const deep = await readNav(page, scope)
      check(`${k}-deeplink`, deep?.row.join() === "customers" && deep.subs.join() === "leads", deep)

      // A row navigates (and on mobile, the sheet closes behind it).
      await page.locator(`${scope ?? "body"} a[data-nav-item="inbox"]`).filter({ visible: true }).first().click()
      await page.waitForURL(`${ws}/inbox`, { timeout: 15000 }).catch(() => {})
      check(`${k}-row-link`, page.url() === `${ws}/inbox`, page.url())
      if (mobile) {
        await page.waitForTimeout(600)
        check(`${k}-sheet-closed`, (await page.$(sheet)) === null)
      }

      // Old research tab links still land on the tab.
      if (w === 1440) {
        await page.goto(`${ws}/research?tab=evidence`, { waitUntil: "domcontentloaded" })
        await page.waitForURL(`${ws}/research/evidence`, { timeout: 15000 }).catch(() => {})
        check(`${k}-research-tab-redirect`, page.url() === `${ws}/research/evidence`, page.url())
      }
    }

  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
