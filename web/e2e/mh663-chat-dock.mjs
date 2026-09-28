// MH-663: the chat launcher is no longer a side-over — it's a docked copilot bar at the
// bottom of every workspace page except the inbox itself. Opening a chat from the compact
// list docks it as a small window (several side by side on desktop, one full-width sheet on
// mobile), each minimisable/closable, surviving navigation and reload, and popping open (or
// badging if minimised) on new inbound messages unless muted.
// WRITES: it signs up a throwaway account and seeds its own chats through a widget it makes,
// so it touches nothing else — for a LOCAL server only, never production.
//   node e2e/mh663-chat-dock.mjs http://localhost:3143 [shots-dir]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3143"
const shots = process.argv[3]
if (/moharrik\.com/.test(base)) throw new Error("mh663-chat-dock writes: run it against a local server")

const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh663-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))

// ---- a throwaway account and workspace ----
const email = `mh663${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH663 " + Date.now())
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]

async function call(method, p, json) {
  return page.evaluate(
    async ({ method, p, json, slug }) => {
      const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
      const r = await fetch(p, {
        method,
        headers: { "X-Workspace-ID": slug, "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: json ? JSON.stringify(json) : undefined,
      })
      return { status: r.status, body: await r.json().catch(() => null) }
    },
    { method, p, json, slug },
  )
}

// ---- a widget, and two visitor conversations to dock ----
const w = await call("POST", "/api/w/chat_widgets", { name: "MH663", allowed_origins: [base] })
const key = w.body?.widget?.public_key
check("widget", !!key, w.status)
async function seed(name, body) {
  const r = await fetch(`${base}/api/chat/${key}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: base },
    body: JSON.stringify({ body: body ?? `Hello from ${name}`, name }),
  })
  return { status: r.status, body: await r.json().catch(() => null) }
}
for (const name of ["Alpha", "Bravo"]) {
  const r = await seed(name)
  check(`seed-${name}`, r.status === 201, r.status)
}

const settle = () => page.waitForTimeout(700)
const dockList = page.locator("[data-dock-list]")
const dockItem = (name) => page.locator(`[data-dock-list-item]:has-text("${name}")`)
const dockWindow = (name) => page.locator(`[data-dock-window]:has([data-dock-window-title]:has-text("${name}"))`)
const launcher = page.locator("[data-chat-dock-launcher]")

async function openLauncher() {
  await launcher.click()
  await dockList.waitFor({ state: "visible", timeout: 10000 })
}
async function openChat(name) {
  await openLauncher()
  await dockItem(name).first().click()
  await dockWindow(name).waitFor({ state: "visible", timeout: 15000 })
}

// ---- a non-inbox page: the old side-over launcher is gone, the dock bar is here ----
await page.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })
await launcher.waitFor({ state: "visible", timeout: 30000 })
await page.waitForSelector("[data-chat-dock-count]", { timeout: 15000 }).catch(() => {})
const gone = await page.evaluate(() => ({
  oldLauncher: !!document.querySelector("[data-floating-chat]"),
  dockBar: !!document.querySelector("[data-chat-dock-bar]"),
  count: document.querySelector("[data-chat-dock-count]")?.textContent ?? null,
}))
check("old-launcher-gone", !gone.oldLauncher, gone)
check("dock-bar-present", gone.dockBar, gone)
check("unread-count-shown", gone.count === "2", gone)

// ---- the inbox itself never shows the dock ----
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 }).catch(() => {})
await settle()
check("no-dock-on-inbox", (await page.locator("[data-chat-dock-launcher]").count()) === 0)

// ---- back to a normal page: open the list, dock two chats side by side ----
await page.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })
await launcher.waitFor({ state: "visible", timeout: 30000 })
await openLauncher()
const rows = await page.locator("[data-dock-list-item]").count()
check("dock-list-has-rows", rows >= 2, rows)
await dockItem("Alpha").first().click()
await dockWindow("Alpha").waitFor({ state: "visible", timeout: 15000 })
await openChat("Bravo")
await settle()
const rectA = await dockWindow("Alpha").boundingBox()
const rectB = await dockWindow("Bravo").boundingBox()
const noOverlap =
  !!rectA && !!rectB && (rectA.x + rectA.width <= rectB.x + 1 || rectB.x + rectB.width <= rectA.x + 1)
