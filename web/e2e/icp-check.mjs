// Lifecycle + ideal-customer score check (MH-395), against a running web + API with the demo
// workspace seeded:
//
//   node e2e/icp-check.mjs http://localhost:3512 [shots-dir]
//
// Signs in as the demo user (password from DEMO_PASSWORD or ~/.moharrik-demo.json, never printed)
// and, in a real browser:
//   - changes the profile's weights on Settings → Ideal customer and asserts the ranking beside the
//     form re-orders, equals the API's ranking, and opens a score's reasons; then puts the profile back
//   - sorts and filters the contacts list by score and lifecycle and checks every row agrees
//   - sets a contact's stage by hand and sees it in its history, recorded as the member's
//   - takes the pages in en/ar × light/dark × 1440/390 with no horizontal overflow
// Screenshots land in shots-dir as MH-395-<page>-<lang>-<theme>-<width>.png.
import { chromium } from "playwright-core"
import { existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3512").replace(/\/$/, "")
const shots = process.argv[3] ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
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

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "icp-")), {
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
const ws = await page.evaluate(async () =>
  (await (await fetch("/api/me/workspaces")).json()).workspaces.find((w) =>
    w.name.startsWith("Moharrik Demo"),
  ),
)
if (!ws) throw new Error("no demo workspace")

const apiGet = (p) =>
  page.evaluate(
    async ([p, id]) =>
      (await fetch(p, { headers: { Accept: "application/json", "X-Workspace-ID": id } })).json(),
    [p, ws.id],
  )
const apiPut = (p, body) =>
  page.evaluate(
    async ([p, id, body]) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      const r = await fetch(p, {
        method: "PUT",
        headers: { "Content-Type": "application/json", "X-Workspace-ID": id, "X-CSRF-Token": csrf },
        body: JSON.stringify(body),
      })
      return r.json()
    },
    [p, ws.id, body],
  )

// ---- the profile re-ranks, visibly, with reasons ----------------------------------------------
const original = (await apiGet("/api/w/icp")).profile
const ranking = () =>
  page.$$eval("[data-ranking] [data-rank-row]", (rows) =>
    rows.map((r) => r.getAttribute("data-rank-row")),
  )
const apiRanking = async () =>
  ((await apiGet("/api/w/contacts?sort=score&limit=8")).contacts ?? []).map((c) => c.email)

await page.goto(`${base}/en/w/${ws.slug}/settings/icp`, { waitUntil: "load" })
await page.waitForSelector("[data-ranking] [data-rank-row]", { timeout: 20000 })
const before = await ranking()
check(
  JSON.stringify(before) === JSON.stringify(await apiRanking()),
  `ranking beside the form = API: ${before.slice(0, 3).join(", ")}…`,
)

// Behaviour over fit: industries and sizes off, open deal and WhatsApp heavy.
await page.fill("#icp-industries-weight", "0")
await page.fill("#icp-sizes-weight", "0")
await page.fill("#icp-regions-weight", "0")
await page.fill("#icp-signal-open_deal", "10")
await page.fill("#icp-signal-whatsapp_opt_in", "8")
await page.click("[data-save-icp]")
await page.waitForFunction(
  (b) => {
    const now = [...document.querySelectorAll("[data-ranking] [data-rank-row]")].map((r) =>
      r.getAttribute("data-rank-row"),
    )
    return now.length > 0 && JSON.stringify(now) !== JSON.stringify(b)
  },
  before,
  { timeout: 20000 },
)
const after = await ranking()
check(
  JSON.stringify(after) !== JSON.stringify(before),
  `new weights re-rank: ${after.slice(0, 3).join(", ")}…`,
)
check(
  JSON.stringify(after) === JSON.stringify(await apiRanking()),
  "the re-ranked list = the API's ranking",
)
const saved = await apiGet("/api/w/icp")
check(
  saved.profile.signals.open_deal === 10 && saved.profile.industries.weight === 0,
  "the weights were saved",
)
// The top row's score opens its reasons.
await page.click("[data-ranking] [data-rank-row] [data-score-button]")
await page.waitForSelector("[data-score-reasons] [data-reason]", { timeout: 10000 })
const reasons = await page.$$eval("[data-score-reasons] [data-reason]", (rs) =>
  rs.map((r) => r.getAttribute("data-reason")),
)
check(
  reasons.includes("open_deal") && !reasons.includes("industry"),
  `the reasons follow the profile: ${reasons.join(", ")}`,
)
await page.keyboard.press("Escape")
if (process.argv[3] !== "-")
  await page.screenshot({
    path: path.join(shots, "MH-395-reranked-en-light-1440.png"),
    fullPage: true,
  })
