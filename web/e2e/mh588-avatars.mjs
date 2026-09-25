// MH-588: contact avatars from every channel, shown on the contacts list, the
// contact page, the inbox conversation list and every thread header — so
// it's easy to see who a person is. Against a LOCAL dev stack only (never
// production, never a real external provider): seeds one contact with a
// provider-cached photo (a data: URI, the same shape internal/whatsapp/
// avatar.go and the new Telegram/Slack/Discord fetchers all write) and one
// contact with none, then drives the real UI with playwright-core against
// real Chrome, checking all three surfaces across en/ar x light/dark x
// 1440/390, with no horizontal overflow anywhere. Follows web/e2e/
// inbox-avatars.mjs's shape (same seeding-via-psql, same viewport matrix).
//
//   API on :8151, web (`next dev -p 3151`, API_ORIGIN=http://localhost:8151) already running.
//   node e2e/mh588-avatars.mjs http://localhost:3151 E:/tmp/mh588 "<owner DATABASE_URL>"
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3151"
const shots = process.argv[3] ?? "E:/tmp/mh588"
const ownerURL = process.argv[4]
if (!ownerURL) {
  console.error('usage: node e2e/mh588-avatars.mjs <base> <shots> "<owner DATABASE_URL>"')
  process.exit(2)
}

const out = {}
const fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

function psql(sql) {
  // -t -A hides column headers, but psql still prints its own completion tag
  // on the line after a RETURNING result — take the first line.
  return execFileSync("psql", [ownerURL, "-t", "-A", "-c", sql], { encoding: "utf8" }).trim().split(/\r?\n/)[0].trim()
}

const RUN = Date.now()

// A tiny real PNG (1x1), the same fixture internal/crm's own tests use.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh588-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- an owner with a fresh workspace ---------------------------------------
await page.goto(`${base}/en/login`)
const owner = `mh588-${RUN}@brand.test`
const slug = await page.evaluate(async (email) => {
  const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
  const post = (u, b) =>
    fetch(u, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify(b) })
  const r = await post("/api/auth/register", { email, password: "correct-horse-battery" })
  if (!r.ok) throw new Error(`register failed: ${r.status} ${await r.text()}`)
  const ws = await (await post("/api/me/workspaces", { name: `MH588 ${email.split("@")[0]}` })).json()
  return ws.workspace.slug
}, owner)
check("registeredAndWorkspaceCreated", !!slug, slug)

const wsID = psql(`SELECT id FROM workspaces WHERE slug = '${slug}'`)
check("workspaceIdFound", /^[0-9a-f-]{10,}$/.test(wsID), wsID)

// ---- seed: one contact with a provider-cached photo (Telegram-sourced, a
// data: URI — exactly the shape RefreshContactPhoto's WhatsApp fetcher and
// the new fetchTelegramPhotoURL/fetchSlackPhotoURL/fetchDiscordAvatarURL all
// write), one with none. Both get a WhatsApp conversation with a message so
// they show up in the inbox list (MH-599: a thread with no message never
// appears).
const photoContactID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source, photo_url, photo_source, photo_fetch_status)
  VALUES ('${wsID}', 'Layla Hassan', '201005551111', 'manual', 'data:image/png;base64,${tinyPNGBase64}', 'telegram', 'ok') RETURNING id`)
const noPhotoContactID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source)
  VALUES ('${wsID}', 'Youssef Adel', '201005552222', 'manual') RETURNING id`)
check("seededContacts", !!(photoContactID && noPhotoContactID), { photoContactID, noPhotoContactID })

const photoConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, contact_id, subject, last_message_at)
  VALUES ('${wsID}', 'whatsapp', 'Layla Hassan', '201005551111', '${photoContactID}', 'hi', now()) RETURNING id`)
const noPhotoConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, contact_id, subject, last_message_at)
  VALUES ('${wsID}', 'whatsapp', 'Youssef Adel', '201005552222', '${noPhotoContactID}', 'hi', now() - interval '1 minute') RETURNING id`)
for (const [convID, body] of [
  [photoConvID, "hi"],
  [noPhotoConvID, "hi"],
]) {
  psql(`INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ('${wsID}', '${convID}', 'in', '${body}') RETURNING id`)
}
check("seededConversations", !!(photoConvID && noPhotoConvID), { photoConvID, noPhotoConvID })

const W = (locale) => `${base}/${locale}/w/${slug}`

