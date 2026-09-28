// MH-673: a sidebar section is a page. Each group row is a link to that section's dashboard; the
// sidebar follows the URL (back, the section overview, the section's pages); back goes to the
// workspace overview; a deep link opens its section; /research keeps its old ?tab= links. Runs
// en/ar × light/dark × 1440/390 (390 through the mobile sheet) plus the mini rail at 1440.
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

// Mirrors web/lib/nav-config.ts's group homes.
const HOMES = {
  crm: "crm",
  marketing: "marketing",
  inbox: "inbox/overview",
  automation: "automation",
  research: "research",
  insights: "insights",
  notifications: "notifications/overview",
  settings: "settings",
}

async function settle(page) {
  // The workspace event stream stays open, so the page never goes fully idle: cap the wait.
  await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => {})
  await page.waitForTimeout(800)
}

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

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      const ws = `${base}/${loc}/w/${slug}`
      const mobile = w < 768
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(ws, { waitUntil: "domcontentloaded" })
      await settle(page)
      const env = await page.evaluate(() => ({ dir: document.documentElement.dir, dark: document.documentElement.classList.contains("dark") }))
      check(`${k}-dir`, env.dir === (loc === "ar" ? "rtl" : "ltr"), env.dir)
      check(`${k}-theme`, env.dark === (scheme === "dark"), env.dark)
      const nav = mobile ? "[data-mobile-nav]" : "aside"
      const openSheet = async () => {
        if (!mobile) return
        await page.click("[data-mobile-nav-toggle]")
        await page.waitForSelector("[data-mobile-nav]", { timeout: 10000 })
      }

      const groups = await (async () => {
        await openSheet()
        const g = await page.$$eval(`${mobile ? "[data-mobile-nav] " : ""}[data-nav-group]`, (as) =>
          as.filter((a) => a.offsetParent !== null).map((a) => ({ key: a.dataset.navGroup, tag: a.tagName, href: a.getAttribute("href") })),
        )
        if (mobile) await page.keyboard.press("Escape")
        return g
      })()
      check(`${k}-groups-are-links`, groups.length >= 7 && groups.every((g) => g.tag === "A" && g.href === `/${loc}/w/${slug}/${HOMES[g.key]}`), groups)

      for (const g of groups) {
        const gk = `${k}-${g.key}`
        await page.goto(ws, { waitUntil: "domcontentloaded" })
        await settle(page)
        await openSheet()
        await page.locator(`${mobile ? "[data-mobile-nav] " : ""}[data-nav-group="${g.key}"]`).filter({ visible: true }).first().click()
        await page.waitForURL(`${ws}/${HOMES[g.key]}`, { timeout: 20000 }).catch(() => {})
        check(`${gk}-navigates`, page.url() === `${ws}/${HOMES[g.key]}`, page.url())
        const dash = await page.waitForSelector(`[data-section-home="${g.key}"] [data-section-page]`, { timeout: 20000 }).then(() => true, () => false)
        check(`${gk}-dashboard`, dash)
        await settle(page)
        if (mobile) check(`${gk}-sheet-closed`, (await page.$("[data-mobile-nav]")) === null)
        await openSheet()
        const side = await page.evaluate(
          ({ nav, key }) => {
            const root = [...document.querySelectorAll(`${nav} [data-nav-drilled]`)].find((n) => n.offsetParent !== null)
            if (!root) return null
            const items = [...root.querySelectorAll("a[data-nav-item]")]
            return {
              drilled: root.dataset.navDrilled,
              back: root.querySelector("[data-nav-back]")?.getAttribute("href"),
              first: items[0]?.dataset.navItem,
              current: items.filter((a) => a.getAttribute("aria-current") === "page").map((a) => a.dataset.navItem),
              hrefs: items.slice(1).map((a) => a.getAttribute("href")),
              tiles: [...document.querySelectorAll(`[data-section-home="${key}"] [data-section-page]`)].map((a) => a.getAttribute("href")),
              title: document.title,
              h1: document.querySelector("main h1, h1")?.textContent,
              blankDesc: [...document.querySelectorAll("[data-section-page] span span:last-child")].filter((s) => !s.textContent.trim() || s.textContent.startsWith("section.")).length,
            }
          },
          { nav, key: g.key },
        )
        check(`${gk}-sidebar`, !!side && side.drilled === g.key && side.back === `/${loc}/w/${slug}` && side.first === `${g.key}-home` && side.current.join() === `${g.key}-home`, side)
        check(`${gk}-tiles-match-sidebar`, !!side && side.tiles.length > 0 && side.tiles.join() === side.hrefs.join(), side && { tiles: side.tiles, hrefs: side.hrefs })
        check(`${gk}-descriptions`, !!side && side.blankDesc === 0, side?.blankDesc)
        check(`${gk}-title`, !!side && side.title.includes(side.h1 ?? "\u0000"), side && { title: side.title, h1: side.h1 })
        if (mobile) await page.keyboard.press("Escape")
        await page.waitForTimeout(300)
        check(`${gk}-no-overflow`, !(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)))
        if (shots) await page.screenshot({ path: `${shots}/mh673-${gk}.png` })

        // A page inside the section keeps the section in the sidebar, with that page current.
        if (g.key === "crm" || g.key === "notifications") {
          const item = g.key === "crm" ? "leads" : "notifications"
          await page.goto(`${ws}/${item}`, { waitUntil: "domcontentloaded" })
          await settle(page)
          await openSheet()
          const deep = await page.evaluate((nav) => {
            const root = [...document.querySelectorAll(`${nav} [data-nav-drilled]`)].find((n) => n.offsetParent !== null)
            return root && { drilled: root.dataset.navDrilled, current: [...root.querySelectorAll("a[aria-current=page]")].map((a) => a.dataset.navItem) }
          }, nav)
          const want = g.key === "crm" ? "leads" : "notifications-center"
          check(`${gk}-deeplink`, deep?.drilled === g.key && deep.current.join() === want, deep)
          // Back from inside a section goes to the workspace overview.
          await page.locator(`${nav} [data-nav-back]`).filter({ visible: true }).first().click()
          await page.waitForURL(ws, { timeout: 15000 }).catch(() => {})
          check(`${gk}-back`, page.url() === ws, page.url())
          if (mobile) await page.keyboard.press("Escape").catch(() => {})
        }
      }

      // Old research tab links still land on the tab.
      if (w === 1440) {
        await page.goto(`${ws}/research?tab=evidence`, { waitUntil: "domcontentloaded" })
        await page.waitForURL(`${ws}/research/evidence`, { timeout: 15000 }).catch(() => {})
        check(`${k}-research-tab-redirect`, page.url() === `${ws}/research/evidence`, page.url())
      }
    }

  // The mini rail: the section's rows are icons with titles, the section overview first.
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.evaluate(() => localStorage.setItem("moharrik.nav", "mini"))
  for (const loc of ["en", "ar"]) {
    const k = `mini-${loc}-${scheme}`
    await page.goto(`${base}/${loc}/w/${slug}/crm`, { waitUntil: "domcontentloaded" })
    await settle(page)
    const rail = await page.evaluate(() => {
      const root = [...document.querySelectorAll("aside [data-nav-drilled]")].find((n) => n.offsetParent !== null)
      const home = root?.querySelector('[data-nav-item="crm-home"]')
      return {
        mini: document.querySelector(".grid-shell")?.dataset.nav,
        drilled: root?.dataset.navDrilled,
        homeTitle: home?.getAttribute("title"),
        homeCurrent: home?.getAttribute("aria-current"),
        width: Math.round(root?.getBoundingClientRect().width ?? 0),
      }
    })
    check(`${k}-rail`, rail.mini === "mini" && rail.drilled === "crm" && !!rail.homeTitle && rail.homeCurrent === "page", rail)
    if (shots) await page.screenshot({ path: `${shots}/mh673-${k}.png` })
    await page.locator('aside [data-nav-back]').filter({ visible: true }).first().click()
    await page.waitForURL(`${base}/${loc}/w/${slug}`, { timeout: 15000 }).catch(() => {})
    await page.locator('aside [data-nav-group="inbox"]').filter({ visible: true }).first().click()
    await page.waitForURL(`${base}/${loc}/w/${slug}/inbox/overview`, { timeout: 15000 }).catch(() => {})
    check(`${k}-rail-group-link`, page.url() === `${base}/${loc}/w/${slug}/inbox/overview`, page.url())
  }
  await page.evaluate(() => localStorage.removeItem("moharrik.nav"))

  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
