// Read-only live check for MH-604. Opens no conversation (GET /conversations/{id} marks it read):
//   - the conversation list carries is_group on every row
//   - the inbox page renders in en/ar × light/dark with no page errors and no sideways overflow
// HOST_RULES="MAP app.moharrik.com <ip>" bash demo-run.sh <base> e2e/mh604-live.mjs <base>
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "https://app.moharrik.com").replace(/\/$/, "")
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh604live-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "load" })

const r = await page.evaluate(async (slug) => {
  const id = ((await (await fetch("/api/me/workspaces")).json()).workspaces ?? []).find(
    (w) => w.slug === slug,
  )?.id
  const res = await fetch("/api/w/conversations?limit=20", { headers: { "X-Workspace-ID": id } })
  const j = await res.json()
  const rows = j.conversations ?? j.items ?? []
  return {
    status: res.status,
    rows: rows.length,
    withFlag: rows.filter((c) => typeof c.is_group === "boolean").length,
    groups: rows.filter((c) => c.is_group).length,
  }
}, slug)
check("conversations-is_group", r.status === 200 && r.rows > 0 && r.withFlag === r.rows, r)
out.groupThreads = r.groups

for (const locale of ["en", "ar"])
  for (const scheme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto(`${base}/${locale}/w/${slug}/inbox`, { waitUntil: "load" })
    await page.waitForTimeout(2000)
    check(
      `${locale}-${scheme}:inbox-no-overflow`,
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
    )
  }
check("errors", errs.length === 0, errs.slice(0, 3))
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
