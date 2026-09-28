// MH-661: pick an email template when replying over email. Full CRUD for
// email templates through the settings page, the reply-box picker filling
// both subject and body from a real conversation's contact, an unknown
// {{...}} placeholder staying verbatim, and the branded HTML shell actually
// going out on Send — caught by a local SMTP sink, never a real mailbox.
// Then the templates page is looked at in en/ar x light/dark x 1440/390.
//
// WRITES: it signs up a throwaway account, creates its own SMTP service and
// mailbox pointed at a local sink, and seeds one email conversation directly
// in the database (there is no "create a conversation" REST endpoint — every
// real one arrives inbound) — for a LOCAL server only, never production.
//   node e2e/mh661-email-templates.mjs http://localhost:3142 [shots-dir] [databaseUrl]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import net from "node:net"
import { execFileSync } from "node:child_process"

const base = process.argv[2] ?? "http://localhost:3142"
const shots = process.argv[3]
const dbUrl = process.argv[4] ?? process.env.DATABASE_URL
if (/moharrik\.com/.test(base)) throw new Error("mh661-email-templates writes: run it against a local server")
if (!dbUrl) throw new Error("need a database URL: pass it as the 3rd argument or set DATABASE_URL")

const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

function sql(query, params = []) {
  const escaped = params.reduce((q, p, i) => q.replaceAll(`$${i + 1}`, `'${String(p).replace(/'/g, "''")}'`), query)
  const res = execFileSync("psql", [dbUrl, "-t", "-A", "-c", escaped], { encoding: "utf8" }).trim()
  // -t -A still prints the "INSERT 0 1" command tag after a RETURNING value on
  // this psql build; the first non-empty line is always the real result.
  return res.split(/\r?\n/).find((l) => l.trim() !== "") ?? ""
}

// ---- a local SMTP sink: nothing here reaches a real mailbox ----
const SINK_PORT = 2716
const sinkLog = []
const sinkServer = net
  .createServer((sock) => {
    let buf = "",
      data = null,
      m = {}
    const say = (l) => sock.write(l + "\r\n")
    say("220 mh661-sink")
    sock.on("data", (chunk) => {
      buf += chunk.toString("utf8")
      let i
      while ((i = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, i).replace(/\r$/, "")
        buf = buf.slice(i + 1)
        if (data !== null) {
          if (line === ".") {
            m.data = data
            sinkLog.push(m)
            data = null
            m = {}
            say("250 queued")
          } else data += line + "\n"
          continue
        }
        const cmd = line.toUpperCase()
        if (cmd.startsWith("MAIL FROM:")) {
          m.from = line.slice(10).replace(/[<> ]/g, "")
          say("250 ok")
        } else if (cmd.startsWith("RCPT TO:")) {
          m.to = line.slice(8).replace(/[<> ]/g, "")
          say("250 ok")
        } else if (cmd === "DATA") {
          data = ""
          say("354 go")
        } else if (cmd === "QUIT") {
          say("221 bye")
          sock.end()
        } else say("250 ok")
      }
    })
    sock.on("error", () => {})
  })
  .listen(SINK_PORT, "127.0.0.1")

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh661-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))

