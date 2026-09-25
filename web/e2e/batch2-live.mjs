// Read-only live check for the MH-591/602/558 deploy. Never saves, sends or runs anything:
//   - GraphQL `contacts` answers the workspace's contacts (MH-591)
//   - contact JSON carries no data: URIs, and a provider-photo URL loads as an image (MH-602)
//   - a publishing workflow's "Test workflow" asks first; only Cancel is pressed (MH-558)
// HOST_RULES="MAP app.moharrik.com <ip>" bash demo-run.sh <base> e2e/batch2-live.mjs <base>
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "https://app.moharrik.com").replace(/\/$/, "")
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "b2live-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "load" })

const r = await page.evaluate(async (slug) => {
  const ws = (await (await fetch("/api/me/workspaces")).json())
  const list = ws.workspaces ?? []
  const id = list.find((w) => w.slug === slug)?.id
  const H = { "X-Workspace-ID": id }
  const csrf = (await (await fetch("/api/auth/csrf", { cache: "no-store" })).json()).csrf_token
  // A query, not a mutation: /graphql is POST, so it carries the CSRF token like any POST.
  const gq = await fetch("/graphql", {
    method: "POST", headers: { ...H, "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: JSON.stringify({ query: "{ contacts(limit: 5) { total contacts { id } } }" }),
  })
  const g = await gq.json()
  const cr = await fetch("/api/w/contacts?limit=50", { headers: H })
  const raw = await cr.text()
  const photo = (raw.match(/\/api\/provider-photos\/[^"\\]+/) ?? [])[0]?.replace(/\\u0026/g, "&")
  let photoStatus = null, photoType = null
  if (photo) { const p = await fetch(photo); photoStatus = p.status; photoType = p.headers.get("content-type") }
  return {
    wsFound: !!id, gqlStatus: gq.status, gqlErrors: g.errors?.map((e) => e.message) ?? null,
    gqlTotal: g.data?.contacts?.total, gqlCount: g.data?.contacts?.contacts?.length,
    contactsStatus: cr.status, dataUris: (raw.match(/"data:/g) ?? []).length, hasPhotoUrl: !!photo, photoStatus, photoType,
  }
}, slug)
check("workspace", r.wsFound)
check("graphql-contacts", r.gqlStatus === 200 && !r.gqlErrors && r.gqlTotal > 0 && r.gqlCount > 0, r)
check("no-data-uris", r.contactsStatus === 200 && r.dataUris === 0, r)
check("provider-photo-loads", !r.hasPhotoUrl || (r.photoStatus === 200 && /^image\//.test(r.photoType ?? "")), r)
out.providerPhotoSeen = r.hasPhotoUrl

// MH-558: open a publishing workflow's canvas, press Test, see the question, Cancel. Nothing runs.
await page.goto(`${base}/en/w/${slug}/automation/workflows`, { waitUntil: "load" })
await page.waitForTimeout(2500)
const href = await page.evaluate(() => {
  const row = [...document.querySelectorAll("[data-workflow-writes='true'], [data-workflow-writes]")].find((e) => e.getAttribute("data-workflow-writes") !== "false")
  return row?.querySelector("a[href*='/workflows/']")?.getAttribute("href") ?? row?.closest("a")?.getAttribute("href") ?? null
})
out.writesWorkflowFound = !!href
if (href) {
  await page.goto(new URL(href, base).href, { waitUntil: "load" })
  const btn = page.locator("[data-test-run]:not([disabled])")
  if (await btn.count().then((n) => n > 0).catch(() => false)) {
    await btn.first().click()
    const asked = await page.waitForSelector("[data-confirm-run-dialog]", { timeout: 10000 }).then(() => true).catch(() => false)
    check("publish-asks-first", asked)
    if (asked) {
      await page.click("[data-confirm-run-dialog] button:has-text('Cancel')")
      check("cancel-closes", await page.waitForSelector("[data-confirm-run-dialog]", { state: "hidden", timeout: 10000 }).then(() => true).catch(() => false))
    }
  } else out.testRunDisabled = "no enabled Test button (no browser connected) — dialog not reachable read-only"
}
check("errors", errs.length === 0, errs.slice(0, 2))
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
