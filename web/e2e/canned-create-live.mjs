// Creates the demo's canned replies through the real UI (inbox → Canned replies → Add),
// skipping any that already exist. DEMO_EMAIL/DEMO_PASSWORD; argv[2] = base.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"; import { tmpdir } from "node:os"; import path from "node:path"
const base = process.argv[2] ?? "https://app.moharrik.com"
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const CANNED = [
  ["Opening hours", "We're open every day from 8 am to 11 pm, and until midnight on Thursdays and Fridays.", "نفتح يوميًا من 8 صباحًا حتى 11 مساءً، وحتى منتصف الليل يومي الخميس والجمعة."],
  ["Wholesale enquiry", "Thanks for your interest in wholesale! Could you share your café's name, city and roughly how many kilos a month you need? Our team will reply within one working day.", "شكرًا لاهتمامك بالجملة! ممكن تشاركنا اسم المقهى والمدينة وتقريبًا كم كيلو تحتاج شهريًا؟ فريقنا هيرد عليك خلال يوم عمل."],
  ["Order status", "Let me check that for you. Could you send your order number? It's in the confirmation email.", "خليني أتأكد لك. ممكن تبعت رقم الطلب؟ هتلاقيه في إيميل التأكيد."],
]
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ccl-")), { executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
await page.goto(`${base}/en/w/${slug}/inbox/canned`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-action="add-canned"]:not([disabled])', { timeout: 30000 })
for (const [title, en, ar] of CANNED) {
  if (await page.locator(`[data-canned-row="${title}"]`).count()) { console.log(`${title}: exists`); continue }
  await page.click('[data-action="add-canned"]')
  await page.fill("#canned-title", title); await page.fill("#canned-en", en); await page.fill("#canned-ar", ar)
  await page.click('form[data-form="canned"] button[type=submit]')
  await page.waitForSelector(`[data-canned-row="${title}"]`, { timeout: 15000 })
  console.log(`${title}: created`)
}
await ctx.close()