// ---- a throwaway account and workspace ----
const email = `mh661${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH661 " + Date.now())
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
        body: json !== undefined ? JSON.stringify(json) : undefined,
      })
      return { status: r.status, body: await r.json().catch(() => null) }
    },
    { method, p, json, slug },
  )
}

const wsID = sql("SELECT id FROM workspaces WHERE slug = $1", [slug])
check("workspace-row", !!wsID, wsID)

// ---- an SMTP service pointed at the local sink, and a mailbox using it ----
const smtp = await call("POST", "/api/w/smtp-services", {
  name: "MH661 sink",
  host: "127.0.0.1",
  port: SINK_PORT,
  from_name: "MH661",
  from_email: "support@mh661.test",
  tls_mode: "none",
})
check("smtp-service", smtp.status === 201, JSON.stringify(smtp))
const smtpID = smtp.body?.service?.id

const mailbox = await call("POST", "/api/w/mailboxes", {
  name: "MH661 mailbox",
  host: "imap.mh661.test",
  port: 993,
  username: "support@mh661.test",
  password: "x",
  tls_mode: "tls",
  smtp_service_id: smtpID,
  is_primary: true,
})
check("mailbox", mailbox.status === 201, JSON.stringify(mailbox))
const mailboxID = mailbox.body?.mailbox?.id

// ---- seed one email conversation directly: no REST endpoint creates one, every
// real one arrives inbound. Contact is @mail.example per the task's own rule
// for a seeded, never-real address. ----
const convID = sql(
  `INSERT INTO conversations (workspace_id, channel, mailbox_id, customer_name, customer_email, subject)
   VALUES ($1, 'email', $2, $3, $4, $5) RETURNING id`,
  [wsID, mailboxID, "Jordan Visitor", "jordan@mail.example", "Question about pricing"],
)
check("conversation-row", !!convID, convID)
sql(
  `INSERT INTO messages (workspace_id, conversation_id, direction, mailbox_id, subject, body)
   VALUES ($1, $2, 'in', $3, $4, $5)`,
  [wsID, convID, mailboxID, "Question about pricing", "Hi, could you tell me more about your plans?"],
)

// ---- email templates settings page: add, with a deliberate unknown placeholder ----
await page.goto(`${base}/en/w/${slug}/inbox/email-templates`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-action="add-email-template"]', { timeout: 15000 })
await page.click('[data-action="add-email-template"]')
const form = page.locator('[data-form="email-template"]')
await form.waitFor({ state: "visible" })
await form.locator("#email-template-name").fill("Pricing info")
await form.locator("#email-template-subject-en").fill("Re: pricing for {{workspace.name}}")
await form.locator("#email-template-body-en").fill(
  "Hi {{contact.first_name}},\n\nThanks for reaching out to {{workspace.name}}. " +
    "I'm {{agent.name}} and I'll help with {{deal.unknown}}.\n\nBest,\n{{agent.name}}",
)
await page.click('[data-form="email-template"] button[type=submit]')
await page.waitForSelector('[data-form="email-template"]', { state: "hidden", timeout: 10000 })
await page.waitForTimeout(500)
check(
  "template-listed",
  (await page.locator("[data-email-template-row]").innerText().catch(() => "")).includes("Pricing info"),
)

// ---- edit it ----
await page.click('[data-email-template-row="Pricing info"] [data-action="edit-email-template"]')
await form.waitFor({ state: "visible" })
await form.locator("#email-template-subject-ar").fill("رد: أسعار {{workspace.name}}")
await page.click('[data-form="email-template"] button[type=submit]')
await page.waitForSelector('[data-form="email-template"]', { state: "hidden", timeout: 10000 })
await page.waitForTimeout(500)
check(
  "template-edited",
  (await page.locator('[data-email-template-row="Pricing info"]').innerText()).includes("أسعار"),
)

// ---- preview with sample data: the unknown placeholder survives verbatim ----
await page.click('[data-email-template-row="Pricing info"] [data-action="edit-email-template"]')
await form.waitFor({ state: "visible" })
await page.click('[data-action="preview-email-template"]')
await page.waitForSelector("[data-email-template-preview]", { timeout: 10000 })
const previewText = await page.locator("[data-email-template-preview]").innerText()
check("preview-unknown-placeholder-verbatim", previewText.includes("{{deal.unknown}}"), previewText)
check("preview-sample-contact", previewText.includes("Jamie Sample") || previewText.toLowerCase().includes("jamie"), previewText)
await page.keyboard.press("Escape")
await page.waitForTimeout(300)

// ---- the reply box: pick the template on the real conversation ----
await page.goto(`${base}/en/w/${slug}/inbox?c=${convID}`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="reply"]', { timeout: 15000 })
check("subject-field-shown", await page.locator("[data-reply-subject]").isVisible())
await page.click('[data-action="email-templates"]')
await page.waitForSelector('[data-email-template="Pricing info"]', { timeout: 10000 })
await page.click('[data-email-template="Pricing info"]')
await page.waitForTimeout(700)
const subjectVal = await page.locator("[data-reply-subject]").inputValue()
const bodyVal = await page.locator("#reply-body").inputValue()
check("subject-has-workspace-name", subjectVal.includes("MH661"), subjectVal)
check("body-has-contact-first-name", bodyVal.includes("Jordan"), bodyVal)
check("body-has-agent-email", bodyVal.includes(email), bodyVal)
check("body-unknown-placeholder-verbatim", bodyVal.includes("{{deal.unknown}}"), bodyVal)

// ---- send it, and check the branded HTML actually going out ----
await page.click('[data-form="reply"] [data-action="send"]')
await page.waitForFunction(() => document.querySelector("#reply-body")?.value === "", null, { timeout: 15000 })
await page.waitForTimeout(1200)
check("sink-received", sinkLog.length === 1, sinkLog.length)
const raw = sinkLog[0]?.data ?? ""
check("sink-to-contact", (sinkLog[0]?.to ?? "").includes("jordan"), sinkLog[0]?.to)
check("branded-doctype", /<!doctype html>/i.test(raw))
check("branded-workspace-footer", raw.includes("MH661"), raw.slice(0, 4000))
check("branded-subject-line", /Subject:.*pricing/i.test(raw), raw.slice(0, 800))

// ---- delete it: settings page ----
await page.goto(`${base}/en/w/${slug}/inbox/email-templates`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-email-template-row="Pricing info"]', { timeout: 15000 })
page.once("dialog", (d) => d.accept())
await page.locator('[data-email-template-row="Pricing info"]').getByText("Delete", { exact: true }).click()
await page.locator('[data-confirm-dialog] [data-confirm], [role="alertdialog"] button:has-text("Delete")').first().click()
await page.waitForTimeout(600)
check("template-deleted", !(await page.locator('[data-email-template-row="Pricing info"]').count()))

// ---- a second template for the screenshot sweep (arabic-only, so the list is non-empty) ----
await page.click('[data-action="add-email-template"]')
await form.waitFor({ state: "visible" })
await form.locator("#email-template-name").fill("رد سريع")
await form.locator("#email-template-subject-ar").fill("أهلاً {{contact.name}}")
await form.locator("#email-template-body-ar").fill("مرحبًا {{contact.first_name}}، شكرًا لتواصلك مع {{workspace.name}}.")
await page.click('[data-form="email-template"] button[type=submit]')
await page.waitForSelector('[data-form="email-template"]', { state: "hidden", timeout: 10000 })
await page.waitForTimeout(500)

// ---- en/ar x light/dark x 1440/390 ----
for (const scheme of ["light", "dark"])
  for (const loc of ["en", "ar"])
    for (const wd of [1440, 390]) {
      const k = `${loc}-${scheme}-${wd}`
      await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: wd, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox/email-templates`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector("[data-email-template-row]", { timeout: 15000 })
      await page.waitForTimeout(600)
      const m = await page.evaluate(() => {
        const dark = document.documentElement.classList.contains("dark") || document.documentElement.dataset.theme === "dark"
        const overflow = document.documentElement.scrollWidth > window.innerWidth + 1
        return { dark, rtl: document.documentElement.dir === "rtl", overflow }
      })
      check(`${k}-theme`, m.dark === (scheme === "dark"), m.dark)
      check(`${k}-dir`, m.rtl === (loc === "ar"), m.rtl)
      check(`${k}-no-overflow`, !m.overflow)
      if (loc === "ar") {
        const txt = await page.locator("[data-email-template-row]").first().innerText()
        check(`${k}-arabic-text`, /[\u0600-\u06FF]/.test(txt), txt)
      }
      await page.waitForTimeout(300)
      if (shots) await page.screenshot({ path: path.join(shots, `mh661-${k}.png`) })
    }

check("no-page-errors", errs.length === 0, errs)
console.log(JSON.stringify({ ok: fail.length === 0, fail, out }, null, 1))
await ctx.close()
sinkServer.close()
process.exit(fail.length ? 1 : 0)
