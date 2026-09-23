// Campaign edit, preview and test send (MH-407), signed in, through the real web + API:
//   node e2e/campaign-edit-check.mjs [base] [shots-dir]
// Runs a throwaway SMTP sink on 127.0.0.1:2526 and prints what it received, so the
// test email is really delivered through the workspace's own SMTP settings.
import { chromium } from "playwright-core"
import { mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import net from "node:net"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3407"
const shots = process.argv[3] ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
mkdirSync(shots, { recursive: true })
const SINK_PORT = 2526

const inbox = []
const sink = net.createServer((sock) => {
  let data = false, buf = "", msg = { rcpt: [], body: "" }
  sock.write("220 sink ESMTP\r\n")
  sock.on("data", (chunk) => {
    buf += chunk.toString("utf8")
    let i
    while ((i = buf.indexOf("\r\n")) >= 0) {
      const line = buf.slice(0, i); buf = buf.slice(i + 2)
      if (data) {
        if (line === ".") { data = false; inbox.push(msg); msg = { rcpt: [], body: "" }; sock.write("250 queued\r\n") }
        else msg.body += line + "\n"
        continue
      }
      const cmd = line.slice(0, 4).toUpperCase()
      if (cmd === "EHLO" || cmd === "HELO") sock.write("250 sink\r\n")
      else if (cmd === "RCPT") { msg.rcpt.push(line.replace(/^RCPT TO:\s*/i, "")); sock.write("250 ok\r\n") }
      else if (cmd === "DATA") { data = true; sock.write("354 go\r\n") }
      else if (cmd === "QUIT") { sock.write("221 bye\r\n"); sock.end() }
      else sock.write("250 ok\r\n")
    }
  })
})
await new Promise((r) => sink.listen(SINK_PORT, "127.0.0.1", r))

const launch = (opts = {}) => chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh407-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, viewport: { width: 1440, height: 900 }, ...opts,
})
const waitFor = async (fn, ms = 20000) => { const end = Date.now() + ms; while (Date.now() < end) { const v = await fn(); if (v) return v; await new Promise((r) => setTimeout(r, 250)) } throw new Error("timeout") }
const out = {}

