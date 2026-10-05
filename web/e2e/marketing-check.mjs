// Email marketing end to end, signed in, through the real web + API:
//   node e2e/marketing-check.mjs [base]
// Runs a throwaway SMTP sink on 127.0.0.1:2525 (plaintext, tls "none") so the double opt-in email
// and the campaign are really delivered and their links really followed.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import net from "node:net"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const inbox = []
const sink = net.createServer((sock) => {
  let data = false,
    buf = "",
    msg = { rcpt: [], body: "" }
  sock.write("220 sink ESMTP\r\n")
  sock.on("data", (chunk) => {
    buf += chunk.toString("utf8")
    let i
    while ((i = buf.indexOf("\r\n")) >= 0) {
      const line = buf.slice(0, i)
      buf = buf.slice(i + 2)
      if (data) {
        if (line === ".") {
          data = false
          inbox.push(msg)
          msg = { rcpt: [], body: "" }
          sock.write("250 queued\r\n")
        } else msg.body += line + "\n"
        continue
      }
      const cmd = line.slice(0, 4).toUpperCase()
      if (cmd === "EHLO" || cmd === "HELO") sock.write("250 sink\r\n")
      else if (cmd === "RCPT") {
        msg.rcpt.push(line)
        sock.write("250 ok\r\n")
      } else if (cmd === "DATA") {
        data = true
        sock.write("354 go\r\n")
      } else if (cmd === "QUIT") {
        sock.write("221 bye\r\n")
        sock.end()
      } else sock.write("250 ok\r\n")
    }
  })
})
await new Promise((r) => sink.listen(2525, "127.0.0.1", r))

const launch = () =>
  chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mk-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
  })
