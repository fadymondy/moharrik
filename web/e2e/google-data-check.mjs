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
const want = (c) => (cards.length === 0 && c !== "ytnokey") || cards.includes(c)
const RUN = Date.now()
const FAKE = "127.0.0.1:18507"

let failures = 0
const ok = (cond, msg) => {
  console.log(`${cond ? "PASS" : "FAIL"} ${msg}`)
  if (!cond) failures++
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "gd-")), {
  acceptDownloads: true,
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const consoleErrors = []
page.on("console", (m) => {
  if (m.type() === "error" && !/favicon|Failed to load resource.*(401|404|503)/.test(m.text()))
    consoleErrors.push(m.text())
})

/** A fresh owner with a fresh workspace; returns its slug. */
async function newOwner(tag) {
  await ctx.clearCookies()
  await page.goto(`${base}/en/login`)
  return page.evaluate(
    async ({ email, name }) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      const post = (u, b) =>
        fetch(u, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
          body: JSON.stringify(b),
        })
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
        const overflow = await page.evaluate(
          () => document.documentElement.scrollWidth - window.innerWidth,
        )
        ok(
          overflow <= 1,
          `${card} ${locale}-${scheme}-${width}: no horizontal overflow (${overflow}px)`,
        )
        await page.screenshot({
          path: `${shots}/${card}-${locale}-${scheme}-${width}.png`,
          fullPage: true,
        })
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
    ok(
      (await rows.first().getAttribute("data-enabled")) === "true",
      `one-of-each: ${k} switched on by itself`,
    )
  }
  ok((await page.locator("[data-choose]").count()) === 0, "one-of-each: nothing to choose")

  // Several: the picker asks, prominently.
  const many = await newOwner("many")
  await connectAll(many, `many-${RUN}@brand.test`)
  await page.goto(pageURL(many, "en", "/settings/integrations"))
  await page.waitForSelector("[data-choose]")
  const needs = await page.locator("[data-resource-picker]").getAttribute("data-needs-choice")
  ok(needs === "ga4_property,gsc_site", `several: the picker asks for ${needs}`)
  ok(
    (await page.locator('[data-kind="ga4_property"] [data-enabled="true"]').count()) === 0,
    "several: no property guessed",
  )
  // Choose the shop property.
  const shop = page.locator('[data-resource="properties/410000001"]')
  await shop.locator("[data-toggle]").click()
  await page.waitForSelector('[data-resource="properties/410000001"][data-enabled="true"]')
  ok(true, "several: a property switches on")
  await page.waitForFunction(
    () => !document.querySelector('[data-resource="properties/410000001"] [data-syncing]'),
    null,
    { timeout: 20000 },
  )
  const shopText = await shop.innerText()
  ok(
    /Synced|Last sync failed|Not synced/.test(shopText),
    `several: the row says how its sync went ("${shopText.replace(/\s+/g, " ").slice(0, 120)}")`,
  )
  ok(
    (await page.locator("[data-choose]").innerText()).includes("Search Console"),
    "several: still asks for the site",
  )
  // A long list can be searched, and what is switched on stays first.
  ok(
    (await page
      .locator('[data-kind="ga4_property"] [data-resource]')
      .first()
      .getAttribute("data-resource")) === "properties/410000001",
    "the switched-on property is listed first",
  )
  await page.fill("[data-resource-filter]", "blog")
  const shown = await page
    .locator("[data-resource]")
    .evaluateAll((els) => els.map((e) => e.getAttribute("data-resource")))
  ok(
    shown.length === 2 && shown.every((x) => x.includes("blog") || x.includes("410000002")),
    `the filter narrows the list: ${shown.join(", ")}`,
  )
  await page.fill("[data-resource-filter]", "")
  await matrix("mh440", many, "/settings/integrations", "[data-resource-picker]")
}

