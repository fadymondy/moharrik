// Read-only production check for MH-562 on the demo workspace: the contacts import page
// in en/ar, light/dark, 1440/390, its sources, and a preview of each ready source.
// No import is started, so nothing is written and nothing is sent.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/import-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const browser = await chromium.launch({ executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args })

for (const scheme of ["light", "dark"]) {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  if (scheme === "light") {
    // the API: sources, and a read-only preview of each one that is ready
    const api = await page.evaluate(async (slug) => {
      const ws = await (await fetch("/api/me/workspaces")).json()
      const id = (ws.workspaces ?? ws.items ?? ws).find((w) => w.slug === slug).id
      const get = async (u) => { const r = await fetch(u, { headers: { "X-Workspace-ID": id } }); return { status: r.status, body: r.ok ? await r.json() : await r.text() } }
      const src = await get("/api/w/import/sources")
      const previews = {}
      for (const s of src.body?.sources ?? []) for (const c of s.connections ?? []) {
        if (!c.ready) continue
        const p = await get(`/api/w/import/${encodeURIComponent(s.source)}/preview?connection_id=${encodeURIComponent(c.id)}`)
        previews[`${s.source}:${c.label}`] = { status: p.status, items: p.body?.items?.length ?? 0, error: p.status === 200 ? undefined : String(p.body).slice(0, 300) }
      }
      return {
        status: src.status,
        sources: (src.body?.sources ?? []).map((s) => ({ source: s.source, connections: (s.connections ?? []).map((c) => ({ label: c.label, ready: c.ready, note: c.note ?? "" })) })),
        previews,
      }
    }, slug)
    out.api = api
    check("sourcesListed", api.status === 200 && api.sources.length > 0, api)
    // Every connection kind is an import source, even with nothing connected yet (MH-562).
    const kinds = api.sources.map((s) => s.source)
    check("everyKindListed", ["whatsapp", "email", "slack", "discord", "telegram", "workflow"].every((k) => kinds.includes(k)), kinds)
    check("previewsOk", Object.values(api.previews).every((p) => p.status === 200), api.previews)
  }

  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    for (const lang of ["en", "ar"]) {
      const key = `page-${lang}-${scheme}-${width}`
      const res = await page.goto(`${base}/${lang}/w/${slug}/contacts/import`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("h1", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(1500)
      const r = await page.evaluate(() => ({
        h1: document.querySelector("h1")?.textContent?.trim() ?? "",
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      }))
      check(key, res?.status() < 400 && r.h1 && r.overflow <= 1 && r.dir === (lang === "ar" ? "rtl" : "ltr") && r.dark === (scheme === "dark"), { status: res?.status(), ...r })
      if (shots) await page.screenshot({ path: path.join(shots, `mh562-import-${lang}-${scheme}-${width}.png`), fullPage: true })
    }
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
await browser.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
