// Signed-in check of the collapsible sidebar: node e2e/sidebar-check.mjs [base]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const base = process.argv[2] ?? "http://localhost:3120"
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "sb-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, viewport: { width: 1440, height: 900 }, colorScheme: "dark",
})
const page = ctx.pages()[0]
const email = `sb${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email); await page.fill("#password", "Passw0rd!long-enough")
const name = page.locator("#name"); if (await name.count()) await name.fill("SB")
await page.click('button[type=submit]'); await page.waitForURL(/workspaces|\/w\//, { timeout: 20000 })
if (!/\/w\//.test(page.url())) { await page.fill("#ws-name", "SB " + Date.now()); await page.click('button[type=submit]'); await page.waitForURL(/\/w\//, { timeout: 20000 }) }
await page.waitForSelector("[data-nav-toggle]")
const w = () => page.evaluate(() => ({ nav: Math.round(document.querySelector(".grid-shell-nav").getBoundingClientRect().width), links: [...document.querySelectorAll(".grid-shell-nav nav a")].map((a) => a.getAttribute("href").split("/w/")[1]), banner: !!document.querySelector('[role=status].hatch-band'), sw: document.documentElement.scrollWidth }))
const out = { full: await w() }
await page.click("[data-nav-toggle]"); await page.waitForTimeout(400); out.mini = await w()
await page.screenshot({ path: "E:/tmp/mshots/sb-mini.png" })
await page.reload({ waitUntil: "networkidle" }); await page.waitForSelector("[data-nav-toggle]"); out.afterReload = (await w()).nav
await page.keyboard.press("Control+b"); await page.waitForTimeout(400); out.afterCtrlB = (await w()).nav
await page.goto(page.url().replace("/en/", "/ar/"), { waitUntil: "networkidle" }); await page.click("[data-nav-toggle]"); await page.waitForTimeout(400)
out.arMini = await page.evaluate(() => { const r = document.querySelector(".grid-shell-nav").getBoundingClientRect(); return { w: Math.round(r.width), right: Math.round(r.right) } })
for (const l of out.full.links) { const r = await page.request.get(`${base}/en/w/${l}`); out["status:" + l] = r.status() }
console.log(JSON.stringify(out, null, 1)); await ctx.close()