check("two-docks-no-overlap-1440", noOverlap, { rectA, rectB })

// ---- minimise Alpha, then restore it ----
await dockWindow("Alpha").locator("[data-dock-minimize]").click()
await settle()
check("alpha-minimized", (await dockWindow("Alpha").count()) === 0)
const chip = page.locator('[data-dock-minimized]:has-text("Alpha")')
check("alpha-chip-shown", await chip.first().isVisible().catch(() => false))
await chip.first().click()
await dockWindow("Alpha").waitFor({ state: "visible", timeout: 15000 })
check("alpha-restored", await dockWindow("Alpha").isVisible())

// ---- reply from a docked chat: stored locally through this app's own API, sent nowhere real ----
const replyText = `Docked reply ${Date.now()}`
const composer = dockWindow("Alpha").locator('[data-form="reply"] textarea, [data-form="reply"] input[type=text]')
if (await composer.count()) {
  await composer.first().fill(replyText)
  await dockWindow("Alpha").locator('[data-form="reply"] button[type=submit]').click()
  await page.waitForFunction(
    (t) => document.body.innerText.includes(t),
    replyText,
    { timeout: 15000 },
  ).catch(() => {})
}
check("docked-reply-sent", (await page.locator(`[data-dock-window] :text("${replyText}")`).count()) > 0)

// ---- navigate to another page: both docks persist ----
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "domcontentloaded" }).catch(async () => {
  await page.goto(`${base}/en/w/${slug}/inbox/channels`, { waitUntil: "domcontentloaded" })
})
await launcher.waitFor({ state: "visible", timeout: 30000 }).catch(() => {})
await settle()
check("dock-persists-after-nav", await dockWindow("Alpha").isVisible().catch(() => false))

// ---- reload: docks survive from localStorage ----
await page.reload({ waitUntil: "domcontentloaded" })
await launcher.waitFor({ state: "visible", timeout: 30000 }).catch(() => {})
await settle()
check("dock-persists-after-reload", await dockWindow("Alpha").isVisible().catch(() => false))

// ---- a brand new inbound message pops a docked window open in realtime ----
const r = await seed("Charlie", "New from Charlie")
check("seed-Charlie", r.status === 201, r.status)
await page.waitForFunction(
  () => !!document.querySelector('[data-dock-window-title]') &&
    [...document.querySelectorAll('[data-dock-window-title]')].some((e) => e.textContent?.includes("Charlie")),
  null,
  { timeout: 20000 },
).catch(() => {})
check("realtime-pop", (await dockWindow("Charlie").count()) > 0)

// ---- 390: one full-width sheet, not several side-by-side windows ----
await page.setViewportSize({ width: 390, height: 800 })
await page.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })
await launcher.waitFor({ state: "visible", timeout: 30000 })
await settle()
const sheet = page.locator("[data-dock-sheet]")
check("mobile-single-sheet", (await sheet.count()) === 1, await sheet.count())
const sheetBox = await sheet.boundingBox()
check("mobile-sheet-full-width", !!sheetBox && Math.round(390 - sheetBox.width) <= 4, sheetBox)
await page.setViewportSize({ width: 1440, height: 900 })

// ---- en/ar x light/dark, no overflow ----
for (const scheme of ["light", "dark"])
  for (const loc of ["en", "ar"])
    for (const wd of [1440, 390]) {
      const k = `${loc}-${scheme}-${wd}`
      await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: wd, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-chat-dock-launcher]", { timeout: 30000 }).catch(() => {})
      await page.waitForTimeout(500)
      const m = await page.evaluate(() => ({
        dark: document.documentElement.classList.contains("dark"),
        rtl: document.documentElement.dir === "rtl",
        overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
        launcher: !!document.querySelector("[data-chat-dock-launcher]"),
      }))
      check(`${k}-theme`, m.dark === (scheme === "dark"), m.dark)
      check(`${k}-dir`, m.rtl === (loc === "ar"), m.rtl)
      check(`${k}-no-overflow`, !m.overflow, m)
      check(`${k}-launcher`, m.launcher)
      if (shots) await page.screenshot({ path: path.join(shots, `mh663-${k}.png`) })
    }

check("no-page-errors", errs.length === 0, errs)
console.log(JSON.stringify({ ok: fail.length === 0, fail, out }, null, 1))
await ctx.close()
process.exit(fail.length ? 1 : 0)
