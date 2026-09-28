// MH-605 live, read-only: the desktop sidebar collapses to the 3.5rem icon rail from the grid's
// .grid-shell[data-nav=mini] rule, and expands back — en/ar × light/dark at 1440. The toggle is
// kept per browser, so nothing is written; fails on any API write.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh605-nav-live.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
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
  const dir = mkdtempSync(path.join(tmpdir(), "mh605-"))
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
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/"))
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.waitForLoadState("networkidle").catch(() => {})
  await page.waitForTimeout(1500)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, { timeout: 30000 })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) {
    const k = `${loc}-${scheme}`
    await page.goto(`${base}/${loc}/w/${slug}`, { waitUntil: "domcontentloaded" })
    await page.waitForSelector("[data-nav-toggle]", { timeout: 30000 })
    await page.waitForFunction(() => !document.querySelector("[data-nav-toggle]")?.disabled, null, { timeout: 30000 })
    // A click before hydration does nothing; wait for the page to settle first.
    await page.waitForLoadState("networkidle").catch(() => {})
    await page.waitForTimeout(1500)
    const measure = () =>
      page.evaluate(() => {
        const shell = document.querySelector(".grid-shell")
        const nav = document.querySelector(".grid-shell-nav").getBoundingClientRect()
        const main = shell.children[1]?.getBoundingClientRect()
        return {
          state: shell.dataset.nav,
          cols: getComputedStyle(shell).gridTemplateColumns,
          navW: Math.round(nav.width),
          navLeft: Math.round(nav.left),
          mainW: main ? Math.round(main.width) : null,
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflowX: document.documentElement.scrollWidth > innerWidth + 1,
        }
      })
    let m = await measure()
    if (m.state === "mini") {
      await page.click("[data-nav-toggle]")
      await page.waitForTimeout(500)
      m = await measure()
    }
    out[`${k}-full`] = m
    check(`${k}-full-wide`, m.state === "full" && m.navW > 150, m)
    await page.click("[data-nav-toggle]")
    await page.waitForFunction(() => document.querySelector(".grid-shell")?.dataset.nav === "mini", null, { timeout: 5000 }).catch(() => {})
    await page.waitForTimeout(500)
    const mini = await measure()
    out[`${k}-mini`] = mini
    // 3.5rem = 56px
    check(`${k}-mini-rail`, mini.state === "mini" && Math.abs(mini.navW - 56) <= 1, mini)
    check(`${k}-mini-main-grows`, mini.mainW > m.mainW + 100, { full: m.mainW, mini: mini.mainW })
    check(`${k}-rail-side`, loc === "ar" ? mini.navLeft > 1000 : mini.navLeft === 0, mini.navLeft)
    check(`${k}-dir`, mini.dir === (loc === "ar" ? "rtl" : "ltr"), mini.dir)
    check(`${k}-theme`, mini.dark === (scheme === "dark"), mini.dark)
    check(`${k}-no-overflow`, !mini.overflowX && !m.overflowX)
    if (shots) await page.screenshot({ path: path.join(shots, `mh605-${k}-mini.png`) })
    await page.click("[data-nav-toggle]")
    await page.waitForTimeout(500)
    const back = await measure()
    check(`${k}-expands-back`, back.state === "full" && back.navW === m.navW, back)
  }
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-errors`, errs.length === 0, errs)
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL ${fail.length}: ${fail.join(", ")}` : "PASS")
process.exit(fail.length ? 1 : 0)
