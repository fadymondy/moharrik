// MH-601: no scroll area draws a scrollbar, and scrolling still works (wheel on the
// inbox list). Headless Chrome hides scrollbars by default (--hide-scrollbars), which
// would pass this vacuously, so that flag is dropped. en/ar × light/dark × 1440/390.
//   DEMO_EMAIL=... DEMO_PASSWORD=... DEMO_SLUG=... node e2e/scrollbars-hidden.mjs <base> [shots]
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
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh601-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args, ignoreDefaultArgs: ["--hide-scrollbars"],
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
    await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 30000 }).catch(() => {})
    await page.waitForTimeout(1000)
    // Every element that overflows: its scrollbar takes no room.
    const bars = await page.evaluate(() => [...document.querySelectorAll("*")].filter((el) => {
      const cs = getComputedStyle(el)
      const scrolls = /auto|scroll/.test(cs.overflowY + cs.overflowX) && (el.scrollHeight > el.clientHeight || el.scrollWidth > el.clientWidth)
      return scrolls && (el.offsetWidth - el.clientWidth - parseFloat(cs.borderLeftWidth) - parseFloat(cs.borderRightWidth) > 0 ||
        el.offsetHeight - el.clientHeight - parseFloat(cs.borderTopWidth) - parseFloat(cs.borderBottomWidth) > 0)
    }).map((el) => el.tagName + (el.getAttribute("data-slot") ? `[${el.getAttribute("data-slot")}]` : "")))
    check(`${k}-no-bars`, bars.length === 0, bars)
    const box = await (await page.$("[data-conversation-list]")).boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + Math.min(box.height / 2, 200))
    for (let i = 0; i < 5; i++) { await page.mouse.wheel(0, 400); await page.waitForTimeout(100) }
    await page.waitForTimeout(800)
    const s = await page.$eval("[data-conversation-list]", (el) => ({ top: el.scrollTop, room: el.scrollHeight - el.clientHeight }))
    check(`${k}-list-scrolls`, s.room <= 0 || s.top > 0, s)
    if (shots) await page.screenshot({ path: path.join(shots, `mh601-${k}.png`) })
  }
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
