// MH-430: the moharrik.com landing (served by fadymondy.com-v2) shows current copy, links to the
// app, no pricing, en/ar × light/dark × 1440/390. BASE is the landing origin; for a local dev
// server map the host: HOST_RULES="MAP moharrik.com 127.0.0.1:3003" BASE=http://moharrik.com
//   node e2e/moharrik-landing-check.mjs [shots]
import { chromium } from "playwright-core"
import path from "node:path"

const base = (process.env.BASE ?? "https://moharrik.com").replace(/\/$/, "")
const shots = process.argv[2]
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const browser = await chromium.launch({ executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args })

for (const scheme of ["light", "dark"]) for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
  const k = `${loc}-${scheme}-${w}`
  const ctx = await browser.newContext({ viewport: { width: w, height: 900 }, colorScheme: scheme })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/${loc}`, { waitUntil: "load", timeout: 90000 })
  await page.waitForTimeout(1500)
  const r = await page.evaluate(() => {
    const text = document.body.innerText
    const hrefs = [...document.querySelectorAll("a[href]")].map((a) => a.href)
    return {
      stale: /early access|in development|وصول مبكر|قيد التطوير/i.test(text),
      pricing: !!document.querySelector("#pricing") || /\bpricing\b|الأسعار/i.test(text),
      register: hrefs.some((h) => h.startsWith("https://app.moharrik.com/register")),
      login: hrefs.some((h) => h.startsWith("https://app.moharrik.com/login")),
      docs: hrefs.some((h) => h.includes("app.moharrik.com/en/docs/mcp")),
      arabicName: document.documentElement.lang === "ar" ? text.includes("محرّك") : true,
      dir: document.documentElement.dir,
      title: document.title,
      overflow: document.documentElement.scrollWidth - window.innerWidth,
    }
  })
  check(`${k}-current-copy`, !r.stale, r)
  check(`${k}-no-pricing`, !r.pricing, r)
  check(`${k}-app-links`, r.register && r.login && r.docs, { register: r.register, login: r.login, docs: r.docs })
  check(`${k}-arabic`, r.arabicName && (loc === "ar" ? r.dir === "rtl" : true), { dir: r.dir })
  check(`${k}-overflow`, r.overflow <= 1, r.overflow)
  check(`${k}-errors`, errs.length === 0, errs.slice(0, 2))
  if (w === 1440 && scheme === "light") out[`${loc}-title`] = r.title
  if (shots) await page.screenshot({ path: path.join(shots, `mh430-${k}.png`), fullPage: true })
  await ctx.close()
}
await browser.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
