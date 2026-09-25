// Read-only production check for MH-524: a rule's dry-run result reads in the
// page's language. Uses the "new rule" editor (lead template)'s Test button, which posts to
// /api/w/rules/test — that endpoint sends, writes and logs nothing. Any other
// non-GET API call fails the check. en/ar × light/dark × 1440/390.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/rule-test-i18n-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const latin = /[A-Za-z]{4,}/

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh524-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  const writes = []
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (r.method() !== "GET" && u.pathname.startsWith("/api/") && !/^\/api\/(auth\/|w\/rules\/test$)/.test(u.pathname)) writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto(`${base}/${loc}/w/${slug}/automation/rules/new?template=lead`, { waitUntil: "load" })
    await page.waitForSelector("[data-open-test]", { timeout: 30000 })
    await page.waitForTimeout(800)
    await page.click("[data-open-test]")
    await page.waitForSelector("[data-run-test]", { timeout: 10000 })
    await page.locator("[data-run-test]").scrollIntoViewIfNeeded()
    await page.click("[data-run-test]")
    const got = await page.waitForSelector("[data-run-result], [data-test-error]", { timeout: 20000 }).then(() => true, () => false)
    const res = await page.evaluate(() => {
      const r = document.querySelector("[data-run-result]")
      if (!r) return { err: document.querySelector("[data-test-error]")?.textContent ?? null }
      return {
        status: r.querySelector("[data-run-status]")?.textContent ?? "",
        reason: r.querySelector("[data-run-reason]")?.textContent ?? "",
        actions: [...r.querySelectorAll("[data-run-actions] li")].map((li) => li.innerText.replace(/\s+/g, " ").trim()),
      }
    })
    // Arabic: the sentence the log composes must be Arabic. Quoted values,
    // addresses and names are the event's own data and stay as they are; any
    // other Latin word is an untranslated English string.
    const text = [res.status, res.reason, ...(res.actions ?? [])].join(" ")
    const ok = got && !res.err && (loc === "en" || !latin.test(text.replace(/"[^"]*"|“[^”]*”|https?:\S+|[\w.+-]+@[\w.-]+/g, "")))
    check(`${k}-result`, ok, res)
    out[`${k}-text`] = text.slice(0, 200)
    if (shots) {
      await page.locator("[data-run-result]").scrollIntoViewIfNeeded().catch(() => {})
      await page.screenshot({ path: path.join(shots, `mh524-${k}.png`) })
    }
  }
  check(`noWrites-${scheme}`, writes.length === 0, writes)
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
