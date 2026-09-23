// Screenshot tour of the seeded demo workspace (see seed-demo.mjs), page by page.
//
//   node e2e/demo-tour.mjs https://app.moharrik.com <out-dir>
//
// Signs in as demo@moharrik.com through the login form (password from DEMO_PASSWORD or
// ~/.moharrik-demo.json), then visits every workspace page in en/light and ar/dark at 1440,
// in a real painting headless Chrome. For each page it records page errors, console errors,
// failed requests, untranslated keys, horizontal overflow, the direction, a 404, a page still
// showing skeletons, an error box, and how much it actually shows. Prints JSON.
import { chromium } from "playwright-core"
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const base = (process.argv[2] ?? "https://app.moharrik.com").replace(/\/$/, "")
const OUT = process.argv[3] ?? path.join(tmpdir(), "demo-tour")
const ONLY = process.env.ONLY?.split(",")
const WS_NAME = "Moharrik Demo — Nile Coffee Co."
const EMAIL = process.env.DEMO_EMAIL ?? "demo@moharrik.com"
const store = path.join(homedir(), ".moharrik-demo.json")
const PASSWORD = process.env.DEMO_PASSWORD ?? (existsSync(store) ? JSON.parse(readFileSync(store, "utf8"))[base]?.password : undefined)
if (!PASSWORD) throw new Error("no password: set DEMO_PASSWORD")
mkdirSync(OUT, { recursive: true })

const here = path.dirname(fileURLToPath(import.meta.url))
const flat = (o, p = "") => Object.entries(o).flatMap(([k, v]) => (typeof v === "object" ? flat(v, p + k + ".") : [p + k]))
const KEYS = flat(JSON.parse(readFileSync(path.join(here, "..", "lang", "en.json"), "utf8"))).filter((k) => k.includes("."))

async function session(theme, locale) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "demo-tour-")), {
    executablePath: process.env.CHROME_PATH ?? "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true, viewport: { width: 1440, height: 900 }, colorScheme: theme, locale: locale === "ar" ? "ar-EG" : "en-US",
    args: ["--disable-extensions", "--no-first-run"],
  })
  await ctx.addInitScript((t) => { try { localStorage.setItem("theme", t) } catch {} }, theme)
  const page = ctx.pages()[0]
  await page.goto(`${base}/${locale}/login`, { waitUntil: "networkidle" })
  const frames = await page.evaluate(() => new Promise((r) => { let n = 0; const t0 = performance.now(); const f = () => (performance.now() - t0 < 1000 ? (n++, requestAnimationFrame(f)) : r(n)); requestAnimationFrame(f) }))
  await page.fill("#email", EMAIL)
  await page.fill("#password", PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  return { ctx, page, frames }
}

const en = await session("light", "en")
// Ids for the detail pages, read the way the app reads them.
const ids = await en.page.evaluate(async (name) => {
  const j = (u, ws) => fetch(u, { headers: ws ? { "X-Workspace-ID": ws } : {} }).then((r) => r.json())
  const ws = (await j("/api/me/workspaces")).workspaces.find((w) => w.name === name)
  const g = (u) => j(u, ws.id)
  const [co, ct, dl, ld, rs, cv] = await Promise.all([g("/api/w/companies"), g("/api/w/contacts"), g("/api/w/deals"), g("/api/w/leads"), g("/api/w/research"), g("/api/w/conversations")])
  const pick = (arr, f) => arr.find(f) ?? arr[0]
  return {
    ws: ws.id, slug: ws.slug,
    company: pick(co.companies, (c) => c.name.startsWith("فندق النيل"))?.id,
    contact: pick(ct.contacts, (c) => c.email === "hana.elsayed@beanandbrew.example")?.id,
    deal: pick(dl.deals, (d) => d.title.startsWith("Nile Residence"))?.id,
    lead: pick(ld.leads, (l) => l.email === "j.whitfield@gulfholdings.example")?.id,
    leadConverted: pick(ld.leads, (l) => l.status === "converted")?.id,
    research: rs.runs[0]?.id,
    conversation: pick(cv.conversations, (c) => c.customer_email === "ahmed.saeed@mail.example")?.id,
  }
}, WS_NAME)
const w = `/w/${ids.slug}`

