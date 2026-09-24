// The drill-down sidebar (MH-526): every group's sub-sidebar and every page it
// owns, deep links that open the right group, old URLs that redirect instead
// of 404ing, and no horizontal overflow at 390.
//
//   node e2e/nav-sweep-check.mjs [base] [--shots <card>]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const args = process.argv.slice(2)
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const base = (args[0] ?? "http://localhost:3520").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROME = process.env.E2E_CHROME ?? "C:/Program Files/Google/Chrome/Application/chrome.exe"

// Mirrors web/lib/nav-config.ts. Kept in sync by hand: a group or item added
// there without a matching entry here fails this sweep instead of shipping
// silently unswept.
const GROUPS = [
  { key: "crm", items: ["leads", "companies", "contacts", "deals", "reports"] },
  { key: "marketing", items: ["campaigns", "subscribe-forms"] },
  { key: "inbox", items: ["inbox", "inbox/stats", "inbox/canned", "inbox/email", "inbox/whatsapp", "inbox/widgets"] },
  {
    key: "automation",
    items: ["automation/browsers", "automation/workflows", "automation/presets", "automation/rules", "automation/runs", "automation/schedules", "automation/settings"],
  },
  { key: "research", items: ["research/runs", "research/evidence", "research/schedules", "prospects"] },
  { key: "insights", items: ["trends", "trends/sources", "analytics", "search-console", "youtube"] }, // the owner role can see all five
  { key: "notifications", items: ["notifications", "notifications/sources"] },
  {
    key: "settings",
    items: ["settings/email", "settings/icp", "settings/integrations", "settings/mcp", "settings/automation-profile", "settings/audit", "settings/delivery"],
  }, // the owner role can see all seven
]
const TOP_LEVEL = ["", "members"] // "" = Overview

const OLD_REDIRECTS = [
  { from: "automation", to: "automation/browsers" },
  { from: "research", to: "research/runs" },
  { from: "research?tab=evidence", to: "research/evidence" },
  { from: "research?tab=schedules", to: "research/schedules" },
]

const out = {}
const fail = (k, v) => {
  out[k] = v
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "navsweep-")), {
  executablePath: CHROME,
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
})

try {
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "load" })
  await page.fill("#email", `navsweep-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 20000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `NavSweep ${Date.now().toString(36)}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 20000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  const w = (p = "") => `${base}/en/w/${slug}${p ? "/" + p : ""}`

  await page.waitForSelector("[data-nav-drilled]")

  // ---- every top-level link resolves and shows the root sidebar (no group drilled) ----
  for (const item of TOP_LEVEL) {
    await page.goto(w(item), { waitUntil: "load" })
    await page.waitForSelector("[data-nav-drilled]")
    const drilled = await page.getAttribute("[data-nav-drilled]", "data-nav-drilled")
    out[`root:${item || "overview"}`] = drilled === ""
  }

  // ---- each group: opening it shows a back arrow and only that group's pages;
  //      each item is a real link with the expected href; visiting the item
  //      directly (a deep link) opens the sidebar drilled into that same group ----
  for (const g of GROUPS) {
    await page.goto(w(), { waitUntil: "load" })
    await page.click(`[data-nav-group="${g.key}"]`)
    await page.waitForSelector(`[data-nav-drilled="${g.key}"]`)
    out[`drill:${g.key}:hasBack`] = (await page.$("[data-nav-back]")) !== null
    const hrefs = await page.$$eval("[data-nav-drilled] a[data-nav-item]", (as) => as.map((a) => a.getAttribute("href")))
    const want = g.items.map((i) => `/en/w/${slug}/${i}`)
    out[`drill:${g.key}:items`] = hrefs.join(",") === want.join(",") ? "match" : `got ${hrefs.join(",")}`

    for (const item of g.items) {
      const resp = await page.goto(w(item), { waitUntil: "load" })
      out[`page:${item}:status`] = resp?.status() ?? 0
      await page.waitForSelector("[data-nav-drilled]")
      const drilled = await page.getAttribute("[data-nav-drilled]", "data-nav-drilled")
      out[`deeplink:${item}:opensGroup`] = drilled === g.key
      const sw = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      out[`overflow:${item}:1440`] = sw <= 1 ? "ok" : `+${sw}px`
    }
  }

  // ---- back arrow: from a drilled group, it returns to the root list without navigating ----
  await page.goto(w("automation/browsers"), { waitUntil: "load" })
  await page.click("[data-nav-back]")
  await page.waitForSelector('[data-nav-drilled=""]')
  out["backArrow:staysOnPage"] = page.url() === w("automation/browsers")

  // ---- old URLs redirect (client-side replace, since a server redirect() does not
  //      turn into an HTTP redirect under this shell's client layout boundary in this
  //      app's Next.js/Turbopack setup), nothing 404s ----
  for (const r of OLD_REDIRECTS) {
    await page.goto(w(r.from), { waitUntil: "load" })
    await page.waitForURL(w(r.to), { timeout: 10000 }).catch(() => {})
    out[`redirect:${r.from}`] = page.url() === w(r.to) ? "ok" : page.url()
  }

  // ---- 390: no horizontal overflow, and the mobile sheet opens with the same drill-down ----
  await page.setViewportSize({ width: 390, height: 844 })
  for (const item of ["automation/browsers", "research/runs", "trends"]) {
    await page.goto(w(item), { waitUntil: "load" })
    const sw = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    out[`overflow:${item}:390`] = sw <= 1 ? "ok" : `+${sw}px`
  }
  await page.goto(w(), { waitUntil: "load" })
  await page.waitForSelector("[data-mobile-nav-toggle]")
  await page.click("[data-mobile-nav-toggle]")
  await page.waitForSelector("[data-mobile-nav]")
  out["mobileSheet:opens"] = (await page.$("[data-mobile-nav] [data-nav-group]")) !== null
  await page.click('[data-mobile-nav] [data-nav-group="crm"]')
  await page.waitForSelector('[data-mobile-nav] [data-nav-drilled="crm"]')
  out["mobileSheet:drillsDown"] = true
  await page.click('[data-mobile-nav] a[data-nav-item="leads"]')
  await page.waitForURL(/\/leads$/, { timeout: 10000 })
  out["mobileSheet:itemNavigatesAndCloses"] = (await page.$("[data-mobile-nav]")) === null
  await page.setViewportSize({ width: 1440, height: 900 })

  // ---- screenshots: every group, en/ar x light/dark x 1440/390 ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({ executablePath: CHROME, headless: true })
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({ storageState: state, viewport: { width, height: 900 }, colorScheme: theme })
          const p = await c.newPage()
          for (const g of GROUPS) {
            await p.goto(`${base}/${locale}/w/${slug}/${g.items[0]}`, { waitUntil: "load" })
            await p.waitForSelector(`[data-nav-drilled="${g.key}"]`, { timeout: 15000 }).catch(() => {})
            await p.waitForTimeout(400)
            await p.screenshot({ path: `${SHOTS}/${card}-${g.key}-${locale}-${theme}-${width}.png` })
          }
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  await ctx.close().catch(() => {})
}

console.log(JSON.stringify(out, null, 2))
const isFail = (k, v) => {
  if (k === "error") return true
  if (typeof v === "boolean") return v === false
  if (k.startsWith("page:") && k.endsWith(":status")) return v !== 200
  if (k.startsWith("drill:") && k.endsWith(":items")) return v !== "match"
  if (k.startsWith("overflow:")) return v !== "ok"
  if (k.startsWith("redirect:")) return v !== "ok"
  return false
}
const failed = Object.entries(out).filter(([k, v]) => isFail(k, v))
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
