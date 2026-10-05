// MH-659: the inbox composer sends more than text — files, photos/videos, a
// voice note, location and emoji — through a stub Evolution server for
// WhatsApp and a local SMTP sink for email. Never touches a real Evolution
// server, never sends a real email, never dials a real number.
//
// Run the API BEFORE this script, with the Evolution URL/key pointed at this
// script's own stub (LoadConfig() in internal/whatsapp/evolution.go reads
// env once at boot):
//   ADDR=:8144 EVOLUTION_API_URL=http://127.0.0.1:8917 EVOLUTION_API_KEY=stub-key-659 \
//   WHATSAPP_HOOK_BASE=http://localhost:8144 go run ./cmd/api
// and the web dev server on :3144 (next dev --webpack -p 3144).
//
//   node web/e2e/mh659-composer.mjs http://localhost:3144 [shots-dir] [databaseUrl]
import { chromium } from "playwright-core"
import { mkdtempSync, mkdirSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import net from "node:net"
import { createServer } from "node:http"
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const base = process.argv[2] ?? "http://localhost:3144"
const shots = process.argv[3] ?? path.join(__dirname, "shots", "mh659")
const dbUrl = process.argv[4] ?? process.env.DATABASE_URL

if (/moharrik\.com/.test(base)) {
  console.error("refusing to run against moharrik.com — this script sends fake WhatsApp/SMTP traffic")
  process.exit(1)
}
if (!dbUrl) throw new Error("need a database URL: pass it as the 3rd argument or set DATABASE_URL")
mkdirSync(shots, { recursive: true })

const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

function sql(query, params = []) {
  const escaped = params.reduce((q, p, i) => q.replaceAll(`$${i + 1}`, `'${String(p).replace(/'/g, "''")}'`), query)
  const res = execFileSync("psql", [dbUrl, "-t", "-A", "-c", escaped], { encoding: "utf8" }).trim()
  return res.split(/\r?\n/).find((l) => l.trim() !== "") ?? ""
}

// ---- fake Evolution server: instance lifecycle + the three MH-659 sends ----
const EVO_PORT = 8917
const EVO_KEY = "stub-key-659"
const evo = { webhooks: [], sendMedia: [], sendWhatsAppAudio: [], sendLocation: [], sendText: [] }
const evoSrv = createServer((req, res) => {
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
    if (req.headers["apikey"] !== EVO_KEY) return reply(401, { message: "bad apikey" })
    if (req.url === "/instance/create") return reply(201, { qrcode: { base64: "data:image/png;base64,x", code: "2@abc" } })
    if (req.url.startsWith("/webhook/set/")) {
      evo.webhooks.push({ instance: req.url.split("/").pop(), ...json().webhook })
      return reply(200, { webhook: { enabled: true } })
    }
    if (req.url.startsWith("/instance/connectionState/")) return reply(200, { instance: { state: "open" } })
    if (req.url.startsWith("/instance/connect/")) return reply(200, { base64: "data:image/png;base64,x", code: "2@abc" })
    if (req.url.startsWith("/instance/delete/")) return reply(200, { status: "SUCCESS" })
    if (req.url.startsWith("/message/sendText/")) {
      evo.sendText.push(json())
      return reply(201, { key: { id: "WAMID" + evo.sendText.length } })
    }
    if (req.url.startsWith("/message/sendMedia/")) {
      evo.sendMedia.push(json())
      return reply(201, { key: { id: "WAMEDIA" + evo.sendMedia.length } })
    }
    if (req.url.startsWith("/message/sendWhatsAppAudio/")) {
      evo.sendWhatsAppAudio.push(json())
      return reply(201, { key: { id: "WAAUDIO" + evo.sendWhatsAppAudio.length } })
    }
    if (req.url.startsWith("/message/sendLocation/")) {
      evo.sendLocation.push(json())
      return reply(201, { key: { id: "WALOC" + evo.sendLocation.length } })
    }
    return reply(404, { message: "not stubbed: " + req.url })
  })
})
await new Promise((r) => evoSrv.listen(EVO_PORT, "127.0.0.1", r))

