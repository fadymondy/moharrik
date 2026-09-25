// Signed-in sweep of the marketing pages on dev.moharrik.com (en + ar): status, raw keys, page errors.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const base = "https://dev.moharrik.com"
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "dp-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
})
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 120)))
await page.goto(`${base}/en/login`, { waitUntil: "networkidle" })
await page.fill("#email", process.env.E)
await page.fill("#password", process.env.P)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  if (await page.locator('a[href*="/w/"]').count())
    await page.locator('a[href*="/w/"]').first().click()
  else {
    await page.fill("#ws-name", "Dev " + Date.now())
    await page.click("button[type=submit]")
  }
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]
const out = { slug }
for (const loc of ["en", "ar"])
  for (const p of ["", "/contacts", "/campaigns", "/settings/email"]) {
    const r = await page.goto(`${base}/${loc}/w/${slug}${p}`, { waitUntil: "networkidle" })
    await page.waitForTimeout(800)
    const txt = await page.evaluate(() => document.body.innerText)
    out[`${loc}${p || "/"}`] = {
      s: r.status(),
      raw: (txt.match(/\b[a-z]+\.[a-z][a-zA-Z]+\b/g) || [])
        .filter((k) => !/moharrik\.com|3x1\.io|example\.|company\.com|next\.js/i.test(k))
        .slice(0, 4),
    }
    if (loc === "en" && p === "/campaigns")
      await page.screenshot({ path: "E:/tmp/mshots/dev-campaigns.png" })
  }
out.nav = await page.evaluate(() =>
  [...document.querySelectorAll(".grid-shell-nav nav a")].map((a) => a.textContent.trim()),
)
out.errors = errs
console.log(JSON.stringify(out, null, 1))
await ctx.close()
