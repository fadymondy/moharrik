// MH-1147/MH-1148: the inbox is a copilot-style dock on every workspace page, not a page.
// On the home and on /contacts: the bar is there (and the old chat launcher is not), it opens
// into the list + thread, Escape closes it; ?inbox=all opens it in place and leaves the URL;
// /inbox?c= redirects to the home with that thread open; the sidebar has no Inbox row and
// Settings carries Canned replies and Email templates; Integrations has the Channels tab.
// Review round: the filters sit under the search in the list column, the customer panel stays
// closed until the person in the thread header is clicked, and the docked edge resizes along
// one axis only (checked from the keyboard, so no drag maths).
// Runs en/ar × light/dark × 1440/390. Read-only: fails on any API write other than marking a
// thread read.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh1147-inbox-dock.mjs <base> [shots]
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

const BAR = "[data-slot=copilot-dock-bar]"
const DOCK = "[data-slot=copilot-dock][data-open]"

async function settle(page) {
  await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => {})
  await page.waitForTimeout(800)
}

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh1147-"))
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
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, { timeout: 30000 })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of (process.env.LOCS ?? "en,ar").split(","))
    for (const w of (process.env.WIDTHS ?? "1440,390").split(",").map(Number)) {
      const k = `${loc}-${scheme}-${w}`
      const ws = `${base}/${loc}/w/${slug}`
      const narrow = w < 768
      await page.setViewportSize({ width: w, height: 900 })

      for (const p of ["", "/contacts"]) {
        const pk = `${k}${p || "/home"}`
        await page.goto(`${ws}${p}`, { waitUntil: "domcontentloaded" })
        const bar = await page.waitForSelector(BAR, { timeout: 25000 }).then(() => true, () => false)
        check(`${pk}-bar`, bar)
        check(`${pk}-no-old-launcher`, (await page.locator("[data-chat-dock-launcher]").count()) === 0)
        await settle(page)
        const env = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth > innerWidth + 1,
          preview: document.querySelector("[data-inbox-bar-preview]")?.textContent?.trim() ?? "",
        }))
        check(`${pk}-dir`, env.dir === (loc === "ar" ? "rtl" : "ltr"), env.dir)
        check(`${pk}-theme`, env.dark === (scheme === "dark"))
        check(`${pk}-no-overflow`, !env.overflow)
        check(`${pk}-preview`, env.preview.length > 0 && !env.preview.startsWith("inbox."), env.preview)
        if (shots && p === "") await page.screenshot({ path: path.join(shots, `${k}-bar.png`) })
        if (!bar) continue

        await page.click(BAR)
        const opened = await page.waitForSelector(`${DOCK} [data-inbox-panel]`, { timeout: 15000 }).then(() => true, () => false)
        check(`${pk}-opens`, opened)
        const shape = await page.evaluate((dock) => {
          const d = document.querySelector(dock)
          const handle = d?.querySelector("[data-slot=copilot-dock-resize]")
          return {
            side: d?.getAttribute("data-side"),
            handle: handle?.getAttribute("aria-orientation") ?? null,
            filtersInColumn: !!d?.querySelector("[data-conversation-column] [data-filter='all']") && !!d?.querySelector("[data-conversation-column] #inbox-status"),
          }
        }, DOCK)
        check(`${pk}-filters-under-search`, shape.filtersInColumn, shape)
        check(`${pk}-resize-axis`, shape.handle === (shape.side === "bottom" ? "horizontal" : "vertical"), shape)
        if (shape.handle && p === "") {
          const h0 = await page.locator(DOCK).evaluate((e) => e.getBoundingClientRect()[e.dataset.side === "bottom" ? "height" : "width"])
          await page.focus(`${DOCK} [data-slot=copilot-dock-resize]`)
          await page.keyboard.press(shape.side === "bottom" ? "ArrowDown" : loc === "ar" ? "ArrowRight" : "ArrowLeft")
          await page.keyboard.press(shape.side === "bottom" ? "ArrowDown" : loc === "ar" ? "ArrowRight" : "ArrowLeft")
          const h1 = await page.locator(DOCK).evaluate((e) => e.getBoundingClientRect()[e.dataset.side === "bottom" ? "height" : "width"])
          check(`${pk}-resizes`, shape.side === "bottom" ? h1 < h0 : h1 > h0, { h0, h1 })
          await page.dblclick(`${DOCK} [data-slot=copilot-dock-resize]`)
        }
        const rows = await page.waitForSelector(`${DOCK} [data-conversation]`, { timeout: 20000 }).then(() => true, () => false)
        check(`${pk}-rows`, rows)
        if (rows) {
          await page.locator(`${DOCK} [data-conversation]`).first().click()
          const thread = await page.waitForSelector(`${DOCK} [data-thread]`, { timeout: 20000 }).then(() => true, () => false)
          check(`${pk}-thread`, thread)
          const intelShut = await page.evaluate(() => !document.querySelector("[data-intel-column]") && !document.querySelector("[data-intel-drawer]"))
          check(`${pk}-intel-closed-by-default`, intelShut)
          // Narrow docks show one column at a time: the thread replaces the list, with a back button.
          const cols = await page.evaluate((dock) => {
            const vis = (s) => {
              const e = document.querySelector(`${dock} ${s}`)
              return !!e && e.getBoundingClientRect().width > 0
            }
            return { list: vis("[data-conversation-column]"), back: vis("[data-thread-back]"), thread: vis("[data-thread-column]") }
          }, DOCK)
          check(`${pk}-columns`, cols.thread && (narrow ? !cols.list && cols.back : true), cols)
          if (shots && p === "") await page.screenshot({ path: path.join(shots, `${k}-open.png`) })
          // The person opens the customer panel: a column in a wide dock, a drawer otherwise.
          await page.click(`${DOCK} [data-intel-trigger]`)
          const intel = await page.waitForSelector("[data-intel-column], [data-intel-drawer]", { timeout: 15000 }).then(() => true, () => false)
          check(`${pk}-intel-opens`, intel)
          if (shots && p === "") await page.screenshot({ path: path.join(shots, `${k}-intel.png`) })
          if (await page.locator("[data-intel-close]").count()) await page.click("[data-intel-close]")
          else await page.keyboard.press("Escape")
          await page.waitForTimeout(400)
        }
        await page.locator(`${DOCK} [data-inbox-dock-close]`).click()
        check(`${pk}-closes`, await page.waitForSelector(BAR, { timeout: 10000 }).then(() => true, () => false))
      }

      // A link opens the dock in place and takes its parameter back out of the URL.
      await page.goto(`${ws}/contacts?inbox=all`, { waitUntil: "domcontentloaded" })
      check(`${k}-link-opens`, await page.waitForSelector(`${DOCK} [data-inbox-panel]`, { timeout: 25000 }).then(() => true, () => false))
      await page.waitForTimeout(800)
      check(`${k}-link-stays`, page.url() === `${ws}/contacts`, page.url())
      await page.keyboard.press("Escape")
      check(`${k}-escape-closes`, await page.waitForSelector(BAR, { timeout: 10000 }).then(() => true, () => false))

      // The old /inbox route (pushes, desktop notifications) lands on the home with the dock open.
      await page.goto(`${ws}/inbox?unread=1`, { waitUntil: "domcontentloaded" })
      check(`${k}-inbox-redirect`, await page.waitForSelector(`${DOCK} [data-inbox-panel]`, { timeout: 25000 }).then(() => true, () => false))
      await page.waitForTimeout(800)
      check(`${k}-inbox-redirect-url`, page.url() === ws, page.url())

      // Navigation: no Inbox row; Settings carries the inbox's setup pages; Channels sit on Integrations.
      if (!narrow) {
        await page.goto(`${ws}/settings/canned`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("a[data-nav-item='settings-canned']", { timeout: 20000 }).catch(() => {})
        const nav = await page.evaluate(() => ({
          inbox: !!document.querySelector("a[data-nav-item='inbox']"),
          canned: !!document.querySelector("a[data-nav-item='settings-canned']"),
          templates: !!document.querySelector("a[data-nav-item='settings-email-templates']"),
          channels: !!document.querySelector("a[data-nav-item='channels']"),
        }))
        check(`${k}-nav`, !nav.inbox && nav.canned && nav.templates && !nav.channels, nav)
        await page.goto(`${ws}/inbox/channels?type=whatsapp`, { waitUntil: "domcontentloaded" })
        await page.waitForURL(`${ws}/settings/integrations?type=whatsapp`, { timeout: 15000 }).catch(() => {})
        check(`${k}-channels-redirect`, page.url() === `${ws}/settings/integrations?type=whatsapp`, page.url())
        check(`${k}-channels-tab`, await page.waitForSelector("[data-integrations-tab='channels'][aria-selected='true']", { timeout: 20000 }).then(() => true, () => false))
        if (shots) await page.screenshot({ path: path.join(shots, `${k}-integrations.png`) })
      }
    }
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
