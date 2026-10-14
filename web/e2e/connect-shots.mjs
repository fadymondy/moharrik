// Screenshots of the connect-by-approval page (MH-478), en/ar × light/dark ×
// 1440/390, signed in as a fresh owner, on a live request with its key.
//
//   node e2e/connect-shots.mjs <card> [webBase]
import { chromium } from "playwright-core"

const [card = "MH-478-connect", webArg] = process.argv.slice(2)
const web = (webArg ?? "http://localhost:3508").replace(/\/$/, "")
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
  await call("POST", "/api/auth/register", {
    email: `shots-${Date.now()}@x.test`,
    password: "Passw0rd!long-enough",
  })
  await call("POST", "/api/me/workspaces", { name: `Nile Coffee Co ${Date.now().toString(36)}` })
  await call("POST", "/api/me/workspaces", { name: `Cairo Bakery ${Date.now().toString(36)}` })
  const state = await base.storageState()
  await base.close()

  const start = async () => {
    const res = await fetch(`${web}/api/agents/connect/start`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Windows browser",
        platform: "Windows x86-64",
        browser_version: "Chrome 140.0.7339.80",
        extension_version: "0.3.0",
        fingerprint: `fp-${Date.now()}`,
      }),
    })
    return res.json()
  }

  for (const locale of ["en", "ar"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const s = await start()
        const ctx = await browser.newContext({
          storageState: state,
          viewport: { width, height: 900 },
          colorScheme: theme,
        })
        const page = await ctx.newPage()
        await page.goto(`${web}/${locale}/automation/connect/${s.code}#k=${s.browser_key}`, {
          waitUntil: "load",
        })
        await page.waitForSelector("[data-connect-form]", { timeout: 60000 })
        await page.waitForTimeout(800)
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
  // The same page without its key: nothing about the browser, nothing to approve.
  const s = await start()
  const ctx = await browser.newContext({
    storageState: state,
    viewport: { width: 1440, height: 900 },
  })
  const page = await ctx.newPage()
  await page.goto(`${web}/en/automation/connect/${s.code}`, { waitUntil: "load" })
  await page.waitForSelector("[data-connect-problem]", { timeout: 60000 })
  await page.screenshot({ path: `${SHOTS}/${card}-nokey-en-light-1440.png`, fullPage: true })
  report.push({ nokey: true, approveShown: !!(await page.$("[data-approve]")) })
  await ctx.close()
} finally {
  await browser.close()
  console.log(JSON.stringify(report, null, 1))
}
