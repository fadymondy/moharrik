// MH-1067: the workflow page (Overview / Builder / Runs / Settings) and the run detail show exactly
// what the API returns, in en/ar x light/dark x 1440/390. Read-only: it never clicks Re-run; fails on
// any non-GET /api/ request (except /api/auth/).
//   DEMO_EMAIL=... DEMO_PASSWORD=... [LOCS=en,ar] [WIDTHS=1440,390] [SCHEMES=light,dark]
//   node e2e/mh1067-automation-check.mjs <base> [shots-dir]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync, mkdirSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3138").replace(/\/$/, "")
const shots = process.argv[3]
if (shots) mkdirSync(shots, { recursive: true })
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const list = (k, d) => (process.env[k] ?? d).split(",").map((s) => s.trim()).filter(Boolean)
const LOCS = list("LOCS", "en,ar")
const WIDTHS = list("WIDTHS", "1440,390").map(Number)
const SCHEMES = list("SCHEMES", "light,dark")
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const WINDOW_MS = 30 * 24 * 3600 * 1000

for (const scheme of SCHEMES) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh1067-"))
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
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !u.pathname.startsWith("/api/auth/"))
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

  const get = (p) =>
    page.evaluate(
      async ([p, slug]) => {
        const r = await fetch(p, { headers: { "X-Workspace-ID": slug }, credentials: "include" })
        return r.ok ? await r.json() : { __status: r.status }
      },
      [p, slug],
    )

  const { workflows = [] } = await get("/api/w/workflows")
  check(`${scheme}-has-workflows`, workflows.length > 0, workflows.length)
  // One workflow with runs, one without: the empty state is checked as hard as the full one.
  const withRuns = []
  const without = []
  for (const w of workflows) {
    const ov = await get(`/api/w/workflows/${w.id}/overview`)
    check(`${scheme}-overview-api-${w.slug}`, !ov.__status, ov.__status)
    ;(ov.runs?.total > 0 || ov.last_run ? withRuns : without).push({ w, ov })
  }

  // The API itself, against the runs it lists: counted here, independently, from the executions.
  for (const { w, ov } of [...withRuns, ...without]) {
    let rows = [],
      before = ""
    for (let i = 0; i < 20; i++) {
      const pg = await get(`/api/w/workflows/${w.id}/executions?limit=100${before ? `&before=${before}` : ""}`)
      rows = rows.concat(pg.executions ?? [])
      if (!pg.next_before) break
      before = pg.next_before
    }
    const win = rows.filter((r) => Date.now() - new Date(r.created_at).getTime() < WINDOW_MS)
    const n = (s) => win.filter((r) => r.status === s).length
    const want = {
      total: win.length,
      succeeded: n("ok"),
      failed: n("failed"),
      cancelled: n("cancelled"),
      in_progress: n("queued") + n("running"),
    }
    check(`${scheme}-counts-match-executions-${w.slug}`, JSON.stringify(want) === JSON.stringify(ov.runs), { want, got: ov.runs })
    const fin = want.succeeded + want.failed
    check(
      `${scheme}-rate-matches-executions-${w.slug}`,
      ov.success_rate.finished === fin &&
        ov.success_rate.succeeded === want.succeeded &&
        (fin === 0 ? ov.success_rate.percent === null : Math.abs(ov.success_rate.percent - (100 * want.succeeded) / fin) < 0.01),
      ov.success_rate,
    )
    const newest = rows[0]
    check(`${scheme}-last-run-matches-${w.slug}`, newest ? ov.last_run?.id === newest.id : ov.last_run === null, { api: ov.last_run?.id, newest: newest?.id })
    ov.__rows = rows
  }

  // The runs whose detail is checked: a failed one if there is one, the newest, and a run of any other kind.
  const runIds = new Set()
  for (const { ov } of withRuns) {
    const failed = ov.__rows.find((r) => r.status === "failed")
    if (failed) runIds.add(failed.id)
    if (ov.__rows[0]) runIds.add(ov.__rows[0].id)
    const ok = ov.__rows.find((r) => r.status === "ok")
    if (ok) runIds.add(ok.id)
  }
  check(`${scheme}-has-runs-to-check`, runIds.size > 0, runIds.size)

  for (const loc of LOCS)
    for (const w of WIDTHS) {
      await page.setViewportSize({ width: w, height: 900 })
      const k0 = `${loc}-${scheme}-${w}`
      const wsBase = `${base}/${loc}/w/${slug}/automation`
      const generic = async (k) => {
        const dom = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth > innerWidth + 1,
          unlabelled: [...document.querySelectorAll("main button, [data-canvas-page] button")].filter(
            (b) => !b.textContent.trim() && !b.getAttribute("aria-label") && !b.getAttribute("title"),
          ).length,
        }))
        check(`${k}-dir`, dom.dir === (loc === "ar" ? "rtl" : "ltr"), dom.dir)
        check(`${k}-theme`, dom.dark === (scheme === "dark"), dom.dark)
        check(`${k}-no-overflow`, !dom.overflow)
        check(`${k}-no-unlabelled-buttons`, dom.unlabelled === 0, dom.unlabelled)
      }
      const shot = async (name) => {
        if (shots) await page.screenshot({ path: `${shots}/mh1067-${name}-${k0}.png`, fullPage: false })
      }

      for (const { w: wf, ov } of [...withRuns, ...without]) {
        const k = `${k0}-${wf.slug}`
        // ---- Overview (the default for a saved workflow: a bare link lands here) ----
        await page.goto(`${wsBase}/workflows/${wf.id}`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("[data-wfo-overview]", { timeout: 45000 }).catch(() => {})
        await page.waitForTimeout(600)
        const o = await page.evaluate(() => {
          const m = (id) => document.querySelector(`[data-metric=${id}]`)
          const raw = (id) => m(id)?.dataset.raw
          return {
            has: !!document.querySelector("[data-wfo-overview]"),
            status: raw("status"),
            last: raw("last"),
            rate: raw("rate"),
            rateNote: m("rate")?.querySelector("[data-metric-note]")?.textContent ?? "",
            rateText: m("rate")?.querySelector("[data-metric-value]")?.textContent ?? "",
            items: raw("items"),
            itemsText: m("items")?.querySelector("[data-metric-value]")?.textContent ?? "",
            counts: Object.fromEntries([...document.querySelectorAll("[data-count]")].map((n) => [n.dataset.count, n.querySelector("dd")?.textContent])),
            steps: [...document.querySelectorAll("[data-wfo-step]")].map((n) => n.dataset.wfoStep).join(),
            failure: document.querySelector("[data-wfo-failure]") ? "yes" : "no",
            tabs: [...document.querySelectorAll("[data-view-tab]")].map((n) => n.dataset.viewTab).join(),
            title: document.title,
          }
        })
        check(`${k}-overview-renders`, o.has, o)
        check(`${k}-tabs`, o.tabs === "overview,editor,executions,settings", o.tabs)
        check(`${k}-status-matches-api`, o.status === ov.status, { dom: o.status, api: ov.status })
        check(`${k}-last-matches-api`, o.last === (ov.last_run ? `${ov.last_run.id}:${ov.last_run.status}` : "none"), { dom: o.last, api: ov.last_run?.id })
        check(
          `${k}-rate-matches-api`,
          o.rate === (ov.success_rate.percent == null ? "none" : String(Math.round(ov.success_rate.percent))),
          { dom: o.rate, api: ov.success_rate },
        )
        if (ov.success_rate.percent == null) {
          // No finished run: the card says "—" and never an invented 0% or 100%.
          check(`${k}-rate-empty-is-dash`, o.rateText.trim() === "—" && !/%|٪/.test(o.rateText), o.rateText)
        } else {
          // The denominator is on the card, whatever the numerals.
          const digits = o.rateNote.replace(/[٠-٩]/g, (d) => "٠١٢٣٤٥٦٧٨٩".indexOf(d))
          check(
            `${k}-rate-denominator-shown`,
            digits.includes(String(ov.success_rate.finished)) && digits.includes(String(ov.success_rate.succeeded)),
            o.rateNote,
          )
        }
        check(`${k}-items-matches-api`, o.items === (ov.items.total == null ? "none" : String(ov.items.total)), { dom: o.items, api: ov.items })
        if (ov.items.total == null) check(`${k}-items-empty-is-dash`, o.itemsText.trim() === "—", o.itemsText)
        const c = o.counts
        const digitsOf = (s) => String(s ?? "").replace(/[٠-٩]/g, (d) => "٠١٢٣٤٥٦٧٨٩".indexOf(d)).replace(/[^0-9]/g, "")
        check(
          `${k}-counts-match-api`,
          [c.total, c.ok, c.failed, c.cancelled, c.live].map(digitsOf).join() ===
            [ov.runs.total, ov.runs.succeeded, ov.runs.failed, ov.runs.cancelled, ov.runs.in_progress].join(),
          { dom: c, api: ov.runs },
        )
        check(`${k}-steps-listed`, o.steps === wf.steps?.map((_, i) => i).join() || (wf.steps === undefined), { dom: o.steps })
        check(`${k}-failure-callout-iff-last-failed`, o.failure === (ov.last_run?.status === "failed" ? "yes" : "no"), o.failure)
        await generic(`${k}-overview`)
        if (wf === withRuns[0]?.w) await shot("overview")

        // ---- Builder: the canvas nodes are business cards ----
        await page.goto(`${wsBase}/workflows/${wf.id}?view=editor`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("[data-canvas-node=step]", { timeout: 45000 }).catch(() => {})
        await page.waitForTimeout(1200)
        const nodes = await page.evaluate(() => ({
          steps: document.querySelectorAll("[data-canvas-node=step]").length,
          say: document.querySelectorAll("[data-node-say]").length,
          rec: [...document.querySelectorAll("[data-node-record]")].map((n) => n.dataset.nodeRecord).join(),
        }))
        const wantSteps = (await get(`/api/w/workflows/${wf.id}`)).workflow.steps.length
        check(`${k}-builder-nodes`, nodes.steps === wantSteps, { dom: nodes.steps, api: wantSteps })
        check(`${k}-builder-plain-summaries`, nodes.say === wantSteps, nodes)
        const wantRec = Array.from({ length: wantSteps }, (_, i) => {
          const st = ov.steps.find((s) => s.position === i)
          return !st || st.ran === 0 ? "idle" : st.failed > 0 ? "failed" : "ok"
        })
        check(`${k}-builder-records-match-api`, nodes.rec.split(",").filter(Boolean).sort().join() === wantRec.sort().join(), { dom: nodes.rec, api: wantRec })
        await generic(`${k}-builder`)
        if (wf === withRuns[0]?.w) await shot("builder")

        // ---- Runs: the old ?view=executions link ----
        await page.goto(`${wsBase}/workflows/${wf.id}?view=executions`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("[data-view-tab=executions][aria-current=page]", { timeout: 45000 }).catch(() => {})
        await page.waitForTimeout(1200)
        const onRuns = await page.evaluate(() => document.querySelector("[data-view-tab=executions]")?.getAttribute("aria-current"))
        check(`${k}-runs-tab-current`, onRuns === "page", onRuns)
        await generic(`${k}-runs`)

        // ---- Settings ----
        await page.goto(`${wsBase}/workflows/${wf.id}?view=settings`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("[data-wfo-settings]", { timeout: 45000 }).catch(() => {})
        const set = await page.evaluate(() => ({
          has: !!document.querySelector("[data-wfo-settings]"),
          on: document.querySelector("[data-wfo-enabled]")?.getAttribute("aria-checked") ?? document.querySelector("[data-wfo-enabled]")?.getAttribute("data-checked"),
        }))
        check(`${k}-settings-renders`, set.has, set)
        check(`${k}-settings-enabled-matches-api`, String(set.on === "" ? "true" : set.on) === String(wf.enabled) || (wf.enabled && set.on !== "false"), { dom: set.on, api: wf.enabled })
        await generic(`${k}-settings`)
      }

      // ---- Run detail, against /api/w/runs/{id} ----
      for (const id of runIds) {
        const api = await get(`/api/w/runs/${id}`)
        const k = `${k0}-run-${id.slice(0, 8)}`
        await page.goto(`${wsBase}/runs/${id}`, { waitUntil: "domcontentloaded" })
        await page.waitForSelector("[data-run-summary]", { timeout: 45000 }).catch(() => {})
        await page.waitForTimeout(800)
        const d = await page.evaluate(() => ({
          status: document.querySelector("[data-run-status]")?.dataset.runStatus,
          steps: [...document.querySelectorAll("[data-step]")].map((n) => ({
            p: Number(n.dataset.step),
            s: n.dataset.stepStatus,
            input: !!n.querySelector("[data-step-input]"),
            error: n.querySelector("[data-step-error]")?.textContent ?? "",
            result: n.querySelector("pre:not([data-step-input])")?.textContent ?? "",
          })),
          failure: document.querySelector("[data-run-failure]")?.dataset.runFailure ?? null,
          failureText: document.querySelector("[data-run-failure]")?.textContent ?? "",
          rerunOf: document.querySelector("[data-rerun-of]")?.dataset.rerunOf ?? null,
          rerun: !!document.querySelector("[data-rerun]"),
          rerunDisabled: document.querySelector("[data-rerun]")?.disabled ?? null,
          wf: document.querySelector("[data-run-workflow] a")?.getAttribute("href") ?? null,
        }))
        const r = api.run
        check(`${k}-status`, d.status === r.status, { dom: d.status, api: r.status })
        check(`${k}-steps-match-api`, d.steps.map((s) => `${s.p}:${s.s}`).join() === api.steps.map((s) => `${s.position}:${s.status}`).join(), { dom: d.steps.map((s) => `${s.p}:${s.s}`), api: api.steps.map((s) => `${s.position}:${s.status}`) })
        // Only persisted fields: an input box only where args are stored, an error only where one is.
        const hasArgs = (a) => !!a && a !== "{}" && a !== "null"
        check(`${k}-input-only-when-stored`, api.steps.every((s) => d.steps.find((x) => x.p === s.position)?.input === hasArgs(s.args)), d.steps)
        check(`${k}-errors-match-api`, api.steps.every((s) => (d.steps.find((x) => x.p === s.position)?.error.includes(s.error) ?? false) === !!s.error || !s.error), d.steps.map((s) => s.error))
        check(`${k}-results-verbatim`, api.steps.every((s) => !s.result || d.steps.find((x) => x.p === s.position)?.result === s.result), "result differs")
        const firstFailed = api.steps.find((s) => s.status === "failed")
        if (r.status === "failed") {
          check(`${k}-failure-card`, d.failure === (firstFailed ? String(firstFailed.position) : "run"), { dom: d.failure })
          if (firstFailed) {
            check(`${k}-failure-says-error`, !firstFailed.error || d.failureText.includes(firstFailed.error), d.failureText)
            check(`${k}-failure-says-command`, d.failureText.includes(firstFailed.command), d.failureText)
          }
        } else check(`${k}-no-failure-card`, d.failure === null, d.failure)
        check(`${k}-rerun-link`, (r.rerun_of ?? "") === (d.rerunOf ?? ""), { dom: d.rerunOf, api: r.rerun_of })
        // The button exists for people who may run workflows, and is disabled while the run is going. Never clicked here.
        const live = r.status === "queued" || r.status === "running"
        check(`${k}-rerun-button`, d.rerun && d.rerunDisabled === live, d)
        await generic(k)
        if (shots && r.status === "failed") await shot("run-failed")
        else if (shots && r.status === "ok" && id === [...runIds].find((x) => true)) await shot("run")
      }
    }

  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
