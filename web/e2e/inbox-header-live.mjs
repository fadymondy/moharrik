// Read-only production check for MH-581: the inbox header no longer repeats the sidebar's
// Stats / Canned replies / channel links, and the sidebar still has every one of them.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-header-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const subs = ["stats", "canned", "email", "whatsapp", "widgets"]
const out = {},
  fail = []
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const browser = await chromium.launch({
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
})
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
for (const [lang, width] of [
  ["en", 1440],
  ["ar", 1440],
  ["en", 390],
]) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
  await page.goto(`${base}/${lang}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
  await page.waitForSelector("[data-conversation]", { timeout: 30000 })
  const r = await page.evaluate((subs) => {
    const hrefs = (root) =>
      [...(root?.querySelectorAll("a[href]") ?? [])].map((a) => a.getAttribute("href"))
    const main = document.querySelector("main")
    // the sidebar is whatever navigation sits outside <main>
    const outside = [...document.querySelectorAll("a[href]")]
      .filter((a) => !main?.contains(a))
      .map((a) => a.getAttribute("href"))
    const inMain = subs.filter((s) => hrefs(main).some((h) => h.endsWith(`/inbox/${s}`)))
    const inSide = subs.filter((s) => outside.some((h) => h.endsWith(`/inbox/${s}`)))
    return { inMain, inSide }
  }, subs)
  const key = `${lang}-${width}`
  const ok = r.inMain.length === 0 && (width === 390 || r.inSide.length === subs.length)
  out[key] = ok ? true : r
  if (!ok) fail.push(key)
  if (shots) await page.screenshot({ path: path.join(shots, `mh581-inbox-${key}.png`) })
}
await browser.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
