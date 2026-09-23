// Shared by the MH-410..413 checks (batch N): sign in as the demo user of a
// local instance, call the API as that session, and shoot a page in every
// combination the UI must hold up in (en/ar × light/dark × 1440/390).
//
// Nothing here talks to a real WhatsApp number or a real mailbox: the checks
// run against an API whose EVOLUTION_API_URL and SMTP services point at the
// local fakes in e2e/fakes-whatsapp-smtp.mjs.
import { chromium } from "playwright-core"
import { existsSync, readFileSync } from "node:fs"
import { homedir } from "node:os"
import path from "node:path"

export const CHROME = "C:/Program Files/Google/Chrome/Application/chrome.exe"

/** The demo login for a base URL (~/.moharrik-demo.json, never printed). */
export function demoLogin(base) {
  const store = path.join(homedir(), ".moharrik-demo.json")
  if (!existsSync(store)) throw new Error("no ~/.moharrik-demo.json: run e2e/seed-demo.mjs first")
  const entry = JSON.parse(readFileSync(store, "utf8"))[base]
  if (!entry) throw new Error(`no demo login for ${base} in ~/.moharrik-demo.json`)
  return entry
}

export async function launch() {
  return chromium.launch({ executablePath: CHROME, headless: true })
}

/** Signs in through the real form and returns the storage state and workspace. */
export async function signIn(browser, base) {
  const { email, password } = demoLogin(base)
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(`${base}/en/login`, { waitUntil: "domcontentloaded" })
  await page.fill("#email", email)
  await page.fill("#password", password)
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\/|workspaces/, { timeout: 30000 })
  const state = await ctx.storageState()
  const ws = await call(page, "GET", "/api/me/workspaces")
  const list = ws.body.workspaces ?? ws.body
  const demo = list.find((w) => /Nile Coffee/.test(w.name)) ?? list[0]
  await ctx.close()
  return { state, wsId: demo.id, slug: demo.slug }
}

/** A fetch from inside the page, as the signed-in member (CSRF included). */
export async function call(page, method, p, body, wsId) {
  return page.evaluate(
    async ({ method, p, body, wsId }) => {
      const h = { Accept: "application/json" }
      if (body !== undefined) h["Content-Type"] = "application/json"
      if (wsId) h["X-Workspace-ID"] = wsId
      if (method !== "GET") {
        const c = await fetch("/api/auth/csrf", { credentials: "same-origin" }).then((r) => r.json())
        h["X-CSRF-Token"] = c.csrf_token
      }
      const r = await fetch(p, { method, headers: h, body: body !== undefined ? JSON.stringify(body) : undefined, credentials: "same-origin" })
      const text = await r.text()
      let json = null
      try {
        json = JSON.parse(text)
      } catch {}
      return { status: r.status, body: json ?? text }
    },
    { method, p, body, wsId },
  )
}

export const SHOTS = "E:/Sites/fadymondy.com-v2/.setup/shots"

/**
 * Shoots a page in all eight combinations. `ready` is a selector the page
 * shows once loaded; `prepare(page)` runs after it (open a sheet, fill a form...). Returns per-shot facts: the
 * document direction, whether dark applied, and any horizontal overflow.
 */
export async function matrix(browser, state, { base, card, name, url, ready, prepare, fullPage = true }) {
  const facts = []
  for (const locale of ["en", "ar"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const ctx = await browser.newContext({ storageState: state, viewport: { width, height: 900 }, colorScheme: theme })
        const page = await ctx.newPage()
        const errors = []
        page.on("pageerror", (e) => errors.push(String(e).slice(0, 200)))
        // Not "networkidle": the workspace event stream stays open for good.
        await page.goto(`${base}/${locale}${url}`, { waitUntil: "domcontentloaded" })
        if (ready) await page.waitForSelector(ready, { timeout: 30000 })
        await page.waitForTimeout(600)
        if (prepare) await prepare(page, { locale, theme, width })
        await page.waitForTimeout(400)
        const file = `${SHOTS}/${card}-${name}-${locale}-${theme}-${width}.png`
        await page.screenshot({ path: file, fullPage })
        const f = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          rawKeys: [...document.body.innerText.matchAll(/\b[a-z][A-Za-z]+\.[a-z][A-Za-z.]+[A-Za-z]\b/g)]
            .map((m) => m[0])
            .filter((k) => /^(smtp|campaigns|contactWa|waNotify|whatsapp|contacts|delivery|common)\./.test(k)),
        }))
        facts.push({ file: path.basename(file), locale, theme, width, ...f, errors })
        await ctx.close()
      }
    }
  }
  return facts
}

/** Reads a fake's log (written by e2e/fakes-whatsapp-smtp.mjs). */
export function fakeLog(dir, name) {
  const p = path.join(dir, name)
  return existsSync(p) ? JSON.parse(readFileSync(p, "utf8")) : null
}