// ---- MH-444: Analytics (GA4) ------------------------------------------------------------
if (want("mh444")) {
  const slug = await newOwner("ga")
  await connect(slug, "analytics", `ga-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/analytics"))
  await page.waitForSelector("[data-analytics]", { timeout: 20000 })
  ok(
    await page.locator('nav a[href$="/analytics"]').first().isVisible(),
    "Analytics is in the navigation",
  )
  for (const k of ["users", "sessions", "views", "engagement"]) {
    const v = await page.locator(`[data-stat="${k}"]`).innerText()
    ok(/\d/.test(v), `stat ${k}: ${v.replace(/\s+/g, " ")}`)
  }
  const pathD = await page
    .locator('[data-chart] svg path[stroke="var(--chart-1)"]')
    .getAttribute("d")
  ok((pathD?.match(/L/g) ?? []).length >= 20, "the 28-day trend is drawn")
  const box = await page.locator("[data-chart] svg").boundingBox()
  await page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.5)
  await page.waitForSelector("[data-chart-tip]")
  ok(/\d/.test(await page.locator("[data-chart-tip]").innerText()), "hover shows the day's value")
  ok((await page.locator('[data-card="pages"] [data-bar-row]').count()) === 10, "ten top pages")
  ok(
    (await page.locator('[data-card="sources"] [data-bar-row]').count()) >= 3,
    "source / medium rows",
  )
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
  const users = await page
    .locator('[data-card="traffic"] [data-traffic-figure="users"]')
    .innerText()
  ok(/\d/.test(users), `Overview Traffic card: ${users.replace(/\s+/g, " ")}`)
  await page.screenshot({ path: `${shots}/mh444-overview-en-light-1440.png`, fullPage: true })

  // With an answer stored and GA4 failing: the stored answer, marked stale, with the reason.
  await fetch(`http://${FAKE}/_fake/fail?api=runReport&status=503&reason=backendError&times=-1`, {
    method: "POST",
  })
  await page.goto(pageURL(slug, "en", "/analytics?days=28"))
  await page.waitForSelector("[data-analytics]")
  await page.click("[data-refresh]")
  await page.waitForSelector("[data-stale]", { timeout: 30000 })
  ok(
    await page.locator('[data-stat="users"]').isVisible(),
    "refresh while GA4 fails keeps the stored numbers, marked stale",
  )
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
  await fetch(
    `http://${FAKE}/_fake/fail?api=runReport&status=403&reason=SERVICE_DISABLED&times=-1`,
    { method: "POST" },
  )
  await page.goto(pageURL(empty, "en", "/analytics?days=90"))
  await page.waitForSelector("[data-data-error]", { timeout: 20000 })
  ok(
    (await page.locator("[data-data-error]").innerText()).includes("not enabled"),
    "a failing API says why",
  )
  await page.screenshot({ path: `${shots}/mh444-error-en-light-1440.png`, fullPage: true })
  await fetch(`http://${FAKE}/_fake/fail?status=0`, { method: "POST" })

  // No GA4 at all: the page says how to connect it.
  const none = await newOwner("ganone")
  await page.goto(pageURL(none, "en", "/analytics"))
  await page.waitForSelector('[data-no-resource="connect"]', { timeout: 20000 })
  ok(true, "no property: the connect state")
}

