// MH-1065: Opportunities' Signals tab shows exactly what /api/w/opportunities/signals returns, in
// en/ar × light/dark × 1440/390, and the Board and List tabs still render. Read-only: nothing is
// converted, accepted or dismissed; fails on any API write.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh1065-signals-check.mjs <base> [shots]
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

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh1065-"))
  const ctx = await chromium.launchPersistentContext(dir, {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = [],
    writes = []
  page.on("pageerror", (e) => errs.push(String(e)))
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (
      r.method() !== "GET" &&
      u.pathname.startsWith("/api/") &&
      !u.pathname.startsWith("/api/auth/") &&
      !/\/read$/.test(u.pathname)
    )
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.waitForTimeout(1500)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, {
    timeout: 30000,
  })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      const url = `${base}/${loc}/w/${slug}/deals`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${url}?view=signals`, { waitUntil: "domcontentloaded" })
      await page
        .waitForSelector("[data-signal], [data-signal-list]", { timeout: 30000 })
        .catch(() => {})
      await page.waitForTimeout(1500)
      const api = await page.evaluate(async (slug) => {
        const r = await fetch("/api/w/opportunities/signals", {
          headers: { "X-Workspace-ID": slug },
          credentials: "include",
        })
        return r.ok ? r.json() : { status: r.status }
      }, slug)
      const dom = await page.evaluate(() => ({
        ids: [...document.querySelectorAll("[data-signal]")]
          .map((n) => `${n.dataset.signal}:${n.dataset.signalId}`)
          .sort(),
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflow: document.documentElement.scrollWidth > innerWidth + 1,
        tabs: [...document.querySelectorAll("[data-tab]")].map((t) => t.dataset.tab),
        title: document.title,
      }))
      const want = (api.items ?? []).map((i) => `${i.kind}:${i.id}`).sort()
      check(`${k}-api`, Array.isArray(api.items), api.status ?? Object.keys(api))
      check(`${k}-matches-api`, want.length > 0 && dom.ids.join() === want.join(), {
        dom: dom.ids.length,
        api: want.length,
      })
      check(`${k}-dir`, dom.dir === (loc === "ar" ? "rtl" : "ltr"), dom.dir)
      check(`${k}-theme`, dom.dark === (scheme === "dark"), dom.dark)
      check(`${k}-no-overflow`, !dom.overflow)
      check(
        `${k}-tabs`,
        ["board", "list", "signals"].every((t) => dom.tabs.includes(t)),
        dom.tabs,
      )
      if (shots) await page.screenshot({ path: `${shots}/mh1065-${k}.png`, fullPage: false })
      for (const v of ["board", "list"]) {
        await page.goto(`${url}?view=${v}`, { waitUntil: "domcontentloaded" })
        const sel = v === "board" ? "[data-deal-card]" : "tbody tr"
        await page.waitForSelector(sel, { timeout: 30000 }).catch(() => {})
        const n = await page.evaluate((sel) => document.querySelectorAll(sel).length, sel)
        check(`${k}-${v}-renders`, n > 0, n)
      }
    }
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(
  JSON.stringify(
    { fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) },
    null,
    1,
  ),
)
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
