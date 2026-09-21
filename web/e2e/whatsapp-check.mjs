// The inbox's WhatsApp channel, end to end against a STUB Evolution server
// (MH-134).
//
// A real link needs a human with a phone scanning a QR code, which no check can
// assert. Everything either side of that is exercised here against a stub that
// speaks Evolution's API: create the instance, register the webhook, return a
// QR, accept a sendText. The stub records what it was asked for, so the reply
// assertion is "WhatsApp received this text for this number", not "our own row
// says we sent it".
//
// Run the API with EVOLUTION_API_URL pointing at this stub:
//   EVOLUTION_API_URL=http://127.0.0.1:8787 EVOLUTION_API_KEY=stub-key \
//   WHATSAPP_HOOK_BASE=http://localhost:3122 ... .setup/dev.ps1 -Restart
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"
const STUB_PORT = Number(process.argv[3] ?? 8787)

// A 1x1 PNG: the page only has to render whatever the server calls a QR.
const FAKE_QR = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

const stub = {
  created: [],
  webhooks: [],
  sent: [],
  state: "connecting",
}

const srv = createServer((req, res) => {
  let body = ""
  req.on("data", (c) => (body += c))
  req.on("end", () => {
    const json = () => {
      try {
        return JSON.parse(body || "{}")
      } catch {
        return {}
      }
    }
    const reply = (code, obj) => {
      res.writeHead(code, { "Content-Type": "application/json" })
      res.end(JSON.stringify(obj))
    }
    // Every Evolution call carries the global key; refusing without it is what
    // the real server does, and asserting it keeps the client honest.
    if (req.headers["apikey"] !== "stub-key") return reply(401, { message: "bad apikey" })

    if (req.url === "/instance/create") {
      stub.created.push(json().instanceName)
      return reply(201, { qrcode: { base64: FAKE_QR, code: "2@abc" } })
    }
    if (req.url.startsWith("/webhook/set/")) {
      stub.webhooks.push({ instance: req.url.split("/").pop(), ...json().webhook })
      return reply(200, { webhook: { enabled: true } })
    }
    if (req.url.startsWith("/instance/connectionState/")) {
      return reply(200, { instance: { state: stub.state } })
    }
    if (req.url.startsWith("/instance/connect/")) {
      return reply(200, { base64: FAKE_QR, code: "2@abc" })
    }
    if (req.url.startsWith("/message/sendText/")) {
      const b = json()
      stub.sent.push({ instance: req.url.split("/").pop(), number: b.number, text: b.text })
      return reply(201, { key: { id: "WAMID" + stub.sent.length } })
    }
    if (req.url.startsWith("/instance/delete/")) {
      stub.deleted = req.url.split("/").pop()
      return reply(200, { status: "SUCCESS" })
    }
    return reply(404, { message: "not stubbed: " + req.url })
  })
})
await new Promise((r) => srv.listen(STUB_PORT, "127.0.0.1", r))

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "wa-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `wa${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
const { slug } = await signUpAndEnterWorkspace(page, "WA " + Date.now())
out.slug = slug

// ---- the page, before anything is linked ----
await page.goto(`${base}/en/w/${slug}/inbox/whatsapp`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-whatsapp-state]", { timeout: 20000 })
out.startsUnlinked = await page.evaluate(() => document.querySelector("[data-whatsapp-state]")?.getAttribute("data-whatsapp-state") === "close")

// ---- link: the instance is created, the webhook registered, a QR shown ----
await page.click('button:has-text("Link a number")')
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
out.qrShown = true
out.instanceCreated = stub.created.length === 1
out.instanceNamePerWorkspace = /^moh-[0-9a-f]{32}$/.test(stub.created[0] ?? "")
const hook = stub.webhooks[0] ?? {}
out.webhookRegistered = !!hook.url && hook.url.includes("/api/whatsapp/hook/")
out.webhookHasToken = !!(hook.headers && hook.headers["X-Moharrik-Token"])
out.webhookEventsNarrow = JSON.stringify(hook.events ?? []) === JSON.stringify(["MESSAGES_UPSERT", "CONNECTION_UPDATE"])

const hookURL = hook.url.replace(/^https?:\/\/[^/]+/, base)
const token = hook.headers["X-Moharrik-Token"]

// ---- the webhook refuses everything but the right token ----
const post = (payload, headers = {}) =>
  fetch(hookURL, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(payload) })

const msg = (over = {}) => ({
  event: "messages.upsert",
  data: {
    key: { remoteJid: "201234567890@s.whatsapp.net", fromMe: false, id: "WA" + Date.now(), ...(over.key ?? {}) },
    pushName: "Dana",
    message: { conversation: over.text ?? "my order is late" },
  },
})