for (const scheme of ["light", "dark"]) {
  await page.emulateMedia({ colorScheme: scheme })
  for (const locale of ["en", "ar"]) {
    for (const width of [1440, 390]) {
      const k = `${locale}-${scheme}-${width}`
      await page.setViewportSize({ width, height: 900 })

      // ---- contacts list: the photo contact shows an image avatar, the other initials.
      await page.goto(`${W(locale)}/contacts`, { waitUntil: "load" })
      await page.waitForSelector(`[data-contact-row]`, { timeout: 20000 }).catch(() => {})
      await page.waitForTimeout(500)
      const photoRowKind = await page
        .locator(`tr:has-text("201005551111") [data-avatar]`)
        .first()
        .getAttribute("data-avatar-kind")
        .catch(() => null)
      check(`${k}-contactsListPhotoRowIsImage`, photoRowKind === "image", photoRowKind)
      const noPhotoRowKind = await page
        .locator(`tr:has-text("201005552222") [data-avatar]`)
        .first()
        .getAttribute("data-avatar-kind")
        .catch(() => null)
      check(`${k}-contactsListNoPhotoRowIsInitials`, noPhotoRowKind === "initials", noPhotoRowKind)
      let overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-contactsListNoOverflow`, overflow <= 1, overflow)
      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh588-contacts-list-en-light-1440.png") })
      }

      // ---- inbox list: same split.
      await page.goto(`${W(locale)}/inbox`, { waitUntil: "load" })
      await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 }).catch(() => {})
      await page.waitForTimeout(500)
      const inboxPhotoKind = await page
        .locator(`[data-conversation="${photoConvID}"] [data-avatar]`)
        .first()
        .getAttribute("data-avatar-kind")
        .catch(() => null)
      check(`${k}-inboxListPhotoRowIsImage`, inboxPhotoKind === "image", inboxPhotoKind)
      const inboxNoPhotoKind = await page
        .locator(`[data-conversation="${noPhotoConvID}"] [data-avatar]`)
        .first()
        .getAttribute("data-avatar-kind")
        .catch(() => null)
      check(`${k}-inboxListNoPhotoRowIsInitials`, inboxNoPhotoKind === "initials", inboxNoPhotoKind)
      overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-inboxListNoOverflow`, overflow <= 1, overflow)
      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh588-inbox-list-en-light-1440.png") })
      }

      // ---- thread header: open the photo contact's conversation.
      await page.locator(`[data-conversation="${photoConvID}"]`).click()
      await page.waitForSelector(`[data-thread="${photoConvID}"]`, { timeout: 15000 }).catch(() => {})
      const headerKind = await page
        .locator(`[data-thread="${photoConvID}"] header [data-avatar]`)
        .first()
        .getAttribute("data-avatar-kind")
        .catch(() => null)
      check(`${k}-threadHeaderPhotoIsImage`, headerKind === "image", headerKind)
      overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-threadNoOverflow`, overflow <= 1, overflow)
      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh588-thread-en-light-1440.png") })
      }
      if (locale === "ar" && width === 390) {
        await page.screenshot({ path: path.join(shots, `mh588-thread-ar-${scheme}-390.png`) })
      }

      // ---- contact page: the photo contact's own page shows an image avatar.
      await page.goto(`${W(locale)}/contacts/${photoContactID}`, { waitUntil: "load" })
      await page.waitForSelector("[data-avatar]", { timeout: 15000 }).catch(() => {})
      const contactPageKind = await page.locator("[data-avatar]").first().getAttribute("data-avatar-kind").catch(() => null)
      check(`${k}-contactPagePhotoIsImage`, contactPageKind === "image", contactPageKind)
      overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-contactPageNoOverflow`, overflow <= 1, overflow)
      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh588-contact-page-en-light-1440.png") })
      }

      // ---- the no-photo contact's own page shows initials, not a broken image.
      await page.goto(`${W(locale)}/contacts/${noPhotoContactID}`, { waitUntil: "load" })
      await page.waitForSelector("[data-avatar]", { timeout: 15000 }).catch(() => {})
      const noPhotoContactPageKind = await page.locator("[data-avatar]").first().getAttribute("data-avatar-kind").catch(() => null)
      check(`${k}-contactPageNoPhotoIsInitials`, noPhotoContactPageKind === "initials", noPhotoContactPageKind)
    }
  }
}

await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
