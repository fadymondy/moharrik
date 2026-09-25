// Trends smoke check: serve a local RSS feed, add it as a source, see the first
// fetch collect its items, add a topic and see it backfill onto items already
// collected, filter and save on the timeline, reject a URL that is not a feed,
// pause a source — plus Arabic at 390px and cross-workspace isolation.
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"

const rss = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
  <title>Trend Check Feed</title>
  <item><title>Postgres 18 ships async I/O</title><link>https://example.test/pg18</link>
    <guid>tc-pg18</guid><description>&lt;p&gt;The release notes.&lt;/p&gt;</description>
    <pubDate>Mon, 15 Sep 2026 09:30:00 +0000</pubDate></item>
  <item><title>A note about kubernetes</title><link>https://example.test/k8s</link>
    <guid>tc-k8s</guid><description>Cluster things.</description>
    <pubDate>Tue, 16 Sep 2026 09:30:00 +0000</pubDate></item>
  <item><title>Something entirely unrelated</title><link>https://example.test/misc</link>
    <guid>tc-misc</guid><pubDate>Wed, 17 Sep 2026 09:30:00 +0000</pubDate></item>
</channel></rss>`

// The API fetches the URL itself, so it has to be reachable from this machine —
// a throwaway server on an ephemeral port is enough, and keeps the check offline.
const srv = createServer((req, res) => {
  if (req.url.startsWith("/feed.xml")) {
    res.writeHead(200, { "Content-Type": "application/rss+xml" })
    res.end(rss)
  } else {
    res.writeHead(200, { "Content-Type": "text/html" })
    res.end("<!doctype html><html><body>not a feed</body></html>")
  }
})
await new Promise((r) => srv.listen(0, "127.0.0.1", r))
const feedBase = `http://127.0.0.1:${srv.address().port}`

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "tr-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `tr${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", email)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", wsName)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  return { email, slug: page.url().split("/w/")[1].split("/")[0] }
}

const out = {}
const ctxA = await newCtx()
const pageA = ctxA.pages()[0]
const errs = []
pageA.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug: slugA } = await signUpAndEnterWorkspace(pageA, "TR A " + Date.now())
out.slugA = slugA

// ---- the sidebar leads there, and the timeline starts empty ----
await pageA.goto(`${base}/en/w/${slugA}/trends`, { waitUntil: "networkidle" })
out.navHasTrends = await pageA.evaluate(() =>
  [...document.querySelectorAll("nav a")].some((a) => /\/trends$/.test(new URL(a.href).pathname)),
)
out.startsEmpty = await pageA.evaluate(() =>
  document.body.innerText.includes("Nothing collected yet"),
)

// ---- a URL that is not a feed is refused, on the page, with a reason ----
await pageA.goto(`${base}/en/w/${slugA}/trends/sources`, { waitUntil: "networkidle" })
await pageA.fill("#feed-url", `${feedBase}/not-a-feed`)
await pageA.click('form[data-form="add-feed"] button[type=submit]')
await pageA.waitForTimeout(1500)
out.nonFeedRejected = await pageA.evaluate(() =>
  /does not look like an RSS or Atom feed/i.test(document.body.innerText),
)
out.nonFeedAddedNothing = (await pageA.$$("[data-feed-row]")).length === 0

// ---- adding the real feed collects its items on the first fetch ----
await pageA.fill("#feed-url", `${feedBase}/feed.xml`)
await pageA.click('form[data-form="add-feed"] button[type=submit]')
await pageA.waitForSelector("[data-feed-row]", { timeout: 15000 })
out.feedRowName = await pageA.evaluate(() =>
  document.querySelector("[data-feed-row]")?.getAttribute("data-feed-row"),
)
out.feedItemCountText = await pageA.evaluate(
  () => document.querySelector("[data-feed-row]")?.innerText.match(/(\d+) items collected/)?.[1],
)

