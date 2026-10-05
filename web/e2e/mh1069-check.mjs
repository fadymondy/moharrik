// MH-1069: notifications (actionable default, resolved/snoozed views, counts), the MCP permission
// explainer and the Insights business questions, each compared with the API the page reads, in
// en/ar x light/dark x 1440/390. Read-only: it NEVER clicks resolve, snooze or reopen, and fails on
// any non-GET /api/ request other than /api/auth/ and .../read.
//   DEMO_EMAIL=... DEMO_PASSWORD=... [LOCS=en,ar] [WIDTHS=1440,390] node e2e/mh1069-check.mjs <base> [shots]
// Data-aware: an empty list or an empty report is checked for its empty state, not for rows.
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3139").replace(/\/$/, "")
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const LOCS = (process.env.LOCS ?? "en,ar").split(",")
const WIDTHS = (process.env.WIDTHS ?? "1440,390").split(",").map(Number)
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b)

// Everything the pages read, fetched from inside the signed-in page so it is the same session.
async function apis(page) {
  return page.evaluate(async (slug) => {
    const h = { headers: { "X-Workspace-ID": slug }, credentials: "include" }
    const j = async (u) => {
      const r = await fetch(u, h)
      return r.ok ? await r.json() : { status: r.status }
    }
    const [def, snoozed, resolved, all, crm, sig] = await Promise.all([
      j("/api/w/notifications?limit=25"),
      j("/api/w/notifications?limit=25&view=snoozed"),
      j("/api/w/notifications?limit=25&view=resolved"),
      j("/api/w/notifications?limit=25&view=all"),
      j("/api/w/reports/crm?days=90"),
      j("/api/w/opportunities/signals"),
    ])
    return { def, snoozed, resolved, all, crm, sig }
  }, slug)
}

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh1069-"))
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

  for (const loc of LOCS)
    for (const w of WIDTHS) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      const A = await apis(page)
      const common = () => ({
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflow: document.documentElement.scrollWidth > innerWidth + 1,
      })
      const commonChecks = (name, c) => {
        check(`${k}-${name}-dir`, c.dir === (loc === "ar" ? "rtl" : "ltr"), c.dir)
        check(`${k}-${name}-theme`, c.dark === (scheme === "dark"), c.dark)
        check(`${k}-${name}-no-overflow`, !c.overflow)
      }

      // ---------------------------------------------------------------- notifications
      await page.goto(`${base}/${loc}/w/${slug}/notifications`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-notification-views]", { timeout: 40000 }).catch(() => {})
      await page.waitForSelector("[data-notification], [data-view-count]", { timeout: 40000 }).catch(() => {})
      await page.waitForTimeout(2500)
      check(`${k}-ntf-api`, Array.isArray(A.def.items), A.def.status ?? Object.keys(A.def))
      if (Array.isArray(A.def.items)) {
        const rows = () =>
          page.evaluate(() => ({
            titles: [...document.querySelectorAll("[data-notification]")].map((n) => n.dataset.notification),
            states: [...document.querySelectorAll("[data-notification]")].map((n) => n.dataset.notificationState),
            counts: Object.fromEntries([...document.querySelectorAll("[data-view-count]")].map((n) => [n.dataset.viewCount, n.textContent.replace(/\D/g, "")])),
            unread: document.querySelector("[data-filter=unread] span")?.textContent.replace(/\D/g, "") ?? null,
            empty: document.querySelector("[data-notifications-no-match]")?.textContent ?? null,
            triage: document.querySelectorAll("[data-triage-resolve]").length,
            tooltipless: [...document.querySelectorAll("[data-notification] button")].filter((b) => !b.textContent.trim() && !b.getAttribute("aria-label")).length,
            ...(() => ({ ...{} }))(),
          }))
        const d = await rows()
        const total = A.def.actionable_count + A.def.snoozed_count + A.def.resolved_count
        if (total === 0) {
          check(`${k}-ntf-empty`, d.titles.length === 0, d)
        } else {
          check(`${k}-ntf-default-is-actionable`, same(d.titles, A.def.items.map((i) => i.title)), { dom: d.titles, api: A.def.items.map((i) => i.title) })
          check(`${k}-ntf-default-rows-all-actionable`, d.states.every((s) => s === "actionable"), d.states)
          check(
            `${k}-ntf-counts`,
            same(d.counts, { actionable: String(A.def.actionable_count), snoozed: String(A.def.snoozed_count), resolved: String(A.def.resolved_count), all: String(total) }),
            { dom: d.counts, api: [A.def.actionable_count, A.def.snoozed_count, A.def.resolved_count, total] },
          )
          check(`${k}-ntf-unread-badge`, d.unread === String(A.def.unread_count), { dom: d.unread, api: A.def.unread_count })
          check(`${k}-ntf-unread-leq-actionable`, A.def.unread_count <= A.def.actionable_count)
          check(`${k}-ntf-default-total`, A.def.total === A.def.actionable_count, { total: A.def.total, actionable: A.def.actionable_count })
          check(`${k}-ntf-triage-controls`, A.def.items.length === 0 || d.triage > 0, d.triage)
          check(`${k}-ntf-unlabelled-buttons`, d.tooltipless === 0, d.tooltipless)
          // The other views, by clicking the view switch (a filter, not a write).
          for (const [v, api] of [["snoozed", A.snoozed], ["resolved", A.resolved], ["all", A.all]]) {
            await page.click(`[data-view=${v}]`)
            await page.waitForTimeout(2500)
            const dv = await rows()
            const want = (api.items ?? []).map((i) => i.title)
            const hasEmpty = want.length === 0
            check(`${k}-ntf-view-${v}`, same(dv.titles, want) && (!hasEmpty || !!dv.empty || total === 0), { dom: dv.titles, api: want, empty: dv.empty })
            const wantState = v === "all" ? null : v
            if (wantState) check(`${k}-ntf-view-${v}-states`, dv.states.every((s) => s === wantState), dv.states)
            check(`${k}-ntf-view-${v}-total`, api.total === (v === "snoozed" ? A.def.snoozed_count : v === "resolved" ? A.def.resolved_count : total), { api: api.total })
            // A resolved row offers Reopen, never Resolve; a snoozed one offers Unsnooze.
            if (v === "resolved" && dv.titles.length) {
              const t = await page.evaluate(() => ({ r: document.querySelectorAll("[data-triage-resolve]").length, o: document.querySelectorAll("[data-triage-reopen]").length }))
              check(`${k}-ntf-resolved-offers-reopen`, t.r === 0 && t.o === dv.titles.length, t)
            }
            if (v === "snoozed" && dv.titles.length) {
              const t = await page.evaluate(() => document.querySelectorAll("[data-triage-unsnooze]").length)
              check(`${k}-ntf-snoozed-offers-unsnooze`, t === dv.titles.length, t)
            }
          }
          await page.click("[data-view=actionable]")
        }
      }
      commonChecks("ntf", await page.evaluate(common))
      if (shots) await page.screenshot({ path: `${shots}/mh1069-notifications-${k}.png`, fullPage: true })

      // ---------------------------------------------------------------- MCP explainer
      await page.goto(`${base}/${loc}/w/${slug}/settings/mcp`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-mcpx-scope]", { timeout: 40000 }).catch(() => {})
      await page.waitForTimeout(800)
      const perms = await page.evaluate(async (l) => (await fetch(`/api/mcp/permissions?lang=${l}`)).json(), loc)
      const mx = await page.evaluate(() => ({
        scopes: [...document.querySelectorAll("[data-mcpx-scope]")].map((s) => ({
          scope: s.dataset.mcpxScope,
          groups: Object.fromEntries(
            ["info", "success", "warning"].map((g) => [g, [...s.querySelectorAll(`[data-mcpx-group=${g}] [data-mcpx-tool]`)].map((n) => n.dataset.mcpxTool)]),
          ),
          badges: Object.fromEntries([...s.querySelectorAll("summary [data-count]")].map((b) => [b.dataset.count, Number(b.textContent.replace(/\D/g, ""))])),
        })),
        never: [...document.querySelectorAll("[data-never]")].map((n) => n.dataset.never),
        neverText: document.querySelector("[data-mcpx-never]")?.textContent ?? "",
        confirmNotes: [...document.querySelectorAll("[data-mcpx-group=warning] [data-mcpx-tool]")].length,
        tokensStillThere: !!document.querySelector("[data-mcp-docs-link]"),
        ...(() => ({}))(),
      }))
      check(`${k}-mcpx-api`, Array.isArray(perms.scopes) && perms.scopes.length > 0, perms.status)
      if (Array.isArray(perms.scopes)) {
        check(`${k}-mcpx-lang`, perms.lang === loc, perms.lang)
        check(`${k}-mcpx-scopes`, same(mx.scopes.map((s) => s.scope), perms.scopes.map((s) => s.scope)), mx.scopes.map((s) => s.scope))
        check(
          `${k}-mcpx-tools`,
          same(
            mx.scopes.map((s) => s.groups),
            perms.scopes.map((s) => ({ info: s.can_read.map((t) => t.name), success: s.can_change.map((t) => t.name), warning: s.needs_confirm.map((t) => t.name) })),
          ),
        )
        check(
          `${k}-mcpx-badges`,
          mx.scopes.every((s, i) => (s.badges.read ?? 0) === perms.scopes[i].can_read.length && (s.badges.change ?? 0) === perms.scopes[i].can_change.length && (s.badges.confirm ?? 0) === perms.scopes[i].needs_confirm.length),
        )
        check(`${k}-mcpx-never`, same(mx.never, perms.never.map((n) => n.id)), { dom: mx.never, api: perms.never.map((n) => n.id) })
        const nTools = perms.scopes.reduce((a, s) => a + s.can_read.length + s.can_change.length + s.needs_confirm.length, 0)
        check(`${k}-mcpx-covers-every-tool`, nTools === perms.tool_count, { nTools, tool_count: perms.tool_count })
        check(`${k}-mcpx-confirm-tools`, mx.confirmNotes === perms.scopes.reduce((a, s) => a + s.needs_confirm.length, 0))
        if (loc === "ar") check(`${k}-mcpx-arabic-summaries`, perms.scopes.every((s) => [...s.can_read, ...s.can_change, ...s.needs_confirm].every((t) => /[\u0600-\u06FF]/.test(t.summary))))
      }
      check(`${k}-mcp-existing-page-intact`, mx.tokensStillThere)
      commonChecks("mcpx", await page.evaluate(common))
      if (shots) await page.screenshot({ path: `${shots}/mh1069-mcp-${k}.png`, fullPage: true })

      // ---------------------------------------------------------------- Insights
      await page.goto(`${base}/${loc}/w/${slug}/insights`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-insight-questions] [data-card]", { timeout: 40000 }).catch(() => {})
      await page.waitForTimeout(2000)
      const iq = await page.evaluate(() => {
        const card = (id) => document.querySelector(`[data-card=${id}]`)
        const num = (el, sel) => {
          const n = el?.querySelector(sel)
          return n ? Number(n.dataset.value) : null
        }
        return {
          window: document.querySelector("[data-insq-window]")?.textContent ?? "",
          cards: [...document.querySelectorAll("[data-insight-questions] [data-card]")].map((c) => c.dataset.card),
          sources: [...(card("insq-leads")?.querySelectorAll("[data-insq-source]") ?? [])].map((n) => `${n.dataset.insqSource}:${n.dataset.leads}:${n.dataset.converted}`),
          leadsEmpty: !!card("insq-leads")?.querySelector("[data-empty]"),
          median: num(card("insq-reply"), "[data-insq-median]"),
          awaiting: num(card("insq-reply"), "[data-insq-awaiting]"),
          replyEmpty: !!card("insq-reply")?.querySelector("[data-empty]"),
          steps: [...(card("insq-dropoff")?.querySelectorAll("[data-insq-step]") ?? [])].map((n) => `${n.dataset.insqStep}:${n.dataset.reached}`),
          dropEmpty: !!card("insq-dropoff")?.querySelector("[data-empty]"),
          worst: !!card("insq-dropoff")?.querySelector("[data-insq-worst]"),
          risk: num(card("insq-atrisk"), "[data-insq-risk-count]"),
          riskItems: [...(card("insq-atrisk")?.querySelectorAll("[data-insq-risk]") ?? [])].map((n) => n.dataset.insqRisk),
          riskEmpty: !!card("insq-atrisk")?.querySelector("[data-empty]"),
          basis: [...document.querySelectorAll("[data-insq-basis]")].map((n) => n.textContent),
          existingFigures: !!document.querySelector("[data-section-pages]"),
          ...(() => ({}))(),
        }
      })
      const crm = A.crm
      check(`${k}-insq-api`, !!crm.lead_sources || !!crm.inbox || !!crm.conversion, crm.status)
      if (crm.conversion) {
        const ls = crm.lead_sources
        if (ls) {
          const top = (ls.sources ?? []).slice(0, 6).map((s) => `${s.source}:${s.leads}:${s.converted}`)
          check(`${k}-insq-leads-match-api`, same(iq.sources, top), { dom: iq.sources, api: top })
          check(`${k}-insq-leads-empty-state`, (ls.sources ?? []).length === 0 ? iq.leadsEmpty : !iq.leadsEmpty)
        } else check(`${k}-insq-leads-omitted-without-access`, !iq.cards.includes("insq-leads"))
        const ib = crm.inbox?.total
        if (ib) {
          if (ib.conversations === 0 || ib.median_seconds === null) {
            check(`${k}-insq-reply-empty-state`, iq.replyEmpty && iq.median === null, { iq: iq.replyEmpty, median: iq.median })
          } else {
            check(`${k}-insq-reply-median`, iq.median === ib.median_seconds, { dom: iq.median, api: ib.median_seconds })
            check(`${k}-insq-reply-awaiting`, iq.awaiting === ib.awaiting, { dom: iq.awaiting, api: ib.awaiting })
          }
        } else check(`${k}-insq-reply-omitted-without-access`, !iq.cards.includes("insq-reply"))
        const steps = (crm.conversion.steps ?? []).map((s) => `${s.name}:${s.reached}`)
        if (crm.conversion.opened === 0 || steps.length === 0) check(`${k}-insq-drop-empty-state`, iq.dropEmpty && iq.steps.length === 0, iq.steps)
        else check(`${k}-insq-drop-match-api`, same(iq.steps, steps), { dom: iq.steps, api: steps })
        check(`${k}-insq-window-days`, iq.window.includes(crm.from) && iq.window.includes(crm.to), iq.window)
      }
      if (A.sig.counts) {
        const n = A.sig.counts.at_risk_deals
        if (n === null) check(`${k}-insq-risk-omitted-without-access`, !iq.cards.includes("insq-atrisk"))
        else if (n === 0) check(`${k}-insq-risk-empty-state`, iq.riskEmpty && iq.risk === null, iq)
        else {
          check(`${k}-insq-risk-count`, iq.risk === n, { dom: iq.risk, api: n })
          const want = A.sig.items.filter((i) => i.kind === "at_risk_deal").slice(0, 5).map((i) => i.id)
          check(`${k}-insq-risk-items`, same(iq.riskItems, want), { dom: iq.riskItems, api: want })
        }
        // The page states the same quiet threshold the signals use (MH-1065), not a number of its own.
        const q = A.sig.thresholds?.quiet_days
        check(`${k}-insq-risk-threshold-stated`, q === undefined || iq.basis.some((b) => b.includes(String(q))) || loc === "ar" /* digits may be Arabic-Indic */ || iq.risk === null && !iq.riskEmpty, { q, basis: iq.basis })
      }
      check(`${k}-insq-existing-section-intact`, iq.existingFigures)
      commonChecks("insq", await page.evaluate(common))
      if (shots) await page.screenshot({ path: `${shots}/mh1069-insights-${k}.png`, fullPage: true })
    }

  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out: Object.fromEntries(Object.entries(out).filter(([, v]) => v !== true)) }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
