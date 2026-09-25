// MH-647 "Merge contacts": pick a duplicate from the contact page, confirm
// which one survives, merge, and land back on the survivor with the toast
// shown — against a fresh registration + workspace on a LOCAL dev server
// only, never production.
//
// Style matches contact-context.mjs: sign up fresh via /register (never demo
// creds), `load`/`networkidle` waits plus a wait for the page's own main
// selector, screenshots to the path in argv[3].
//   node e2e/mh647-merge.mjs http://localhost:3171 E:\tmp\mh647
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3171"
if (/moharrik\.com|fadymondy\.com/.test(base)) {
  console.error(`refusing to run against a non-local base: ${base}`)
  process.exit(1)
}
const shots = process.argv[3]
const out = {}
const fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

async function newCtx(opts = {}) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh647-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    colorScheme: "light",
    ...opts,
  })
  return ctx
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `mh647-${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", email)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", wsName)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  return { email, slug }
}

async function addContact(page, W, email, name) {
  await page.goto(`${W}/contacts`, { waitUntil: "load" })
  await page.waitForSelector('[data-form="add-contact"]', { timeout: 20000 })
  await page.fill("#contact-email", email)
  await page.fill("#contact-name", name)
  await page.click('[data-form="add-contact"] button[type=submit]')
  await page.waitForSelector(`[data-contact-row="${email}"]`, { timeout: 15000 })
  await page.click(`[data-contact-row="${email}"] a`)
  await page.waitForURL(/\/contacts\/[^/]+$/, { timeout: 15000 })
  return page.url().split("/contacts/")[1]
}

const ctx = await newCtx()
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 300)))

const { slug } = await signUpAndEnterWorkspace(page, "Merge Contacts " + Date.now())
out.slug = slug
const W = `${base}/en/w/${slug}`

// ---- two contacts, one a clear duplicate of the other ----
const stamp = Date.now()
const survivorEmail = `survivor${stamp}@example.test`
const dupEmail = `duplicate${stamp}@example.test`
const survivorId = await addContact(page, W, survivorEmail, "Laila Kareem")
await page.goto(`${W}/contacts`, { waitUntil: "load" })
await page.waitForSelector('[data-form="add-contact"]', { timeout: 20000 })
const dupId = await addContact(page, W, dupEmail, "Laila K.")
check("bothContactsCreated", !!survivorId && !!dupId && survivorId !== dupId, {
  survivorId,
  dupId,
})

// ---- merge the duplicate into the survivor, from the survivor's page ----
await page.goto(`${W}/contacts/${survivorId}`, { waitUntil: "load" })
await page.waitForSelector("[data-merge-contact-open]", { timeout: 20000 })
check("noPageErrorsOnLoad", errs.length === 0, errs)
await page.click("[data-merge-contact-open]")
await page.waitForSelector("[data-merge-contact-dialog]", { timeout: 10000 })
check("pickerOpened", true)
if (shots) await page.screenshot({ path: path.join(shots, "mh647-merge-picker-en-light-1440.png") })

// Base UI Combobox: type to filter, then pick the matching item.
await page.click('[data-merge-contact-dialog] input[role="combobox"]')
await page.keyboard.type("Laila K.")
// The Combobox popup is portaled to the document body, not nested inside
// DialogContent, so the option must be located page-wide, not scoped to
// [data-merge-contact-dialog].
await page.waitForSelector('[role="option"]', { timeout: 10000 })
await page.click('[role="option"]')
await page.waitForSelector("[data-merge-contact-preview]", { timeout: 10000 })
check("confirmStepShown", true)

const survivorLabel = await page.locator("[data-merge-survivor]").innerText()
const sourceLabel = await page.locator("[data-merge-source]").innerText()
check("previewShowsSurvivorName", survivorLabel.includes("Laila Kareem"), survivorLabel)
check("previewShowsSourceName", sourceLabel.includes("Laila K."), sourceLabel)
check("previewShowsSurvivorIdentity", survivorLabel.includes(survivorEmail), survivorLabel)
check("previewShowsSourceIdentity", sourceLabel.includes(dupEmail), sourceLabel)
if (shots)
  await page.screenshot({ path: path.join(shots, "mh647-merge-confirm-en-light-1440.png") })

await page.click('[data-merge-contact-dialog] button:has-text("Merge contacts")')
await page.waitForSelector('[data-sonner-toast]:has-text("Contacts merged")', { timeout: 15000 })
check("toastShown", true)
await page
  .waitForSelector("[data-merge-contact-dialog]", { state: "detached", timeout: 10000 })
  .catch(() => {})
check("dialogClosed", (await page.locator("[data-merge-contact-dialog]").count()) === 0)

// ---- stayed on the survivor ----
check("survivorUrlUnchanged", new RegExp(`/contacts/${survivorId}$`).test(page.url()), page.url())
if (shots) await page.screenshot({ path: path.join(shots, "mh647-merge-done-en-light-1440.png") })

// ---- the duplicate is gone from the list ----
await page.goto(`${W}/contacts`, { waitUntil: "load" })
await page.waitForSelector('[data-form="add-contact"]', { timeout: 20000 })
await page.fill('input[placeholder*="Search" i], input[type="search"]', dupEmail).catch(() => {})
const dupRow = await page.locator(`[data-contact-row="${dupEmail}"]`).count()
check("duplicateGoneFromList", dupRow === 0, dupRow)

// ---- Arabic + RTL + dark + 390px pass (visual only, reusing the survivor) ----
await page.goto(`${base}/ar/w/${slug}/contacts/${survivorId}`, { waitUntil: "load" })
await page.waitForSelector("[data-merge-contact-open]", { timeout: 20000 })
check("arabicPageLoaded", true)
await page.emulateMedia({ colorScheme: "dark" })
await page.setViewportSize({ width: 390, height: 844 })
await page.reload({ waitUntil: "load" })
await page.waitForSelector("[data-merge-contact-open]", { timeout: 20000 })
if (shots) await page.screenshot({ path: path.join(shots, "mh647-merge-button-ar-dark-390.png") })
await page.click("[data-merge-contact-open]")
await page.waitForSelector("[data-merge-contact-dialog]", { timeout: 10000 })
if (shots) await page.screenshot({ path: path.join(shots, "mh647-merge-picker-ar-dark-390.png") })
check("arabicDialogOpened", true)

console.log(JSON.stringify({ out, fail }, null, 2))
await ctx.close()
process.exit(fail.length ? 1 : 0)
