// MH-657: the chat row's menu — right-click and the hover chevron open the same menu; pin,
// favourite, mute, colour, icon, labels, archive and block each show on the row; the filter
// chips (Unread, Favourites, a label) narrow the list; the Archived entry opens the archived
// view; clear and delete ask first. Then the organised list is looked at in en/ar ×
// light/dark × 1440/390.
// WRITES: it signs up a throwaway account and seeds its own chats through a widget it makes,
// so it touches nothing else — for a LOCAL server only, never production.
//   node e2e/mh657-chat-menu.mjs http://localhost:3131 [shots-dir]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3131"
const shots = process.argv[3]
if (/moharrik\.com/.test(base))
  throw new Error("mh657-chat-menu writes: run it against a local server")
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh657-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))

// ---- a throwaway account and workspace ----
const email = `mh657${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH657 " + Date.now())
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
        headers: {
          "X-Workspace-ID": slug,
          "Content-Type": "application/json",
          "X-CSRF-Token": csrf,
        },
        body: json ? JSON.stringify(json) : undefined,
      })
      return { status: r.status, body: await r.json().catch(() => null) }
    },
    { method, p, json, slug },
  )
}

// ---- three chats, through a widget of its own ----
const w = await call("POST", "/api/w/chat_widgets", { name: "MH657", allowed_origins: [base] })
const key = w.body?.widget?.public_key
check("widget", !!key, w.status)
for (const name of ["Alpha", "Bravo", "Charlie"]) {
  const r = await fetch(`${base}/api/chat/${key}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: base },
    body: JSON.stringify({ body: `Hello from ${name}`, name }),
  })
  check(`seed-${name}`, r.status === 201, r.status)
}

const row = (name) => page.locator(`li[data-row-menu]:has([data-conversation]:has-text("${name}"))`)
const menu = page.locator("[data-chat-row-menu]")
async function openMenu(name, how = "right") {
  if (how === "right") await row(name).click({ button: "right" })
  else {
    await row(name).hover()
    await row(name).locator("[data-row-menu-button]").click()
  }
  await menu.waitFor({ state: "visible", timeout: 5000 })
}
async function pick(sel) {
  await menu.locator(sel).first().click()
  await menu.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {})
}
const settle = () => page.waitForTimeout(900)

await page.goto(`${base}/en/w/${slug}/inbox?`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
await page.waitForFunction(
  () => document.querySelectorAll("[data-conversation]").length >= 3,
  null,
  {
    timeout: 15000,
  },
)

// ---- the chevron and right-click open the same menu ----
await openMenu("Bravo", "chevron")
check("chevron-opens-menu", await menu.isVisible())
check(
  "menu-items",
  (await menu.locator("[data-action]").evaluateAll((els) => els.map((e) => e.dataset.action))).join(
    ",",
  ) ===
    "pin,archive,mute,toggle-unread,favourite,toggle-status,color,icon,labels,block,clear,delete",
  await menu
    .locator("[data-action]")
    .evaluateAll((els) => els.map((e) => e.dataset.action).join(",")),
)
await page.keyboard.press("Escape")
await menu.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {})

// ---- pin: Charlie moves to the top ----
await openMenu("Charlie")
await pick('[data-action="pin"]')
await settle()
const firstName = () =>
  page.locator("[data-conversation-list] > li[data-row-menu]").evaluateAll((lis) => {
    const top = lis.sort((a, b) => a.getBoundingClientRect().top - b.getBoundingClientRect().top)[0]
    return top?.textContent ?? ""
  })
check("pinned-on-top", (await firstName()).includes("Charlie"), await firstName())
check("pin-icon", (await row("Charlie").locator("[data-row-pinned]").count()) === 1)
check("pinned-attr", (await row("Charlie").getAttribute("data-pinned")) === "true")

// ---- favourite + the Favourites chip ----
await openMenu("Alpha")
await pick('[data-action="favourite"]')
await settle()
check("favourite-icon", (await row("Alpha").locator("[data-row-favourite]").count()) === 1)
await page.click('[data-filter="favourites"]')
await settle()
const shown = async () =>
  page.locator("[data-conversation]").evaluateAll((els) => els.map((e) => e.textContent ?? ""))
let names = await shown()
check("favourites-chip", names.length === 1 && names[0].includes("Alpha"), names.length)
await page.click('[data-filter="favourites"]')
await settle()

// ---- mute 8h ----
await openMenu("Alpha")
await menu.locator('[data-action="mute"]').hover()
await page.locator('[data-mute="8h"]').click()
await settle()
check("mute-icon", (await row("Alpha").locator("[data-row-muted]").count()) === 1)

// ---- colour + icon ----
await openMenu("Bravo")
await menu.locator('[data-action="color"]').hover()
await page.locator('[data-color="teal"]').click()
await settle()
check("color-stripe", (await row("Bravo").getAttribute("data-tag")) === "teal")
const stripe = await row("Bravo")
  .locator("[data-row-color]")
  .evaluate((el) => getComputedStyle(el).backgroundColor)
  .catch(() => "")
check("color-stripe-painted", !!stripe && stripe !== "rgba(0, 0, 0, 0)", stripe)
await openMenu("Bravo")
await menu.locator('[data-action="icon"]').hover()
await page.locator('[data-icon="briefcase"]').click()
await settle()
check("icon-badge", (await row("Bravo").locator('[data-row-icon="briefcase"]').count()) === 1)

// ---- labels: create one in the dialog, assign it, filter by it ----
await openMenu("Bravo")
await pick('[data-action="labels"]')
const dlg = page.locator("[data-labels-dialog]")
await dlg.waitFor({ state: "visible", timeout: 5000 })
await dlg.locator("[data-new-label]").fill("VIP")
await dlg.locator('[role="radio"][data-tag="violet"]').click()
await dlg.locator("[data-new-label]").press("Enter")
await dlg
  .locator('[data-label-option="VIP"]:checked')
  .waitFor({ timeout: 5000 })
  .catch(() => {})
