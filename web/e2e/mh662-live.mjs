// MH-662 live, read-only: on a real WhatsApp conversation, opens the thread
// header's search bar and its ⋮ menu (asserts the WhatsApp action set is
// there) and the Contact info panel (asserts the gallery tabs render) — then
// closes everything without selecting messages, exporting, muting, or any of
// the destructive actions. In en/ar × light/dark × 1440/390. Only GETs; safe
// on production.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh662-live.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh662-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  // Guard: this check never writes. Any non-GET to the API outside auth fails the run.
  const writes = []
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (
      r.method() !== "GET" &&
      u.pathname.startsWith("/api/") &&
      !u.pathname.startsWith("/api/auth/")
    )
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  const api = await page.evaluate(async (slug) => {
    const h = { headers: { "X-Workspace-ID": slug } }
    const c = await fetch("/api/w/conversations?limit=100&channel=whatsapp&status=", h)
    const cj = await c.json().catch(() => ({}))
    const all = [...(cj.pinned ?? []), ...(cj.conversations ?? cj.items ?? [])]
    return { whatsapp: all.find((x) => x.channel === "whatsapp")?.id ?? null }
  }, slug)
  out[`${scheme}-api`] = api

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      if (!api.whatsapp) continue

      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${api.whatsapp}`, {
        waitUntil: "domcontentloaded",
      })
      const hasMenuTrigger = await page
        .waitForSelector("[data-thread-menu-trigger]", { timeout: 30000 })
        .then(
          () => true,
          () => false,
        )
      check(`${k}-menu-trigger`, hasMenuTrigger)
      if (!hasMenuTrigger) continue

      const pg = await page.evaluate(() => ({
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflowX: document.documentElement.scrollWidth > innerWidth + 1,
      }))
      check(`${k}-dir`, pg.dir === (loc === "ar" ? "rtl" : "ltr"), pg.dir)
      check(`${k}-theme`, pg.dark === (scheme === "dark"), pg.dark)
      check(`${k}-no-overflow`, !pg.overflowX)

      // ⋮ menu: open it, read the action set, close without clicking anything.
      await page.click("[data-thread-menu-trigger]")
      await page.waitForSelector("[data-thread-menu]", { timeout: 10000 })
      const actions = await page
        .locator("[data-thread-menu] [data-action]")
        .evaluateAll((els) => els.map((e) => e.getAttribute("data-action")))
      check(
        `${k}-menu-actions`,
        ["contact-info", "search", "select-messages", "export", "toggle-status"].every((a) =>
          actions.includes(a),
        ),
        actions,
      )
      await page.keyboard.press("Escape")
      await page.waitForTimeout(200)

      // Search bar: open, confirm it renders, close without typing a query.
      await page.click("[data-thread-search-trigger]")
      const hasSearch = await page.waitForSelector("[data-thread-search]", { timeout: 10000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-search-opens`, hasSearch)
      if (hasSearch) await page.click("[data-search-close]")
      await page.waitForTimeout(200)

      // Contact info panel: open, confirm the gallery tabs render, close.
      await page.click("[data-thread-menu-trigger]")
      await page.waitForSelector("[data-thread-menu]", { timeout: 10000 })
      await page.click('[data-thread-menu] [data-action="contact-info"]')
      const hasPanel = await page
        .waitForSelector("[data-contact-info-panel]", { timeout: 10000 })
        .then(
          () => true,
          () => false,
        )
      check(`${k}-panel-opens`, hasPanel)
      if (hasPanel) {
        const tabs = await page.locator("[data-contact-info-panel] [data-tab]").count()
        check(`${k}-panel-tabs`, tabs === 4, tabs)
        if (w === 390) {
          const width = await page
            .locator("[data-contact-info-panel]")
            .evaluate((el) => el.getBoundingClientRect().width)
          check(`${k}-panel-fullscreen`, width >= w - 4, width)
        }
        if (shots) await page.screenshot({ path: `${shots}/mh662-live-${k}-panel.png` })
      }
      await page.keyboard.press("Escape")
      await page.waitForTimeout(200)

      const of = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
      check(`${k}-thread-no-overflow`, !of)
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
