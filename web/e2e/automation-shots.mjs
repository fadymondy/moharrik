// Screenshots of the automation pages, en/ar × light/dark × 1440/390, signed in
// as a fresh owner of a fresh workspace (MH-399/400/403).
//
//   node e2e/automation-shots.mjs <card> <path-after-/automation> [webBase] [--slug s --email e]
//
// e.g. node e2e/automation-shots.mjs MH-399-install extension
// With --slug/--email/--password it signs in to an existing workspace instead
// (to photograph one that already has browsers, workflows and schedules).
import { chromium } from "playwright-core"

const args = process.argv.slice(2)
const flag = (name) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : ""
}
const slugArg = flag("slug")
const emailArg = flag("email")
const passwordArg = flag("password") || "Passw0rd!long-enough"
const [card, sub = "", webArg] = args
const web = (webArg ?? "http://localhost:3506").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "e2e-shots"
const CHROMIUM =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"

const browser = await chromium.launch({ executablePath: CHROMIUM, headless: true })
const report = []
try {
  const base = await browser.newContext()
  const http = base.request
  const csrf = async () => (await (await http.get(`${web}/api/auth/csrf`)).json()).csrf_token
  const call = async (method, p, data) => {
    const res = await http.fetch(`${web}${p}`, {
      method,
      headers: { "X-CSRF-Token": await csrf() },
      data,
    })
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${await res.text()}`)
    return res.json()
  }
  let slug = slugArg
  if (emailArg) {
    await call("POST", "/api/auth/login", { email: emailArg, password: passwordArg })
  } else {
    await call("POST", "/api/auth/register", {
      email: `shots-${Date.now()}@x.test`,
      password: passwordArg,
    })
    slug = (
      await call("POST", "/api/me/workspaces", {
        name: `Nile Coffee Co ${Date.now().toString(36)}`,
      })
    ).workspace.slug
  }
  const state = await base.storageState()
  await base.close()

  for (const locale of ["en", "ar"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const ctx = await browser.newContext({
          storageState: state,
          viewport: { width, height: 900 },
          colorScheme: theme,
        })
        const page = await ctx.newPage()
        await page.goto(`${web}/${locale}/w/${slug}/automation${sub ? "/" + sub : ""}`, {
          waitUntil: "load",
        })
        await page.waitForSelector("h1", { timeout: 60000 })
        await page.waitForTimeout(2500)
        const file = `${SHOTS}/${card}-${locale}-${theme}-${width}.png`
        await page.screenshot({ path: file, fullPage: true })
        const info = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - window.innerWidth,
          title: document.title,
        }))
        report.push({ file, ...info })
        await ctx.close()
      }
    }
  }
} finally {
  await browser.close()
}
console.log(JSON.stringify(report, null, 1))
