import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "lv-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
  locale: "ar",
})
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto("https://app.moharrik.com/ar/login", { waitUntil: "networkidle" })
await page.fill("#email", process.env.E)
await page.fill("#password", process.env.P)
await page.click("button[type=submit]")
await page.waitForURL(/\/w\/|workspaces/, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.click('a[href*="/w/"]')
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
await page.waitForSelector("[data-nav-toggle]")
const read = () =>
  page.evaluate(() => ({
    nav: Math.round(document.querySelector(".grid-shell-nav").getBoundingClientRect().width),
    labels: [...document.querySelectorAll(".grid-shell-nav nav a")].map((a) =>
      a.textContent.trim(),
    ),
    rawKeys: document.body.innerText.match(/\b(nav|dev|shell)\.[a-zA-Z]+/g) || [],
    banner: !!document.querySelector("[role=status]"),
    sw: document.documentElement.scrollWidth,
  }))
const out = { full: await read() }
await page.click("[data-nav-toggle]")
await page.waitForTimeout(500)
out.mini = await read()
await page.screenshot({ path: "E:/tmp/mshots/live-mini-ar.png" })
await page.click("[data-nav-toggle]")
await page.waitForTimeout(500)
await page.screenshot({ path: "E:/tmp/mshots/live-full-ar.png" })
out.errors = errs
console.log(JSON.stringify(out, null, 1))
await ctx.close()