// Put the profile back.
await apiPut("/api/w/icp", original)
check(
  JSON.stringify(await apiRanking()) === JSON.stringify(before),
  "the original profile restores the original ranking",
)

// ---- the contacts list: sort and filter by score and stage ----------------------------------
await page.goto(`${base}/en/w/${ws.slug}/contacts`, { waitUntil: "load" })
await page.waitForSelector("[data-contact-row]", { timeout: 20000 })
await page.click("[data-sort=score]")
await page.waitForFunction(
  () =>
    document.querySelector("[data-contacts-current=yes]") &&
    document.querySelectorAll("[data-contact-row] [data-score]").length > 0,
  null,
  { timeout: 15000 },
)
await page.waitForTimeout(500)
const scores = await page.$$eval("[data-contact-row]", (rows) =>
  rows.map((r) => {
    const s = r.querySelector("[data-score]")
    return s ? Number(s.getAttribute("data-score")) : -1
  }),
)
check(
  scores.length > 5 && scores.every((s, i) => i === 0 || scores[i - 1] >= s),
  `best fit first: ${scores.slice(0, 6).join(" ≥ ")}…`,
)
await page.click("[data-stage-filter]")
await page.click("[role=option] >> text=Customer")
await page.waitForFunction(
  () => {
    const rows = [...document.querySelectorAll("[data-contact-row]")]
    return (
      document.querySelector("[data-contacts-current=yes]") &&
      rows.length > 0 &&
      rows.every((r) => r.querySelector("[data-lifecycle=customer]"))
    )
  },
  null,
  { timeout: 15000 },
)
const customers = await page.$$eval("[data-contact-row]", (rows) => rows.length)
const apiCustomers = (await apiGet("/api/w/contacts?lifecycle=customer")).total
check(customers === apiCustomers, `lifecycle=customer: ${customers} rows = API ${apiCustomers}`)

// ---- a contact's stage, by hand, recorded as the member's ------------------------------------
const target = ((await apiGet("/api/w/contacts?lifecycle=none&limit=1")).contacts ?? [])[0]
if (target) {
  await page.goto(`${base}/en/w/${ws.slug}/contacts/${target.id}/profile`, { waitUntil: "load" })
  await page.waitForSelector("[data-lifecycle-panel]", { timeout: 20000 })
  await page.click("#contact-lifecycle")
  await page.click("[role=option] >> text=Lead")
  await page.waitForSelector("[data-lifecycle-history] [data-lifecycle-event=manual]", {
    timeout: 15000,
  })
  const line = await page.$eval(
    "[data-lifecycle-history] [data-lifecycle-event=manual]",
    (e) => e.textContent,
  )
  check(
    line.includes("Set by") && line.includes(EMAIL),
    `hand-set stage recorded as the member's: "${line.replace(/\s+/g, " ").trim().slice(0, 80)}"`,
  )
  const detail = await apiGet(`/api/w/contacts/${target.id}`)
  check(
    detail.contact.lifecycle === "lead" && detail.contact.lifecycle_history[0].source === "manual",
    "the API agrees",
  )
  const reasonRows = await page.$$eval("[data-score-panel] [data-reason]", (r) => r.length)
  check(
    reasonRows === (detail.contact.score_reasons ?? []).length && reasonRows > 0,
    `the contact page shows all ${reasonRows} reasons`,
  )
}

