// Reports page check (MH-394), against a running web + API with the demo workspace seeded:
//
//   node e2e/reports-check.mjs http://localhost:3512 [shots-dir]
//
// Signs in as the demo user (password from DEMO_PASSWORD or ~/.moharrik-demo.json, never
// printed), opens /reports for the last 12 months in en/ar × light/dark × 1440/390, and asserts
// that what the page shows is what GET /api/w/reports/crm answered: every tile, the pipeline and
// funnel tables, the won/lost buckets, the lead sources. Also checks no horizontal overflow, that
// the charts draw, that RTL runs the time axis right to left, and that the page follows the theme.
// Screenshots land in shots-dir as MH-394-<lang>-<theme>-<width>.png.
import { chromium } from "playwright-core"
import { existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3512").replace(/\/$/, "")
const shots = process.argv[3] ?? "e2e-shots"
const store = path.join(homedir(), ".moharrik-demo.json")
const creds = JSON.parse(existsSync(store) ? readFileSync(store, "utf8") : "{}")[base] ?? {}
const EMAIL = process.env.DEMO_EMAIL ?? creds.email ?? "demo@moharrik.com"
const PASSWORD = process.env.DEMO_PASSWORD ?? creds.password
if (!PASSWORD) throw new Error(`no demo password for ${base}`)
mkdirSync(shots, { recursive: true })

const failures = []
const check = (ok, what) => {
  if (!ok) failures.push(what)
  console.log(`${ok ? "ok  " : "FAIL"} ${what}`)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "rep-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0]
const errors = []
page.on("pageerror", (e) => errors.push(String(e).slice(0, 300)))

await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", EMAIL)
await page.fill("#password", PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const ws = await page.evaluate(async () => {
  const r = await fetch("/api/me/workspaces", { headers: { Accept: "application/json" } })
  return (await r.json()).workspaces.find((w) => w.name.startsWith("Moharrik Demo"))
})
if (!ws) throw new Error("no demo workspace")

const api = await page.evaluate(async (id) => {
  const r = await fetch("/api/w/reports/crm?days=365", {
    headers: { Accept: "application/json", "X-Workspace-ID": id },
  })
  return r.json()
}, ws.id)

for (const lang of ["en", "ar"]) {
  for (const theme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      const tag = `${lang}-${theme}-${width}`
      await page.emulateMedia({ colorScheme: theme })
      await page.setViewportSize({ width, height: 900 })
      await page.goto(`${base}/${lang}/w/${ws.slug}/reports?days=365`, {
        waitUntil: "load",
      })
      await page.waitForSelector("[data-report]", { timeout: 20000 })
      await page.waitForSelector("[data-chart-id=won-lost] .recharts-bar-rectangle", {
        timeout: 20000,
      })
      await page.waitForTimeout(400)
      const dom = await page.evaluate(() => {
        const stat = (id) =>
          document
            .querySelector(`[data-stat="${id}"] p:nth-of-type(2)`)
            ?.textContent?.replace(/[⁦-⁩]/g, "")
            .trim()
        const bars = [
          ...document.querySelectorAll("[data-chart-id=won-lost] .recharts-bar-rectangle path"),
        ].map((p) => p.getBoundingClientRect().x)
        return {
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          tiles: {
            open: stat("open-pipeline"),
            won: stat("won"),
            rate: stat("win-rate"),
            avg: stat("avg-deal"),
            opened: stat("opened"),
          },
          pipelineBars: document.querySelectorAll(
            "[data-chart-id=pipeline] .recharts-bar-rectangle",
          ).length,
          funnelBars: document.querySelectorAll(
            "[data-chart-id=conversion] .recharts-bar-rectangle",
          ).length,
          firstBarX: bars[0],
          lastBarX: bars.at(-1),
          arabicDigits: /[٠-٩]/.test(document.querySelector("[data-report]")?.textContent ?? ""),
          sources: [...document.querySelectorAll("[data-table=sources] tbody tr")].map((tr) =>
            [...tr.querySelectorAll("td")].map((td) => td.textContent.trim()),
          ),
          bg: getComputedStyle(document.body).backgroundColor,
        }
      })
      check(dom.dir === (lang === "ar" ? "rtl" : "ltr"), `${tag}: direction ${dom.dir}`)
      check(dom.dark === (theme === "dark"), `${tag}: theme follows the system (${dom.bg})`)
      check(dom.overflow <= 0, `${tag}: no horizontal overflow (${dom.overflow}px)`)
      check(!dom.arabicDigits, `${tag}: Western digits only`)
      check(
        dom.pipelineBars === (api.pipeline.stages ?? []).length,
        `${tag}: pipeline chart draws ${dom.pipelineBars} stages`,
      )
      check(
        dom.funnelBars === (api.conversion.steps ?? []).length,
        `${tag}: funnel draws ${dom.funnelBars} steps`,
      )
      // Time runs start-to-end: left to right in English, right to left in Arabic.
      check(
        lang === "ar" ? dom.firstBarX > dom.lastBarX : dom.firstBarX < dom.lastBarX,
        `${tag}: time axis direction (${Math.round(dom.firstBarX)} → ${Math.round(dom.lastBarX)})`,
      )
      check(
        (api.lead_sources?.sources ?? []).length === dom.sources.length,
        `${tag}: ${dom.sources.length} lead-source rows`,
      )
      if (lang === "en" && theme === "light" && width === 1440) {
        // The numbers on the page are the API's numbers.
        const money = (minor) =>
          new Intl.NumberFormat("en-US", {
            style: "currency",
            currency: api.currency,
            maximumFractionDigits: 2,
          }).format(minor / 100)
        check(
          dom.tiles.open === money(api.pipeline.open_value_minor),
          `tile open pipeline ${dom.tiles.open} = ${money(api.pipeline.open_value_minor)}`,
        )
        check(
          dom.tiles.won === money(api.deals.won_value_minor),
          `tile won ${dom.tiles.won} = ${money(api.deals.won_value_minor)}`,
        )
        check(
          dom.tiles.rate === `${Math.round(api.deals.win_rate * 100)}%`,
          `tile win rate ${dom.tiles.rate} = ${Math.round(api.deals.win_rate * 100)}%`,
        )
        check(
          dom.tiles.avg === money(api.deals.avg_won_value_minor),
          `tile average ${dom.tiles.avg} = ${money(api.deals.avg_won_value_minor)}`,
        )
        check(
          dom.tiles.opened === String(api.deals.opened),
          `tile opened ${dom.tiles.opened} = ${api.deals.opened}`,
        )
        const src = api.lead_sources.sources.map((s) => [
          s.source === "direct" ? "Direct (no source)" : s.source,
          String(s.leads),
          String(s.converted),
          String(s.won_deals),
          money(s.won_value_minor),
        ])
        check(
          JSON.stringify(dom.sources) === JSON.stringify(src),
          `lead sources table = API ${JSON.stringify(src)}`,
        )
        // The table views carry the same figures as the charts.
        await page.click("[data-view-toggle=timeline] >> text=Table")
        const rows = await page.$$eval("[data-table=timeline] tbody tr", (trs) =>
          trs.map((tr) => [...tr.querySelectorAll("td")].map((td) => td.textContent.trim())),
        )
        const want = api.timeline.filter((b) => b.won + b.lost > 0)
        check(
          rows.length === want.length &&
            rows.every((r, i) => r[1] === String(want[i].won) && r[3] === String(want[i].lost)),
          `won/lost table = API (${rows.length} buckets)`,
        )
        await page.click("[data-view-toggle=conversion] >> text=Table")
        const steps = await page.$$eval("[data-table=conversion] tbody tr", (trs) =>
          trs.map((tr) => tr.querySelectorAll("td")[1].textContent.trim()),
        )
        check(
          JSON.stringify(steps) ===
            JSON.stringify(api.conversion.steps.map((s) => String(s.reached))),
          `funnel table = API ${JSON.stringify(steps)}`,
        )
        await page.reload({ waitUntil: "load" })
        await page.waitForSelector("[data-chart-id=won-lost] .recharts-bar-rectangle", {
          timeout: 20000,
        })
        await page.waitForTimeout(400)
      }
      await page.screenshot({ path: path.join(shots, `MH-394-${tag}.png`), fullPage: true })
    }
  }
}
// The presets and a custom range drive the request, and the page follows the URL.
await page.emulateMedia({ colorScheme: "light" })
await page.setViewportSize({ width: 1440, height: 900 })
await page.goto(`${base}/en/w/${ws.slug}/reports`, { waitUntil: "load" })
await page.waitForSelector("[data-report]", { timeout: 20000 })
await page.click("[data-preset='30']")
await page.waitForURL(/days=30/, { timeout: 10000 })
await page.waitForFunction(
  () => document.querySelector("[data-report]")?.getAttribute("data-from") !== null,
  null,
  { timeout: 10000 },
)
const api30 = await page.evaluate(
  async (id) =>
    (
      await fetch("/api/w/reports/crm?days=30", {
        headers: { Accept: "application/json", "X-Workspace-ID": id },
      })
    ).json(),
  ws.id,
)
await page.waitForSelector(`[data-report][data-from="${api30.from}"]`, { timeout: 10000 })
check(true, `preset 30 days shows ${api30.from}..${api30.to}`)
await page.click("[data-custom-range]")
await page.fill("#report-from", "2026-06-01")
await page.fill("#report-to", "2026-08-31")
await page.click("text=Show report")
await page.waitForURL(/from=2026-06-01&to=2026-08-31/, { timeout: 10000 })
await page.waitForSelector(`[data-report][data-from="2026-06-01"][data-to="2026-08-31"]`, {
  timeout: 10000,
})
const apiQ = await page.evaluate(
  async (id) =>
    (
      await fetch("/api/w/reports/crm?from=2026-06-01&to=2026-08-31", {
        headers: { Accept: "application/json", "X-Workspace-ID": id },
      })
    ).json(),
  ws.id,
)
const opened = await page.$eval('[data-stat="opened"] p:nth-of-type(2)', (e) =>
  e.textContent.trim(),
)
const won = await page.$eval('[data-stat="won"] p:nth-of-type(2)', (e) =>
  e.textContent.replace(/[⁦-⁩]/g, "").trim(),
)
check(
  opened === String(apiQ.deals.opened) &&
    won ===
      new Intl.NumberFormat("en-US", { style: "currency", currency: apiQ.currency }).format(
        apiQ.deals.won_value_minor / 100,
      ),
  `custom range Jun–Aug: opened ${opened}, won ${won} = API ${apiQ.deals.opened} / ${apiQ.deals.won_value_minor}`,
)

// A won deal's close day can be changed on its page, and the change is what the API stores.
// Put back afterwards, so the demo's figures are what they were.
{
  const deal = await page.evaluate(async (id) => {
    const r = await fetch("/api/w/deals?view=list&limit=200", {
      headers: { Accept: "application/json", "X-Workspace-ID": id },
    })
    return (await r.json()).deals.find((d) => d.stage_kind === "won")
  }, ws.id)
  const before = deal.closed_at.slice(0, 10)
  const target = "2026-01-15"
  await page.goto(`${base}/en/w/${ws.slug}/deals/${deal.id}`, { waitUntil: "load" })
  await page.click("[data-closed-on-edit]")
  await page.fill("#deal-closed-on", target)
  await page.click("form:has(#deal-closed-on) button[type=submit]")
  await page.waitForSelector("#deal-closed-on", { state: "detached", timeout: 10000 })
  await page.waitForFunction((t) => document.body.innerText.includes(t), "1/15/2026", {
    timeout: 10000,
  })
  const after = await page.evaluate(
    async ([id, d]) =>
      (
        await (
          await fetch(`/api/w/deals/${d}`, {
            headers: { Accept: "application/json", "X-Workspace-ID": id },
          })
        ).json()
      ).deal.closed_at,
    [ws.id, deal.id],
  )
  check(after.startsWith(target), `deal close day set on its page: ${after}`)
  await page.evaluate(
    async ([id, d, day]) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      await fetch(`/api/w/deals/${d}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", "X-Workspace-ID": id, "X-CSRF-Token": csrf },
        body: JSON.stringify({ closed_on: day }),
      })
    },
    [ws.id, deal.id, before],
  )
}

// A workspace with no CRM data says "no data yet" everywhere and draws no zeros. A throwaway
// user of its own (an @x.test address), so the demo account gains no workspace.
const ctx2 = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "rep-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
})
const page2 = ctx2.pages()[0]
page2.on("pageerror", (e) => errors.push(String(e).slice(0, 300)))
await page2.goto(`${base}/en/register`, { waitUntil: "load" })
await page2.fill("#email", `reports-empty-${Date.now()}@x.test`)
await page2.fill("#password", `Empty-${Math.random().toString(36).slice(2)}-9x`)
await page2.click("button[type=submit]")
await page2.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page2.url())) {
  await page2.fill("#ws-name", `Reports empty ${Date.now()}`)
  await page2.click("button[type=submit]")
  await page2.waitForURL(/\/w\//, { timeout: 30000, waitUntil: "commit" })
}
const empty = { slug: page2.url().split("/w/")[1].split("/")[0] }
await page2.goto(`${base}/en/w/${empty.slug}/reports`, { waitUntil: "load" })
await page2.waitForSelector("[data-report]", { timeout: 20000 })
const emptyDom = await page2.evaluate(() => ({
  noData: [...document.querySelectorAll("[data-no-data]")].map((e) =>
    e.getAttribute("data-no-data"),
  ),
  rate: document.querySelector('[data-stat="win-rate"] p:nth-of-type(2)')?.textContent,
  bars: document.querySelectorAll(".recharts-bar-rectangle").length,
}))
check(
  emptyDom.noData.join(",") === "pipeline,conversion,timeline,sources,inbox" &&
    emptyDom.rate === "No data yet" &&
    emptyDom.bars === 0,
  `empty workspace: ${JSON.stringify(emptyDom)}`,
)
await page2.screenshot({ path: path.join(shots, "MH-394-en-light-1440-empty.png"), fullPage: true })
await ctx2.close()

check(errors.length === 0, `no page errors ${errors.join(" | ")}`)
console.log(
  JSON.stringify(
    {
      workspace: ws.slug,
      range: `${api.from}..${api.to}`,
      deals: api.deals,
      sources: api.lead_sources?.sources?.length,
    },
    null,
    0,
  ),
)
await ctx.close()
if (failures.length) {
  console.log(`\n${failures.length} FAILED`)
  process.exit(1)
}
console.log("\nall checks passed")