// [name, route, action?]
const PAGES = [
  ["workspaces", "/workspaces"],
  ["overview", w],
  ["leads", `${w}/leads`],
  ["lead-detail", `${w}/leads/${ids.lead}`],
  ["lead-converted", `${w}/leads/${ids.leadConverted}`],
  ["companies", `${w}/companies`],
  ["company-detail", `${w}/companies/${ids.company}`],
  ["contacts", `${w}/contacts`],
  ["contact-detail", `${w}/contacts/${ids.contact}`],
  ["deals", `${w}/deals`],
  ["deal-detail", `${w}/deals/${ids.deal}`],
  ["campaigns", `${w}/campaigns`],
  ["campaign-edit-preview", `${w}/campaigns`, async (p) => {
    await p.click("[data-edit-campaign]", { timeout: 10000 })
    await p.waitForSelector("[data-edit-sheet]", { timeout: 10000 })
    await p.click("[data-edit-sheet] [data-tab=preview]", { timeout: 10000 })
    await p.waitForTimeout(1500)
  }],
  ["website-forms", `${w}/subscribe-forms`],
  ["inbox", `${w}/inbox`],
  ["inbox-thread", `${w}/inbox`, async (p) => {
    await p.click(`[data-conversation="${ids.conversation}"]`, { timeout: 10000 })
    await p.waitForSelector(`[data-thread="${ids.conversation}"]`, { timeout: 10000 })
    await p.waitForTimeout(1200)
  }],
  ["inbox-widgets", `${w}/inbox/widgets`],
  ["inbox-email", `${w}/inbox/email`],
  ["inbox-whatsapp", `${w}/inbox/whatsapp`],
  ["automation", `${w}/automation`],
  ["trends", `${w}/trends`],
  ["trends-sources", `${w}/trends/sources`],
  ["research", `${w}/research`],
  ["research-run", `${w}/research/${ids.research}`],
  ["notifications", `${w}/notifications`],
  ["notifications-sources", `${w}/notifications/sources`],
  ["members", `${w}/members`],
  ["settings-email", `${w}/settings/email`],
  ["settings-integrations", `${w}/settings/integrations`],
  ["settings-mcp", `${w}/settings/mcp`],
]