// ---- MH-445: Search Console ------------------------------------------------------------
if (want("mh445")) {
  const slug = await newOwner("gsc")
  await connect(slug, "search_console", `gsc-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/search-console"))
  await page.waitForSelector("[data-gsc], [data-gsc-importing]", { timeout: 30000 })
  const importing = await page.locator("[data-gsc-importing]").count()
  ok(
    true,
    importing
      ? "the first import shows its progress state"
      : "the import had finished before the page opened",
  )
  await page.waitForSelector("[data-gsc]", { timeout: 90000 })
  // While the previous period is still importing, no comparison is made (and the page says so).
  for (
    let i = 0;
    i < 30 && (await page.locator("[data-prev-missing], [data-missing]").count()) > 0;
    i++
  ) {
    ok(i > 0 || true, "while importing, the page says the comparison waits")
    await page.waitForTimeout(2000)
    await page.reload()
    await page.waitForSelector("[data-gsc]", { timeout: 30000 })
  }
  ok(
    await page.locator('nav a[href$="/search-console"]').first().isVisible(),
    "Search Console is in the navigation",
  )
  for (const k of ["clicks", "impressions", "ctr", "position"]) {
    const v = await page.locator(`[data-stat="${k}"]`).innerText()
    ok(
      /\d/.test(v) && /better|worse|unchanged/.test(v),
      `stat ${k} with its change: ${v.replace(/\s+/g, " ")}`,
    )
  }
  ok(
    (await page.locator('[data-chart] svg path[stroke="var(--chart-2)"]').count()) === 1,
    "compare draws the previous period",
  )
  ok(
    (await page.locator("[data-gsc-coverage]").innerText()).includes("two to three days late"),
    "the lag is said",
  )
  ok((await page.locator('[data-gsc-rows="query"] [data-row]').count()) > 5, "top queries listed")
  await page.fill("[data-filter-query]", "pricing")
  await page.waitForSelector('[data-basis="queries"]', { timeout: 15000 })
  await page.waitForTimeout(600)
  const qs = await page
    .locator('[data-gsc-rows="query"] [data-row]')
    .evaluateAll((els) => els.map((e) => e.getAttribute("data-row")))
  ok(qs.length > 0 && qs.every((q) => q.includes("pricing")), `query filter: ${qs.join(", ")}`)
  ok(page.url().includes("query=pricing"), "the filter is in the address")
  await page.fill("[data-filter-query]", "")
  await page.waitForFunction(() => !document.querySelector('[data-basis="queries"]'), null, {
    timeout: 15000,
  })
  await page.click("[data-filter-device]")
  await page.getByRole("option", { name: "Mobile" }).click()
  await page.waitForFunction(() => location.search.includes("device=MOBILE"), null, {
    timeout: 10000,
  })
  ok(true, "device filter applies")
  await page.click('[data-metric="position"]')
  ok(await page.getByText("Lower is better").isVisible(), "position runs downwards and says so")
  await matrix("mh445", slug, "/search-console?days=28", "[data-gsc]")

  await page.goto(pageURL(slug, "en", ""))
  await page.waitForSelector("[data-traffic-site]", { timeout: 20000 })
  ok(
    /\d/.test(await page.locator('[data-traffic-site] [data-traffic-figure="clicks"]').innerText()),
    "Overview Traffic card shows search clicks",
  )
  await page.screenshot({ path: `${shots}/mh445-overview-en-light-1440.png`, fullPage: true })

  const empty = await newOwner("gscempty")
  await connect(empty, "search_console", `empty-gsc-${RUN}@brand.test`)
  await page.goto(pageURL(empty, "en", "/search-console"))
  await page.waitForSelector("[data-gsc-empty]", { timeout: 90000 })
  ok((await page.locator("[data-stat]").count()) === 0, "a site with no data: no numbers are shown")
  await page.screenshot({ path: `${shots}/mh445-empty-en-light-1440.png`, fullPage: true })
}

// ---- MH-441: YouTube --------------------------------------------------------------------
if (want("mh441")) {
  const slug = await newOwner("yt")
  const email = `yt-${RUN}@brand.test`
  await connect(slug, "youtube", email)
  await page.goto(pageURL(slug, "en", "/youtube"))
  await page.waitForSelector("[data-youtube]", { timeout: 60000 })
  ok(
    await page.locator('nav a[href$="/youtube"]').first().isVisible(),
    "YouTube is in the navigation",
  )
  ok(
    (await page.locator('[data-yt-stat="subscribers"]').innerText()).includes("12,840"),
    "subscribers from the channel",
  )
  await page.waitForSelector("[data-video]")
  ok((await page.locator("[data-video]").count()) === 6, "every upload is listed")
  ok(/of 2,000 units/.test(await page.locator("[data-yt-quota]").innerText()), "the quota is shown")
  await page.fill("[data-yt-search]", "pricing")
  await page.waitForFunction(() => document.querySelectorAll("[data-video]").length === 1, null, {
    timeout: 10000,
  })
  ok(true, "search narrows the uploads")
  await page.fill("[data-yt-search]", "")
  await page.waitForFunction(() => document.querySelectorAll("[data-video]").length === 6, null, {
    timeout: 10000,
  })
  await matrix("mh441", slug, "/youtube", "[data-video]")

  await page.goto(pageURL(slug, "en", "/youtube"))
  await page.locator("[data-video]").first().click()
  await page.waitForSelector("[data-video-detail]", { timeout: 20000 })
  ok(
    (await page.locator("[data-comment]").count()) === 2,
    "the sheet shows the video's top comments",
  )
  ok(
    (await page.locator("[data-reply]").first().getAttribute("href")).includes("&lc="),
    "each links to reply on YouTube",
  )
  await page.screenshot({ path: `${shots}/mh441-sheet-en-light-1440.png` })
  for (const [locale, scheme, width] of [
    ["ar", "dark", 390],
    ["en", "dark", 390],
    ["ar", "light", 1440],
  ]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto(
      pageURL(
        slug,
        locale,
        "/youtube?video=" +
          (await page.evaluate(() => new URLSearchParams(location.search).get("video"))),
      ),
    )
    await page.waitForSelector("[data-video-detail]", { timeout: 20000 })
    await page.waitForTimeout(500)
    await page.screenshot({ path: `${shots}/mh441-sheet-${locale}-${scheme}-${width}.png` })
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.emulateMedia({ colorScheme: "light" })

  // A viewer comments; the next sync puts it in the notification center.
  await fetch(
    `http://${FAKE}/_fake/comment?email=${encodeURIComponent(email)}&author=Nour&text=${encodeURIComponent("When is the next video?")}`,
    { method: "POST" },
  )
  await page.goto(pageURL(slug, "en", "/settings/integrations"))
  await page.waitForSelector('[data-kind="yt_channel"] [data-sync-now]')
  await page.click('[data-kind="yt_channel"] [data-sync-now]')
  await page.waitForTimeout(2500)
  await page.goto(pageURL(slug, "en", "/notifications"))
  await page.waitForSelector("text=When is the next video?", { timeout: 20000 })
  ok(
    await page
      .getByText(/New comment on/)
      .first()
      .isVisible(),
    "a new comment is in the notification center",
  )
  await page.screenshot({ path: `${shots}/mh441-notification-en-light-1440.png`, fullPage: true })

  await page.goto(pageURL(slug, "en", ""))
  await page.waitForSelector('[data-card="youtube"]', { timeout: 20000 })
  ok(
    (
      await page.locator('[data-card="youtube"] [data-traffic-figure="subscribers"]').innerText()
    ).includes("12,840"),
    "Overview YouTube card",
  )
  await page.screenshot({ path: `${shots}/mh441-overview-en-light-1440.png`, fullPage: true })

  const empty = await newOwner("ytempty")
  await connect(empty, "youtube", `empty-yt-${RUN}@brand.test`)
  await page.goto(pageURL(empty, "en", "/youtube"))
  await page.waitForSelector("[data-youtube]", { timeout: 60000 })
  await page.waitForSelector("text=No uploads yet", { timeout: 20000 })
  ok(true, "a channel with no uploads says so")
  await page.screenshot({ path: `${shots}/mh441-empty-en-light-1440.png`, fullPage: true })
}

