// Read-only production check for MH-587: the inbox thread renders as WhatsApp-style
// bubbles, and the floating launcher sits on every workspace page except the inbox,
// opening a panel that lists conversations and opens one — en/ar × light/dark × 1440/390.
// Nothing is sent.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/chat-ui-live.mjs https://app.moharrik.com [shots]
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
const shot = async (page, name) => { if (shots) await page.screenshot({ path: path.join(shots, `mh587-live-${name}.png`) }) }
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "chat-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    // The inbox: open the first conversation, expect bubbles and no launcher.
    await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
    await page.waitForSelector("[data-conversation-list] button", { timeout: 30000 }).catch(() => {})
    await page.waitForLoadState("networkidle", { timeout: 15000 }).catch(() => {})
    // A click that lands before the list hydrates only focuses the row; retry once.
    let clicks = 0
    for (; clicks < 2; clicks++) {
      await page.locator("[data-conversation-list] button").first().click().catch(() => {})
      if (await page.waitForSelector("[data-chat-thread] [data-message]", { timeout: 10000 }).then(() => true, () => false)) break
    }
    await page.waitForTimeout(600)
    const inbox = await page.evaluate(() => {
      const b = document.querySelector("[data-chat-thread] [data-message]")
      const cs = b && getComputedStyle(b)
      return {
        thread: !!document.querySelector("[data-chat-thread]"),
        bubbles: document.querySelectorAll("[data-chat-thread] [data-message]").length,
        groups: [...new Set([...document.querySelectorAll("[data-message-group]")].map((e) => e.getAttribute("data-message-group")))],
        rounded: cs ? parseFloat(cs.borderTopLeftRadius) > 4 || parseFloat(cs.borderTopRightRadius) > 4 : false,
        launcher: !!document.querySelector("[data-floating-chat]"),
        dir: document.documentElement.dir, dark: document.documentElement.classList.contains("dark"),
        overflow: document.documentElement.scrollWidth - window.innerWidth,
      }
    })
    inbox.clicks = clicks + 1
    if (inbox.clicks > 1) console.log(`${k}: thread opened on click ${inbox.clicks}`)
    check(`${k}-inbox`, inbox.thread && inbox.bubbles > 0 && inbox.rounded && !inbox.launcher &&
      inbox.dir === (loc === "ar" ? "rtl" : "ltr") && inbox.dark === (scheme === "dark") && inbox.overflow <= 1, inbox)
    await shot(page, `${k}-inbox`)
    // Another page: the launcher is there, opens a panel, and opens a conversation in it.
    await page.goto(`${base}/${loc}/w/${slug}/inbox/channels`, { waitUntil: "domcontentloaded" })
    await page.goto(`${base}/${loc}/w/${slug}`, { waitUntil: "domcontentloaded" })
    await page.waitForSelector("[data-floating-chat]", { timeout: 30000 }).catch(() => {})
    await page.locator("[data-floating-chat]").first().click().catch(() => {})
    await page.waitForSelector("[data-floating-chat-panel]", { timeout: 15000 }).catch(() => {})
    await page.waitForSelector("[data-launcher-conversation]", { timeout: 15000 }).catch(() => {})
    const panel = await page.evaluate(() => {
      const p = document.querySelector("[data-floating-chat-panel]")
      const r = p?.getBoundingClientRect()
      return { panel: !!p, rows: document.querySelectorAll("[data-launcher-conversation]").length,
        left: r ? Math.round(r.left) : null, right: r ? Math.round(window.innerWidth - r.right) : null }
    })
    // The panel docks to the inline-end edge: right in LTR, left in RTL.
    const docked = panel.panel && (w === 390 || (loc === "ar" ? panel.left <= 2 : panel.right <= 2))
    check(`${k}-launcher`, panel.panel && panel.rows > 0 && docked, panel)
    await shot(page, `${k}-launcher`)
    await page.locator("[data-launcher-conversation]").first().click().catch(() => {})
    await page.waitForSelector("[data-floating-chat-panel] [data-message]", { timeout: 20000 }).catch(() => {})
    const opened = await page.evaluate(() => ({
      bubbles: document.querySelectorAll("[data-floating-chat-panel] [data-message]").length,
      back: !!document.querySelector("[data-floating-chat-back]"),
      // The thread fills the panel: its bottom meets the "open the full inbox" footer.
      gap: (() => {
        const t = document.querySelector("[data-floating-chat-panel] [data-chat-thread]")?.getBoundingClientRect()
        const f = document.querySelector("[data-floating-chat-open-inbox]")?.parentElement?.getBoundingClientRect()
        return t && f ? Math.round(f.top - t.bottom) : null
      })(),
    }))
    check(`${k}-launcher-thread`, opened.bubbles > 0 && opened.back && opened.gap !== null && Math.abs(opened.gap) <= 2, opened)
    await shot(page, `${k}-launcher-thread`)
    await page.keyboard.press("Escape").catch(() => {})
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
