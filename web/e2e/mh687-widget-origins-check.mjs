// MH-687: a chat widget's name and allowed origins can be edited after it is
// created. Drives a real Chromium against a NON-production stack (it creates a
// throwaway widget, edits it and deletes it) and asserts:
//
//   - Channels → a chat widget's Edit opens the settings sheet with its name
//     and origins filled in
//   - a path or a bare host ("example.com") is refused in place, nothing saved
//   - renaming and changing the origins saves, and the API returns the new list
//   - the create form refuses a bad origin the same way
//   - en/ar × light/dark × 1440/390 render the sheet with no sideways overflow
//
//   DEMO_EMAIL=… DEMO_PASSWORD=… node e2e/mh687-widget-origins-check.mjs <webBase> [slug]
import { chromium } from "playwright-core"
import { mkdirSync } from "node:fs"

const base = (process.argv[2] ?? "http://localhost:3687").replace(/\/$/, "")
if (/app\.moharrik\.com/.test(base)) throw new Error("this check writes; never run it on production")
const SHOTS = process.env.SHOTS ?? "E:/tmp/shots687"
mkdirSync(SHOTS, { recursive: true })
const CHROME =
  process.env.CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"

const failures = []
const check = (ok, what) => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}`)
  if (!ok) failures.push(what)
}

const browser = await chromium.launch({ executablePath: CHROME })
const warnings = []
let cookies = null

async function session({ width = 1440, theme = "light" } = {}) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme })
  await ctx.addInitScript((t) => localStorage.setItem("theme", t), theme)
  const page = await ctx.newPage()
  page.on("console", (m) => {
    if (m.type() === "error" || /Base UI/.test(m.text())) warnings.push(m.text().slice(0, 200))
  })
  page.on("pageerror", (e) => warnings.push(String(e).slice(0, 200)))
  // /feedback/session 503s on a stack with no Mahaam feedback key (worktrees blank it).
  // A 403 naming CSRF is the stale token the app retries by itself.
  page.on("response", async (r) => {
    if (r.status() < 400 || r.url().includes("/feedback/session")) return
    const body = await r.text().catch(() => "")
    if (r.status() === 403 && /csrf/i.test(body)) return
    warnings.push(`${r.status()} ${r.url().replace(base, "")}`)
  })
  if (cookies) {
    await ctx.addCookies(cookies)
    return { ctx, page }
  }
  await page.goto(`${base}/en/login`)
  const status = await page.evaluate(
    async ({ email, pw }) => {
      const { csrf_token } = await (await fetch("/api/auth/csrf")).json()
      const r = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json", "X-CSRF-Token": csrf_token },
        body: JSON.stringify({ email, password: pw }),
      })
      return r.status
    },
    { email: process.env.DEMO_EMAIL, pw: process.env.DEMO_PASSWORD },
  )
  if (status !== 200) throw new Error(`login: ${status}`)
  cookies = await ctx.cookies()
  return { ctx, page }
}

const api = (page, method, path, wsId, json) =>
  page.evaluate(
    async ({ method, path, wsId, json }) => {
      const { csrf_token } = await (await fetch("/api/auth/csrf")).json()
      const r = await fetch(path, {
        method,
        headers: {
          "X-Workspace-ID": wsId,
          "X-CSRF-Token": csrf_token,
          ...(json ? { "content-type": "application/json" } : {}),
        },
        body: json ? JSON.stringify(json) : undefined,
      })
      return { status: r.status, body: r.status === 204 ? null : await r.json().catch(() => null) }
    },
    { method, path, wsId, json },
  )

const { ctx, page } = await session()
const me = await page.evaluate(async () => (await fetch("/api/me/workspaces")).json())
const list = me.workspaces ?? me
const ws =
  list.find((w) => w.slug === process.argv[3]) ??
  list.find((w) => ["owner", "admin"].includes(w.role)) ??
  list[0]
console.log(`workspace ${ws.slug} (${ws.role})`)

const made = await api(page, "POST", "/api/w/chat_widgets", ws.id, {
  name: "MH-687 check " + Date.now(),
  allowed_origins: ["https://one.example"],
})
check(made.status === 201, `create a throwaway widget (${made.status})`)
const widget = made.body.widget
const channelRow = `[data-channel-row="${widget.id}"]`

async function openSheet(p, locale) {
  await p.goto(`${base}/${locale}/w/${ws.slug}/settings/integrations`)
  await p.locator(channelRow).waitFor({ timeout: 20000 })
  await p.locator(`${channelRow} [data-edit-channel]`).click()
  await p.locator("[data-widget-settings-form]").waitFor()
}

try {
  await openSheet(page, "en")
  const name = page.locator("#ws-name")
  const origins = page.locator("#ws-origins")
  check((await name.inputValue()) === widget.name, "the sheet shows the widget's name")
  check((await origins.inputValue()) === "https://one.example", "the sheet shows its origins")

  // A path, then a bare host: refused in place, nothing saved.
  for (const bad of ["https://one.example/contact", "one.example"]) {
    await origins.fill(bad)
    await page.locator("[data-widget-settings-form] button[type=submit]").click()
    await page.getByText("Each origin must be a scheme and host only").waitFor({ timeout: 5000 })
    const now = await api(page, "GET", "/api/w/chat_widgets", ws.id)
    const w = now.body.widgets.find((x) => x.id === widget.id)
    check(
      JSON.stringify(w.allowed_origins) === JSON.stringify(["https://one.example"]),
      `"${bad}" is refused and nothing is saved`,
    )
  }

  await name.fill("MH-687 renamed")
  await origins.fill("https://one.example, https://two.example/ ,https://*.two.example")
  await page.locator("[data-widget-settings-form] button[type=submit]").click()
  await page.getByText("Widget settings saved").waitFor({ timeout: 10000 })
  const after = (await api(page, "GET", "/api/w/chat_widgets", ws.id)).body.widgets.find(
    (x) => x.id === widget.id,
  )
  check(after.name === "MH-687 renamed", `the new name is saved (${after.name})`)
  check(
    JSON.stringify(after.allowed_origins) ===
      JSON.stringify(["https://one.example", "https://two.example", "https://*.two.example"]),
    `the new origins are saved (${JSON.stringify(after.allowed_origins)})`,
  )
  await page.locator(`${channelRow}`).getByText("MH-687 renamed").waitFor({ timeout: 10000 })
  check(true, "the Channels list shows the new name")

  // The server refuses what the form refuses, so the API and MCP cannot store
  // an origin that matches nothing either.
  for (const bad of ["fadymondy.com", "https://two.example/contact", "https://a.*.test"]) {
    const r = await api(page, "PATCH", `/api/w/chat_widgets/${widget.id}`, ws.id, { allowed_origins: [bad] })
    check(r.status === 422, `the API refuses "${bad}" (${r.status})`)
  }
  // A wildcard entry lets a subdomain in and keeps the apex out.
  const cfg = (origin) =>
    fetch(`${base}/api/chat/${widget.public_key}/config`, { headers: { Origin: origin } }).then((r) => r.status)
  check((await cfg("https://www.two.example")) === 200, "https://*.two.example lets www.two.example in")
  check((await cfg("https://evil.example")) === 403, "and still refuses other origins")

  // Emptying the list is allowed (the hint says the widget is then blocked).
  await page.locator(`${channelRow} [data-edit-channel]`).click()
  await page.locator("#ws-origins").fill("")
  const saved = page.waitForResponse(
    // ok() only: my api() helper fetched a fresh CSRF token, so the page's
    // first PATCH is refused and the app retries it with a new one.
    (r) => r.request().method() === "PATCH" && r.url().includes(`/api/w/chat_widgets/${widget.id}`) && r.ok(),
  )
  await page.locator("[data-widget-settings-form] button[type=submit]").click()
  console.log(`  clear → PATCH ${(await saved).status()}`)
  const empty = (await api(page, "GET", "/api/w/chat_widgets", ws.id)).body.widgets.find(
    (x) => x.id === widget.id,
  )
  check((empty.allowed_origins ?? []).length === 0, "the origins can be cleared")

  // Every language, theme and width: the sheet fits.
  for (const locale of ["en", "ar"])
    for (const theme of ["light", "dark"])
      for (const width of [1440, 390]) {
        const s = await session({ width, theme })
        await openSheet(s.page, locale)
        await s.page.waitForTimeout(400)
        const over = await s.page.evaluate(() => {
          const el = document.querySelector("[data-widget-settings]")
          return el ? el.scrollWidth - el.clientWidth : -1
        })
        const dir = await s.page.evaluate(() => document.documentElement.dir)
        check(over === 0, `${locale}/${theme}/${width}: the sheet has no sideways overflow (${over})`)
        check(dir === (locale === "ar" ? "rtl" : "ltr"), `${locale}: dir=${dir}`)
        const originsDir = await s.page.locator("#ws-origins").getAttribute("dir")
        check(originsDir === "ltr", `${locale}: origins are typed left to right`)
        await s.page.screenshot({ path: `${SHOTS}/sheet-${locale}-${theme}-${width}.png` })
        await s.ctx.close()
      }

  // The create form refuses the same inputs.
  await page.goto(`${base}/en/w/${ws.slug}/settings/integrations`)
  await page.locator("[data-add-channel]").click()
  await page.locator('[data-add-type="chat"]').click()
  const createOrigins = page.locator("#widget-origins")
  await createOrigins.waitFor({ timeout: 10000 })
  {
    await page.locator("#widget-name").fill("never saved")
    await createOrigins.fill("example.com")
    await page.locator("[data-form=chat-widget] button[type=submit]").click()
    await page.getByText("Each origin must be a scheme and host only").waitFor({ timeout: 5000 })
    const all = (await api(page, "GET", "/api/w/chat_widgets", ws.id)).body.widgets
    check(!all.some((w) => w.name === "never saved"), "the create form refuses a bare host too")
  }
} finally {
  const del = await api(page, "DELETE", `/api/w/chat_widgets/${widget.id}`, ws.id)
  console.log(`cleanup: delete the throwaway widget → ${del.status}`)
  await ctx.close()
  await browser.close()
}

const relevant = warnings.filter((w) => !/Download the React DevTools|HMR|Fast Refresh|Failed to load resource/.test(w))
check(relevant.length === 0, `no console errors or Base UI warnings${relevant.length ? ": " + relevant.join(" | ") : ""}`)
console.log(failures.length ? `\n${failures.length} FAILED` : "\nall passed")
process.exit(failures.length ? 1 : 0)
