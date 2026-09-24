// Read-only production check for MH-563 / MH-564 on the demo workspace: the Telegram, Slack,
// Discord and workflow-channel pages at 1440 and 390, light and dark, en and ar.
// Nothing is connected or sent.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/channels-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const pages = ["telegram", "slack", "discord", "workflow-channels"]
const out = {}, fail = []
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const browser = await chromium.launch({ executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args })

for (const scheme of ["light", "dark"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    for (const lang of ["en", "ar"]) for (const p of pages) {
      const key = `${p}-${lang}-${scheme}-${width}`
      const res = await page.goto(`${base}/${lang}/w/${slug}/inbox/${p}`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("main h1, h1", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(800)
      const r = await page.evaluate(() => ({
        h1: document.querySelector("h1")?.textContent?.trim() ?? "",
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        notFound: /not found|404/i.test(document.title),
      }))
      const ok = res?.status() < 400 && r.h1 && !r.notFound && r.overflow <= 1 &&
        (r.dir === (lang === "ar" ? "rtl" : "ltr")) && r.dark === (scheme === "dark")
      out[key] = ok ? true : { status: res?.status(), ...r }
      if (!ok) fail.push(key)
      if (shots) await page.screenshot({ path: path.join(shots, `${key}.png`) })
    }
  }
  if (errs.length) { out[`pageErrors-${scheme}`] = errs; fail.push(`pageErrors-${scheme}`) }
  await ctx.close()
}
await browser.close()
console.log(JSON.stringify(Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)), null, 1))
console.log(`${Object.values(out).filter((v) => v === true).length}/${Object.keys(out).length} ok`)
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
