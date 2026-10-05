// Read-only production check for MH-571: one Channels page with type/status filters, the
// old per-type pages redirecting to it, one sidebar entry — en/ar × light/dark × 1440/390.
// Nothing is connected, edited or deleted.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/channels-page-live.mjs https://app.moharrik.com [shots]
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
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "chp-")), {
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
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/settings/integrations`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-channel-row]", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(800)
      const k = `${loc}-${scheme}-${w}`
      const r = await page.evaluate(() => ({
        rows: document.querySelectorAll("[data-channel-row]").length,
        types: [
          ...new Set(
            [...document.querySelectorAll("[data-channel-row]")].map((e) =>
              e.getAttribute("data-channel-type"),
            ),
          ),
        ],
        typeFilters: document.querySelectorAll("[data-type-filter]").length,
        statusFilters: document.querySelectorAll("[data-status-filter]").length,
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        navChannels: [...document.querySelectorAll("a[href]")].filter((a) =>
          /\/inbox\/channels$/.test(a.getAttribute("href")),
        ).length,
        navOld: [...document.querySelectorAll("a[href]")].filter((a) =>
          /\/inbox\/(telegram|slack|discord|widgets|workflow-channels|email|whatsapp)$/.test(
            a.getAttribute("href"),
          ),
        ).length,
        overflow: document.documentElement.scrollWidth - window.innerWidth,
      }))
      check(
        k,
        r.rows > 0 &&
          r.typeFilters >= 7 &&
          r.statusFilters > 1 &&
          r.dir === (loc === "ar" ? "rtl" : "ltr") &&
          r.dark === (scheme === "dark") &&
          r.navOld === 0 &&
          r.overflow <= 1 &&
          (w === 390 || r.navChannels >= 1),
        r,
      )
      if (shots) await page.screenshot({ path: path.join(shots, `mh571-live-${k}.png`) })
      if (w === 1440 && loc === "en") {
        await page.click('[data-type-filter="chat"]')
        await page.waitForTimeout(1200)
        const f = await page.evaluate(() => ({
          url: location.search,
          types: [
            ...new Set(
              [...document.querySelectorAll("[data-channel-row]")].map((e) =>
                e.getAttribute("data-channel-type"),
              ),
            ),
          ],
        }))
        check(
          `${k}-filter`,
          /type=chat/.test(f.url) && f.types.length === 1 && f.types[0] === "chat",
          f,
        )
        await page.goto(`${base}/en/w/${slug}/inbox/widgets`, { waitUntil: "domcontentloaded" })
        await page.waitForURL(/inbox\/channels\?type=chat/, { timeout: 20000 }).catch(() => {})
        check(`${k}-redirect`, /inbox\/channels\?type=chat/.test(page.url()), page.url())
      }
    }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
