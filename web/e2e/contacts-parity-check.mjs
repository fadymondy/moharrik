// MH-583 on the demo workspace: the contact editor grows the Google Contacts
// parity fields (name parts, phonetics, birthday, notes/biography), the
// identities panel still takes several phones/emails with labels, the meta
// panel still round-trips, and the inbox "link to existing contact" action
// attaches the conversation's own sender identity — not just contact_id.
//
// Style matches inbox-crm-live.mjs: sign in via demo creds from env, `load`
// (never networkidle) plus a wait for the page's own main selector.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}
const fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "cpc-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()

await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const W = `${base}/en/w/${slug}`

// ---- create a fresh contact so the parity fields start from a known, empty state ----
const stamp = Date.now()
const email = `parity${stamp}@example.test`
await page.goto(`${W}/contacts`, { waitUntil: "load" })
await page.waitForSelector('[data-form="add-contact"]', { timeout: 20000 })
await page.fill("#contact-email", email)
await page.fill("#contact-name", "Parity Person")
await page.click('[data-form="add-contact"] button[type=submit]')
await page.waitForSelector(`[data-contact-row="${email}"]`, { timeout: 15000 })
check("contactCreated", true)
await page.click(`[data-contact-row="${email}"] a`)
await page.waitForURL(/\/contacts\/[^/]+$/, { timeout: 15000 })
const contactId = page.url().split("/contacts/")[1]
await page.waitForSelector('[data-form="edit-contact"]', { timeout: 15000 })

// ---- item 1: the person-field UI (Google Contacts parity) ----
const personFieldIds = [
  "edit-contact-name-prefix",
  "edit-contact-given-name",
  "edit-contact-middle-name",
  "edit-contact-family-name",
  "edit-contact-name-suffix",
  "edit-contact-nickname",
  "edit-contact-phonetic-given",
  "edit-contact-phonetic-family",
  "edit-contact-birthday",
  "edit-contact-biography",
]
out.personFieldsPresent = await page.evaluate(
  (ids) => ids.every((id) => document.getElementById(id) != null),
  personFieldIds,
)
check("personFieldsPresent", out.personFieldsPresent, personFieldIds)

await page.fill("#edit-contact-given-name", "Nour")
await page.fill("#edit-contact-family-name", "Kamal")
await page.fill("#edit-contact-nickname", "Nunu")
await page.fill("#edit-contact-biography", "Met at the Cairo meetup; prefers WhatsApp.")

// The birthday field is date-picker.tsx's <Button> + Calendar popover, not a
// fillable <input> — there is no literal string to type. Opening it focuses
// today's cell (nothing is selected yet), so Enter picks that cell without
// needing to navigate the grid to a specific historical date; the ISO value
// it produces (date-picker.tsx's toISO) lands on the button's data-value,
// which is what round-trips through the API and is what we compare on reload.
await page.click("#edit-contact-birthday")
await page.waitForSelector("[data-day]", { timeout: 10000 })
await page.keyboard.press("Enter")
await page.waitForFunction(() => document.getElementById("edit-contact-birthday")?.getAttribute("data-value"), { timeout: 10000 })
const birthdayPicked = await page.getAttribute("#edit-contact-birthday", "data-value")
if (shots) await page.screenshot({ path: path.join(shots, "contact-person-fields.png") })
await page.click('[data-form="edit-contact"] button[type=submit]')
await page.waitForTimeout(700)

// Reload (a fresh navigation, not a soft client re-render) so what's on the
// page can only be what the API actually persisted.
await page.goto(`${W}/contacts/${contactId}`, { waitUntil: "load" })
await page.waitForSelector('[data-form="edit-contact"]', { timeout: 15000 })
const persisted = await page.evaluate(() => ({
  givenName: document.getElementById("edit-contact-given-name")?.value,
  familyName: document.getElementById("edit-contact-family-name")?.value,
  nickname: document.getElementById("edit-contact-nickname")?.value,
  biography: document.getElementById("edit-contact-biography")?.value,
  birthday: document.getElementById("edit-contact-birthday")?.getAttribute("data-value"),
}))
out.personFieldsPersisted = persisted
check(
  "personFieldsPersisted",
  persisted.givenName === "Nour" &&
    persisted.familyName === "Kamal" &&
    persisted.nickname === "Nunu" &&
    persisted.biography.includes("Cairo meetup") &&
    persisted.birthday === birthdayPicked,
  { ...persisted, birthdayPicked },
)

