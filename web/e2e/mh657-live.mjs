// MH-657 live, read-only: the filter chips, the archived entry, the row chevron and the
// chat row menu (opened, read, closed — nothing in it is ever clicked) in
// en/ar × light/dark × 1440/390. Only GETs and menu opens; safe on production.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh657-live.mjs <base> [shots]
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
const ACTIONS = "archive,mute,toggle-unread,favourite,toggle-status,color,icon,labels,block,clear,delete"

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh657-")), {
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
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/"))
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  if (scheme === "light") {
    const api = await page.evaluate(async (slug) => {
      const h = { headers: { "X-Workspace-ID": slug } }
      const l = await fetch("/api/w/conversation_labels", h)
      const c = await fetch("/api/w/conversations?limit=5", h)
      const a = await fetch("/api/w/conversations?limit=5&archived=true", h)
      const cj = await c.json()
      return {
        labels: l.status,
        list: c.status,
        archived: a.status,
        hasPinned: Array.isArray(cj.pinned),
        hasArchivedCount: typeof cj.archived_count === "number",
      }
    }, slug)
    out.api = api
    check("api-labels-200", api.labels === 200, api.labels)
    check("api-list-pinned-field", api.list === 200 && api.hasPinned, api)
    check("api-list-archived-count", api.hasArchivedCount, api)
    check("api-archived-200", api.archived === 200, api.archived)
  }

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
      const ok = await page.waitForSelector("[data-conversation]", { timeout: 30000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-list`, ok)
      if (!ok) continue
      await page.waitForTimeout(600)
      const r = await page.evaluate(() => {
        const chips = [...document.querySelectorAll("[data-filter]")]
        const chev = document.querySelector("[data-conversation]")?.closest("li")?.querySelector("[data-row-menu-button]")
        return {
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          chips: chips.map((c) => c.dataset.filter),
          round: chips.every((c) => parseFloat(getComputedStyle(c).borderRadius) >= 12),
          chevron: !!chev,
          chevronOpacity: chev ? getComputedStyle(chev).opacity : null,
          overflowX: document.documentElement.scrollWidth > innerWidth + 1,
        }
      })
      out[k] = r
      check(`${k}-dir`, r.dir === (loc === "ar" ? "rtl" : "ltr"), r.dir)
      check(`${k}-theme`, r.dark === (scheme === "dark"), r.dark)
      check(
        `${k}-chips`,
        ["all", "unread", "favourites", "groups"].every((c) => r.chips.includes(c)),
        r.chips.join(","),
      )
      check(`${k}-chips-round`, r.round)
      check(`${k}-chevron`, r.chevron)
      if (w === 390) check(`${k}-chevron-visible-mobile`, r.chevronOpacity === "1", r.chevronOpacity)
      check(`${k}-no-overflow`, !r.overflowX)

      // Right-click the first row: the menu opens with every item. Escape closes it.
      const row = page.locator("[data-conversation]").first()
      await row.click({ button: "right" })
      const menu = page.locator("[data-chat-row-menu]")
      const open = await menu.waitFor({ state: "visible", timeout: 5000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-menu-opens`, open)
      if (open) {
        await page.waitForTimeout(400)
        const acts = await menu.locator("[data-action]").evaluateAll((els) => els.map((e) => e.dataset.action))
        const first = acts[0]
        const rest = acts.slice(1).filter((a) => a !== "unmute").join(",")
        check(`${k}-menu-items`, ["pin"].includes(first) && rest === ACTIONS, acts.join(","))
        const inView = await menu.evaluate((m) => {
          const b = m.getBoundingClientRect()
          return b.left >= 0 && b.right <= innerWidth + 1
        })
        check(`${k}-menu-in-view`, inView)
        if (loc === "ar")
          check(`${k}-menu-arabic`, /[\u0600-\u06FF]/.test(await menu.innerText()))
        if (shots) await page.screenshot({ path: `${shots}/mh657-live-${k}-menu.png` })
        await page.keyboard.press("Escape")
        await menu.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {})
      }
      if (shots) await page.screenshot({ path: `${shots}/mh657-live-${k}.png` })
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
