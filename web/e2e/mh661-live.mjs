// MH-661 live, read-only: the email templates page and, on an email
// conversation, the reply's subject field and template picker (opened, read,
// closed — no template is picked, nothing is sent) in en/ar × light/dark ×
// 1440/390. Only GETs; safe on production.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh661-live.mjs <base> [shots]
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
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh661-")), {
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
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/"))
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

  const api = await page.evaluate(async (slug) => {
    const h = { headers: { "X-Workspace-ID": slug } }
    const t = await fetch("/api/w/email_templates", h)
    const tj = await t.json().catch(() => ({}))
    const c = await fetch("/api/w/conversations?limit=100&channel=email&status=", h)
    const cj = await c.json().catch(() => ({}))
    const all = [...(cj.pinned ?? []), ...(cj.conversations ?? cj.items ?? [])]
    return {
      templates: t.status,
      count: Array.isArray(tj.templates) ? tj.templates.length : tj.templates === null ? 0 : -1,
      email: all.find((x) => x.channel === "email")?.id ?? null,
    }
  }, slug)
  out[`${scheme}-api`] = api
  check(`${scheme}-api-templates-200`, api.templates === 200 && api.count >= 0, api)

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })

      // The management page.
      await page.goto(`${base}/${loc}/w/${slug}/inbox/email-templates`, { waitUntil: "domcontentloaded" })
      await page.waitForLoadState("networkidle").catch(() => {})
      await page.waitForTimeout(500)
      const pg = await page.evaluate(() => ({
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        h1: document.querySelector("h1")?.textContent?.trim() ?? "",
        notFound: /404|not found/i.test(document.title),
        overflowX: document.documentElement.scrollWidth > innerWidth + 1,
      }))
      out[`${k}-page`] = pg
      check(`${k}-page-loads`, pg.h1 !== "" && !pg.notFound, pg)
      check(`${k}-page-dir`, pg.dir === (loc === "ar" ? "rtl" : "ltr"), pg.dir)
      check(`${k}-page-theme`, pg.dark === (scheme === "dark"), pg.dark)
      check(`${k}-page-no-overflow`, !pg.overflowX)
      if (loc === "ar") check(`${k}-page-arabic`, /[\u0600-\u06FF]/.test(pg.h1), pg.h1)
      if (shots) await page.screenshot({ path: `${shots}/mh661-live-${k}-page.png` })

      // An email thread: subject field and the picker.
      if (!api.email) continue
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${api.email}`, { waitUntil: "domcontentloaded" })
      const ok = await page.waitForSelector("[data-reply-subject]", { timeout: 30000 }).then(
        () => true,
        () => false,
      )
      check(`${k}-subject-field`, ok)
      const trigger = page.locator('[data-action="email-templates"]').first()
      check(`${k}-picker`, (await trigger.count()) === 1)
      if ((await trigger.count()) === 1) await page.waitForFunction(() => !document.querySelector('[data-action="email-templates"]')?.disabled, null, { timeout: 10000 }).catch(() => {})
      check(`${k}-picker-enabled`, (await trigger.count()) === 1 && (await trigger.isEnabled()))
      if ((await trigger.count()) === 1 && (await trigger.isEnabled())) {
        await trigger.click()
        await page.waitForTimeout(500)
        const items = await page.locator("[data-email-template]").count()
        out[`${k}-picker-items`] = items
        check(`${k}-picker-lists`, items === api.count || (api.count === 0 && items === 0), { items, api: api.count })
        if (shots) await page.screenshot({ path: `${shots}/mh661-live-${k}-picker.png` })
        await page.keyboard.press("Escape")
      }
      const of = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
      check(`${k}-thread-no-overflow`, !of)
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
