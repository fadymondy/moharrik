// The inbox's email channel, end to end against a REAL mail server (MH-133).
//
// GreenMail in Docker plays the customer's mail provider: the check posts a
// message into it over SMTP, points the workspace at it over IMAP, and then
// reads the team's reply back OUT of the customer's own mailbox. Nothing here
// touches an external provider, and nothing is asserted from the database —
// what matters is what a person sees on the page and what a customer receives.
//
//   docker run -d --name moh-greenmail -p 3025:3025 -p 3143:3143 \
//     -e GREENMAIL_OPTS='-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 -Dgreenmail.auth.disabled' \
//     greenmail/standalone:2.1.0
//
// GreenMail is plaintext, which is why the mailbox offers a "no encryption"
// mode at all — that mode exists for a mail server on the same host, and this
// is one.
import { chromium } from "playwright-core"
import { createConnection } from "node:net"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const mailHost = process.argv[3] ?? "127.0.0.1"
const SMTP_PORT = 3025
const IMAP_PORT = 3143

const CUSTOMER = "dana@example.test"
// A mailbox PER RUN. GreenMail keeps its state for the life of the container,
// so a shared support@ address means one run collects the previous run's
// unread mail and every count is off by however many times this was run. That
// is exactly what happened the first time.
const RUN = Date.now()
const SUPPORT = `support-${RUN}@shop.test`

/** Speak just enough SMTP to post a message. */
function smtpSend({ from, to, raw }) {
  return new Promise((resolve, reject) => {
    const sock = createConnection(SMTP_PORT, mailHost)
    const steps = [`EHLO check.test`, `MAIL FROM:<${from}>`, `RCPT TO:<${to}>`, `DATA`, `${raw}\r\n.`, `QUIT`]
    let i = -1
    let buf = ""
    sock.setTimeout(20000, () => reject(new Error("smtp timeout")))
    sock.on("data", (d) => {
      buf += String(d)
      if (!buf.endsWith("\r\n")) return
      buf = ""
      i++
      if (i >= steps.length) return
      sock.write(steps[i] + "\r\n")
    })
    sock.on("error", reject)
    sock.on("close", () => resolve())
  })
}

