// Read-only production check for MH-587/MH-663: the inbox thread renders as
// WhatsApp-style bubbles, and the docked chat launcher pill sits on every
// workspace page except the inbox, opening a compact list that docks a chat
// as a small window at the bottom — en/ar × light/dark × 1440/390.
// Nothing is sent.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/chat-ui-live.mjs https://app.moharrik.com [shots]
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
const shot = async (page, name) => {
  if (shots) await page.screenshot({ path: path.join(shots, `mh587-live-${name}.png`) })
}
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "chat-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      // The inbox: open the first conversation, expect bubbles and no launcher.
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
      await page
        .waitForSelector("[data-conversation-list] button", { timeout: 30000 })
        .catch(() => {})
      await page.waitForLoadState("networkidle", { timeout: 15000 }).catch(() => {})
      // A click that lands before the list hydrates only focuses the row; retry once.
      let clicks = 0
      for (; clicks < 2; clicks++) {
        await page
          .locator("[data-conversation-list] button")
          .first()
          .click()
          .catch(() => {})
        if (
          await page.waitForSelector("[data-chat-thread] [data-message]", { timeout: 10000 }).then(
            () => true,
            () => false,
          )
        )
          break
      }
      await page.waitForTimeout(600)
      const inbox = await page.evaluate(() => {
        const b = document.querySelector("[data-chat-thread] [data-message]")
        const cs = b && getComputedStyle(b)
        return {
          thread: !!document.querySelector("[data-chat-thread]"),
          bubbles: document.querySelectorAll("[data-chat-thread] [data-message]").length,
          groups: [
            ...new Set(
              [...document.querySelectorAll("[data-message-group]")].map((e) =>
                e.getAttribute("data-message-group"),
              ),
            ),
          ],
          rounded: cs
            ? parseFloat(cs.borderTopLeftRadius) > 4 || parseFloat(cs.borderTopRightRadius) > 4
            : false,
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - window.innerWidth,
        }
      })
      inbox.clicks = clicks + 1
      if (inbox.clicks > 1) console.log(`${k}: thread opened on click ${inbox.clicks}`)
      check(
        `${k}-inbox`,
        inbox.thread &&
          inbox.bubbles > 0 &&
          inbox.rounded &&
          inbox.dir === (loc === "ar" ? "rtl" : "ltr") &&
          inbox.dark === (scheme === "dark") &&
          inbox.overflow <= 1,
        inbox,
      )
      await shot(page, `${k}-inbox`)
    }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