// ---- a real event moves the stage: a deal opened with a contact makes them qualified ----------
const fresh = ((await apiGet("/api/w/contacts?lifecycle=none&limit=1")).contacts ?? [])[0]
if (fresh) {
  const deal = await page.evaluate(
    async ([id, contactId]) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      const r = await fetch("/api/w/deals", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Workspace-ID": id, "X-CSRF-Token": csrf },
        body: JSON.stringify({
          title: "Lifecycle check — trial order",
          value_minor: 50000,
          contact_id: contactId,
        }),
      })
      return (await r.json()).deal
    },
    [ws.id, fresh.id],
  )
  await page.goto(`${base}/en/w/${ws.slug}/contacts/${fresh.id}/profile`, { waitUntil: "load" })
  await page.waitForSelector("[data-lifecycle-history] [data-lifecycle-event=deal_opened]", {
    timeout: 15000,
  })
  const badge = await page.$eval("[data-lifecycle-panel] [data-lifecycle]", (e) =>
    e.getAttribute("data-lifecycle"),
  )
  check(
    badge === "qualified",
    `opening a deal moved ${fresh.email} to qualified, shown as "Deal opened"`,
  )
  // Tidy up: the check's deal goes; the history it made stays, as history does.
  await page.evaluate(
    async ([id, dealId]) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      await fetch(`/api/w/deals/${dealId}`, {
        method: "DELETE",
        headers: { "X-Workspace-ID": id, "X-CSRF-Token": csrf },
      })
    },
    [ws.id, deal.id],
  )
}

// ---- every page in en/ar × light/dark × 1440/390 ---------------------------------------------
const someCompany = ((await apiGet("/api/w/companies?sort=score&limit=1")).companies ?? [])[0]
const someContact = ((await apiGet("/api/w/contacts?sort=score&limit=1")).contacts ?? [])[0]
const pages = [
  ["icp", "settings/icp", "[data-ranking] [data-rank-row]"],
  ["contacts", "contacts", "[data-contact-row]"],
  ["contact", `contacts/${someContact.id}/profile`, "[data-score-panel] [data-reason]"],
  ["companies", "companies", "[data-score-button]"],
  ["company", `companies/${someCompany.id}/details`, "[data-score-panel] [data-reason]"],
]
for (const [name, route, ready] of pages) {
  for (const lang of ["en", "ar"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const tag = `${name}-${lang}-${theme}-${width}`
        await page.emulateMedia({ colorScheme: theme })
        await page.setViewportSize({ width, height: 900 })
        await page.goto(`${base}/${lang}/w/${ws.slug}/${route}`, { waitUntil: "load" })
        await page.waitForSelector(ready, { timeout: 20000 })
        if (name === "contacts") {
          await page.click("[data-sort=score]")
          await page.waitForFunction(
            () => document.querySelector("[data-contacts-current=yes]"),
            null,
            { timeout: 15000 },
          )
        }
        await page.waitForTimeout(400)
        const dom = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          arabicDigits: /[٠-٩]/.test(document.querySelector("main")?.textContent ?? ""),
        }))
        check(
          dom.dir === (lang === "ar" ? "rtl" : "ltr") && dom.dark === (theme === "dark"),
          `${tag}: direction and theme`,
        )
        check(dom.overflow <= 0, `${tag}: no horizontal overflow (${dom.overflow}px)`)
        check(!dom.arabicDigits, `${tag}: Western digits`)
        if (process.argv[3] !== "-")
          await page.screenshot({ path: path.join(shots, `MH-395-${tag}.png`), fullPage: true })
      }
    }
  }
}

check(errors.length === 0, `no page errors ${errors.join(" | ")}`)
await ctx.close()
if (failures.length) {
  console.log(`\n${failures.length} FAILED`)
  process.exit(1)
}
console.log("\nall checks passed")