/** Speak just enough IMAP to read a mailbox and count what matches. */
function imapFetchAll(user, pass) {
  return new Promise((resolve, reject) => {
    const sock = createConnection(IMAP_PORT, mailHost)
    const steps = [`a1 LOGIN ${user} ${pass}`, `a2 SELECT INBOX`, `a3 FETCH 1:* (BODY.PEEK[])`, `a4 LOGOUT`]
    let i = -1
    let all = ""
    sock.setTimeout(20000, () => reject(new Error("imap timeout")))
    sock.on("data", (d) => {
      all += String(d)
      const chunk = String(d)
      if (/^(\* OK|a\d+ (OK|NO|BAD))/m.test(chunk)) {
        i++
        if (i < steps.length) sock.write(steps[i] + "\r\n")
      }
    })
    sock.on("error", reject)
    sock.on("close", () => resolve(all))
  })
}

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mb-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `mb${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
  return { email, slug: page.url().split("/w/")[1].split("/")[0] }
}

const out = {}
const ctx = await newCtx()
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "MB " + Date.now())
out.slug = slug

const stamp = RUN
const firstID = `first-${stamp}@example.test`

// ---- the customer writes in ----
await smtpSend({
  from: CUSTOMER,
  to: SUPPORT,
  raw:
    `From: Dana Customer <${CUSTOMER}>\r\n` +
    `To: ${SUPPORT}\r\n` +
    `Subject: Invoice ${stamp} looks wrong\r\n` +
    `Message-Id: <${firstID}>\r\n` +
    `Content-Type: text/plain; charset=utf-8\r\n\r\n` +
    `You charged me 300, I paid 200.\r\n`,
})

// ---- point the workspace at that mailbox ----
await page.goto(`${base}/en/w/${slug}/inbox/email`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="mailbox"]', { timeout: 20000 })
out.startsUnconfigured = await page.evaluate(() => document.querySelector("[data-mailbox-configured]")?.getAttribute("data-mailbox-configured") === "false")

await page.fill("#mb-host", mailHost)
await page.fill("#mb-port", String(IMAP_PORT))
await page.fill("#mb-user", SUPPORT)
await page.fill("#mb-pass", "secret")
await page.fill("#mb-folder", "INBOX")
await page.click("#mb-tls")
await page.click('[role="option"]:has-text("No encryption")')
await page.click('form[data-form="mailbox"] button[type=submit]')
await page.waitForFunction(() => document.querySelector("[data-mailbox-configured]")?.getAttribute("data-mailbox-configured") === "true", { timeout: 15000 })
out.saved = true

// The password is write-only: reloading must not put it back in the field.
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("#mb-pass", { timeout: 15000 })
out.passwordNotEchoed = await page.evaluate(() => document.querySelector("#mb-pass")?.value === "")
out.saysPasswordStored = await page.evaluate(() => document.body.innerText.includes("A password is stored"))

// ---- collect it ----
await page.click('button:has-text("Check now")')
await page.waitForFunction(() => /1 emails collected|collected/i.test(document.body.innerText), { timeout: 20000 }).catch(() => {})
await page.waitForTimeout(1500)
out.mailboxError = await page.evaluate(() => document.querySelector("[data-mailbox-error]")?.innerText?.slice(0, 160) ?? "")

// ---- it is in the inbox, as an email conversation ----
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 20000 })
// The list row shows the sender and a preview of the last message; the
// subject is on the thread itself. Asserting the subject here was wrong about
// the UI, not about the feature.
out.inInbox = await page.evaluate(() => document.body.innerText.includes("Dana Customer"))
out.showsEmailChannel = await page.evaluate(() => /email/i.test(document.querySelector("[data-conversation]")?.innerText ?? ""))
out.unread = await page.evaluate(() => document.querySelector("[data-conversation]")?.getAttribute("data-conversation-unread") === "true")
await page.click("[data-conversation]")
await page.waitForSelector("[data-messages]", { timeout: 15000 })
out.bodyShown = await page.evaluate(() => document.body.innerText.includes("You charged me 300"))
out.subjectOnThread = await page.evaluate((st) => document.body.innerText.includes(`Invoice ${st} looks wrong`), stamp)

// ---- a second email in the same thread must NOT open a second conversation ----
await smtpSend({
  from: CUSTOMER,
  to: SUPPORT,
  raw:
    `From: Dana Customer <${CUSTOMER}>\r\n` +
    `To: ${SUPPORT}\r\n` +
    `Subject: Re: Invoice ${stamp} looks wrong\r\n` +
    `Message-Id: <second-${stamp}@example.test>\r\n` +
    `In-Reply-To: <${firstID}>\r\n` +
    `References: <${firstID}>\r\n` +
    `Content-Type: text/plain; charset=utf-8\r\n\r\n` +
    `Any update on this?\r\n`,
})
await page.goto(`${base}/en/w/${slug}/inbox/email`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('button:has-text("Check now")', { timeout: 15000 })
await page.click('button:has-text("Check now")')
await page.waitForTimeout(2500)
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 15000 })
out.conversationCount = await page.evaluate(() => document.querySelectorAll("[data-conversation]").length)
await page.click("[data-conversation]")
await page.waitForSelector("[data-messages]", { timeout: 15000 })
// Both messages in ONE thread is the assertion that matters; the count above
// proves no second conversation was opened.
out.threadedOntoSameConversation = await page.evaluate(
  () => document.body.innerText.includes("Any update on this?") && document.body.innerText.includes("You charged me 300"),
)

// ---- replying without SMTP configured must say so, not fail silently ----
await page.fill("#reply-body", "This should not be sendable yet.")
await page.click('[data-form="reply"] button[type=submit]')
await page.waitForTimeout(2000)
out.replyWithoutSMTPExplains = await page.evaluate(() => /SMTP/i.test(document.body.innerText))
out.messagesAfterFailedReply = await page.evaluate(() => document.querySelectorAll('[data-message="out"]').length)

// ---- configure SMTP, then reply for real ----
await page.goto(`${base}/en/w/${slug}/settings/email`, { waitUntil: "networkidle" })
await page.fill("#smtp-host", mailHost)
await page.fill("#smtp-port", String(SMTP_PORT))
await page.fill("#smtp-username", SUPPORT)
await page.fill("#smtp-password", "secret")
await page.fill("#smtp-from-email", SUPPORT)
await page.fill("#smtp-from-name", "Shop Support")
await page.click("#smtp-tls")
await page.click('[role="option"]:has-text("None (local relay only)")')
await page.click('form[data-form="smtp"] button[type=submit]')
await page.waitForTimeout(2000)
out.smtpSaved = await page.evaluate(() => !/could not/i.test(document.body.innerText))

await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 15000 })
await page.click("[data-conversation]")
await page.waitForSelector("#reply-body", { timeout: 15000 })
await page.fill("#reply-body", `We refunded 100 — reference ${stamp}.`)
await page.click('[data-form="reply"] button[type=submit]')
await page.waitForFunction(() => document.querySelectorAll('[data-message="out"]').length > 0, { timeout: 20000 })
  .then(() => (out.replySaved = true))
  .catch(() => (out.replySaved = false))

// ---- the customer actually received it, threaded ----
const mailbox = await imapFetchAll(CUSTOMER, "secret")
out.customerGotTheReply = mailbox.includes(`We refunded 100 — reference ${stamp}`) || mailbox.includes(`reference ${stamp}`)
out.replyIsThreaded = mailbox.includes(`In-Reply-To: <second-${stamp}@example.test>`) || mailbox.includes(firstID)
out.replySubjectIsRe = /Subject: Re: Invoice/i.test(mailbox)

await ctx.close()

// ---- Arabic at 390px ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "MB AR " + Date.now())
await pageAr.goto(`${base}/ar/w/${slugAr}/inbox/email`, { waitUntil: "domcontentloaded" })
await pageAr.waitForTimeout(2000)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_rawKeys = await pageAr.evaluate(() => /mailbox\.[a-zA-Z]/.test(document.body.innerText))
await ctxAr.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