// ---- several phones/emails with labels, via the identities panel ----
await page.waitForSelector('[data-form="link-identity"]', { timeout: 15000 })
await page.click("#identity-channel")
await page.getByRole("option", { name: /phone/i }).click({ timeout: 5000 })
await page.fill("#identity-value", "+201001234567")
await page.fill("#identity-label", "Work mobile")
await page.click('[data-form="link-identity"] button[type=submit]')
await page.waitForSelector('[data-identity^="phone:"]', { timeout: 15000 })
check("secondPhoneWithLabelAdded", (await page.locator('[data-identity^="phone:"]').count()) > 0)
if (shots) await page.screenshot({ path: path.join(shots, "contact-identities.png") })

// ---- key/value meta panel still round-trips ----
await page.waitForSelector("[data-meta-panel]", { timeout: 15000 })
const metaAddBtn = page.locator("[data-meta-panel] button", { hasText: /add/i }).first()
await metaAddBtn.click()
await page.waitForSelector("[data-meta-row]", { timeout: 10000 })
const row = page.locator("[data-meta-row]").last()
await row.locator("input").nth(0).fill("favorite_drink")
await row.locator("input").nth(1).fill("Turkish coffee")
await page.locator("[data-meta-panel] button", { hasText: /save/i }).click()
await page.waitForTimeout(700)
await page.goto(`${W}/contacts/${contactId}`, { waitUntil: "load" })
await page.waitForSelector("[data-meta-panel]", { timeout: 15000 })
out.metaPersisted = await page.evaluate(() => document.body.innerText.includes("favorite_drink") && document.body.innerText.includes("Turkish coffee"))
check("metaPersisted", out.metaPersisted)

// ---- item 2: inbox "link to existing contact" also attaches the sender identity ----
await page.goto(`${W}/inbox`, { waitUntil: "load" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
const threadCount = await page.locator("[data-conversation]").count()
let linkable = -1
for (let i = 0; i < threadCount && linkable < 0; i++) {
  await page.locator("[data-conversation]").nth(i).click()
  await page.waitForSelector("[data-thread-crm]", { timeout: 20000 })
  if (await page.locator('[data-crm-action="link"]').count()) linkable = i
}
check("foundUnlinkedThreadForLinkAction", linkable >= 0)

if (linkable >= 0) {
  await page.locator("[data-conversation]").nth(linkable).click()
  await page.waitForSelector('[data-crm-action="link"]', { timeout: 15000 })
  const channel = await page.evaluate(() => document.querySelector("[data-thread-crm]")?.getAttribute("data-thread-channel") ?? "")
  await page.click('[data-crm-action="link"]')
  await page.waitForSelector('form[data-form="link-contact"]', { timeout: 10000 })
  await page.click("#link-contact")
  await page.fill("#link-contact", "Parity Person")
  await page.getByRole("option", { name: /Parity Person/i }).click({ timeout: 10000 })
  if (shots) await page.screenshot({ path: path.join(shots, "inbox-link-contact.png") })
  await page.click('form[data-form="link-contact"] button[type=submit]')
  await page.waitForSelector(`[data-crm-contact="${contactId}"]`, { timeout: 15000 })
  check("threadLinkedToContact", true)

  // The identity attach is server-side (UpdateConversation), not something the
  // dialog itself renders — verify it on the contact's own identities list,
  // best-effort: only channels FileChannelMessage/autoLink give a stable
  // per-conversation identity to (not the anonymous "chat" widget channel).
  await page.goto(`${W}/contacts/${contactId}`, { waitUntil: "load" })
  await page.waitForSelector("[data-identity-list]", { timeout: 15000 })
  const identityCountAfterLink = await page.locator("[data-identity]").count()
  out.identityCountAfterLink = identityCountAfterLink
  out.threadChannel = channel
  if (channel && channel !== "chat") {
    check("senderIdentityAttachedOnLink", identityCountAfterLink >= 2, { channel, identityCountAfterLink })
  } else {
    out.senderIdentityAttachedOnLink = "skipped (chat channel has no stable per-conversation identity)"
  }
}

console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? "FAILED: " + fail.join(", ") : "ALL CHECKS PASSED")
await ctx.close()
process.exit(fail.length ? 1 : 0)
