// MH-1066: the Research home shows exactly what /api/w/research-overview (and /api/w/prospects)
// return, in en/ar x light/dark x 1440/390, and the Runs, Evidence, Schedules sub-pages and the
// ?tab= redirects still work. Read-only: nothing is started, accepted, drafted or scheduled; fails on
// any API write.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh1066-research-check.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3137").replace(/\/$/, "")
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh1066-"))
  const ctx = await chromium.launchPersistentContext(dir, {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
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
  await page.waitForTimeout(1500)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, { timeout: 30000 })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      const url = `${base}/${loc}/w/${slug}/research`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(url, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-flagship-funnel]", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(1500)
      const api = await page.evaluate(async (slug) => {
        const h = { headers: { "X-Workspace-ID": slug }, credentials: "include" }
        const [o, p] = await Promise.all([fetch("/api/w/research-overview", h), fetch("/api/w/prospects?status=new", h)])
        return { ov: o.ok ? await o.json() : { status: o.status }, pr: p.ok ? await p.json() : { status: p.status } }
      }, slug)
      const dom = await page.evaluate(() => {
        const num = (s) => Number(document.querySelector(s)?.dataset.value ?? NaN)
        const stagesFor = [...document.querySelectorAll("[data-flow-stages] [data-stage]")].map((n) => `${n.dataset.stage}:${n.dataset.stageStatus}`)
        return {
          funnel: ["found", "evidence", "prospects", "high_fit"].map((f) => num(`[data-funnel=${f}]`)),
          flowRun: document.querySelector("[data-flow-run]")?.dataset.flowRun,
          stages: stagesFor,
          runs: [...document.querySelectorAll("[data-recent-runs] [data-run]")].map((n) => {
            const c = (x) => Number(n.querySelector(`[data-count=${x}]`)?.dataset.value ?? NaN)
            return `${n.dataset.run}:${c("found")}/${c("evidence")}/${c("prospects")}/${c("high_fit")}`
          }),
          waiting: [...document.querySelectorAll("[data-waiting-list] [data-prospect]")].map((n) => n.dataset.prospect),
          hasDescribe: !!document.querySelector("[data-flagship-describe] [data-input=describe]"),
          hasCapNote: !!document.querySelector("[data-flagship-cap], [data-research-flagship] [data-capability=search]"),
          emptyText: [...document.querySelectorAll("[data-research-flagship] #research-flow, [data-research-flagship] #research-recent")].map((n) => n.textContent).join(" "),
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth > innerWidth + 1,
          title: document.title,
          deadButtons: [...document.querySelectorAll("[data-research-flagship] button")].filter((b) => !b.textContent.trim() && !b.getAttribute("aria-label")).length,
        }
      })
      const ov = api.ov
      check(`${k}-api`, Array.isArray(ov.runs), ov.status ?? Object.keys(ov))
      if (Array.isArray(ov.runs)) {
        const t = ov.totals
        check(`${k}-funnel-matches-api`, dom.funnel.join() === [t.found, t.evidence, t.prospects, t.high_fit].join(), { dom: dom.funnel, api: t })
        const flow = ov.runs.find((r) => r.status === "queued" || r.status === "running") ?? ov.runs[0]
        if (!flow) {
          // A workspace with no runs: no flow is drawn, and the empty state only points at the input if it is there.
          check(`${k}-no-runs-empty`, !dom.flowRun && dom.runs.length === 0, { dom })
          const pointsUp = /above|أعلاه/.test(dom.emptyText)
          check(`${k}-empty-copy-honest`, pointsUp === dom.hasDescribe, { pointsUp, describe: dom.hasDescribe })
        } else {
        check(`${k}-flow-run`, !!flow && dom.flowRun === flow.id, { dom: dom.flowRun, api: flow?.id })
        check(`${k}-stages-match-api`, !!flow && dom.stages.join() === flow.stages.map((s) => `${s.stage}:${s.status}`).join(), { dom: dom.stages })
        const want = ov.runs.slice(0, 6).map((r) => `${r.id}:${r.counts.found}/${r.counts.evidence}/${r.counts.prospects}/${r.counts.high_fit}`)
        check(`${k}-run-counts-match-api`, want.length > 0 && dom.runs.join() === want.join(), { dom: dom.runs, api: want })
        }
      }
      const wantWaiting = (api.pr.prospects ?? [])
        .slice()
        .sort((a, b) => b.score - a.score)
        .slice(0, 5)
        .map((p) => p.id)
      check(`${k}-waiting-matches-api`, dom.waiting.join() === wantWaiting.join(), { dom: dom.waiting, api: wantWaiting })
      // Without web search the input is not drawn; the page says so instead. Exactly one of the two.
      check(`${k}-describe-or-cap-note`, dom.hasDescribe !== dom.hasCapNote, { describe: dom.hasDescribe, note: dom.hasCapNote })
      check(`${k}-dir`, dom.dir === (loc === "ar" ? "rtl" : "ltr"), dom.dir)
      check(`${k}-theme`, dom.dark === (scheme === "dark"), dom.dark)
      check(`${k}-no-overflow`, !dom.overflow)
      check(`${k}-no-unlabelled-buttons`, dom.deadButtons === 0, dom.deadButtons)
      if (shots) await page.screenshot({ path: `${shots}/mh1066-${k}.png`, fullPage: true })
    }

  // The sub-pages and the old ?tab= links keep working.
  for (const sub of ["runs", "evidence", "schedules"]) {
    await page.goto(`${base}/en/w/${slug}/research/${sub}`, { waitUntil: "domcontentloaded" })
    // A cold dev compile shows a loading shell first: wait for the page to fill in.
    await page.waitForFunction(() => document.body.innerText.length > 200, null, { timeout: 30000 }).catch(() => {})
    const body = await page.evaluate(() => document.body.innerText.length)
    check(`${scheme}-sub-${sub}`, page.url().includes(`/research/${sub}`) && body > 200, { url: page.url(), body })
  }
  for (const [tab, dest] of [["runs", "runs"], ["evidence", "evidence"], ["schedules", "schedules"]]) {
    await page.goto(`${base}/en/w/${slug}/research?tab=${tab}`, { waitUntil: "domcontentloaded" })
    await page.waitForURL(new RegExp(`/research/${dest}$`), { timeout: 15000 }).catch(() => {})
    check(`${scheme}-tab-redirect-${tab}`, page.url().endsWith(`/research/${dest}`), page.url())
  }
  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