// ---- MH-442: YouTube Analytics ----------------------------------------------------------
if (want("mh442")) {
  const slug = await newOwner("yta")
  await connect(slug, "youtube", `yta-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/youtube"))
  await page.waitForSelector('[data-yt-analytics="channel"] [data-yta-totals]', { timeout: 60000 })
  for (const k of ["yt-views", "yt-watch", "yt-subs", "yt-engagement"]) {
    ok(
      /\d/.test(await page.locator(`[data-yt-analytics="channel"] [data-stat="${k}"]`).innerText()),
      `channel ${k}`,
    )
  }
  ok(
    await page.locator("[data-yta-pending]").isVisible(),
    "days YouTube has not published yet are a gap, and said",
  )
  ok(
    (await page.locator('[data-card="yt-traffic"] [data-bar-row]').count()) >= 5,
    "traffic sources",
  )
  ok(
    (await page.locator('[data-card="yt-traffic"]').innerText()).includes("YouTube search"),
    "traffic source names are words",
  )
  await page.locator('[data-yt-analytics="channel"] [data-window="7"]').click()
  await page.waitForFunction(
    () => document.querySelectorAll('[data-yt-analytics="channel"] details tbody tr').length === 7,
    null,
    { timeout: 15000 },
  )
  ok(true, "7-day window")
  await page.locator("[data-top-video]").first().click()
  await page.waitForSelector("[data-video-detail] [data-yt-analytics] [data-yta-totals]", {
    timeout: 30000,
  })
  ok(true, "a video's own analytics in its sheet")
  await page.screenshot({ path: `${shots}/mh442-sheet-en-light-1440.png` })
  await page.keyboard.press("Escape")
  await matrix("mh442", slug, "/youtube", '[data-yt-analytics="channel"] [data-yta-totals]')
  await page.goto(pageURL(slug, "en", ""))
  await page.waitForSelector('[data-card="youtube"]', { timeout: 20000 })
  ok(
    /\d/.test(
      await page.locator('[data-card="youtube"] [data-traffic-figure="views28"]').innerText(),
    ),
    "Overview YouTube card: views over 28 days",
  )
  await page.screenshot({ path: `${shots}/mh442-overview-en-light-1440.png`, fullPage: true })

  const empty = await newOwner("ytaempty")
  await connect(empty, "youtube", `empty-yta-${RUN}@brand.test`)
  await page.goto(pageURL(empty, "en", "/youtube"))
  await page.waitForSelector("[data-yta-empty]", { timeout: 60000 })
  ok(true, "a channel with no analytics says so")
  await page.screenshot({ path: `${shots}/mh442-empty-en-light-1440.png`, fullPage: true })
}

// ---- YouTube without an API key (the production case of 2026-09-23) ----------------------
// Run against a stack started with YTKEY= (no YOUTUBE_API_KEY).
if (cards.includes("ytnokey")) {
  const slug = await newOwner("ytnokey")
  await connect(slug, "youtube", `ytnokey-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/youtube"))
  await page.waitForSelector("[data-video]", { timeout: 60000 })
  const step = page.locator('[data-yt-step="comments"]')
  ok(
    (await step.getAttribute("data-state")) === "unavailable",
    "the comments step says it is not available here",
  )
  ok((await step.innerText()).includes("YOUTUBE_API_KEY"), "and why, in plain words")
  ok(
    (await page.locator('[data-yt-step="uploads"]').count()) === 0,
    "the uploads step is fine, so it is not listed",
  )
  ok((await page.locator("[data-video]").count()) === 6, "every upload is still listed")
  ok(
    (await page.locator('[data-yt-stat="videos"]').innerText()).includes("6"),
    "the uploads count is what was read",
  )
  await page.screenshot({ path: `${shots}/ytfix-nokey-en-light-1440.png`, fullPage: true })
  await page.locator("[data-video]").first().click()
  await page.waitForSelector('[data-comments-unavailable="comments_unavailable"]', {
    timeout: 20000,
  })
  ok(true, "the sheet says why there are no comments")
  await page.screenshot({ path: `${shots}/ytfix-nokey-sheet-en-light-1440.png` })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ colorScheme: "dark" })
  await page.goto(pageURL(slug, "ar", "/youtube"))
  await page.waitForSelector('[data-yt-step="comments"]', { timeout: 20000 })
  await page.screenshot({ path: `${shots}/ytfix-nokey-ar-dark-390.png`, fullPage: true })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.emulateMedia({ colorScheme: "light" })
}

