// Is https://dev.moharrik.com the live `next dev` (HMR) and does a signed-in page render?
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const base = process.argv[2] ?? "https://dev.moharrik.com"
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "dv-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
})
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
const failed = []
page.on("response", (r) => {
  if (r.status() >= 400 && !r.url().includes("/api/auth/me"))
    failed.push(r.status() + " " + r.url().slice(0, 90))
})
await page.goto(`${base}/en/login`, { waitUntil: "networkidle" })
const hmr = await page.evaluate(() =>
  [...document.scripts].some((s) => /hmr-client|turbopack/.test(s.src)),
)
await page.fill("#email", process.env.E)
await page.fill("#password", process.env.P)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
await page.waitForTimeout(1500)
await page.screenshot({ path: "E:/tmp/mshots/dev-signed-in.png" })
console.log(
  JSON.stringify({
    hmr,
    url: page.url().replace(base, ""),
    title: await page.title(),
    errors: errs,
    failed,
  }),
)
await ctx.close()
