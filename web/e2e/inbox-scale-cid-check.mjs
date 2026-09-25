// MH-556 remaining proof, against a local stack seeded with 1,200+
// conversations and an email carrying an inline cid: image:
//  - the list is windowed: scrolling deep through many pages keeps the live
//    DOM rows small while the scrollable height grows with what is loaded;
//  - the inline image resolves to its attachment and actually loads.
// en/ar × light/dark × 1440/390, screenshots of each.
//   DEMO_EMAIL=... DEMO_PASSWORD=... DEMO_SLUG=... node e2e/inbox-scale-cid-check.mjs http://localhost:3131 [shots]
import { chromium } from "playwright-core"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3131"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const b = await chromium.launch({ executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true })

for (const scheme of ["light", "dark"]) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`); await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 60000 })

  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
    await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 60000 })
    const box = await (await page.$("[data-conversation-list]")).boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + Math.min(box.height / 2, 200))
    let peakRows = 0
    for (let i = 0; i < 60; i++) {
      await page.mouse.wheel(0, 1500); await page.waitForTimeout(150)
      peakRows = Math.max(peakRows, await page.$$eval("[data-conversation-list] > li", (l) => l.length))
    }
    await page.waitForTimeout(1200)
    const s = await page.$eval("[data-conversation-list]", (el) => ({ rows: el.querySelectorAll(":scope > li").length, height: el.scrollHeight, top: el.scrollTop, rowH: el.querySelector(":scope > li")?.getBoundingClientRect().height ?? 0 }))
    const loaded = Math.round(s.height / Math.max(s.rowH, 1))
    // Deep: well past the first pages. Windowed: live rows a small fraction of what is loaded.
    check(`${k}-windowed`, loaded >= 200 && peakRows <= 60 && s.top > 0, { ...s, loaded, peakRows })

    await page.goto(`${base}/${loc}/w/${slug}/inbox?channel=email`, { waitUntil: "load" })
    await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 30000 })
    await page.locator("[data-conversation-list] [data-conversation]").first().click()
    const img = await page.waitForFunction(() => {
      for (const f of document.querySelectorAll("iframe")) {
        const i = f.contentDocument?.querySelector('img[alt="inline logo"]')
        if (i && i.complete && i.naturalWidth > 0) return { src: i.getAttribute("src").slice(0, 40), w: i.naturalWidth }
      }
      return null
    }, null, { timeout: 15000 }).then((h) => h.jsonValue(), () => null)
    check(`${k}-cid-image`, !!img && !img.src.startsWith("cid:") && img.w === 40, img)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
    check(`${k}-overflow`, overflow <= 1, overflow)
    if (shots) await page.screenshot({ path: path.join(shots, `mh556-${k}.png`) })
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
await b.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
