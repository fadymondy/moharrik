// Read-only production check for MH-590: an imported conversation's row in the inbox
// list shows its newest message's date, the same day the thread's separator shows.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-dates-live.mjs https://app.moharrik.com
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "dates-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
  viewport: { width: 1440, height: 900 }, timezoneId: "Africa/Cairo",
})
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForLoadState("networkidle", { timeout: 15000 }).catch(() => {})
await page.waitForSelector("[data-conversation-list] button", { timeout: 30000 })
// Every row whose thread holds one day's messages: the row's date must be that day.
const rows = page.locator("[data-conversation-list] > *")
const bad = []
let checked = 0
for (let i = 0; i < Math.min(6, await rows.count()); i++) {
  const row = rows.nth(i)
  const rowText = (await row.innerText()).replace(/\s+/g, " ")
  const rowDate = rowText.match(/[A-Z][a-z]{2} \d{1,2}, \d{4}/)?.[0]
  await row.locator("button").first().click()
  await page.waitForSelector("[data-chat-thread] [data-day-separator]", { timeout: 20000 }).catch(() => {})
  const days = await page.$$eval("[data-chat-thread] [data-day-separator]", (els) => els.map((e) => e.textContent.trim()))
  if (!rowDate || !days.length) continue
  checked++
  const last = new Date(days[days.length - 1]), shown = new Date(rowDate)
  // "Today"/"Yesterday" labels are not dates; only compare real ones.
  if (!isNaN(last) && last.toDateString() !== shown.toDateString()) bad.push({ row: rowText.slice(0, 40), rowDate, lastDay: days[days.length - 1] })
}
await ctx.close()
console.log(JSON.stringify({ checked, bad }, null, 1))
console.log(checked > 0 && !bad.length ? "ALL OK" : "FAIL")
process.exit(checked > 0 && !bad.length ? 0 : 1)
