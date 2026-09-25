// MH-602, against a LOCAL dev stack only (never production, never a real
// WhatsApp number): a linked contact's name wins over customer_name/phone
// everywhere a conversation is shown, every row/header always has a round
// avatar (photo or initials, never blank), and the contact avatar
// upload/replace/remove control works end to end.
//
// Seeds three conversations directly via SQL (owner DATABASE_URL) into a
// freshly-registered workspace, then drives the real UI with playwright-core
// against real Chrome. en/ar x light/dark x 1440/390.
//
//   API on :8151, web (`next dev -p 3151`, API_ORIGIN=http://localhost:8151) already running.
//   node e2e/inbox-avatars.mjs http://localhost:3151 E:/tmp/mh602 "<owner DATABASE_URL>"
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3151"
const shots = process.argv[3] ?? "E:/tmp/mh602"
const ownerURL = process.argv[4]
if (!ownerURL) {
  console.error("usage: node e2e/inbox-avatars.mjs <base> <shots> <owner DATABASE_URL>")
  process.exit(2)
}

const out = {}
const fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

function psql(sql) {
  // -t -A hides column headers, but psql still prints its own "INSERT 0 1"
  // completion tag on the line after a RETURNING result — take the first line.
  return execFileSync("psql", [ownerURL, "-t", "-A", "-c", sql], { encoding: "utf8" }).trim().split(/\r?\n/)[0].trim()
}

const RUN = Date.now()

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh602-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- an owner with a fresh workspace ---------------------------------------
await page.goto(`${base}/en/login`)
const owner = `mh602-${RUN}@brand.test`
const slug = await page.evaluate(async (email) => {
  const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
  const post = (u, b) =>
    fetch(u, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(b) })
  const r = await post("/api/auth/register", { email, password: "correct-horse-battery" })
  if (!r.ok) throw new Error(`register failed: ${r.status} ${await r.text()}`)
  const ws = await (await post("/api/me/workspaces", { name: `MH602 ${email.split("@")[0]}` })).json()
  return ws.workspace.slug
}, owner)
check("registeredAndWorkspaceCreated", !!slug, slug)

const wsID = psql(`SELECT id FROM workspaces WHERE slug = '${slug}'`)
check("workspaceIdFound", /^[0-9a-f-]{10,}$/.test(wsID), wsID)

// ---- seed: a linked WhatsApp conversation (customer_name is the phone,
// exactly the case Fady called out), an unlinked WhatsApp conversation, and
// an email conversation — none of it touches a real number or inbox. ------
const namedContactID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source)
  VALUES ('${wsID}', 'Salma Youssef', '201005551212', 'manual') RETURNING id`)
const linkedConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, contact_id, subject, last_message_at)
  VALUES ('${wsID}', 'whatsapp', '201005551212', '201005551212', '${namedContactID}', 'hi there', now()) RETURNING id`)
const unlinkedConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_phone, subject, last_message_at)
  VALUES ('${wsID}', 'whatsapp', '201009998888', 'need help', now() - interval '1 minute') RETURNING id`)
const emailConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, subject, last_message_at)
  VALUES ('${wsID}', 'email', 'Omar Khaled', 'omar@example.test', 'question', now() - interval '2 minutes') RETURNING id`)
check("seeded", !!(namedContactID && linkedConvID && unlinkedConvID && emailConvID), { namedContactID, linkedConvID, unlinkedConvID, emailConvID })

// ListConversations only shows a conversation that has at least one message
// (MH-599: a thread with none has nothing to open) — every seeded conversation
// needs one inbound message or it never appears in the inbox at all.
for (const [convID, body] of [
  [linkedConvID, "hi there"],
  [unlinkedConvID, "need help"],
  [emailConvID, "question"],
]) {
  psql(`INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ('${wsID}', '${convID}', 'in', '${body}') RETURNING id`)
}

const W = (locale) => `${base}/${locale}/w/${slug}`

