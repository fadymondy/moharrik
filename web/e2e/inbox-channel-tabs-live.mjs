// Read-only production check for MH-568: the inbox's data-driven channel tabs, and
// "[" / "]" stepping between them, en/ar × light/dark × 1440/390. Nothing is changed.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-channel-tabs-live.mjs https://app.moharrik.com [shots]
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
const active = (page) =>
  page.evaluate(() =>
    document
      .querySelector(
        "[data-channel-tab][aria-selected=true], [data-channel-tab][data-active], [data-channel-tab][data-selected]",
      )
      ?.getAttribute("data-channel-tab"),
  )
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ict-")), {
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
  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-channel-tab]", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(1000)
      const k = `${loc}-${scheme}-${w}`
      const tabs = await page.$$eval("[data-channel-tab]", (els) =>
        els.map((e) => e.getAttribute("data-channel-tab")),
      )
      const r = {
        tabs,
        overflow: await page.evaluate(
          () => document.documentElement.scrollWidth - window.innerWidth,
        ),
      }
      if (w === 1440) {
        await page
          .locator("body")
          .click({ position: { x: 5, y: 5 } })
          .catch(() => {})
        r.start = await active(page)
        await page.keyboard.press("]")
        await page.waitForTimeout(600)
        r.afterNext = await active(page)
        await page.keyboard.press("[")
        await page.waitForTimeout(600)
        r.afterPrev = await active(page)
        check(`${k}-keys`, tabs.length > 1 && r.afterNext === tabs[1] && r.afterPrev === tabs[0], r)
      }
      check(k, tabs[0] === "all" && tabs.length > 1 && r.overflow <= 1, r)
      if (shots) await page.screenshot({ path: path.join(shots, `mh568-live-${k}.png`) })
    }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
