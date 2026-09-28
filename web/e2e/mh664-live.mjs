// MH-664 live, read-only: on a WhatsApp and an email conversation, hovering a
// message shows React and Reply, the reaction bar opens with six quick emoji
// and "+" (then closes — nothing is picked), Reply puts the quote above the
// composer (then Escape cancels it — nothing is sent), and any existing
// reaction chips / quotes render, in en/ar × light/dark × 1440/390.
// Only GETs; safe on production.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh664-live.mjs <base> [shots]
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
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh664-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  // Guard: this check never writes. Any non-GET to the API outside auth fails
  // the run — and is aborted before it leaves the browser, so a regression
  // that would react or send cannot do so on production.
  const writes = []
  await page.route("**/api/**", (route) => {
    const r = route.request()
    const u = new URL(r.url())
    if (r.method() !== "GET" && !u.pathname.startsWith("/api/auth/")) {
      writes.push(`${r.method()} ${u.pathname}`)
      return route.abort()
    }
    return route.continue()
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  const convs = await page.evaluate(async (slug) => {
    const h = { headers: { "X-Workspace-ID": slug } }
    const pick = async (channel) => {
      const c = await fetch(`/api/w/conversations?limit=50&channel=${channel}&status=`, h)
      const cj = await c.json().catch(() => ({}))
      const all = [...(cj.pinned ?? []), ...(cj.conversations ?? cj.items ?? [])]
      return all.find((x) => x.channel === channel && !x.blocked)?.id ?? null
    }
    return { whatsapp: await pick("whatsapp"), email: await pick("email") }
  }, slug)
  out[`${scheme}-conversations`] = convs
  check(`${scheme}-has-a-conversation`, !!(convs.whatsapp || convs.email), convs)

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390])
      for (const [channel, id] of Object.entries(convs)) {
        if (!id) continue
        const k = `${loc}-${scheme}-${w}-${channel}`
        await page.setViewportSize({ width: w, height: 900 })
        await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${id}`, { waitUntil: "domcontentloaded" })
        const loaded = await page.waitForSelector("[data-message-id]", { timeout: 30000 }).then(
          () => true,
          () => false,
        )
        check(`${k}-thread`, loaded)
        if (!loaded) continue
        await page.waitForTimeout(800)

        // The newest message: hover it, the action bar appears.
        const target = page.locator("[data-message-id]").last()
        const msgID = await target.getAttribute("data-message-id")
        await target.hover()
        await page.waitForTimeout(400)
        const scope =
          channel === "email" ? `article[data-message-id="${msgID}"]` : `[data-message-row="${msgID}"]`
        const react = page.locator(`${scope} [data-action="react"]`)
        const reply = page.locator(`${scope} [data-action="reply-to"]`)
        check(`${k}-react-button`, (await react.count()) === 1)
        check(`${k}-reply-button`, (await reply.count()) === 1)

        if ((await react.count()) === 1) {
          await react.click()
          const bar = await page.waitForSelector("[data-reaction-bar]", { timeout: 5000 }).then(
            () => true,
            () => false,
          )
          check(`${k}-reaction-bar`, bar)
          check(`${k}-six-quick`, (await page.locator("[data-reaction-bar] [data-quick-reaction]").count()) === 6)
          if (shots) await page.screenshot({ path: `${shots}/mh664-live-${k}-bar.png` })
          await page.keyboard.press("Escape")
        }
        if ((await reply.count()) === 1) {
          await target.hover()
          await reply.click()
          const pv = await page.waitForSelector(`[data-reply-preview="${msgID}"]`, { timeout: 5000 }).then(
            () => true,
            () => false,
          )
          check(`${k}-reply-preview`, pv)
          await page.press("#reply-body", "Escape")
          check(`${k}-reply-cancelled`, (await page.locator("[data-reply-preview]").count()) === 0)
        }

        const seen = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          chips: document.querySelectorAll("[data-reaction]").length,
          quotes: document.querySelectorAll("[data-quote]").length,
          overflowX: document.documentElement.scrollWidth > innerWidth + 1,
        }))
        out[`${k}-seen`] = seen
        check(`${k}-dir`, seen.dir === (loc === "ar" ? "rtl" : "ltr"), seen.dir)
        check(`${k}-theme`, seen.dark === (scheme === "dark"), seen.dark)
        check(`${k}-no-overflow`, !seen.overflowX, seen)
        if (shots) await page.screenshot({ path: `${shots}/mh664-live-${k}.png` })
      }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  await ctx.close()
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