for (const scheme of ["light", "dark"]) {
  await page.emulateMedia({ colorScheme: scheme })
  for (const locale of ["en", "ar"]) {
    for (const width of [1440, 390]) {
      const k = `${locale}-${scheme}-${width}`
      await page.setViewportSize({ width, height: 900 })
      await page.goto(`${W(locale)}/inbox`, { waitUntil: "load" })
      await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 }).catch(() => {})
      await page.waitForTimeout(500)

      // ---- name precedence: the linked row shows the CONTACT's name, not the phone.
      const linkedRowText = await page.locator(`[data-conversation="${linkedConvID}"]`).innerText().catch(() => "")
      check(`${k}-linkedRowShowsContactName`, linkedRowText.includes("Salma Youssef"), linkedRowText)
      check(`${k}-linkedRowHidesRawPhone`, !linkedRowText.includes("201005551212"), linkedRowText)

      // ---- every row has an avatar, and it is initials (no photo seeded for anyone here).
      const rowAvatars = await page.locator("[data-conversation-list] [data-avatar]").count()
      const rowCount = await page.locator("[data-conversation-list] [data-conversation]").count()
      check(`${k}-everyRowHasAvatar`, rowAvatars >= rowCount && rowCount > 0, { rowAvatars, rowCount })
      const kinds = await page.locator("[data-conversation-list] [data-avatar]").evaluateAll((els) => els.map((e) => e.getAttribute("data-avatar-kind")))
      check(`${k}-avatarsAreInitials`, kinds.length > 0 && kinds.every((v) => v === "initials"), kinds)

      // ---- unlinked conversation still shows something sensible (its own phone), no crash.
      const unlinkedRowText = await page.locator(`[data-conversation="${unlinkedConvID}"]`).innerText().catch(() => "")
      check(`${k}-unlinkedRowRenders`, unlinkedRowText.length > 0, unlinkedRowText)

      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh602-list-en-light-1440.png") })
      }

      // ---- open the linked thread: header shows the contact's name + an avatar.
      await page.locator(`[data-conversation="${linkedConvID}"]`).click()
      await page.waitForSelector(`[data-thread="${linkedConvID}"]`, { timeout: 15000 }).catch(() => {})
      const headerTitle = await page.locator(`[data-thread="${linkedConvID}"] [data-thread-title]`).innerText().catch(() => "")
      check(`${k}-threadHeaderShowsContactName`, headerTitle.includes("Salma Youssef"), headerTitle)
      const headerAvatarKind = await page.locator(`[data-thread="${linkedConvID}"] header [data-avatar]`).first().getAttribute("data-avatar-kind").catch(() => null)
      check(`${k}-threadHeaderHasAvatar`, headerAvatarKind === "initials", headerAvatarKind)

      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh602-thread-en-light-1440.png") })
      }
      if (locale === "ar" && width === 390) {
        await page.screenshot({ path: path.join(shots, `mh602-thread-ar-${scheme}-390.png`) })
      }

      // ---- contact dialog title: opening "edit contact" on the linked row shows the contact's own name (once loaded).
      await page.locator(`[data-conversation="${linkedConvID}"]`).click({ button: "right" })
      const editItem = page.locator("[data-contact-edit]")
      const menuOk = await editItem.first().waitFor({ state: "visible", timeout: 5000 }).then(() => true, () => false)
      check(`${k}-contextMenuOpens`, menuOk)
      if (menuOk) {
        await editItem.first().click()
        const dialog = page.locator("[data-contact-dialog]")
        await dialog.waitFor({ state: "visible", timeout: 5000 }).catch(() => {})
        await page.waitForSelector("[data-contact-dialog] [data-form=edit-contact]", { timeout: 10000 }).catch(() => {})
        const dialogTitle = await page.locator("[data-contact-dialog] h2, [data-contact-dialog] [data-slot=dialog-title]").first().innerText().catch(() => "")
        check(`${k}-dialogTitleShowsContactName`, dialogTitle.includes("Salma Youssef"), dialogTitle)

        // ---- avatar upload control is present and shows initials before any upload.
        const uploadBtn = page.locator("[data-contact-avatar-control] [data-avatar-upload]")
        check(`${k}-uploadControlPresent`, await uploadBtn.count() > 0)
        const dialogAvatarKind = await page.locator("[data-contact-avatar-control] [data-avatar]").first().getAttribute("data-avatar-kind").catch(() => null)
        check(`${k}-dialogAvatarShowsInitials`, dialogAvatarKind === "initials", dialogAvatarKind)

        if (locale === "en" && scheme === "light" && width === 1440) {
          await page.screenshot({ path: path.join(shots, "mh602-contact-dialog-en-light-1440.png") })
        }

        await page.keyboard.press("Escape")
        await dialog.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {})
      }
      await page.keyboard.press("Escape").catch(() => {})
    }
  }
}

// ---- avatar upload flow, once, end to end: a tiny real PNG uploaded through
// the file input actually shows up as the row's photo. -----------------------
await page.emulateMedia({ colorScheme: "light" })
await page.setViewportSize({ width: 1440, height: 900 })
await page.goto(`${W("en")}/inbox`, { waitUntil: "load" })
await page.waitForSelector(`[data-conversation="${linkedConvID}"]`, { timeout: 20000 })
await page.locator(`[data-conversation="${linkedConvID}"]`).click({ button: "right" })
await page.locator("[data-contact-edit]").first().click()
await page.waitForSelector("[data-contact-dialog] [data-form=edit-contact]", { timeout: 10000 })

const tinyPNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  "base64",
)
const tmpPng = path.join(mkdtempSync(path.join(tmpdir(), "mh602-avatar-")), "avatar.png")
await (await import("node:fs/promises")).writeFile(tmpPng, tinyPNG)

const fileInput = page.locator("[data-contact-dialog] [data-avatar-input]")
await fileInput.setInputFiles(tmpPng)
await page.waitForTimeout(1500)
const afterUploadKind = await page.locator("[data-contact-avatar-control] [data-avatar]").first().getAttribute("data-avatar-kind").catch(() => null)
check("uploadReflectsAsImage", afterUploadKind === "image", afterUploadKind)
await page.screenshot({ path: path.join(shots, "mh602-contact-avatar-uploaded.png") })

// Reflected on the row itself once the dialog closes and the list revalidates.
await page.keyboard.press("Escape")
await page.locator("[data-contact-dialog]").waitFor({ state: "hidden", timeout: 5000 }).catch(() => {})
await page.waitForTimeout(1000)
const rowKindAfterUpload = await page.locator(`[data-conversation="${linkedConvID}"] [data-avatar]`).getAttribute("data-avatar-kind").catch(() => null)
check("rowReflectsUploadedAvatar", rowKindAfterUpload === "image", rowKindAfterUpload)
await page.screenshot({ path: path.join(shots, "mh602-list-avatar-uploaded.png") })

// Remove it again: back to initials.
await page.locator(`[data-conversation="${linkedConvID}"]`).click({ button: "right" })
await page.locator("[data-contact-edit]").first().click()
await page.waitForSelector("[data-contact-dialog] [data-form=edit-contact]", { timeout: 10000 })
await page.locator("[data-contact-avatar-control] [data-avatar-remove]").click()
await page.waitForTimeout(1000)
const afterRemoveKind = await page.locator("[data-contact-avatar-control] [data-avatar]").first().getAttribute("data-avatar-kind").catch(() => null)
check("removeReflectsAsInitials", afterRemoveKind === "initials", afterRemoveKind)

await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
