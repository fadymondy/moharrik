// The intent sidebar (MH-1060, swept since MH-1073): the sections and rows in order, every
// row a link to its page, the active row decided by the URL's longest prefix (so /settings/mcp
// is MCP, not Settings), each section's sub-pages nested under the active row, every page
// answering 200, old URLs that redirect instead of 404ing, no horizontal overflow, and the
// mobile sheet at 390.
//
// Registers a throwaway owner, so run it against local or dev, never production:
//   node e2e/nav-sweep-check.mjs [base] [--shots <card>]
// Mirrors web/lib/nav-top.ts (and the sub-page paths from nav-config.ts). Kept in sync by
// hand: a row or sub-page added there without a matching entry here fails this sweep
// instead of shipping silently unswept.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const args = process.argv.slice(2)
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const base = (args[0] ?? "http://localhost:3520").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROME = process.env.E2E_CHROME ?? "C:/Program Files/Google/Chrome/Application/chrome.exe"

// Sections in sidebar order; each row: [key, path, subs]. An owner sees every row and sub.
const SECTIONS = [
  {
    key: "main",
    rows: [
      ["home", ""],
      ["customers", "contacts", ["crm", "contacts", "companies", "leads", "contacts/import"]],
      ["inbox", "inbox", ["inbox/overview", "inbox", "inbox/stats", "inbox/canned", "inbox/email-templates"]],
      ["opportunities", "deals"],
      ["research", "research", ["research/runs", "research/evidence", "research/schedules", "prospects"]],
    ],
  },
  {
    key: "automate",
    rows: [
      [
        "automations",
        "automation",
        [
          "automation/workflows",
          "automation/rules",
          "automation/presets",
          "automation/runs",
          "automation/browsers",
          "automation/extension",
          "automation/settings",
        ],
      ],
      ["campaigns", "campaigns", ["marketing", "campaigns", "subscribe-forms"]],
      ["schedules", "automation/schedules"],
    ],
  },
  {
    key: "understand",
    rows: [
      ["insights", "insights", ["analytics", "search-console", "youtube", "trends", "trends/sources"]],
      ["reports", "reports"],
    ],
  },
  {
    key: "connect",
    rows: [
      ["channels", "inbox/channels"],
      ["integrations", "settings/integrations"],
      ["mcp", "settings/mcp"],
    ],
  },
  { key: "docs", rows: [["mcp-docs", null]] },
  // Footer rows never nest their sub-pages; Settings' pages are cards on the Settings page.
  { key: "footer", rows: [["team", "members"], ["settings", "settings"]] },
]
const ROWS = SECTIONS.flatMap((s) => s.rows)
const SETTINGS_PAGES = [
  "settings/general",
  "settings/email",
  "settings/icp",
  "settings/automation-profile",
  "settings/audit",
  "settings/delivery",
]

// The row that must light up for a path: the longest-prefix rule's sharp edges.
const OWNERS = [
  ["settings/mcp", "mcp"],
  ["settings/integrations", "integrations"],
  ["settings/icp", "settings"],
  ["automation/schedules", "schedules"],
  ["automation/runs", "automations"],
  ["inbox/channels", "channels"],
  ["inbox/stats", "inbox"],
  ["prospects", "research"],
  ["marketing", "campaigns"],
  ["trends", "insights"],
  ["crm", "customers"],
]

// Pages no row owns any more (reached from the bell); they must still load.
const UNLISTED = ["notifications", "notifications/overview", "notifications/sources"]

// /research keeps its old tab links (MH-673).
const OLD_REDIRECTS = [
  { from: "research?tab=evidence", to: "research/evidence" },
  { from: "research?tab=schedules", to: "research/schedules" },
]

const out = {}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "navsweep-")), {
  executablePath: CHROME,
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "dark",
})

// Rows and their nested sub-pages both mark the current one with aria-current; tell them apart.
const activeRow = (page) =>
  page.evaluate(() => {
    const a = [...document.querySelectorAll("[data-nav-section] a[data-nav-item][aria-current=page]")]
    return a
      .filter((x) => !x.closest("[data-nav-subs]"))
      .map((x) => x.getAttribute("data-nav-item"))
      .join(",")
  })