async function signUp(page, tag) {
  const email = `${tag}${Date.now()}@x.test`
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", email)
  await page.fill("#password", "Passw0rd!long-enough")
  const name = page.locator("#name")
  if (await name.count()) await name.fill(tag)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `${tag} ${Date.now()}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  return { email, slug }
}
const waitFor = async (fn, ms = 20000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    const v = await fn()
    if (v) return v
    await new Promise((r) => setTimeout(r, 300))
  }
  throw new Error("timeout")
}
const out = {}

// ---- workspace A ----
const ctxA = await launch()
const a = ctxA.pages()[0]
const A = await signUp(a, "mka")
out.workspaceA = A.slug
out.nav = await a.evaluate(() =>
  [...document.querySelectorAll(".grid-shell-nav nav a")].map((x) =>
    x.getAttribute("href").split("/w/")[1].split("/").slice(1).join("/"),
  ),
)

// MH-1156: lists live in a sideover opened from the header.
await a.goto(`${base}/en/w/${A.slug}/contacts`, { waitUntil: "networkidle" })
await a.click('[data-open="lists"]')
await a.click('[data-open="create-list"]')
await a.fill("#list-name", "Newsletter")
await a.click('[data-form="create-list"] button[type=submit]')
await a.waitForSelector('[data-list-row="Newsletter"]')
await a.click('[data-open="create-list"]')
await a.fill("#list-name", "Customers")
await a.locator("[data-form=create-list]").getByRole("switch").click()
await a.click('[data-form="create-list"] button[type=submit]')
await a.waitForSelector('[data-list-row="Customers"]')
out.lists = await a.$$eval("[data-list-row]", (r) =>
  r.map((x) => x.innerText.replace(/\s+/g, " ").trim()),
)
await a.keyboard.press("Escape")
await a.waitForSelector("[data-lists-sheet]", { state: "detached" })

// Contact on the double opt-in list, before SMTP exists: pending, confirmation not sent.
await a.click('[data-open="add-contact"]')
await a.fill("#contact-email", "reader@example.test")
await a.fill("#contact-name", "Reader")
await a.click("#contact-list")
await a.getByRole("option", { name: "Newsletter" }).click()
await a.click('[data-form="add-contact"] button[type=submit]')
await a.waitForSelector('[data-contact-row="reader@example.test"]')
out.contactPending = await a.$eval('[data-contact-row="reader@example.test"]', (r) =>
  r.innerText.replace(/\s+/g, " "),
)
// The SMTP hint lives in the add-contact dialog (MH-1144).
await a.click('[data-open="add-contact"]')
out.smtpMissingHintOnContacts = await a.locator("text=Confirmation emails need SMTP").count()
await a.keyboard.press("Escape")

// ---- SMTP settings page ----
await a.goto(`${base}/en/w/${A.slug}/settings/email`, { waitUntil: "networkidle" })
out.smtpStateBefore = await a.getAttribute("[data-smtp-state]", "data-smtp-state")
await a.fill("#smtp-host", "127.0.0.1")
await a.fill("#smtp-port", "2525")
await a.click("#smtp-tls")
await a.getByRole("option", { name: /None/ }).click()
await a.fill("#smtp-password", "sink-secret")
await a.fill("#smtp-from-name", "Acme News")
await a.fill("#smtp-from-email", "news@acme.test")
await a.click('[data-form="smtp"] button[type=submit]')
await a.waitForSelector('[data-smtp-state="configured"]')
out.smtpPasswordPlaceholder = await a.getAttribute("#smtp-password", "placeholder")
await a.click('[data-form="smtp-test"] button[type=submit]')
await waitFor(() => inbox.some((m) => /Moharrik SMTP test/.test(m.body)))
out.testEmailDelivered = true

// ---- double opt-in: resend, follow the link from the delivered email ----
await a.goto(`${base}/en/w/${A.slug}/contacts`, { waitUntil: "networkidle" })
const before = inbox.length
await a.click('[data-contact-row="reader@example.test"] >> text=Resend confirmation')
const confirmMail = await waitFor(() =>
  inbox.slice(before).find((m) => /mail\/confirm\?token=/.test(m.body)),
)
const token = confirmMail.body.match(/mail\/confirm\?token=([0-9a-f]{64})/)[1]
out.confirmHasListUnsubscribeHeader = /List-Unsubscribe: </.test(confirmMail.body)
const p2 = await ctxA.newPage()
await p2.goto(`${base}/en/mail/confirm?token=${token}`, { waitUntil: "networkidle" })
await p2.getByRole("button", { name: "Confirm subscription" }).click()
out.confirmResult = await (
  await p2.waitForSelector("[data-mail-result]")
).getAttribute("data-mail-result")
await p2.close()
await a.reload({ waitUntil: "networkidle" })
out.contactAfterConfirm = await a.$eval('[data-contact-row="reader@example.test"]', (r) =>
  r.innerText.replace(/\s+/g, " "),
)
// A second contact straight onto the list without double opt-in.
await a.click('[data-open="add-contact"]')
await a.fill("#contact-email", "buyer@example.test")
await a.click("#contact-list")
await a.getByRole("option", { name: "Customers" }).click()
await a.click('[data-form="add-contact"] button[type=submit]')
await a.waitForSelector('[data-contact-row="buyer@example.test"]')
out.contactNoDoi = await a.$eval('[data-contact-row="buyer@example.test"]', (r) =>
  r.innerText.replace(/\s+/g, " "),
)

// ---- campaign ----
await a.goto(`${base}/en/w/${A.slug}/campaigns`, { waitUntil: "networkidle" })
await a.fill("#campaign-subject", "September update")
await a.fill(
  "#campaign-body",
  "# Hello\n\nWe shipped **email marketing**.\n\n- lists\n- campaigns\n\n[Read more](https://moharrik.com)",
)
await a.click('[data-form="new-campaign"] button[type=submit]')
await a.waitForSelector('[data-campaign-row="September update"]')
out.campaignDraft = await a.$eval('[data-campaign-row="September update"]', (r) =>
  r.innerText.replace(/\s+/g, " "),
)
const beforeSend = inbox.length
await a.click('[data-campaign-row="September update"] >> text=Send')
await a.getByRole("alertdialog").getByRole("button", { name: "Send" }).click()
await waitFor(
  async () =>
    /Sent/.test(await a.$eval('[data-campaign-row="September update"]', (r) => r.innerText)),
  30000,
)
out.campaignSent = await a.$eval('[data-campaign-row="September update"]', (r) =>
  r.innerText.replace(/\s+/g, " "),
)
const campaignMails = inbox.slice(beforeSend).filter((m) => /September update/.test(m.body))
out.campaignDelivered = campaignMails.map((m) => m.rcpt.join(" "))
const unsub = campaignMails[0].body
  .replace(/=\r?\n/g, "")
  .match(/mail\/unsubscribe\?token=([0-9a-f]{64})/)[1]
const p3 = await ctxA.newPage()
await p3.goto(`${base}/ar/mail/unsubscribe?token=${unsub}`, { waitUntil: "networkidle" })
out.unsubscribePageDir = await p3.evaluate(() => document.documentElement.dir)
await p3.getByRole("button", { name: "إلغاء الاشتراك" }).click()
out.unsubscribeResult = await (
  await p3.waitForSelector("[data-mail-result]")
).getAttribute("data-mail-result")
await p3.close()
await a.goto(`${base}/en/w/${A.slug}/contacts`, { waitUntil: "networkidle" })
await a.click('[data-open="lists"]')
await a.waitForSelector("[data-list-row]")
out.listsAfter = await a.$$eval("[data-list-row]", (r) =>
  r.map((x) => x.innerText.replace(/\s+/g, " ").trim()),
)
await a.screenshot({ path: "E:/tmp/mshots/mk-contacts-en.png", fullPage: true })
await a.goto(`${base}/ar/w/${A.slug}/campaigns`, { waitUntil: "networkidle" })
await a.screenshot({ path: "E:/tmp/mshots/mk-campaigns-ar.png", fullPage: true })
const wsA = await a.evaluate(
  async (slug) =>
    (await (await fetch("/api/me/workspaces")).json()).workspaces.find((w) => w.slug === slug).id,
  A.slug,
)
out.workspaceAId = wsA

// ---- workspace B: must see none of A ----
const ctxB = await launch()
const b = ctxB.pages()[0]
const B = await signUp(b, "mkb")
out.workspaceB = B.slug
await b.goto(`${base}/en/w/${B.slug}/contacts`, { waitUntil: "networkidle" })
await b.click('[data-open="lists"]')
await b.waitForSelector("text=No lists yet")
out.bContactsRows = await b.locator("[data-contact-row]").count()
out.bListRows = await b.locator("[data-list-row]").count()
out.bApi = await b.evaluate(async (wsA) => {
  const r1 = await fetch("/api/w/contacts", { headers: { "X-Workspace-ID": wsA } })
  const r2 = await fetch("/api/w/campaigns", { headers: { "X-Workspace-ID": wsA } })
  return { contactsWithA: r1.status, campaignsWithA: r2.status }
}, wsA)
await ctxA.close()
await ctxB.close()
sink.close()
console.log(JSON.stringify(out, null, 1))
