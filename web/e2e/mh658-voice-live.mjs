// MH-658 live, read-only: a real inbound WhatsApp voice note in the demo
// workspace renders as the voice player, its audio loads with a duration, and
// play advances it — en/ar × light/dark × 1440/390. Fails on any API write
// other than marking an already-read thread read.
//   DEMO_EMAIL=... DEMO_PASSWORD=... CONV=<id> node e2e/mh658-voice-live.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const conv = process.env.CONV
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh658-"))
  const ctx = await chromium.launchPersistentContext(dir, {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args: [...args, "--autoplay-policy=no-user-gesture-required"],
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = [],
    writes = []
  page.on("pageerror", (e) => errs.push(String(e)))
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/") && !/\/read$/.test(u.pathname))
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.waitForLoadState("networkidle").catch(() => {})
  await page.waitForTimeout(1500)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, { timeout: 30000 })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${conv}`, { waitUntil: "domcontentloaded" })
      // The thread is virtualised and anchors to the newest message once link previews settle, so
      // the voice note can unmount; scroll up until it is rendered and stays rendered for a second.
      await page.waitForSelector("[data-messages]", { timeout: 30000 }).catch(() => {})
      await page.waitForLoadState("networkidle").catch(() => {})
      await page.waitForTimeout(1500)
      const shown = await page.evaluate(async () => {
        const pane = document.querySelector("[data-messages]")
        const sleep = (ms) => new Promise((z) => setTimeout(z, ms))
        for (let i = 0; pane && i < 60; i++) {
          const el = document.querySelector("[data-attachment-voice]")
          if (el) {
            el.scrollIntoView({ block: "center" })
            await sleep(1000)
            if (document.querySelector("[data-attachment-voice] audio")) return true
          } else {
            pane.scrollBy(0, -pane.clientHeight * 0.6)
            await sleep(300)
          }
        }
        return false
      })
      check(`${k}-voice-player`, shown)
      if (!shown) continue
      const meta = await page.evaluate(async () => {
        // The player can re-render after hydration; look it up again until it is attached.
        let box
        for (let i = 0; i < 100 && !(box = document.querySelector("[data-attachment-voice]"))?.querySelector("audio"); i++)
          await new Promise((z) => setTimeout(z, 100))
        const au = box.querySelector("audio")
        for (let i = 0; i < 100 && !(au.duration > 0); i++) await new Promise((z) => setTimeout(z, 100))
        const r = await fetch(au.currentSrc || au.src)
        return {
          status: r.status,
          type: r.headers.get("content-type"),
          bytes: (await r.arrayBuffer()).byteLength,
          duration: au.duration,
          label: box.querySelector("span")?.textContent,
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflowX: document.documentElement.scrollWidth > innerWidth + 1,
        }
      })
      out[`${k}-meta`] = meta
      check(`${k}-audio-200`, meta.status === 200 && /audio|ogg|octet/.test(meta.type ?? "") && meta.bytes > 1000, meta)
      check(`${k}-duration`, meta.duration > 0 && meta.label !== "0:00", meta)
      check(`${k}-dir`, meta.dir === (loc === "ar" ? "rtl" : "ltr"), meta.dir)
      check(`${k}-theme`, meta.dark === (scheme === "dark"), meta.dark)
      check(`${k}-no-overflow`, !meta.overflowX)
      const played = await page.locator("[data-attachment-voice] button").first().click({ timeout: 15000 }).then(() => true, async (e) => {
        out[`${k}-click-state`] = await page.evaluate(() => ({
          url: location.pathname + location.search,
          players: document.querySelectorAll("[data-attachment-voice]").length,
          buttons: document.querySelectorAll("[data-attachment-voice] button").length,
        }))
        if (shots) await page.screenshot({ path: `${shots}/mh658-voice-${k}-noclick.png` })
        return false
      })
      check(`${k}-play-button`, played)
      if (!played) continue
      await page.waitForTimeout(1500)
      const t = await page.evaluate(() => document.querySelector("[data-attachment-voice] audio").currentTime)
      check(`${k}-plays`, t > 0, t)
      if (shots) {
        await page.locator("[data-attachment-voice]").first().scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${shots}/mh658-voice-${k}.png` })
      }
      await page.locator("[data-attachment-voice] button").first().click()
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