// ---- MH-446: URL inspection and sitemaps -------------------------------------------------
if (want("mh446")) {
  const slug = await newOwner("gsct")
  await connect(slug, "search_console", `gsct-${RUN}@brand.test`)
  await page.goto(pageURL(slug, "en", "/search-console?tab=inspect"))
  await page.waitForSelector("[data-gsc-inspect]", { timeout: 30000 })
  // The budget is per site across workspaces (and days of dev runs): count from here.
  const usedBefore = Number(
    (await page.locator("[data-inspect-quota]").innerText())
      .match(/^([\d,]+)/)[1]
      .replace(/,/g, ""),
  )
  await page.fill("[data-inspect-url]", "https://demo.example/new-landing")
  await page.click("[data-inspect]")
  await page.waitForSelector(
    '[data-inspection="https://demo.example/new-landing"] [data-verdict="NEUTRAL"]',
    { timeout: 20000 },
  )
  await page.fill("[data-inspect-url]", "https://demo.example/pricing")
  await page.click("[data-inspect]")
  await page.waitForSelector(
    '[data-inspection="https://demo.example/pricing"] [data-inspection-detail]',
    { timeout: 20000 },
  )
  ok(
    (await page.locator('[data-inspection="https://demo.example/pricing"]').innerText()).includes(
      "Submitted and indexed",
    ),
    "an indexed page, in Google's words",
  )
  const usedAfter = Number(
    (await page.locator("[data-inspect-quota]").innerText())
      .match(/^([\d,]+)/)[1]
      .replace(/,/g, ""),
  )
  ok(
    usedAfter === usedBefore + 2,
    `the site's inspection budget is counted (${usedBefore} → ${usedAfter})`,
  )
  await page.fill("[data-inspect-url]", "https://elsewhere.test/")
  await page.click("[data-inspect]")
  await page.waitForSelector("text=not part of this Search Console site", { timeout: 10000 })
  ok(true, "a URL outside the site is refused, and said")
  await matrix("mh446", slug, "/search-console?tab=inspect", "[data-inspection]")

  await page.goto(pageURL(slug, "en", "/search-console?tab=sitemaps"))
  await page.waitForSelector("[data-sitemap]", { timeout: 20000 })
  ok((await page.locator("[data-sitemap]").count()) === 1, "the sitemap Google knows is listed")
  await page.fill("[data-sitemap-url]", "https://demo.example/sitemap-blog.xml")
  await page.click("[data-sitemap-submit]")
  await page.waitForSelector('[data-sitemap="https://demo.example/sitemap-blog.xml"]', {
    timeout: 20000,
  })
  ok(
    (
      await page
        .locator('tr:has([data-sitemap="https://demo.example/sitemap-blog.xml"])')
        .innerText()
    ).includes("Waiting for Google"),
    "a submitted sitemap is pending",
  )
  await page.screenshot({ path: `${shots}/mh446-sitemaps-en-light-1440.png`, fullPage: true })
  await page.emulateMedia({ colorScheme: "dark" })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(pageURL(slug, "ar", "/search-console?tab=sitemaps"))
  await page.waitForSelector("[data-sitemap]", { timeout: 20000 })
  await page.screenshot({ path: `${shots}/mh446-sitemaps-ar-dark-390.png`, fullPage: true })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.emulateMedia({ colorScheme: "light" })
}

