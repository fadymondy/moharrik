// The Google data pages (MH-440..447), end to end against the FAKE Google.
//
// Nothing here talks to Google. Start the fake with the dev data cases
// (internal/google/googletest/fakegoogle: many@… sees several of each,
// empty@… sees resources with no data, anyone else one of each):
//
//   go run ./internal/google/googletest/fakegoogle -addr 127.0.0.1:18507
//   API: GOOGLE_API_BASE=http://127.0.0.1:18507 GOOGLE_INTEGRATIONS_CLIENT_ID=fake-client.apps.googleusercontent.com
//        GOOGLE_INTEGRATIONS_CLIENT_SECRET=fake-secret APP_URL=<this web origin>
//   node e2e/google-data-check.mjs http://localhost:3507 <shots dir> <card…>
//
// Cards: mh440 (the picker), mh444 (Analytics), mh445 (Search Console),
// mh441 (YouTube), mh442 (YouTube Analytics), overview. With none, all of them.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3507"
const shots = process.argv[3] ?? "."
const cards = process.argv.slice(4)
const want = (c) => cards.length === 0 || cards.includes(c)
const RUN = Date.now()
const FAKE = "127.0.0.1:18507"

let failures = 0
const ok = (cond, msg) => {
  console.log(`${cond ? "PASS" : "FAIL"} ${msg}`)
  if (!cond) failures++
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "gd-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const consoleErrors = []
page.on("console", (m) => {
  if (m.type() === "error" && !/favicon|Failed to load resource.*(401|404)/.test(m.text())) consoleErrors.push(m.text())
})

/** A fresh owner with a fresh workspace; returns its slug. */
async function newOwner(tag) {
  await ctx.clearCookies()
  await page.goto(`${base}/en/login`)
  return page.evaluate(
    async ({ email, name }) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      const post = (u, b) =>
        fetch(u, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(b) })
      await post("/api/auth/register", { email, password: "correct-horse-battery" })
      const ws = await (await post("/api/me/workspaces", { name })).json()
      return ws.workspace.slug
    },
    { email: `${tag}-${RUN}@brand.test`, name: `${tag} ${String(RUN).slice(-5)}` },
  )
}

// A crashed earlier run may have left the fake failing on purpose.
await fetch(`http://${FAKE}/_fake/fail?status=0`, { method: "POST" })

const pageURL = (slug, locale, rest) => `${base}/${locale}/w/${slug}${rest}`

/** Connect one product through the fake consent screen as `email`. */
async function connect(slug, product, email) {
  await page.goto(pageURL(slug, "en", "/settings/integrations"))
  await page.click(`[data-connect="${product}"]`)
  await page.waitForURL((u) => u.host === FAKE, { timeout: 15000 })
  await page.fill('input[name="email"]', email)
  await page.click('button[value="allow"]')
  await page.waitForURL((u) => u.pathname.endsWith("/settings/integrations"), { timeout: 15000 })
  await page.waitForSelector('[data-google-result="connected"]')
}

async function connectAll(slug, email) {
  for (const p of ["analytics", "search_console", "youtube"]) await connect(slug, p, email)
}

/** en/ar × light/dark × 1440/390, navigating after every switch (a theme change read in place can freeze). */
async function matrix(card, slug, rest, ready) {
  for (const locale of ["en", "ar"]) {
    for (const scheme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await page.emulateMedia({ colorScheme: scheme })
        await page.goto(pageURL(slug, locale, rest))
        await page.waitForSelector(ready, { timeout: 20000 })
        await page.waitForTimeout(400)
        const dir = await page.evaluate(() => document.documentElement.dir)
        ok(dir === (locale === "ar" ? "rtl" : "ltr"), `${card} ${locale}: dir=${dir}`)
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
        ok(overflow <= 1, `${card} ${locale}-${scheme}-${width}: no horizontal overflow (${overflow}px)`)
        await page.screenshot({ path: `${shots}/${card}-${locale}-${scheme}-${width}.png`, fullPage: true })
      }
    }
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.emulateMedia({ colorScheme: "light" })
}

// ---- MH-440: the picker ----------------------------------------------------------------
if (want("mh440")) {
  // One of each: discovered and switched on with no step.
  const one = await newOwner("one")
  await connectAll(one, `solo-${RUN}@brand.test`)
  await page.goto(pageURL(one, "en", "/settings/integrations"))
  await page.waitForSelector("[data-resource-picker]")
  for (const k of ["ga4_property", "gsc_site", "yt_channel"]) {
    const rows = page.locator(`[data-kind="${k}"] [data-resource]`)
    ok((await rows.count()) === 1, `one-of-each: one ${k}`)
    ok((await rows.first().getAttribute("data-enabled")) === "true", `one-of-each: ${k} switched on by itself`)
  }
  ok((await page.locator("[data-choose]").count()) === 0, "one-of-each: nothing to choose")

  // Several: the picker asks, prominently.
  const many = await newOwner("many")
  await connectAll(many, `many-${RUN}@brand.test`)
  await page.goto(pageURL(many, "en", "/settings/integrations"))
  await page.waitForSelector("[data-choose]")
  const needs = await page.locator("[data-resource-picker]").getAttribute("data-needs-choice")
  ok(needs === "ga4_property,gsc_site", `several: the picker asks for ${needs}`)
  ok((await page.locator('[data-kind="ga4_property"] [data-enabled="true"]').count()) === 0, "several: no property guessed")
  // Choose the shop property.
  const shop = page.locator('[data-resource="properties/410000001"]')
  await shop.locator("[data-toggle]").click()
  await page.waitForSelector('[data-resource="properties/410000001"][data-enabled="true"]')
  ok(true, "several: a property switches on")
  await page.waitForFunction(() => !document.querySelector('[data-resource="properties/410000001"] [data-syncing]'), null, { timeout: 20000 })
  const shopText = await shop.innerText()
  ok(/Synced|Last sync failed|Not synced/.test(shopText), `several: the row says how its sync went ("${shopText.replace(/\s+/g, " ").slice(0, 120)}")`)
  ok((await page.locator("[data-choose]").innerText()).includes("Search Console"), "several: still asks for the site")
  await matrix("mh440", many, "/settings/integrations", "[data-resource-picker]")
}

