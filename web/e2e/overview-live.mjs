// Read-only production check for MH-567: the workspace home's analytics summary and
// charts, en/ar × light/dark × 1440/390. Nothing is created or changed.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/overview-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ovl-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto(`${base}/${loc}/w/${slug}`, { waitUntil: "domcontentloaded" })
    await page.waitForSelector("[data-chart-id], [data-traffic-ga4], [data-traffic-figure]", { timeout: 30000 }).catch(() => {})
    await page.waitForTimeout(1500)
    const k = `${loc}-${scheme}-${w}`
    const st = await page.evaluate(() => ({
      dir: document.documentElement.dir,
      dark: document.documentElement.classList.contains("dark"),
      charts: document.querySelectorAll("[data-chart-id]").length,
      svgs: document.querySelectorAll("[data-chart-id] svg").length,
      figures: document.querySelectorAll("[data-traffic-figure]").length,
      overflow: document.documentElement.scrollWidth - window.innerWidth,
    }))
    check(k, st.charts > 0 && st.svgs > 0 && st.dir === (loc === "ar" ? "rtl" : "ltr") && st.dark === (scheme === "dark") && st.overflow <= 1, st)
    if (shots) await page.screenshot({ path: path.join(shots, `mh567-live-${k}.png`), fullPage: true })
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