// ---- sign up, workspace ----
const ctx = await launch(); const page = ctx.pages()[0]
const email = `mh407-${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email); await page.fill("#password", "Passw0rd!long-enough")
const nameField = page.locator("#name"); if (await nameField.count()) await nameField.fill("Owner")
await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) { await page.fill("#ws-name", `MH407 ${Date.now()}`); await page.click("button[type=submit]"); await page.waitForURL(/\/w\//, { timeout: 30000 }) }
const slug = page.url().split("/w/")[1].split("/")[0]
out.workspace = slug; out.member = email

// The API from inside the page, as the signed-in member.
const call = (method, url, body) => page.evaluate(async ({ method, url, body, slug }) => {
  const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
  const r = await fetch(url, { method, headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf, "X-Workspace-ID": slug }, body: body ? JSON.stringify(body) : undefined })
  let json = null; try { json = await r.json() } catch {}
  return { status: r.status, json }
}, { method, url, body, slug })

// A list without double opt-in, 40 subscribers (so a send stays "sending" for a few seconds),
// and SMTP pointing at the sink.
const list = (await call("POST", "/api/w/mail/lists", { name: "Newsletter", double_opt_in: false })).json.list
for (let i = 0; i < 40; i++) await call("POST", "/api/w/contacts", { email: `reader${i}@example.test`, list_id: list.id })
out.smtp = (await call("PUT", "/api/w/mail/smtp", { host: "127.0.0.1", port: SINK_PORT, from_name: "Acme", from_email: "news@acme.test", tls_mode: "none" })).status

// ---- draft with a typo, through the form ----
await page.goto(`${base}/en/w/${slug}/campaigns`, { waitUntil: "networkidle" })
await page.fill("#campaign-subject", "Septmber update")
await page.fill("#campaign-body", "# Hello\n\nWe shipped editing.\n\n- preview\n- test send")
await page.click('[data-form="new-campaign"] button[type=submit]')
await page.waitForSelector('[data-campaign-row="Septmber update"]')

// ---- edit: fix the typo, bold a word with the toolbar ----
await page.click('[data-edit-campaign="Septmber update"]')
await page.waitForSelector("[data-edit-sheet] [data-form=edit-campaign]")
await page.fill("#edit-subject", "September update")
await page.evaluate(() => { const ta = document.querySelector("#edit-body"); const s = ta.value.indexOf("editing"); ta.focus(); ta.setSelectionRange(s, s + "editing".length) })
await page.click('[data-edit-sheet] [data-md-action="bold"]')
out.bodyAfterBold = await page.inputValue("#edit-body")
await page.screenshot({ path: `${shots}/mh407-edit-write-en-light-1440.png` })
await page.click('[data-edit-sheet] button[type=submit]')
await page.waitForSelector('[data-campaign-row="September update"]')
const listed = (await call("GET", "/api/w/campaigns")).json.campaigns.find((c) => c.subject === "September update")
out.savedEdit = { subject: listed.subject, body: listed.body, status: listed.status }

// ---- preview: en, then ar through the switch, then back ----
await page.click('[data-edit-sheet] [data-tab="preview"]')
await waitFor(() => page.$('[data-campaign-preview="email:en"]'))
const srcdoc = () => page.$eval("[data-email-preview]", (f) => f.getAttribute("srcdoc"))
const en = await srcdoc()
out.previewEn = { dir: /<html dir="ltr" lang="en">/.test(en), bold: /<strong[^>]*>editing<\/strong>/.test(en), unsubscribe: />Unsubscribe<\/a>/.test(en), sandbox: await page.$eval("[data-email-preview]", (f) => f.getAttribute("sandbox")) }
await page.screenshot({ path: `${shots}/mh407-edit-preview-en-light-1440.png` })
await page.click('[data-preview-lang="ar"]')
await waitFor(() => page.$('[data-campaign-preview="email:ar"]'))
const ar = await srcdoc()
out.previewAr = { dir: /<html dir="rtl" lang="ar">/.test(ar), footer: /إلغاء الاشتراك/.test(ar), unsavedShown: (await page.locator("[data-unsaved]").count()) === 1 }
await page.screenshot({ path: `${shots}/mh407-edit-preview-ar-switch-light-1440.png` })
await page.click('[data-preview-lang="en"]')
await waitFor(() => page.$('[data-campaign-preview="email:en"]'))

// ---- test send: prefilled with the member's address ----
const before = (await call("GET", "/api/w/campaigns")).json.campaigns.find((c) => c.subject === "September update")
await page.click('[data-edit-sheet] [data-tab="test"]')
out.testPrefill = await page.inputValue("#campaign-test-to")
const n0 = inbox.length
await page.click('[data-send-test]')
const testMail = await waitFor(() => inbox.slice(n0).find((m) => /Subject: \[TEST\] September update/.test(m.body)))
await page.waitForSelector("[data-test-result]")
await page.screenshot({ path: `${shots}/mh407-edit-test-en-light-1440.png` })
const after = (await call("GET", "/api/w/campaigns")).json.campaigns.find((c) => c.subject === "September update")
out.testMail = { to: testMail.rcpt, subject: testMail.body.match(/^Subject: .*$/m)[0], heading: /September update<\/h1>/.test(testMail.body), unsubPlaceholder: /token=preview/.test(testMail.body) }
out.testChangedNothing = JSON.stringify([before.status, before.sent_count, before.recipient_count, before.failed_count, before.sent_at]) === JSON.stringify([after.status, after.sent_count, after.recipient_count, after.failed_count, after.sent_at])
out.statusAfterTest = after.status
await page.keyboard.press("Escape")

// ---- an Arabic draft and a WhatsApp draft, for the other screenshots ----
await call("POST", "/api/w/campaigns", { list_id: list.id, subject: "تحديث سبتمبر", body: "# مرحبًا\n\nأطلقنا **التحرير** والمعاينة.\n\n- معاينة\n- رسالة تجربة", locale: "ar" })
await call("POST", "/api/w/campaigns", { list_id: list.id, channel: "whatsapp", subject: "Promo", body: "Sale today: 20% off everything.", locale: "en" })

// ---- screenshots: en/ar × light/dark × 1440/390, the edit sheet on its preview tab ----
for (const locale of ["en", "ar"]) for (const theme of ["light", "dark"]) for (const width of [1440, 390]) {
  const c2 = await launch({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme: theme })
  await c2.addCookies((await ctx.cookies()).map((c) => ({ ...c })))
  const p = c2.pages()[0]
  await p.goto(`${base}/${locale}/w/${slug}/campaigns`, { waitUntil: "networkidle" })
  await p.screenshot({ path: `${shots}/mh407-list-${locale}-${theme}-${width}.png`, fullPage: true })
  const subject = locale === "ar" ? "تحديث سبتمبر" : "September update"
  await p.click(`[data-edit-campaign="${subject}"]`)
  await p.waitForSelector("[data-edit-sheet] [data-form=edit-campaign]")
  await p.click('[data-edit-sheet] [data-tab="preview"]')
  await p.waitForSelector(`[data-campaign-preview="email:${locale}"]`)
  await p.waitForTimeout(500)
  await p.screenshot({ path: `${shots}/mh407-preview-${locale}-${theme}-${width}.png` })
  if (width === 1440) {
    out[`sheetSide_${locale}`] = await p.$eval("[data-edit-sheet]", (el) => el.getAttribute("data-side"))
    out[`docDir_${locale}`] = await p.evaluate(() => document.documentElement.dir)
    out[`htmlClass_${locale}_${theme}`] = await p.evaluate(() => document.documentElement.className)
  }
  await p.keyboard.press("Escape")
  // WhatsApp: the bubble with its stop line.
  if (width === 1440) {
    await p.click('[data-edit-campaign="Promo"]')
    await p.click('[data-edit-sheet] [data-tab="preview"]')
    await p.waitForSelector("[data-whatsapp-bubble]")
    out[`whatsappBubble_${locale}_${theme}`] = await p.$eval("[data-whatsapp-bubble]", (e) => e.innerText)
    await p.screenshot({ path: `${shots}/mh407-whatsapp-preview-${locale}-${theme}-1440.png` })
    out[`whatsappTestTab_${locale}`] = await p.locator('[data-edit-sheet] [data-tab="test"]').count()
    await p.click('[data-edit-sheet] [data-tab="test"]')
    out[`whatsappTestBlocked_${locale}`] = await p.locator("[data-edit-sheet] [data-test-blocked=whatsapp]").count(); out[`whatsappSendTestButton_${locale}`] = await p.locator("[data-send-test]").count()
    await p.keyboard.press("Escape")
  }
  await c2.close()
}

// ---- send it, then: no Edit while sending, and the API refuses the edit with 409 ----
await page.goto(`${base}/en/w/${slug}/campaigns`, { waitUntil: "networkidle" })
await page.click('[data-campaign-row="September update"] >> text=Send')
await page.getByRole("alertdialog").getByRole("button", { name: "Send" }).click()
await waitFor(async () => /Sending/.test(await page.$eval('[data-campaign-row="September update"]', (r) => r.innerText)), 10000)
out.editButtonWhileSending = await page.locator('[data-edit-campaign="September update"]').count()
await page.screenshot({ path: `${shots}/mh407-sending-no-edit-en-light-1440.png`, fullPage: true })
const id = listed.id
out.editWhileSending = await call("PUT", `/api/w/campaigns/${id}`, { list_id: list.id, subject: "Too late", body: "x" })
out.testWhileSending = (await call("POST", `/api/w/campaigns/${id}/test`, {})).status
await waitFor(async () => /Sent/.test(await page.$eval('[data-campaign-row="September update"]', (r) => r.innerText)), 30000)
out.editAfterSent = await call("PUT", `/api/w/campaigns/${id}`, { list_id: list.id, subject: "Too late", body: "x" })
out.delivered = inbox.filter((m) => /Subject: September update/.test(m.body)).length

// ---- MCP: update_campaign + preview_campaign over real JSON-RPC ----
const token = (await call("POST", "/api/w/mcp_tokens", { name: "mh407", scopes: ["campaigns:read", "campaigns:write"] })).json.plaintext
const rpc = async (method, params) => {
  const r = await fetch(`${base}/api/mcp`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }) })
  return (await r.json())
}
const tool = async (name, args) => { const j = await rpc("tools/call", { name, arguments: args }); return j.error ? { error: j.error.message } : { text: j.result.content?.[0]?.text ?? "", isError: !!j.result.isError } }
out.mcpTools = (await rpc("tools/list", {})).result.tools.map((t) => t.name).filter((n) => /campaign/.test(n))
out.mcpUpdateDraft = await tool("update_campaign", { subject: "Promo", new_subject: "Promo (edited)", body: "Sale today: 25% off." })
out.mcpPreviewWhatsApp = await tool("preview_campaign", { subject: "Promo (edited)" })
out.mcpPreviewArabic = (await tool("preview_campaign", { subject: "تحديث سبتمبر" })).text.slice(0, 160)
out.mcpUpdateSent = await tool("update_campaign", { subject: "September update", body: "too late" })

// The logged mail: what the sink received for the test.
out.sinkLog = inbox.map((m) => ({ to: m.rcpt.join(","), subject: (m.body.match(/^Subject: .*$/m) || [""])[0] })).slice(0, 3)
console.log("---- test email as received by the SMTP sink ----\n" + testMail.body.split("\n").slice(0, 14).join("\n") + "\n…")
await ctx.close(); sink.close()
console.log(JSON.stringify(out, null, 1))