out.noTokenStatus = (await post(msg())).status
out.wrongTokenStatus = (await post(msg(), { "X-Moharrik-Token": "nope" })).status
out.unknownHookStatus = (await post(msg(), { "X-Moharrik-Token": token })).status === 404 ? 404 : (await fetch(`${base}/api/whatsapp/hook/wh_nothing`, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Moharrik-Token": token },
  body: "{}",
})).status

// ---- a customer message arrives ----
const firstID = "WA-first-" + Date.now()
out.deliveredStatus = (await post({ ...msg(), data: { ...msg().data, key: { remoteJid: "201234567890@s.whatsapp.net", fromMe: false, id: firstID } } }, { "X-Moharrik-Token": token })).status

await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 20000 })
out.inInbox = await page.evaluate(() => document.body.innerText.includes("Dana"))
out.showsWhatsAppChannel = await page.evaluate(() => /whatsapp/i.test(document.querySelector("[data-conversation]")?.innerText ?? ""))
await page.click("[data-conversation]")
await page.waitForSelector("[data-messages]", { timeout: 15000 })
out.bodyShown = await page.evaluate(() => document.body.innerText.includes("my order is late"))

// ---- a redelivery of the same id must not duplicate ----
await post({ ...msg(), data: { ...msg().data, key: { remoteJid: "201234567890@s.whatsapp.net", fromMe: false, id: firstID } } }, { "X-Moharrik-Token": token })
// ---- our own echo must not look like the customer ----
await post({ ...msg(), data: { ...msg().data, key: { remoteJid: "201234567890@s.whatsapp.net", fromMe: true, id: "echo-1" }, message: { conversation: "this is us talking" } } }, { "X-Moharrik-Token": token })
// ---- a group message must not open a conversation ----
await post({ ...msg(), data: { ...msg().data, key: { remoteJid: "999-888@g.us", fromMe: false, id: "grp-1" }, message: { conversation: "group noise" } } }, { "X-Moharrik-Token": token })

await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 15000 })
out.conversationCount = await page.evaluate(() => document.querySelectorAll("[data-conversation]").length)
await page.click("[data-conversation]")
await page.waitForSelector("[data-messages]", { timeout: 15000 })
out.messagesInThread = await page.evaluate(() => document.querySelectorAll("[data-message]").length)
out.echoNotFiled = await page.evaluate(() => !document.body.innerText.includes("this is us talking"))
out.groupNotFiled = await page.evaluate(() => !document.body.innerText.includes("group noise"))

// ---- the team replies, and WhatsApp receives it ----
await page.fill("#reply-body", "Sorry — it ships today.")
await page.click('[data-form="reply"] button[type=submit]')
await page.waitForFunction(() => document.querySelectorAll('[data-message="out"]').length > 0, { timeout: 20000 })
  .then(() => (out.replySaved = true))
  .catch(() => (out.replySaved = false))
const sent = stub.sent.at(-1) ?? {}
out.whatsappReceivedTheReply = sent.text === "Sorry — it ships today."
out.sentToTheRightNumber = sent.number === "201234567890"

// ---- connection.update moves the state without anyone asking ----
stub.state = "open"
await post({ event: "connection.update", data: { state: "open", wuid: "201111111111@s.whatsapp.net" } }, { "X-Moharrik-Token": token })
await page.goto(`${base}/en/w/${slug}/inbox/whatsapp`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-whatsapp-state]", { timeout: 15000 })
out.stateOpenAfterUpdate = await page.evaluate(() => document.querySelector("[data-whatsapp-state]")?.getAttribute("data-whatsapp-state") === "open")
out.showsLinkedNumber = await page.evaluate(() => document.body.innerText.includes("201111111111"))

// ---- unlink ends the session on the server, not just here ----
await page.click('button:has-text("Unlink")')
await page.waitForSelector('[role="alertdialog"] button:has-text("Unlink")', { timeout: 10000 })
await page.click('[role="alertdialog"] button:has-text("Unlink")')
await page.waitForFunction(() => document.querySelector("[data-whatsapp-state]")?.getAttribute("data-whatsapp-state") === "close", { timeout: 15000 })
  .then(() => (out.unlinked = true))
  .catch(() => (out.unlinked = false))
out.instanceDeletedOnServer = stub.deleted === stub.created[0]

await ctx.close()

// ---- Arabic at 390px ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "WA AR " + Date.now())
await pageAr.goto(`${base}/ar/w/${slugAr}/inbox/whatsapp`, { waitUntil: "domcontentloaded" })
await pageAr.waitForTimeout(2000)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_rawKeys = await pageAr.evaluate(() => /whatsapp\.[a-zA-Z]/.test(document.body.innerText))
await ctxAr.close()

srv.close()
out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
