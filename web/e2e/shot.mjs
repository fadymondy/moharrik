// Ad-hoc screenshots: node e2e/shot.mjs <url> <out.png> [width] [theme]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const [url, out, width = "1440", theme = "light"] = process.argv.slice(2)
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "shot-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: +width, height: 900 },
  colorScheme: theme,
})
const page = ctx.pages()[0]
await page.goto(url, { waitUntil: "networkidle" })
await page.waitForTimeout(800)
await page.screenshot({ path: out, fullPage: true })
const info = await page.evaluate(() => {
  const f = document.querySelector("form")
  return {
    title: document.title,
    form: f ? Math.round(f.getBoundingClientRect().width) : null,
    sw: document.documentElement.scrollWidth,
  }
})
console.log(JSON.stringify(info))
await ctx.close()
