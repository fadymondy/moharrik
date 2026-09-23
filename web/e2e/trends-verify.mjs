// MH-385 / MH-387 in a real browser against REAL feeds, as the demo workspace:
// add sources from the catalog in a click (Hacker News through its API), edit a
// source and a keyword topic, then read clustered, scored topics with their
// reasons and summaries, review / dismiss / save, open a topic, page, filter by
// day, and page the raw items.
//
//   DEMO_PASSWORD=... node e2e/trends-verify.mjs http://localhost:3505 [shots-dir]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3505"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}
const fail = []
const check = (name, ok, detail) => {
  out[name] = ok ? true : detail ?? false
  if (!ok) fail.push(name)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "tverify-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "networkidle" })
await page.fill("#email", process.env.DEMO_EMAIL ?? "demo@moharrik.com")
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

const W = `${base}/en/w/${slug}`
const shot = async (name) => shots && page.screenshot({ path: path.join(shots, name), fullPage: true })

// ---- sources: the catalog, one click each -------------------------------------------
await page.goto(`${W}/trends/sources`, { waitUntil: "load" })
await page.waitForSelector("[data-catalog]", { timeout: 30000 })
for (const key of ["hackernews", "bbc-world", "guardian-business", "cnn-arabic", "dw-arabic"]) {
  const card = page.locator(`[data-catalog-entry="${key}"]`)
  if ((await card.getAttribute("data-added")) === "true") {
    out[`catalog:${key}`] = "already added"
    continue
  }
  await card.locator('[data-action="add"]').click()
  await page.waitForSelector(`[data-catalog-entry="${key}"][data-added="true"]`, { timeout: 60000 })
  out[`catalog:${key}`] = "added in one click"
}
const hnRow = page.locator('[data-feed-row="Hacker News"][data-provider="hn"]')
check("hnViaApi", (await hnRow.count()) === 1 && (await hnRow.innerText()).includes("API"))
check("hnStatusOk", (await hnRow.locator("[data-source-status]").getAttribute("data-source-status")) === "ok")
check("nextRunShown", (await page.locator("[data-next-run]").count()) >= 5)

// Language filter on the catalog: Arabic shows only Arabic entries.
await page.click('[data-catalog-lang] [data-lang="ar"]')
const langs = await page.$$eval("[data-catalog-entry]", (els) => els.map((e) => e.innerText))
check("catalogArabicFilter", langs.length > 0 && langs.every((x) => x.includes("Arabic")), langs.length)
await page.click('[data-catalog-lang] [data-lang="all"]')

// Rename a source through its menu.
const dcn = page.locator('[data-feed-row="Daily Coffee News"]')
if (await dcn.count()) {
  await dcn.locator("[data-row-actions]").click()
  await page.click('[data-action="rename"]')
  await page.fill("#rename", "Daily Coffee News (trade)")
  await page.click('form[data-form="rename"] button[type=submit]')
  await page.waitForSelector('[data-feed-row="Daily Coffee News (trade)"]', { timeout: 10000 })
  check("renamed", true)
  // ...and back, so the check can run again.
  await page.locator('[data-feed-row="Daily Coffee News (trade)"] [data-row-actions]').click()
  await page.click('[data-action="rename"]')
  await page.fill("#rename", "Daily Coffee News")
  await page.click('form[data-form="rename"] button[type=submit]')
  await page.waitForSelector('[data-feed-row="Daily Coffee News"]', { timeout: 10000 })
}

// Change a source's interval.
const bbcw = page.locator('[data-feed-row="BBC News: World"]')
await bbcw.locator("button[role=combobox], [id^=interval-]").first().click()
await page.getByRole("option", { name: "Every 24 hours" }).click()
await page.waitForFunction(() => document.querySelector('[data-feed-row="BBC News: World"]')?.innerText.includes("Every 24 hours"), null, { timeout: 10000 })
check("intervalChanged", true)

// Edit a keyword topic: add keywords (Arabic too), then back.
const topicRow = page.locator('[data-topic-row="coffee prices"]')
await topicRow.locator('[data-action="edit-topic"]').click()
const kw = page.locator('form[data-form="edit-topic"] input').nth(1)
const before = await kw.inputValue()
await kw.fill(before + ", Brazil, القهوة")
await page.click('form[data-form="edit-topic"] button[type=submit]')
await page.waitForFunction(() => document.querySelector('[data-topic-row="coffee prices"] [data-topic-keywords]')?.innerText.includes("Brazil"), null, { timeout: 15000 })
check("topicEdited", true)
out.topicCountIsolated = await topicRow.locator("[data-topic-count]").innerText()
// ...and back, so the check can run again.
await topicRow.locator('[data-action="edit-topic"]').click()
await page.locator('form[data-form="edit-topic"] input').nth(1).fill(before)
await page.click('form[data-form="edit-topic"] button[type=submit]')
await page.waitForFunction(() => !document.querySelector('[data-topic-row="coffee prices"] [data-topic-keywords]')?.innerText.includes("Brazil"), null, { timeout: 15000 })
// Off, and on.
await topicRow.locator("[role=switch]").click()
await page.waitForSelector('[data-topic-row="coffee prices"][data-enabled="false"]', { timeout: 10000 })
await page.locator('[data-topic-row="coffee prices"] [role=switch]').click()
await page.waitForSelector('[data-topic-row="coffee prices"][data-enabled="true"]', { timeout: 10000 })
check("topicToggled", true)
await shot("MH-387-verify-sources.png")

// ---- the topics ------------------------------------------------------------------------
await page.goto(`${W}/trends`, { waitUntil: "load" })
await page.waitForSelector("[data-trend-topic]", { timeout: 30000 })
const firstPage = await page.$$eval("[data-trend-topic]", (els) => els.length)
check("firstPage20", firstPage === 20, firstPage)
check("everyTopicHasReasons", await page.$$eval("[data-trend-topic]", (els) => els.every((e) => e.querySelector('[data-reason="sources"]'))))

// Read all pages, then look for the stories.
while (await page.locator('[data-load-more="topics"]').count()) {
  const n = await page.locator("[data-trend-topic]").count()
  await page.click('[data-load-more="topics"]')
  await page.waitForFunction((n) => document.querySelectorAll("[data-trend-topic]").length > n, n, { timeout: 15000 })
}
const topics = await page.$$eval("[data-trend-topic]", (els) =>
  els.map((e) => ({
    id: e.getAttribute("data-trend-topic"),
    title: e.querySelector("[data-topic-link]")?.innerText ?? "",
    summary: e.querySelector("[data-topic-summary]")?.getAttribute("data-topic-summary") ?? "",
    reasons: [...e.querySelectorAll("[data-reason]")].map((r) => `${r.getAttribute("data-reason")}: ${r.innerText.replace(/\s+/g, " ")}`),
    items: [...e.querySelectorAll("[data-item]")].length,
  })),
)
const summary = await page.locator("[data-topics-summary]").getAttribute("data-topics-summary")
check("pagedToTheEnd", summary.split("/")[0] === summary.split("/")[1], summary)
const multi = topics.filter((x) => x.reasons.some((r) => /sources: \+\d+ [2-9] sources/.test(r)))
const arabic = multi.filter((x) => /[\u0600-\u06FF]/.test(x.title))
const hn = topics.filter((x) => x.reasons.some((r) => r.includes("Hacker News #")))
const relevant = topics.filter((x) => x.reasons.some((r) => r.startsWith("relevance")))
check("multiSourceTopics", multi.length >= 3, multi.length)
check("arabicGroups", arabic.length >= 1, arabic.length)
check("hnVelocity", hn.length >= 5, hn.length)
check("relevanceChips", relevant.length >= 1, relevant.length)
check("extractiveSummaries", topics.filter((x) => x.summary.startsWith("extractive")).length >= 10)
out.examples = {
  multiSource: multi.slice(0, 4).map((x) => `${x.title} | ${x.reasons.join(" ; ")}`),
  arabic: arabic.slice(0, 4).map((x) => `${x.title} | ${x.reasons.join(" ; ")}`),
  hn: hn.slice(0, 2).map((x) => `${x.title} | ${x.reasons.join(" ; ")}`),
  relevant: relevant.slice(0, 2).map((x) => `${x.title} | ${x.reasons.join(" ; ")}`),
}

// ---- review / dismiss / save ------------------------------------------------------------
await page.goto(`${W}/trends`, { waitUntil: "load" })
await page.waitForSelector("[data-trend-topic]", { timeout: 30000 })
const openBefore = Number((await page.locator("[data-topics-summary]").getAttribute("data-topics-summary")).split("/")[1])
const target = arabic[0] ?? multi[0]
const firstId = await page.locator("[data-trend-topic]").first().getAttribute("data-trend-topic")
await page.locator(`[data-trend-topic="${firstId}"] [data-action="review"]`).click()
await page.waitForFunction((id) => !document.querySelector(`[data-trend-topic="${id}"]`), firstId, { timeout: 10000 })
const secondId = await page.locator("[data-trend-topic]").first().getAttribute("data-trend-topic")
await page.locator(`[data-trend-topic="${secondId}"] [data-action="dismiss"]`).click()
await page.waitForFunction((id) => !document.querySelector(`[data-trend-topic="${id}"]`), secondId, { timeout: 10000 })
const thirdId = await page.locator("[data-trend-topic]").first().getAttribute("data-trend-topic")
await page.locator(`[data-trend-topic="${thirdId}"] [data-action="save"]`).click()
await page.waitForFunction((id) => !document.querySelector(`[data-trend-topic="${id}"]`), thirdId, { timeout: 10000 })
await page.waitForTimeout(1200)
const openAfter = Number((await page.locator("[data-topics-summary]").getAttribute("data-topics-summary")).split("/")[1])
check("reviewDismissSave", openAfter === openBefore - 3, `${openBefore} -> ${openAfter}`)
// The saved one is under Saved.
await page.click("#trends-state")
await page.getByRole("option", { name: /^Saved/ }).click()
await page.waitForSelector(`[data-trend-topic="${thirdId}"][data-state="saved"]`, { timeout: 10000 })
check("savedListed", true)
// Put the three back, so the check can run again.
for (const [id, body] of [[firstId, { reviewed: false }], [secondId, { dismissed: false }], [thirdId, { saved: false }]]) {
  await page.evaluate(
    async ([id, body, slug]) => {
      const ws = await (await fetch("/api/me/workspaces")).json()
      const w = (ws.workspaces ?? []).find((x) => x.slug === slug)
      await fetch(`/api/w/trends/${id}`, { method: "PATCH", headers: { "Content-Type": "application/json", "X-Workspace-ID": w?.id ?? slug }, body: JSON.stringify(body) })
    },
    [id, body, slug],
  )
}

// ---- the day filter ----------------------------------------------------------------------
await page.goto(`${W}/trends`, { waitUntil: "load" })
await page.waitForSelector("[data-trend-topic]", { timeout: 30000 })
await page.click("#trends-day")
const dayOptions = await page.getByRole("option").allInnerTexts()
await page.getByRole("option").nth(dayOptions.length > 2 ? 2 : 1).click()
await page.waitForTimeout(1500)
const dayHeads = await page.$$eval("[data-trend-topics] h2", (els) => els.map((e) => e.innerText))
check("dayFilterOneDay", dayHeads.length === 1, dayHeads)

// ---- one topic ---------------------------------------------------------------------------
if (target) {
  await page.goto(`${W}/trends/${target.id}`, { waitUntil: "load" })
  await page.waitForSelector("[data-topic-items]", { timeout: 30000 })
  const items = await page.locator("[data-topic-items] [data-item]").count()
  const outlets = await page.locator("[data-topic-outlets] li").count()
  check("detailItemsAndOutlets", items >= 2 && outlets >= 2, `${items} items, ${outlets} outlets`)
  out.detailTitle = await page.locator("[data-topic-title]").innerText()
  await shot("MH-385-verify-detail.png")
}

// ---- the raw items, paged ----------------------------------------------------------------
await page.goto(`${W}/trends`, { waitUntil: "load" })
await page.click('[data-tab="items"]')
await page.waitForSelector("[data-news]", { timeout: 30000 })
const n1 = await page.locator("[data-news]").count()
await page.click('[data-load-more="items"]')
await page.waitForFunction((n) => document.querySelectorAll("[data-news]").length > n, n1, { timeout: 15000 })
const n2 = await page.locator("[data-news]").count()
check("itemsPaged", n1 === 30 && n2 === 60, `${n1} -> ${n2}`)
out.pageHeightItemsFirstPage = await page.evaluate(() => document.documentElement.scrollHeight)

console.log(JSON.stringify(out, null, 2))
console.log(fail.length ? `FAILED: ${fail.join(", ")}` : "ALL CHECKS PASSED")
await ctx.close()
process.exit(fail.length ? 1 : 0)