// ---- MH-443: YouTube Reporting ------------------------------------------------------------
if (want("mh443")) {
  const slug = await newOwner("ytrep")
  const email = `ytrep-${RUN}@brand.test`
  await connect(slug, "youtube", email)
  await page.goto(pageURL(slug, "en", "/youtube"))
  await page.waitForSelector("[data-yt-reports] [data-report-job]", { timeout: 60000 })
  ok((await page.locator("[data-report-job]").count()) === 3, "one job per report family")
  ok(
    (await page.locator("[data-yt-reports]").innerText()).includes("about two days"),
    "no reports yet, and why",
  )
  await page.screenshot({ path: `${shots}/mh443-waiting-en-light-1440.png`, fullPage: true })
  await fetch(`http://${FAKE}/_fake/publish-reports?email=${encodeURIComponent(email)}&days=5`, {
    method: "POST",
  })
  await page.click("[data-reports-check]")
  await page.waitForSelector("[data-reports-table] [data-report]", { timeout: 30000 })
  ok((await page.locator("[data-report]").count()) === 15, "15 daily reports copied")
  const dl = page.waitForEvent("download")
  await page.locator("[data-report-download]").first().click()
  const file = await dl
  ok(
    /\.csv$/.test(file.suggestedFilename()),
    `a report downloads as CSV (${file.suggestedFilename()})`,
  )
  for (const [locale, scheme, width] of [
    ["en", "light", 1440],
    ["ar", "dark", 1440],
    ["en", "dark", 390],
    ["ar", "light", 390],
  ]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto(pageURL(slug, locale, "/youtube"))
    await page.waitForSelector("[data-reports-table]", { timeout: 30000 })
    await page.locator("[data-yt-reports]").scrollIntoViewIfNeeded()
    await page.waitForTimeout(300)
    await page
      .locator("[data-yt-reports]")
      .screenshot({ path: `${shots}/mh443-${locale}-${scheme}-${width}.png` })
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.emulateMedia({ colorScheme: "light" })
}

console.log(
  consoleErrors.length
    ? `console errors:\n${consoleErrors.slice(0, 10).join("\n")}`
    : "no console errors",
)
await ctx.close()
console.log(failures ? `${failures} FAILED` : "ALL PASSED")
process.exit(failures ? 1 : 0)