// ---- local SMTP sink (mh661's pattern): captures full RFC822 bodies ----
const SINK_PORT = 2659
const sinkLog = []
const sinkServer = net
  .createServer((sock) => {
    let buf = "",
      data = null,
      m = {}
    const say = (l) => sock.write(l + "\r\n")
    say("220 mh659-sink")
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

async function newCtx(viewport, colorScheme, useFakeMedia = false) {
  const args = useFakeMedia
    ? ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"]
    : []
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh659-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme,
    args,
  })
}

const ctx = await newCtx({ width: 1440, height: 900 }, "light", true)
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
await ctx.grantPermissions(["microphone"], { origin: base })

// ---- a throwaway account and workspace ----
const email = `mh659${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH659 " + Date.now())
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]
const wsID = sql("SELECT id FROM workspaces WHERE slug = $1", [slug])
check("workspace-row", !!wsID, wsID)

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

// ---- WhatsApp channel: link a number, exactly like MH-658's stub flow ----
await page.goto(`${base}/en/w/${slug}/settings/integrations`, { waitUntil: "domcontentloaded" })
await page.click("[data-add-channel]")
await page.click('[data-add-type="whatsapp"]')
await page.waitForSelector("[data-whatsapp-state]", { timeout: 20000 })
await page.click('button:has-text("Link a number")')
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
const hook = evo.webhooks[0] ?? {}
check("webhook-registered", !!hook.url, hook)
const hookURL = hook.url.replace(/^https?:\/\/[^/]+/, base)
const token = hook.headers?.["X-Moharrik-Token"]
const instance = evo.webhooks[0]?.instance

const postWebhook = (payload) =>
  fetch(hookURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Moharrik-Token": token },
    body: JSON.stringify(payload),
  })

const CUSTOMER = "201234567890@s.whatsapp.net"
const inboundMsg = (id, message) => ({
  event: "messages.upsert",
  data: { key: { remoteJid: CUSTOMER, fromMe: false, id }, pushName: "Dana", message },
})

// ---- seed a WhatsApp conversation by delivering one inbound text ----
const wa1 = await postWebhook(inboundMsg("in-1", { conversation: "Hi there" }))
check("wa-inbound-seeded", wa1.status === 200, wa1.status)
// Once the email conversation below is seeded, the list can sort it ahead of
// this one — "open the first conversation" would silently run every
// WhatsApp-composer assertion against the email thread instead (found by
// direct DB comparison: evo.sendMedia/sendWhatsAppAudio never recorded a
// call even though messages persisted — they were emails, not WhatsApp
// sends). Address this conversation by id from here on, never "first".
const waConvID = sql("SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp'", [wsID])
check("whatsapp-conversation-row", !!waConvID, waConvID)

// ---- an SMTP service + mailbox pointed at the local sink, and a seeded email conversation ----
const smtp = await call("POST", "/api/w/smtp-services", {
  name: "MH659 sink",
  host: "127.0.0.1",
  port: SINK_PORT,
  from_name: "MH659",
  from_email: "support@mh659.test",
  tls_mode: "none",
})
check("smtp-service", smtp.status === 201, JSON.stringify(smtp))
const smtpID = smtp.body?.service?.id
const mailbox = await call("POST", "/api/w/mailboxes", {
  name: "MH659 mailbox",
  host: "imap.mh659.test",
  port: 993,
  username: "support@mh659.test",
  password: "x",
  tls_mode: "tls",
  smtp_service_id: smtpID,
  is_primary: true,
})
check("mailbox", mailbox.status === 201, JSON.stringify(mailbox))
const mailboxID = mailbox.body?.mailbox?.id
const emailConvID = sql(
  `INSERT INTO conversations (workspace_id, channel, mailbox_id, customer_name, customer_email, subject)
   VALUES ($1, 'email', $2, $3, $4, $5) RETURNING id`,
  [wsID, mailboxID, "Jordan Visitor", "jordan@mail.example", "Attachment test"],
)
check("email-conversation-row", !!emailConvID, emailConvID)
sql(
  `INSERT INTO messages (workspace_id, conversation_id, direction, mailbox_id, subject, body)
   VALUES ($1, $2, 'in', $3, $4, $5)`,
  [wsID, emailConvID, mailboxID, "Attachment test", "Can you send me the file?"],
)

// ---- fixture bytes for uploads ----
const PNG_1PX = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
)
const IMG_PATH = path.join(tmpdir(), "mh659-photo.png")
const DOC_PATH = path.join(tmpdir(), "mh659-invoice.pdf")
const BIG_PATH = path.join(tmpdir(), "mh659-toolarge.bin")
await import("node:fs/promises").then(async (fs) => {
  await fs.writeFile(IMG_PATH, PNG_1PX)
  await fs.writeFile(DOC_PATH, "%PDF-1.4 fake invoice")
  await fs.writeFile(BIG_PATH, Buffer.alloc(21 * 1024 * 1024, 1)) // > 20MB
})

// ---- open the WhatsApp conversation, by id (never "first" — see the note
// by waConvID above) ----
await page.goto(`${base}/en/w/${slug}/inbox?c=${waConvID}`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="reply"]', { timeout: 15000 })

// ---- disabled-control guard: a plain text send still works, is not the point here ----
check("attach-button-enabled", await page.locator('[data-action="attach"]').isEnabled())
check("mic-button-enabled", await page.locator('[data-action="mic"]').isEnabled())

// ---- oversized file: rejected client-side with a reason, nothing sent ----
await page.click('[data-action="attach"]')
await page.click('[data-attach="document"]')
await page.setInputFiles('[data-testid="attach-document-input"]', BIG_PATH)
await page.waitForSelector('[data-attach-error]', { timeout: 10000 })
check("oversize-rejected", await page.locator('[data-attach-error]').isVisible())
check("oversize-no-pending", (await page.locator('[data-pending-attachment]').count()) === 0)

// ---- send an image ----
await page.click('[data-action="attach"]')
await page.click('[data-attach="media"]')
await page.setInputFiles('[data-testid="attach-media-input"]', IMG_PATH)
await page.waitForSelector('[data-pending-attachment][data-pending-attachment-kind="image"]', { timeout: 10000 })
await page.click('[data-form="reply"] [data-action="send"]')
await page.waitForFunction(
  (n) => document.querySelectorAll('[data-message="out"] [data-attachment-images], [data-message="out"] img').length >= n,
  1,
  { timeout: 15000 },
).catch(() => {})
await new Promise((r) => setTimeout(r, 500))
check("evo-sendMedia-image-called", evo.sendMedia.some((c) => c.mediatype === "image"), evo.sendMedia)
check("thread-renders-sent-image", (await page.locator('[data-message="out"] img').count()) > 0)

// ---- send a document ----
await page.click('[data-action="attach"]')
await page.click('[data-attach="document"]')
await page.setInputFiles('[data-testid="attach-document-input"]', DOC_PATH)
await page.waitForSelector('[data-pending-attachment][data-pending-attachment-kind="document"]', { timeout: 10000 })
await page.click('[data-form="reply"] [data-action="send"]')
await new Promise((r) => setTimeout(r, 800))
check("evo-sendMedia-document-called", evo.sendMedia.some((c) => c.mediatype === "document"), evo.sendMedia)
check(
  "thread-renders-sent-document",
  (await page.locator('[data-message="out"] a', { hasText: /invoice\.pdf|mh659-invoice/ }).count()) > 0 ||
    (await page.locator("[data-attachment-files] a").count()) > 0,
)

// ---- record and send a voice note (fake device stream) ----
await page.click('[data-action="mic"]')
await page.waitForSelector("[data-recording-bar]", { timeout: 10000 })
await page.waitForTimeout(1200)
await page.click('[data-action="recording-send"]')
await new Promise((r) => setTimeout(r, 1000))
check("evo-sendWhatsAppAudio-called", evo.sendWhatsAppAudio.length > 0, evo.sendWhatsAppAudio.length)
check(
  "thread-renders-sent-voice",
  (await page.locator('[data-message="out"] [data-attachment-voice], [data-message="out"] audio').count()) > 0,
)

// ---- send a location (manual entry) ----
await page.click('[data-action="location"]')
await page.waitForSelector("[data-location-picker]", { timeout: 10000 })
await page.fill("#location-lat", "30.0444")
await page.fill("#location-lng", "31.2357")
await page.fill("#location-name", "HQ")
await page.click('[data-action="location-use"]')
await new Promise((r) => setTimeout(r, 1000))
check("evo-sendLocation-called", evo.sendLocation.length > 0, evo.sendLocation)
check(
  "sendLocation-payload-correct",
  evo.sendLocation.some((c) => Math.abs(c.latitude - 30.0444) < 1e-6 && Math.abs(c.longitude - 31.2357) < 1e-6),
  evo.sendLocation,
)
check(
  "thread-renders-sent-location",
  (await page.locator('[data-message="out"] [data-attachment-location]').count()) > 0,
)

await page.screenshot({ path: path.join(shots, "en-light-1440-whatsapp.png") })

// ---- switch to the email conversation: emoji + a file attachment together ----
await page.goto(`${base}/en/w/${slug}/inbox?c=${emailConvID}`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-form="reply"]', { timeout: 15000 })
await page.click("#reply-body")
await page.type("#reply-body", "Here you go ")
await page.click('[data-action="emoji"]')
await page.waitForSelector("[data-emoji-picker]", { timeout: 10000 })
await page.fill("[data-emoji-search]", "party")
await page.waitForSelector('[data-emoji-results] button', { timeout: 10000 })
await page.click('[data-emoji-results] button')
const bodyWithEmoji = await page.locator("#reply-body").inputValue()
check("emoji-inserted", /🎉|🥳/.test(bodyWithEmoji), bodyWithEmoji)
await page.click('[data-action="attach"]')
await page.click('[data-attach="document"]')
await page.setInputFiles('[data-testid="attach-document-input"]', DOC_PATH)
await page.waitForSelector('[data-pending-attachment]', { timeout: 10000 })
await page.click('[data-form="reply"] [data-action="send"]')
await page.waitForFunction(() => document.querySelectorAll('[data-message="out"]').length >= 1, null, { timeout: 15000 })
await new Promise((r) => setTimeout(r, 1500))
check("sink-received-email", sinkLog.length === 1, sinkLog.length)
const raw = sinkLog[0]?.data ?? ""
check("sink-to-contact", (sinkLog[0]?.to ?? "").includes("jordan"), sinkLog[0]?.to)
check("sink-is-multipart", /multipart\/mixed/i.test(raw), raw.slice(0, 500))
check("sink-has-attachment-part", /Content-Disposition: attachment/i.test(raw), raw.slice(0, 2000))
check("sink-has-caption-text", /Here you go/.test(raw), raw.slice(0, 3000))
check(
  "thread-renders-email-attachment",
  (await page.locator("[data-attachment-files] a, [data-attachment-images] img").count()) > 0,
)

// (MH-1147: the floating chat dock is gone — the inbox itself is the dock now, so the
// thread checks above already cover it.)
// ---- en/ar x light/dark x 1440/390, WhatsApp thread + dock ----
for (const scheme of ["light", "dark"])
  for (const loc of ["en", "ar"])
    for (const wd of [1440, 390]) {
      const k = `${loc}-${scheme}-${wd}`
      await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: wd, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${waConvID}`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector('[data-form="reply"]', { timeout: 15000 })
      await page.waitForTimeout(500)
      const m = await page.evaluate(() => ({
        dark: document.documentElement.classList.contains("dark"),
        rtl: document.documentElement.dir === "rtl",
        overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
      }))
      check(`${k}-theme`, m.dark === (scheme === "dark"), m.dark)
      check(`${k}-dir`, m.rtl === (loc === "ar"), m.rtl)
      check(`${k}-no-overflow`, !m.overflow, m)
      await page.screenshot({ path: path.join(shots, `mh659-${k}.png`) })
    }

check("no-page-errors", errs.length === 0, errs)
console.log(JSON.stringify({ ok: fail.length === 0, fail, out }, null, 1))
await ctx.close()
evoSrv.close()
sinkServer.close()
process.exit(fail.length ? 1 : 0)
