// Read-only production check for MH-592: the inbox list asks for ten,
// unread first, and scrolls for more; its search sits in the list column; an
// email thread is full-width cards with its images loaded. en/ar × light/dark
// × 1440/390. Nothing is changed. The list checks run on the whole inbox; the
// email check runs only when the workspace has an email thread (the demo has
// none), and says so rather than passing.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-mh592-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh592-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  // Every list page the inbox asks for, with what it answered.
  const lists = []
  page.on("response", async (r) => {
    const u = new URL(r.url())
    if (u.pathname !== "/api/w/conversations" || r.request().method() !== "GET") return
    try {
      const b = await r.json()
      lists.push({ q: Object.fromEntries(u.searchParams), n: b.conversations?.length ?? -1, unread: (b.conversations ?? []).map((c) => c.unread), next: !!b.next_cursor })
    } catch {}
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    lists.length = 0
    await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
    await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 30000 }).catch(() => {})
    await page.waitForTimeout(1200)

    // The first page: ten, unread first.
    const first = lists.find((l) => !l.q.cursor)
    const unreadOrdered = first ? first.unread.every((u, i) => i === 0 || !u || first.unread[i - 1]) : false
    check(`${k}-page`, !!first && first.q.limit === "10" && first.q.order === "unread_first" && first.n <= 10 && unreadOrdered, first)

    // The search lives in the list column.
    const searchInColumn = await page.evaluate(() => !!document.querySelector("[data-conversation-column] [data-search=conversations]"))
    check(`${k}-search`, searchInColumn)

    // Infinite scroll: reaching the bottom of the list asks for the next page.
    if (first?.next) {
      await page.$eval("[data-conversation-list]", (el) => el.scrollTo({ top: el.scrollHeight }))
      await page.waitForTimeout(2500)
      const more = lists.find((l) => l.q.cursor)
      check(`${k}-scroll`, !!more && more.q.order === "unread_first", { requests: lists.length })
    }

    // An email thread: full-width cards, no blocked-images bar.
    await page.goto(`${base}/${loc}/w/${slug}/inbox?channel=email`, { waitUntil: "load" })
    const hasMail = await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 8000 }).then(() => true, () => false)
    if (!hasMail) out[`${k}-email`] = "skipped: no email thread in this workspace"
    else {
      await page.locator("[data-conversation-list] [data-conversation]").first().click()
      await page.waitForSelector("[data-message-layout=email]", { timeout: 20000 }).catch(() => {})
      await page.waitForTimeout(1500)
      const mail = await page.evaluate(() => {
        const pane = document.querySelector("[data-messages]")
        const card = document.querySelector("[data-message-layout=email]")
        if (!pane || !card) return null
        const padding = parseFloat(getComputedStyle(pane).paddingLeft) + parseFloat(getComputedStyle(pane).paddingRight)
        return {
          ratio: +(card.getBoundingClientRect().width / (pane.clientWidth - padding)).toFixed(3),
          blocked: !!document.querySelector("[data-images-blocked]"),
          bubbles: document.querySelectorAll("[data-message]:not([data-message-layout=email])").length,
        }
      })
      check(`${k}-email`, !!mail && mail.ratio > 0.97 && !mail.blocked && mail.bubbles === 0, mail)
    }

    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    check(`${k}-overflow`, overflow <= 1, overflow)
    if (shots) await page.screenshot({ path: path.join(shots, `mh592-live-${k}.png`) })
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
