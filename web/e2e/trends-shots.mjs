// Screenshots of the trends pages for MH-385 / MH-387: en + ar, light + dark,
// 1440 + 390, as the demo workspace sees them.
//
//   node e2e/trends-shots.mjs <base> <out-dir> <prefix> <path...>
//
// <path> is relative to /<locale>/w/<slug>/, e.g. "trends" or "trends/sources".
// DEMO_PASSWORD signs in as demo@moharrik.com; SHOT_THEMES / SHOT_WIDTHS /
// SHOT_LOCALES narrow the matrix ("light,dark", "1440,390", "en,ar").
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const [base = "http://localhost:3505", out = ".", prefix = "shot", ...paths] = process.argv.slice(2)
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const themes = (process.env.SHOT_THEMES ?? "light,dark").split(",")
const widths = (process.env.SHOT_WIDTHS ?? "1440,390").split(",").map(Number)
const locales = (process.env.SHOT_LOCALES ?? "en,ar").split(",")

for (const theme of themes) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "tshots-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    colorScheme: theme,
  })
  const page = await ctx.newPage()
  await page.goto(`${base}/en/login`, { waitUntil: "networkidle" })
  await page.fill("#email", process.env.DEMO_EMAIL ?? "demo@moharrik.com")
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  for (const locale of locales) {
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 })
      for (const p of paths) {
        await page.goto(`${base}/${locale}/w/${slug}/${p}`, { waitUntil: "load" })
        // Not networkidle: the workspace event stream stays open by design.
        await page.waitForTimeout(3000)
        const name = `${prefix}-${p.replaceAll("/", "-")}-${locale}-${theme}-${width}.png`
        await page.screenshot({
          path: path.join(out, name),
          fullPage: process.env.SHOT_FULL !== "0",
        })
        console.log(name)
      }
    }
  }
  await ctx.close()
}