async function visit(sess, locale, theme, [name, route, action], width = 1440) {
  const { page } = sess
  const errors = [], consoleErrors = [], failed = []
  const onErr = (e) => errors.push(String(e).slice(0, 300))
  const onCon = (m) => { if (m.type() === "error") consoleErrors.push(m.text().slice(0, 300)) }
  const onRes = (r) => { if (r.status() >= 400 && !r.url().includes("/_next/")) failed.push(`${r.status()} ${r.request().method()} ${r.url().replace(base, "")}`) }
  const onFail = (r) => { const f = r.failure()?.errorText ?? ""; if (!/ERR_ABORTED/.test(f)) failed.push(`FAILED ${r.url().replace(base, "")} ${f}`) }
  page.on("pageerror", onErr); page.on("console", onCon); page.on("response", onRes); page.on("requestfailed", onFail)
  let status = 0, actionError = ""
  try {
    const resp = await page.goto(`${base}/${locale}${route}`, { waitUntil: "domcontentloaded", timeout: 45000 })
    status = resp?.status() ?? 0
    await page.waitForLoadState("load").catch(() => {})
    await page.waitForFunction(() => !document.querySelector("[aria-busy=true],[aria-busy='']"), null, { timeout: 15000 }).catch(() => {})
    await page.waitForTimeout(1800)
    if (action) await action(page).catch((e) => { actionError = String(e).split("\n")[0].slice(0, 200) })
  } catch (e) { actionError = "navigation: " + String(e).split("\n")[0].slice(0, 200) }
  const file = path.join(OUT, `demo-${name}-${locale}-${theme}${width === 1440 ? "" : "-" + width}.png`)
  await page.screenshot({ path: file, fullPage: true }).catch(() => {})
  const probe = await page.evaluate((keys) => {
    const main = document.querySelector("main") ?? document.body
    const text = main.innerText
    const all = document.body.innerText
    const visibleKeys = keys.filter((k) => all.includes(k))
    const busy = document.querySelectorAll("[aria-busy]").length
    const alerts = [...document.querySelectorAll("[role=alert]")].map((e) => e.innerText.trim()).filter(Boolean).slice(0, 3)
    const notFound = /not found|404|لم يتم العثور|غير موجود/i.test(document.title + " " + (document.querySelector("h1")?.innerText ?? ""))
    const placeholders = (all.match(/\{[a-zA-Z_]+\}/g) ?? []).slice(0, 5)
    // The font body text actually renders in: the declared stack, and which faces loaded.
    // Skip monospace labels (kickers, keys, ids): the question is what running text uses.
    const textEl = [...main.querySelectorAll("p, td, li, h1, h2")].find((e) => e.innerText?.trim().length > 3 && !/mono/i.test(getComputedStyle(e).fontFamily)) ?? main
    const fontFamily = getComputedStyle(textEl).fontFamily
    const bodyFont = getComputedStyle(document.body).fontFamily
    const loadedFaces = [...new Set([...document.fonts].filter((f) => f.status === "loaded").map((f) => f.family.replace(/["']/g, "")))]
    return {
      title: document.title, h1: document.querySelector("h1")?.innerText ?? "",
      dir: document.documentElement.dir, lang: document.documentElement.lang,
      dark: document.documentElement.classList.contains("dark"),
      overflowX: document.documentElement.scrollWidth > window.innerWidth + 1,
      mainChars: text.length, rows: main.querySelectorAll("tr, li, [data-deal-card], [data-conversation]").length,
      busy, alerts, notFound, placeholders, visibleKeys: visibleKeys.slice(0, 10), fontFamily, bodyFont, loadedFaces,
      sample: text.replace(/\s+/g, " ").slice(0, 400),
    }
  }, KEYS).catch((e) => ({ probeError: String(e) }))
  page.off("pageerror", onErr); page.off("console", onCon); page.off("response", onRes); page.off("requestfailed", onFail)
  return { name, locale, theme, width, url: page.url().replace(base, ""), status, file, actionError, errors, consoleErrors: [...new Set(consoleErrors)], failed: [...new Set(failed)], ...probe }
}

const results = []
const ar = await session("dark", "ar")
console.error(`frames/s: en ${en.frames}, ar ${ar.frames}`)
for (const [sess, locale, theme] of [[en, "en", "light"], [ar, "ar", "dark"]]) {
  for (const p of PAGES) {
    if (ONLY && !ONLY.includes(p[0])) continue
    const r = await visit(sess, locale, theme, p)
    results.push(r)
    console.error(`${r.name} ${locale}/${theme}: ${r.status} errs=${r.errors.length} failed=${r.failed.length} keys=${r.visibleKeys?.length ?? "?"} chars=${r.mainChars}`)
  }
}
// Phone width for the pages people will open on the move.
await en.page.setViewportSize({ width: 390, height: 844 })
for (const p of PAGES.filter((x) => ["overview", "contacts", "deals", "leads", "inbox", "research"].includes(x[0]))) {
  const r = await visit(en, "en", "light", p, 390)
  results.push(r)
  console.error(`${r.name} en/light 390: ${r.status} errs=${r.errors.length} failed=${r.failed.length} overflow=${r.overflowX}`)
}
await en.ctx.close()
await ar.ctx.close()
const report = path.join(tmpdir(), "demo-tour.json")
writeFileSync(report, JSON.stringify({ ids, results }, null, 2))
console.log(JSON.stringify({ ids, report, results }, null, 2))