check("label-created-ticked", await dlg.locator('[data-label-option="VIP"]').isChecked())
await dlg.locator("[data-labels-save]").click()
await dlg.waitFor({ state: "hidden", timeout: 5000 })
await settle()
check(
  "label-chip-on-row",
  (
    await row("Bravo")
      .locator("[data-label-chip]")
      .innerText()
      .catch(() => "")
  ).includes("VIP"),
)
await page.locator('[data-filter="label"]:has-text("VIP")').waitFor({ timeout: 5000 })
await page.click('[data-filter="label"]:has-text("VIP")')
await settle()
names = await shown()
check("label-chip-filter", names.length === 1 && names[0].includes("Bravo"), names.length)
await page.click('[data-filter="all"]')
await settle()

// ---- archive: gone from the list, counted in the Archived entry, shown in its view ----
await openMenu("Alpha")
await pick('[data-action="archive"]')
await settle()
names = await shown()
check("archived-hidden", !names.some((n) => n.includes("Alpha")), names.length)
check("archived-entry", await page.locator("[data-archived-entry]").isVisible())
await page.click("[data-archived-entry]")
await settle()
names = await shown()
check("archived-view", names.length === 1 && names[0].includes("Alpha"), names.length)
await openMenu("Alpha")
await pick('[data-action="archive"]') // unarchive
await settle()
await page.click("[data-archived-back]")
await settle()
names = await shown()
check(
  "unarchived-back",
  names.some((n) => n.includes("Alpha")),
  names.length,
)

// ---- block (a widget chat: nothing leaves the machine) — confirm, icon, no composer ----
await openMenu("Alpha")
await pick('[data-action="block"]')
await page.locator("[data-confirm-dialog] [data-confirm]").click()
await settle()
check("blocked-icon", (await row("Alpha").locator("[data-row-blocked]").count()) === 1)
await row("Alpha").locator("[data-conversation]").click()
await page.waitForSelector("[data-blocked-note]", { timeout: 10000 }).catch(() => {})
check(
  "blocked-no-composer",
  (await page.locator("[data-blocked-note]").count()) === 1 &&
    (await page.locator('[data-form="reply"]').count()) === 0,
)
await openMenu("Alpha")
await pick('[data-action="block"]') // unblock
await page.locator("[data-confirm-dialog] [data-confirm]").click()
await settle()
check("unblocked-composer", (await page.locator('[data-form="reply"]').count()) === 1)

// ---- clear asks first; cancel keeps the messages ----
await openMenu("Charlie")
await pick('[data-action="clear"]')
check("clear-confirm", await page.locator('[data-confirm-dialog="clear"]').isVisible())
await page.keyboard.press("Escape")
await settle()
check("clear-cancelled", (await row("Charlie").innerText()).includes("Hello from Charlie"))

// ---- delete, confirmed: the row goes and the open thread closes ----
await row("Charlie").locator("[data-conversation]").click()
await page.waitForURL(/[?&]c=/, { timeout: 5000 })
await openMenu("Charlie")
await pick('[data-action="delete"]')
await page.locator('[data-confirm-dialog="delete"] [data-confirm]').click()
await settle()
names = await shown()
check("deleted", !names.some((n) => n.includes("Charlie")), names.length)
check("deleted-deselected", !/[?&]c=/.test(page.url()), page.url())

// ---- the organised list in en/ar × light/dark × 1440/390 ----
for (const scheme of ["light", "dark"])
  for (const loc of ["en", "ar"])
    for (const wd of [1440, 390]) {
      const k = `${loc}-${scheme}-${wd}`
      await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: wd, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-conversation]", { timeout: 30000 })
      await page.waitForTimeout(600)
      const m = await page.evaluate(() => {
        const dark =
          document.documentElement.classList.contains("dark") ||
          document.documentElement.dataset.theme === "dark"
        const chip = document.querySelector("[data-label-chip]")
        const btn = document.querySelector("[data-row-menu-button]")
        const overflow = document.documentElement.scrollWidth > window.innerWidth + 1
        return {
          dark,
          rtl: document.documentElement.dir === "rtl",
          chip: !!chip && chip.getBoundingClientRect().width > 0,
          chipColor: chip ? getComputedStyle(chip).color : "",
          chevron: !!btn,
          overflow,
        }
      })
      check(`${k}-theme`, m.dark === (scheme === "dark"), m.dark)
      check(`${k}-dir`, m.rtl === (loc === "ar"), m.rtl)
      check(`${k}-label-chip`, m.chip, m)
      check(`${k}-no-overflow`, !m.overflow)
      if (wd === 390) {
        // no hover on a phone: the chevron is always shown there
        const op = await page
          .locator("[data-row-menu-button]")
          .first()
          .evaluate((e) => getComputedStyle(e).opacity)
        check(`${k}-chevron-visible`, op === "1", op)
      }
      await openMenu("Bravo", wd === 390 ? "chevron" : "right")
      check(`${k}-menu`, await menu.isVisible())
      if (loc === "ar") check(`${k}-menu-ar`, /[\u0600-\u06FF]/.test(await menu.innerText()))
      await page.waitForTimeout(400)
      if (shots) await page.screenshot({ path: path.join(shots, `mh657-${k}.png`) })
      await page.keyboard.press("Escape")
    }

check("no-page-errors", errs.length === 0, errs)
console.log(JSON.stringify({ ok: fail.length === 0, fail, out }, null, 1))
await ctx.close()
process.exit(fail.length ? 1 : 0)