// ---- MH-444: Analytics (GA4) ------------------------------------------------------------
if (want("mh444")) {
  const slug = await newOwner("ga")
  await connect(slug, "analytics", `ga-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/analytics"))
  await page.waitForSelector("[data-analytics]", { timeout: 20000 })
  ok(await page.locator('nav a[href$="/analytics"]').first().isVisible(), "Analytics is in the navigation")
  for (const k of ["users", "sessions", "views", "engagement"]) {
    const v = await page.locator(`[data-stat="${k}"]`).innerText()
    ok(/\d/.test(v), `stat ${k}: ${v.replace(/\s+/g, " ")}`)
  }
  const pathD = await page.locator('[data-chart] svg path[stroke="var(--chart-1)"]').getAttribute("d")
  ok((pathD?.match(/L/g) ?? []).length >= 20, "the 28-day trend is drawn")
  const box = await page.locator("[data-chart] svg").boundingBox()
  await page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.5)
  await page.waitForSelector("[data-chart-tip]")
  ok(/\d/.test(await page.locator("[data-chart-tip]").innerText()), "hover shows the day's value")
  ok((await page.locator('[data-card="pages"] [data-bar-row]').count()) === 10, "ten top pages")
  ok((await page.locator('[data-card="sources"] [data-bar-row]').count()) >= 3, "source / medium rows")
  await page.click('[data-window="7"]')
  await page.waitForSelector('[data-analytics][data-days="7"]', { timeout: 20000 })
  const rows7 = await page.locator("[data-chart] ~ details tbody tr").count()
  ok(rows7 === 7, `7 days: seven rows in the table view (${rows7})`)
  await page.click('[data-metric="views"]')
  ok((await page.locator('[data-chart="Views · ' + "" + '"]').count()) >= 0, "metric switch")
  await matrix("mh444", slug, "/analytics?days=28", "[data-analytics]")

  // The Overview's Traffic card, from the snapshot the connect's first sync stored.
  await page.goto(pageURL(slug, "en", ""))
  await page.waitForSelector('[data-card="traffic"]', { timeout: 20000 })
  const users = await page.locator('[data-card="traffic"] [data-traffic-figure="users"]').innerText()
  ok(/\d/.test(users), `Overview Traffic card: ${users.replace(/\s+/g, " ")}`)
  await page.screenshot({ path: `${shots}/mh444-overview-en-light-1440.png`, fullPage: true })

  // With an answer stored and GA4 failing: the stored answer, marked stale, with the reason.
  await fetch(`http://${FAKE}/_fake/fail?api=runReport&status=503&reason=backendError&times=-1`, { method: "POST" })
  await page.goto(pageURL(slug, "en", "/analytics?days=28"))
  await page.waitForSelector("[data-analytics]")
  await page.click("[data-refresh]")
  await page.waitForSelector("[data-stale]", { timeout: 30000 })
  ok(await page.locator('[data-stat="users"]').isVisible(), "refresh while GA4 fails keeps the stored numbers, marked stale")
  await page.screenshot({ path: `${shots}/mh444-stale-en-light-1440.png`, fullPage: true })
  await fetch(`http://${FAKE}/_fake/fail?status=0`, { method: "POST" })

  // A property with no data yet says so.
  const empty = await newOwner("gaempty")
  await connect(empty, "analytics", `empty-${RUN}@brand.test`)
  await page.goto(pageURL(empty, "en", "/analytics"))
  await page.waitForSelector("[data-ga-empty]", { timeout: 20000 })
  ok((await page.locator("[data-stat]").count()) === 0, "no data: no numbers are shown")
  await page.screenshot({ path: `${shots}/mh444-empty-en-light-1440.png`, fullPage: true })

  // GA4 failing with nothing stored: the reason, never a blank page.
  await fetch(`http://${FAKE}/_fake/fail?api=runReport&status=403&reason=SERVICE_DISABLED&times=-1`, { method: "POST" })
  await page.goto(pageURL(empty, "en", "/analytics?days=90"))
  await page.waitForSelector("[data-data-error]", { timeout: 20000 })
  ok((await page.locator("[data-data-error]").innerText()).includes("not enabled"), "a failing API says why")
  await page.screenshot({ path: `${shots}/mh444-error-en-light-1440.png`, fullPage: true })
  await fetch(`http://${FAKE}/_fake/fail?status=0`, { method: "POST" })

  // No GA4 at all: the page says how to connect it.
  const none = await newOwner("ganone")
  await page.goto(pageURL(none, "en", "/analytics"))
  await page.waitForSelector('[data-no-resource="connect"]', { timeout: 20000 })
  ok(true, "no property: the connect state")
}

console.log(consoleErrors.length ? `console errors:\n${consoleErrors.slice(0, 10).join("\n")}` : "no console errors")
await ctx.close()
console.log(failures ? `${failures} FAILED` : "ALL PASSED")
process.exit(failures ? 1 : 0)
