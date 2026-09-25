// MH-600 + MH-648, against a LOCAL dev stack only (never production): a
// sender's email folds into ONE conversation shown as a normal message
// thread — no grouped-by-sender toggle, no drill-in — and every channel's
// thread pages newest-30-first with older messages loading on scroll-up,
// preserving the reader's scroll anchor (no jump).
//
// Seeds, via psql against the owner DB connection: one email sender with
// THREE messages of different subjects folded into one conversation (the
// shape internal/inbox/imap.go's findThread now always produces — seeding
// three separate conversations for one sender would be seeding a state the
// app can no longer reach, and would also violate the new
// conversations_email_sender_uq index from internal/db/post/
// 178_email_one_conversation_per_sender.sql), plus one WhatsApp conversation
// with 80 messages (numbered "WA message N" so a specific message can be
// re-located after older ones load above it, to check the anchor held).
//
// Drives the real UI with playwright-core against real Chrome. en/ar x
// light/dark x 1440/390, no horizontal overflow anywhere, no page errors.
// Follows web/e2e/mh604-avatars.mjs's shape (same seeding-via-psql, same
// viewport matrix, same register-via-fetch bootstrap).
//
//   API + web (`next dev`, API_ORIGIN set) already running locally.
//   node e2e/mh600-648-local.mjs http://localhost:3161 E:/tmp/mh600c "<owner DATABASE_URL>"
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3161"
const shots = process.argv[3] ?? "E:/tmp/mh600c"
const ownerURL = process.argv[4]
if (!ownerURL) {
  console.error('usage: node e2e/mh600-648-local.mjs <base> <shots> "<owner DATABASE_URL>"')
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
  // SQL goes on stdin: Windows re-encodes argv to the ANSI code page, which mangles "—".
  return execFileSync("psql", [ownerURL, "-t", "-A", "-f", "-"], {
    input: sql,
    encoding: "utf8",
    env: { ...process.env, PGCLIENTENCODING: "UTF8" },
  })
    .trim()
    .split(/\r?\n/)[0]
    .trim()
}

const RUN = Date.now()

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh600c-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- an owner with a fresh workspace ---------------------------------------
await page.goto(`${base}/en/login`)
const owner = `mh600c-${RUN}@brand.test`
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
    await post("/api/me/workspaces", { name: `MH600c ${email.split("@")[0]}` })
  ).json()
  return ws.workspace.slug
}, owner)
check("registeredAndWorkspaceCreated", !!slug, slug)

const wsID = psql(`SELECT id FROM workspaces WHERE slug = '${slug}'`)
check("workspaceIdFound", /^[0-9a-f-]{10,}$/.test(wsID), wsID)

