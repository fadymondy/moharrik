// Read-only production sweep for the 502 bug cards (MH-529..MH-560): signs in as the demo
// user and loads every reported page in Arabic, recording the document status, page errors
// and whether the workspace shell rendered. Nothing is created, edited or deleted.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/routes-502-live.mjs https://app.moharrik.com
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const pages = ["", "/contacts", "/companies", "/campaigns", "/automation", "/automation/rules",
  "/automation/workflows", "/reports", "/research", "/notifications", "/youtube", "/search-console",
  "/subscribe-forms", "/inbox/canned", "/inbox/whatsapp"]
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "r502-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const bad = []
page.on("response", (r) => { if (r.status() >= 500) bad.push(`${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`) })
for (const p of pages) {
  const errs = []; const onErr = (e) => errs.push(String(e)); page.on("pageerror", onErr)
  bad.length = 0
  const res = await page.goto(`${base}/ar/w/${slug}${p}`, { waitUntil: "load", timeout: 45000 }).catch((e) => ({ status: () => String(e) }))
  await page.waitForSelector("main", { timeout: 20000 }).catch(() => {}); await page.waitForTimeout(2500)
  const r = await page.evaluate(() => ({ dir: document.documentElement.dir, main: !!document.querySelector("main"),
    errorPage: /Application error|Internal Server Error|Bad Gateway/i.test(document.body.innerText) }))
  check(p || "/", res.status() === 200 && r.main && !r.errorPage && errs.length === 0 && bad.length === 0,
    { status: res.status(), ...r, errs, bad: [...bad] })
  page.off("pageerror", onErr)
}
for (const p of ["/robots.txt", "/icon.svg", "/feedback/config"]) {
  const r = await page.request.get(base + p); check(p, r.status() === 200, r.status())
}
const ev = await page.request.get(`${base}/api/w/events`, { headers: { "X-Workspace": slug } })
check("/api/w/events", ev.status() < 500, ev.status())
await ctx.close()
console.log(JSON.stringify(out, null, 1)); console.log(fail.length ? `FAIL ${fail.join(",")}` : "ALL OK")