// ---- a topic added AFTER collection still labels what is already there ----
await pageA.fill("#topic-name", "Postgres")
await pageA.fill("#topic-keywords", "postgres, pgsql")
await pageA.click('form[data-form="add-topic"] button[type=submit]')
await pageA.waitForSelector("[data-topic-row]", { timeout: 10000 })
out.topicBackfilled = await pageA.evaluate(
  () => document.querySelector("[data-topic-row]")?.innerText.match(/(\d+) items/)?.[1],
)

// ---- the timeline shows everything, newest first ----
await pageA.goto(`${base}/en/w/${slugA}/trends`, { waitUntil: "networkidle" })
await pageA.waitForSelector("[data-news]", { timeout: 10000 })
out.timelineCount = await pageA.evaluate(() => document.querySelectorAll("[data-news]").length)
out.newestFirst = await pageA.evaluate(() =>
  (document.querySelectorAll("[data-news]")[0]?.innerText ?? "").includes("entirely unrelated"),
)
out.showsTopicBadge = await pageA.evaluate(() => document.body.innerText.includes("Postgres"))

// ---- search narrows, saving persists, the saved filter shows only it ----
await pageA.fill('input[aria-label="Search the timeline"]', "kubernetes")
await pageA.press('input[aria-label="Search the timeline"]', "Enter")
await pageA.waitForTimeout(1200)
out.searchCount = await pageA.evaluate(() => document.querySelectorAll("[data-news]").length)
await pageA.fill('input[aria-label="Search the timeline"]', "")
await pageA.press('input[aria-label="Search the timeline"]', "Enter")
await pageA.waitForTimeout(1200)

await pageA.click('[data-news]:first-child button[aria-label="Save this item"]')
await pageA.waitForTimeout(1000)
await pageA.click('[data-filter="saved"]')
await pageA.waitForTimeout(1200)
out.savedFilterCount = await pageA.evaluate(() => document.querySelectorAll("[data-news]").length)
// and it survives a reload, i.e. it was written, not just toggled in state
await pageA.reload({ waitUntil: "networkidle" })
await pageA.waitForTimeout(800)
out.savedAfterReload = await pageA.evaluate(
  () =>
    [...document.querySelectorAll("[data-news]")].filter(
      (l) => l.getAttribute("data-news-saved") === "true",
    ).length,
)

// ---- pausing a source stops it being fetched again ----
await pageA.goto(`${base}/en/w/${slugA}/trends/sources`, { waitUntil: "networkidle" })
await pageA.click('[data-feed-row] [role="switch"]')
await pageA.waitForTimeout(1000)
out.sourcePaused = await pageA.evaluate(() =>
  document.querySelector("[data-feed-row]")?.innerText.includes("Paused"),
)

await ctxA.close()

// ---- Arabic at 390px, no overflow ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "TR AR " + Date.now())
for (const p of ["trends", "trends/sources"]) {
  await pageAr.goto(`${base}/ar/w/${slugAr}/${p}`, { waitUntil: "networkidle" })
  await pageAr.waitForTimeout(600)
  out[`ar390_${p.replace("/", "_")}_overflow`] = await pageAr.evaluate(
    () => document.documentElement.scrollWidth > 390,
  )
  out[`ar390_${p.replace("/", "_")}_rawKeys`] = await pageAr.evaluate(() =>
    /trends\.[a-zA-Z]|nav\.[a-zA-Z]/.test(document.body.innerText),
  )
}
await ctxAr.close()

// ---- cross-workspace isolation: B cannot read or touch A's feed ----
const ctxB = await newCtx()
const pageB = ctxB.pages()[0]
const { slug: slugB } = await signUpAndEnterWorkspace(pageB, "TR B " + Date.now())
await pageB.goto(`${base}/en/w/${slugB}/trends`, { waitUntil: "networkidle" })
out.workspaceBTimelineEmpty = await pageB.evaluate(() =>
  document.body.innerText.includes("Nothing collected yet"),
)
out.workspaceBSourceCount = await pageB.evaluate(
  async ({ ws }) => {
    const r = await fetch("/api/w/feed_sources", { headers: { "X-Workspace-ID": ws } })
    return (await r.json()).sources?.length ?? -1
  },
  { ws: slugB },
)
await ctxB.close()

srv.close()
out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
