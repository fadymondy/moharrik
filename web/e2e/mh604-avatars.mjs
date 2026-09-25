// MH-604: group message bubbles — in a Telegram/Slack/Discord GROUP thread
// (never WhatsApp, which deliberately excludes groups; never email, which is
// document-layout, not chat bubbles), each incoming message shows its
// sender's PersonAvatar beside the bubble, once per run of consecutive
// same-sender messages, and never in a 1:1 thread. Against a LOCAL dev stack
// only (never production, never a real external provider): seeds one Slack
// GROUP conversation with two distinct senders — one with a Google-sourced
// cached photo (the same data: URI shape internal/googlecontacts' own photo
// fetcher writes, tying back to MH-604 item 1's contact-photo work), one with
// none — plus a ordinary 1:1 WhatsApp conversation as the regression case
// that must show no per-message sender avatar at all. Drives the real UI
// with playwright-core against real Chrome, checking en/ar x light/dark x
// 1440/390, with no horizontal overflow anywhere. Follows web/e2e/
// mh588-avatars.mjs's shape (same seeding-via-psql, same viewport matrix).
//
//   API on :8161, web (`next dev -p 3161`, API_ORIGIN=http://localhost:8161) already running.
//   node e2e/mh604-avatars.mjs http://localhost:3161 E:/tmp/mh604 "<owner DATABASE_URL>"
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3161"
const shots = process.argv[3] ?? "E:/tmp/mh604"
const ownerURL = process.argv[4]
if (!ownerURL) {
  console.error('usage: node e2e/mh604-avatars.mjs <base> <shots> "<owner DATABASE_URL>"')
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
  return execFileSync("psql", [ownerURL, "-t", "-A", "-c", sql], { encoding: "utf8" })
    .trim()
    .split(/\r?\n/)[0]
    .trim()
}

const RUN = Date.now()

// A tiny real PNG (1x1), the same fixture internal/crm's own tests use.
const tinyPNGBase64 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh604-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- an owner with a fresh workspace ---------------------------------------
await page.goto(`${base}/en/login`)
const owner = `mh604-${RUN}@brand.test`
const slug = await page.evaluate(async (email) => {
  const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
  const post = (u, b) =>
    fetch(u, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify(b),
    })
  const r = await post("/api/auth/register", { email, password: "correct-horse-battery" })
  if (!r.ok) throw new Error(`register failed: ${r.status} ${await r.text()}`)
  const ws = await (
    await post("/api/me/workspaces", { name: `MH604 ${email.split("@")[0]}` })
  ).json()
  return ws.workspace.slug
}, owner)
check("registeredAndWorkspaceCreated", !!slug, slug)

const wsID = psql(`SELECT id FROM workspaces WHERE slug = '${slug}'`)
check("workspaceIdFound", /^[0-9a-f-]{10,}$/.test(wsID), wsID)

// ---- seed: two group-thread senders — Dana has a Google-sourced cached
// photo (a data: URI, exactly the shape internal/googlecontacts writes),
// Sami has none — and a group conversation with one message from each.
const danaID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source, photo_url, photo_source, photo_fetch_status)
  VALUES ('${wsID}', 'Dana Aziz', '201005553333', 'inbox', 'data:image/png;base64,${tinyPNGBase64}', 'google', 'ok') RETURNING id`)
const samiID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source)
  VALUES ('${wsID}', 'Sami Nabil', '201005554444', 'inbox') RETURNING id`)
check("seededGroupSenderContacts", !!(danaID && samiID), { danaID, samiID })

const groupConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, external_thread_id, customer_name, subject, last_message_at, is_group)
  VALUES ('${wsID}', 'slack', 'grp-${RUN}', 'Design Team', 'group thread', now(), true) RETURNING id`)
check("seededGroupConversation", !!groupConvID, groupConvID)

psql(`
  INSERT INTO messages (workspace_id, conversation_id, direction, body, sender_contact_id, sender_name, sender_external_id, created_at)
  VALUES ('${wsID}', '${groupConvID}', 'in', 'hi everyone', '${danaID}', 'Dana Aziz', 'U-DANA', now() - interval '2 minutes')`)
psql(`
  INSERT INTO messages (workspace_id, conversation_id, direction, body, sender_contact_id, sender_name, sender_external_id, created_at)
  VALUES ('${wsID}', '${groupConvID}', 'in', 'hey team', '${samiID}', 'Sami Nabil', 'U-SAMI', now() - interval '1 minute')`)
check("seededGroupMessages", true)

// ---- regression case: an ordinary 1:1 WhatsApp thread must show NO
// per-message sender avatar at all (conversation.is_group = false).
const dmContactID = psql(`
  INSERT INTO contacts (workspace_id, name, phone, source)
  VALUES ('${wsID}', 'Youssef Adel', '201005552222', 'manual') RETURNING id`)
const dmConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, contact_id, subject, last_message_at)
  VALUES ('${wsID}', 'whatsapp', 'Youssef Adel', '201005552222', '${dmContactID}', 'hi', now()) RETURNING id`)
psql(
  `INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ('${wsID}', '${dmConvID}', 'in', 'hi') RETURNING id`,
)
check("seededDmConversation", !!(dmContactID && dmConvID), { dmContactID, dmConvID })

const W = (locale) => `${base}/${locale}/w/${slug}`

for (const scheme of ["light", "dark"]) {
  await page.emulateMedia({ colorScheme: scheme })
  for (const locale of ["en", "ar"]) {
    for (const width of [1440, 390]) {
      const k = `${locale}-${scheme}-${width}`
      await page.setViewportSize({ width, height: 900 })

      // ---- group thread: open it, check both senders' avatars.
      await page.goto(`${W(locale)}/inbox`, { waitUntil: "load" })
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 })
        .catch(() => {})
      await page.waitForTimeout(500)
      await page.locator(`[data-conversation="${groupConvID}"]`).click()
      await page
        .waitForSelector(`[data-thread="${groupConvID}"] [data-message-group="in"]`, {
          timeout: 15000,
        })
        .catch(() => {})
      await page.waitForTimeout(400)

      const groups = page.locator(`[data-thread="${groupConvID}"] [data-message-group="in"]`)
      const groupCount = await groups.count()
      check(`${k}-groupThreadHasTwoSenderGroups`, groupCount === 2, groupCount)

      const danaAvatar = groups.nth(0).locator("[data-group-sender-avatar] [data-avatar]").first()
      const danaKind = await danaAvatar.getAttribute("data-avatar-kind").catch(() => null)
      check(`${k}-groupSenderWithPhotoIsImage`, danaKind === "image", danaKind)

      const samiAvatar = groups.nth(1).locator("[data-group-sender-avatar] [data-avatar]").first()
      const samiKind = await samiAvatar.getAttribute("data-avatar-kind").catch(() => null)
      check(`${k}-groupSenderNoPhotoIsInitials`, samiKind === "initials", samiKind)

      let overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      check(`${k}-groupThreadNoOverflow`, overflow <= 1, overflow)
      if (width === 1440 && locale === "en" && scheme === "light") {
        await page.screenshot({ path: path.join(shots, "mh604-group-thread-en-light-1440.png") })
      }
      if (locale === "ar" && width === 390) {
        await page.screenshot({ path: path.join(shots, `mh604-group-thread-ar-${scheme}-390.png`) })
      }
      if (locale === "en" && scheme === "dark" && width === 1440) {
        await page.screenshot({ path: path.join(shots, "mh604-group-thread-en-dark-1440.png") })
      }

      // ---- 1:1 thread: no group-sender avatar anywhere, even though the
      // same PersonAvatar component is on the page for the header itself.
      await page.goto(`${W(locale)}/inbox`, { waitUntil: "load" })
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 })
        .catch(() => {})
      await page.waitForTimeout(500)
      await page.locator(`[data-conversation="${dmConvID}"]`).click()
      await page
        .waitForSelector(`[data-thread="${dmConvID}"] [data-message-group="in"]`, {
          timeout: 15000,
        })
        .catch(() => {})
      await page.waitForTimeout(300)
      const dmAvatarCount = await page
        .locator(`[data-thread="${dmConvID}"] [data-group-sender-avatar]`)
        .count()
      check(`${k}-dmThreadHasNoGroupSenderAvatar`, dmAvatarCount === 0, dmAvatarCount)
      overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-dmThreadNoOverflow`, overflow <= 1, overflow)
    }
  }
}

await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