// ---- seed: one email sender, three messages, three different subjects,
// ONE conversation — the post-fold, post-findThread-rewrite shape.
const senderEmail = `sender-${RUN}@customer.test`
const subjects = ["Order question", "Re: Order question — update", "New: shipping delay"]
const emailConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_email, customer_name, subject, last_message_at, unread)
  VALUES ('${wsID}', 'email', '${senderEmail}', 'Nadia Sender', '${subjects[2]}', now(), true) RETURNING id`)
check("seededEmailConversation", !!emailConvID, emailConvID)

subjects.forEach((subject, i) => {
  psql(`
    INSERT INTO messages (workspace_id, conversation_id, direction, body, subject, external_id, created_at)
    VALUES ('${wsID}', '${emailConvID}', 'in', 'Body of message ${i + 1}', '${subject}', 'msg-${RUN}-${i}@customer.test', now() - interval '${3 - i} days')`)
})
check("seededEmailMessages", true)

// ---- seed: one WhatsApp conversation, 80 messages, numbered so a specific
// one (the 30th-from-newest — the first one an older page would surface) can
// be re-located after "load older" runs, to check the scroll anchor held.
const waConvID = psql(`
  INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, last_message_at, unread)
  VALUES ('${wsID}', 'whatsapp', 'Layla WhatsApp', '201005557777', now(), true) RETURNING id`)
check("seededWhatsappConversation", !!waConvID, waConvID)

psql(`
  INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at)
  SELECT '${wsID}', '${waConvID}', CASE WHEN i % 6 = 0 THEN 'out' ELSE 'in' END,
         'WA message ' || i, now() - ((80 - i) * interval '2 minutes')
  FROM generate_series(1, 80) AS i`)
check("seededWhatsappMessages", true)

const W = (locale) => `${base}/${locale}/w/${slug}`

for (const scheme of ["light", "dark"]) {
  await page.emulateMedia({ colorScheme: scheme })
  const errs = []
  const onErr = (e) => errs.push(String(e))
  page.on("pageerror", onErr)

  for (const locale of ["en", "ar"]) {
    for (const width of [1440, 390]) {
      const k = `${locale}-${scheme}-${width}`
      await page.setViewportSize({ width, height: 900 })

      // ---- Email tab: one row for the sender, no grouped-by-sender UI. ----
      await page.goto(`${W(locale)}/inbox?channel=email`, { waitUntil: "load" })
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 })
        .catch(() => {})
      await page.waitForTimeout(500)
      const emailRowCount = await page
        .locator("[data-conversation-list] [data-conversation]")
        .count()
      check(`${k}-emailTabOneRowPerSender`, emailRowCount === 1, emailRowCount)

      const noGroupedUi = await page.evaluate(
        () =>
          !document.querySelector("[data-sender-toggle]") &&
          !document.querySelector("[data-sender-filter-banner]") &&
          !document.querySelector("[data-sender-group-list]") &&
          !document.querySelector("[data-sender-clear]"),
      )
      check(`${k}-noGroupedBySenderUi`, noGroupedUi)

      await page.locator(`[data-conversation="${emailConvID}"]`).click()
      await page
        .waitForSelector(`[data-thread="${emailConvID}"] [data-message-layout=email]`, {
          timeout: 15000,
        })
        .catch(() => {})
      await page.waitForTimeout(800)

      const emailThread = await page.evaluate((convID) => {
        const root = document.querySelector(`[data-thread="${convID}"]`)
        if (!root) return null
        const cards = [...root.querySelectorAll("[data-message-layout=email]")]
        const subjects = [...root.querySelectorAll("[data-message-subject]")].map(
          (el) => el.textContent?.trim() ?? "",
        )
        return { cardCount: cards.length, subjects }
      }, emailConvID)
      check(
        `${k}-emailThreadShowsAllThreeMessages`,
        !!emailThread && emailThread.cardCount === 3,
        emailThread,
      )
      check(
        `${k}-emailThreadShowsEachSubject`,
        !!emailThread && subjects.every((s) => emailThread.subjects.includes(s)),
        emailThread,
      )

      let overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      check(`${k}-emailNoOverflow`, overflow <= 1, overflow)
      if (locale === "en" && scheme === "light" && width === 1440) {
        await page.screenshot({ path: path.join(shots, "mh600c-email-en-light-1440.png") })
      }
      if (locale === "ar" && width === 390) {
        await page.screenshot({ path: path.join(shots, `mh600c-email-ar-${scheme}-390.png`) })
      }
      if (locale === "en" && scheme === "dark" && width === 1440) {
        await page.screenshot({ path: path.join(shots, "mh600c-email-en-dark-1440.png") })
      }

      // ---- WhatsApp tab: 80-message thread opens at the bottom, 30 rendered,
      // scrolling up loads older messages while the view holds its anchor. ----
      await page.goto(`${W(locale)}/inbox?channel=whatsapp`, { waitUntil: "load" })
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 20000 })
        .catch(() => {})
      await page.waitForTimeout(500)
      await page.locator(`[data-conversation="${waConvID}"]`).click()
      await page
        .waitForSelector(`[data-thread="${waConvID}"] [data-messages]`, { timeout: 15000 })
        .catch(() => {})
      // Let the initial jump-to-bottom settle (chat-thread.tsx's own settle
      // window is 350ms plus a 4s anchor-release grace period; this only
      // needs the FIRST paint, not the whole grace window).
      await page.waitForTimeout(1000)

      const initial = await page.evaluate((convID) => {
        const pane = document.querySelector(`[data-thread="${convID}"] [data-messages]`)
        if (!pane) return null
        return {
          count: pane.querySelectorAll("[data-message]").length,
          atBottom: pane.scrollHeight - pane.scrollTop - pane.clientHeight < 200,
        }
      }, waConvID)
      check(
        `${k}-whatsappOpensAtBottomWith30`,
        !!initial && initial.count === 30 && initial.atBottom,
        initial,
      )

      // Scroll up the way a reader does — with the wheel. (Setting scrollTop from
      // script is not a reader: for the first seconds after open the pane holds
      // itself on the latest message until real input — wheel/touch/key — arrives.)
      const paneSel = `[data-thread="${waConvID}"] [data-messages]`
      await page.locator(paneSel).scrollIntoViewIfNeeded()
      const paneBox = await page.locator(paneSel).boundingBox()
      await page.mouse.move(paneBox.x + paneBox.width / 2, paneBox.y + paneBox.height / 2)
      for (let i = 0; i < 12; i++) {
        await page.mouse.wheel(0, -1500)
        await page.waitForTimeout(80)
        const n = await page.evaluate(
          (sel) => document.querySelector(sel)?.querySelectorAll("[data-message]").length ?? 0,
          paneSel,
        )
        if (n > 30) break
      }

      await page
        .waitForFunction(
          (sel) =>
            (document.querySelector(sel)?.querySelectorAll("[data-message]").length ?? 0) > 30,
          paneSel,
          { timeout: 10000 },
        )
        .catch(() => {})
      await page.waitForTimeout(500)

      // No jump: WA message 51 — the oldest of the first page, the one the reader
      // was looking at when they reached the top — is still in view once 30 older
      // messages land above it, and the pane is no longer at scrollTop 0.
      const anchor = await page.evaluate((sel) => {
        const pane = document.querySelector(sel)
        const el = [...(pane?.querySelectorAll("[data-message]") ?? [])].find((m) =>
          m.textContent.includes("WA message 51"),
        )
        if (!pane || !el) return { found: false, scrollTop: pane?.scrollTop ?? null }
        const p = pane.getBoundingClientRect(),
          r = el.getBoundingClientRect()
        return { found: true, scrollTop: pane.scrollTop, top: r.top - p.top, paneHeight: p.height }
      }, paneSel)

      const afterCount = await page.evaluate((convID) => {
        const pane = document.querySelector(`[data-thread="${convID}"] [data-messages]`)
        return pane ? pane.querySelectorAll("[data-message]").length : 0
      }, waConvID)
      check(`${k}-whatsappLoadsOlderOnScrollUp`, afterCount > 30, afterCount)

      const anchorHeld =
        anchor.found && anchor.scrollTop > 0 && anchor.top > -60 && anchor.top < anchor.paneHeight
      check(`${k}-whatsappScrollAnchorHolds`, anchorHeld, anchor)

      overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(`${k}-whatsappNoOverflow`, overflow <= 1, overflow)
      if (locale === "en" && scheme === "light" && width === 1440) {
        await page.screenshot({ path: path.join(shots, "mh600c-whatsapp-en-light-1440.png") })
      }
      if (locale === "ar" && width === 390) {
        await page.screenshot({ path: path.join(shots, `mh600c-whatsapp-ar-${scheme}-390.png`) })
      }
    }
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  page.off("pageerror", onErr)
}

await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