const activeSub = (page) =>
  page.evaluate(() =>
    [...document.querySelectorAll("[data-nav-subs] a[data-nav-item][aria-current=page]")]
      .map((x) => x.getAttribute("href"))
      .join(","),
  )
// Rows gated by a permission appear once the role loads, and the active row follows the
// client router, so read until the value is the wanted one (or 5s pass) and report the last.
async function settle(read, want) {
  let v
  for (let i = 0; i < 25; i++) {
    v = await read()
    if (v === want) return "ok"
    await new Promise((r) => setTimeout(r, 200))
  }
  return `got ${v || "none"}`
}
const overflow = (page) =>
  page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)

try {
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "load" })
  await page.fill("#email", `navsweep-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `NavSweep ${Date.now().toString(36)}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  const w = (p = "") => `${base}/en/w/${slug}${p ? "/" + p : ""}`
  const href = (p) => `/en/w/${slug}${p ? "/" + p : ""}`

  await page.waitForSelector("[data-nav-section] a[data-nav-item]")
  await settle(
    () => page.$$eval("[data-nav-section] a[data-nav-item]", (as) => as.length),
    ROWS.length,
  )

  // ---- the sections and their rows, in order ----
  const layout = await page.evaluate(() =>
    [...document.querySelectorAll("[data-nav-section]")].map((s) => ({
      key: s.getAttribute("data-nav-section"),
      rows: [...s.querySelectorAll(":scope a[data-nav-item]")].map((a) => a.getAttribute("data-nav-item")),
    })),
  )
  for (const s of SECTIONS) {
    const got = layout.find((l) => l.key === s.key)
    const want = s.rows.map((r) => r[0]).join(",")
    out[`section:${s.key}:rows`] = got?.rows.join(",") === want ? "match" : `got ${got?.rows.join(",") ?? "none"}`
  }
  out["sections:order"] =
    layout.map((l) => l.key).join(",") === SECTIONS.map((s) => s.key).join(",")
      ? "match"
      : `got ${layout.map((l) => l.key).join(",")}`

  // ---- every row: clicking it lands on its page, which lights that row and lists its subs ----
  for (const [key, p, subs] of ROWS) {
    if (p === null) continue // MCP docs: a public page outside the workspace
    await page.goto(w(), { waitUntil: "load" })
    await page.click(`[data-nav-section] a[data-nav-item="${key}"]`)
    await page.waitForURL(w(p), { timeout: 15000 }).catch(() => {})
    out[`row:${key}:navigates`] = page.url() === w(p) ? "ok" : page.url()
    await page.waitForTimeout(300)
    out[`row:${key}:active`] = await settle(() => activeRow(page), key)
    if (!subs) continue
    const hrefs = await page
      .$$eval(`[data-nav-subs="${key}"] a[data-nav-item]`, (as) => as.map((a) => a.getAttribute("href")))
      .catch(() => [])
    out[`row:${key}:subs`] =
      hrefs.join(",") === subs.map(href).join(",") ? "match" : `got ${hrefs.join(",") || "none"}`

    // each sub-page answers 200, keeps its parent row lit, and fits at 1440
    for (const sp of subs) {
      const resp = await page.goto(w(sp), { waitUntil: "load" })
      out[`page:${sp}:status`] = resp?.status() ?? 0
      await page.waitForSelector("[data-nav-section] a[data-nav-item]")
      const owner = OWNERS.find(([path]) => path === sp)?.[1] ?? key
      out[`page:${sp}:owner`] = await settle(() => activeRow(page), owner)
      out[`page:${sp}:sub`] = await settle(() => activeSub(page), href(sp))
      const sw = await overflow(page)
      out[`overflow:${sp}:1440`] = sw <= 1 ? "ok" : `+${sw}px`
    }
  }

  // ---- the longest prefix wins, and the pages no row lists still load ----
  for (const [p, owner] of OWNERS) {
    await page.goto(w(p), { waitUntil: "load" })
    await page.waitForSelector("[data-nav-section] a[data-nav-item]")
    out[`owner:${p}`] = await settle(() => activeRow(page), owner)
  }
  for (const p of [...SETTINGS_PAGES, ...UNLISTED]) {
    const resp = await page.goto(w(p), { waitUntil: "load" })
    out[`page:${p}:status`] = resp?.status() ?? 0
  }

  // ---- section dashboards (MH-673) still render their page list ----
  for (const p of ["crm", "marketing", "inbox/overview", "automation", "research", "insights", "settings"]) {
    await page.goto(w(p), { waitUntil: "load" })
    out[`dashboard:${p}`] =
      (await page.waitForSelector("[data-section-home] [data-section-pages]", { timeout: 15000 }).catch(() => null)) !==
      null
  }

  // ---- old URLs redirect (client-side replace), nothing 404s ----
  for (const r of OLD_REDIRECTS) {
    await page.goto(w(r.from), { waitUntil: "load" })
    await page.waitForURL(w(r.to), { timeout: 10000 }).catch(() => {})
    out[`redirect:${r.from}`] = page.url() === w(r.to) ? "ok" : page.url()
  }

  // ---- 390: no horizontal overflow; the sidebar is a sheet that opens on the toggle ----
  await page.setViewportSize({ width: 390, height: 844 })
  for (const p of ["", "contacts", "inbox", "automation/browsers", "research/runs", "trends", "settings"]) {
    await page.goto(w(p), { waitUntil: "load" })
    const sw = await overflow(page)
    out[`overflow:${p || "home"}:390`] = sw <= 1 ? "ok" : `+${sw}px`
  }
  const SHEET = "[data-slot=app-shell-sheet]"
  await page.goto(w(), { waitUntil: "load" })
  await page.click("[data-nav-toggle]")
  await page.waitForSelector(`${SHEET} a[data-nav-item]`, { timeout: 10000 })
  out["mobileSheet:rows"] = await settle(
    () => page.$$eval(`${SHEET} a[data-nav-item]`, (as) => as.map((a) => a.getAttribute("data-nav-item")).join(",")),
    ROWS.map((r) => r[0]).join(","),
  )
  // a row navigates and closes the sheet; reopened, the active row shows its sub-pages
  await page.click(`${SHEET} a[data-nav-item="customers"]`)
  await page.waitForURL(w("contacts"), { timeout: 10000 })
  await page.waitForSelector(SHEET, { state: "detached", timeout: 10000 }).catch(() => {})
  out["mobileSheet:rowNavigatesAndCloses"] = (await page.$(SHEET)) === null
  await page.click("[data-nav-toggle]")
  out["mobileSheet:showsSubs"] =
    (await page.waitForSelector(`${SHEET} [data-nav-subs="customers"] a[data-nav-item="leads"]`, { timeout: 10000 }).catch(() => null)) !==
    null
  await page.click(`${SHEET} [data-nav-subs="customers"] a[data-nav-item="leads"]`)
  await page.waitForURL(w("leads"), { timeout: 10000 })
  await page.waitForSelector(SHEET, { state: "detached", timeout: 10000 }).catch(() => {})
  out["mobileSheet:subNavigatesAndCloses"] = (await page.$(SHEET)) === null
  await page.setViewportSize({ width: 1440, height: 900 })

  // ---- screenshots: a page per section, en/ar x light/dark x 1440/390 ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({ executablePath: CHROME, headless: true })
    const pages = ["", "contacts", "inbox/overview", "automation", "insights", "settings/mcp"]
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({ storageState: state, viewport: { width, height: 900 }, colorScheme: theme })
          const p = await c.newPage()
          for (const pg of pages) {
            await p.goto(`${base}/${locale}/w/${slug}${pg ? "/" + pg : ""}`, { waitUntil: "load" })
            await p.waitForSelector("[data-nav-section] a[data-nav-item]", { timeout: 15000 }).catch(() => {})
            await p.waitForTimeout(400)
            const name = (pg || "home").replace(/\//g, "-")
            await p.screenshot({ path: `${SHOTS}/${card}-${name}-${locale}-${theme}-${width}.png` })
          }
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  await ctx.close().catch(() => {})
}

console.log(JSON.stringify(out, null, 2))
const isFail = (k, v) => {
  if (k === "error") return true
  if (typeof v === "boolean") return v === false
  if (k.startsWith("page:") && k.endsWith(":status")) return v !== 200
  return typeof v === "string" && v !== "ok" && v !== "match"
}
const failed = Object.entries(out).filter(([k, v]) => isFail(k, v))
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
