// MH-659 / MH-660 live, read-only: on a WhatsApp conversation the composer
// carries attach, emoji, location and mic next to send; the emoji picker and
// the attach menu open and close (nothing is picked, typed or sent, so no
// presence goes out either); nothing overflows. en/ar × light/dark × 1440/390.
// Only GETs; safe on production.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh659-live.mjs <base> [shots]
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
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh659-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  // Guard: this check never writes. Any non-GET to the API outside auth fails the run.
  const writes = []
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (
      r.method() !== "GET" &&
      u.pathname.startsWith("/api/") &&
      !u.pathname.startsWith("/api/auth/")
    )
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  const conv = await page.evaluate(async (slug) => {
    const h = { headers: { "X-Workspace-ID": slug } }
    const c = await fetch("/api/w/conversations?limit=100&channel=whatsapp&status=", h)
    const cj = await c.json().catch(() => ({}))
    const all = [...(cj.pinned ?? []), ...(cj.conversations ?? cj.items ?? [])]
    return all.find((x) => x.channel === "whatsapp")?.id ?? null
  }, slug)
  check(`${scheme}-has-whatsapp-conversation`, !!conv)

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      if (!conv) break
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${conv}`, { waitUntil: "domcontentloaded" })
      const form = await page.waitForSelector('[data-form="reply"]', { timeout: 30000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-composer`, form)
      if (!form) continue
      const f = page.locator('[data-form="reply"]').first()
      for (const a of ["attach", "emoji", "location", "mic", "send"]) {
        const b = f.locator(`[data-action="${a}"]`)
        const vis = (await b.count()) > 0 && (await b.first().isVisible())
        check(`${k}-${a}-visible`, vis)
      }
      // Emoji picker: open, see results, close. Nothing inserted.
      await f.locator('[data-action="emoji"]').first().click()
      const emoji = await page
        .waitForSelector("[data-emoji-picker] [data-emoji]", { timeout: 10000 })
        .then(
          () => true,
          () => false,
        )
      const emojis = await page.locator("[data-emoji-picker] [data-emoji]").count()
      check(`${k}-emoji-picker`, emoji && emojis > 50, emojis)
      await page.waitForTimeout(300)
      if (shots) await page.screenshot({ path: `${shots}/mh659-live-${k}-emoji.png` })
      await page.keyboard.press("Escape")
      await page.waitForTimeout(300)
      // Attach menu: open, see the document entry, close. No file chosen.
      await f.locator('[data-action="attach"]').first().click()
      const att = await page.waitForSelector('[data-attach="document"]', { timeout: 10000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-attach-menu`, att)
      await page.waitForTimeout(300)
      // Each entry sits on one line: a menu as narrow as its icon button wraps them.
      const rows = await page
        .locator("[data-attach]")
        .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().height)))
      check(`${k}-attach-one-line`, rows.length > 0 && rows.every((h) => h <= 40), rows)
      if (shots) await page.screenshot({ path: `${shots}/mh659-live-${k}-attach.png` })
      await page.keyboard.press("Escape")
      await page.waitForTimeout(300)
      const pg = await page.evaluate(() => ({
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflowX: document.documentElement.scrollWidth > innerWidth + 1,
        formOverflow: (() => {
          const el = document.querySelector('[data-form="reply"]')
          return el ? el.scrollWidth > el.clientWidth + 1 : null
        })(),
      }))
      check(`${k}-dir`, pg.dir === (loc === "ar" ? "rtl" : "ltr"), pg.dir)
      check(`${k}-theme`, pg.dark === (scheme === "dark"), pg.dark)
      check(`${k}-no-overflow`, !pg.overflowX && !pg.formOverflow, pg)
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
