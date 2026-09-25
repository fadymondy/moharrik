// Read-only production check for MH-555 / MH-556 on the demo workspace: the mailboxes
// list, the inbox's search, infinite scroll, context menu and keyboard navigation.
// Nothing is created, changed or sent.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-mailbox-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "imc-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const W = `${base}/en/w/${slug}`

// Mailboxes (MH-555)
await page.goto(`${W}/inbox/email`, { waitUntil: "domcontentloaded" })
const addBtn = page.getByRole("button", { name: /add mailbox/i }).first()
await addBtn.waitFor({ timeout: 30000 })
out.mailboxes = await page.locator("[data-mailbox]").count()
check("addMailboxButton", await addBtn.isVisible())
check("mailboxPasswordNeverShown", !(await page.content()).includes('"password":"'))
if (shots) await page.screenshot({ path: path.join(shots, "mh555-mailboxes-en-light-1440.png") })

// Inbox (MH-556)
await page.goto(`${W}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
const first = await page.locator("[data-conversation]").count()
out.firstPage = first
// Infinite scroll: the demo has fewer threads than a page (50), so the cursor walk the list
// uses is proven on the same endpoint with a page of 2.
const wsId = await page.evaluate(async (slug) => {
  const r = await fetch("/api/me/workspaces", { credentials: "include" })
  const j = await r.json()
  const list = j.workspaces ?? j.items ?? j
  return (list.find?.((w) => w.slug === slug) ?? {}).id
}, slug)
const walk = await page.evaluate(async (id) => {
  const seen = []
  let cursor = ""
  let pages = 0
  do {
    const r = await fetch(
      `/api/w/conversations?limit=2${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
      { credentials: "include", headers: { "X-Workspace-ID": id } },
    )
    const j = await r.json()
    pages++
    seen.push(...j.conversations.map((c) => c.id))
    cursor = j.next_cursor ?? ""
  } while (cursor && pages < 20)
  return { pages, total: seen.length, unique: new Set(seen).size }
}, wsId)
out.cursorWalk = walk
check("cursorPaging", walk.pages > 1 && walk.total === first && walk.unique === first, walk)
check("scrollSentinel", (await page.locator("[data-scroll-sentinel]").count()) > 0)
// context menu: right-click a row
await page.locator("[data-conversation]").first().click({ button: "right" })
await page.waitForSelector("[data-action=toggle-unread]", { timeout: 5000 }).catch(() => {})
out.contextItems = await page.$$eval("[role=menu] [data-action]", (els) =>
  els.map((e) => e.getAttribute("data-action")),
)
check(
  "contextMenu",
  out.contextItems.includes("toggle-unread") && out.contextItems.includes("toggle-status"),
  out.contextItems,
)
if (shots) await page.screenshot({ path: path.join(shots, "mh556-context-en-light-1440.png") })
await page.keyboard.press("Escape")
await page.waitForTimeout(300)
// keyboard: j j moves the cursor, Enter opens that thread
await page.goto(`${W}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
const ids = await page.$$eval("[data-conversation]", (els) =>
  els.map((e) => e.getAttribute("data-conversation")),
)
out.activeBeforeKeys = await page.evaluate(
  () => document.activeElement?.tagName + "#" + (document.activeElement?.id || ""),
)
await page.keyboard.press("j")
await page.waitForTimeout(200)
await page.keyboard.press("j")
await page.waitForTimeout(200)
out.cursorRow = await page.evaluate(() =>
  [...document.querySelectorAll("[data-conversation]")].findIndex((e) =>
    e.matches("[data-cursor], [aria-current], [data-active]"),
  ),
)
await page.keyboard.press("Enter")
await page.waitForURL(/[?&]c=/, { timeout: 10000 }).catch(() => {})
check("keyboardNav", page.url().includes(`c=${ids[2]}`), { url: page.url(), want: ids[2] })
// search: the first word of a message body finds its thread
const search = page.locator("[data-search]").first()
await search.fill("coffee")
await page.waitForTimeout(1500)
out.searchHits = await page.locator("[data-conversation]").count()
check("searchWorks", out.searchHits > 0 && out.searchHits <= first, out.searchHits)
await search.fill("zzqq-no-such-thing-zzqq")
await page.waitForTimeout(1500)
check("searchNarrows", (await page.locator("[data-conversation]").count()) === 0)
check("noPageErrors", errs.length === 0, errs)
if (shots) await page.screenshot({ path: path.join(shots, "mh556-search-en-light-1440.png") })

await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
