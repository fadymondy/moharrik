// MH-664: emoji reactions and reply-to-message quotes, end to end in the
// browser, through a stub Evolution server for WhatsApp and a local SMTP sink
// for email. Never touches a real Evolution server, never sends a real email.
//
// Run the API BEFORE this script, with the Evolution URL/key pointed at this
// script's own stub (LoadConfig() in internal/whatsapp/evolution.go reads env
// once at boot):
//   ADDR=:8147 EVOLUTION_API_URL=http://127.0.0.1:8964 EVOLUTION_API_KEY=stub-key-664 \
//   WHATSAPP_HOOK_BASE=http://localhost:8147 go run ./cmd/api
// and the web dev server on :3147 (next dev --webpack -p 3147).
//
//   node web/e2e/mh664-reactions.mjs http://localhost:3147 [shots-dir] [databaseUrl]
//
// Asserts the exact payloads Evolution receives:
//   POST /message/sendReaction/{instance}  {key:{remoteJid, fromMe, id}, reaction}   ("" removes)
//   POST /message/sendText/{instance}      {..., quoted:{key:{id}, message:{conversation}}}
//   POST /message/sendLocation/{instance}  the same `quoted`
import { chromium } from "playwright-core"
import { mkdtempSync, mkdirSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import net from "node:net"
import { createServer } from "node:http"
import { execFileSync } from "node:child_process"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const base = process.argv[2] ?? "http://localhost:3147"
const shots = process.argv[3] ?? path.join(__dirname, "shots", "mh664")
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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
// Key order is Go's struct order, not ours: compare the JSON value, not its text.
const canon = (v) =>
  v && typeof v === "object" && !Array.isArray(v)
    ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, canon(v[k])]))
    : v
const same = (a, b) => JSON.stringify(canon(a)) === JSON.stringify(canon(b))
// Poll the stub rather than guess how long a round trip takes.
async function until(fn, ms = 10000) {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await fn()) return true
    await sleep(100)
  }
  return false
}

function sql(query, params = []) {
  const escaped = params.reduce((q, p, i) => q.replaceAll(`$${i + 1}`, `'${String(p).replace(/'/g, "''")}'`), query)
  const res = execFileSync("psql", [dbUrl, "-t", "-A", "-c", escaped], { encoding: "utf8" }).trim()
  return res.split(/\r?\n/).find((l) => l.trim() !== "") ?? ""
}

// ---- fake Evolution server ----
const EVO_PORT = 8964
const EVO_KEY = "stub-key-664"
const evo = { webhooks: [], sendText: [], sendLocation: [], sendMedia: [], sendReaction: [] }
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
    for (const kind of ["sendText", "sendLocation", "sendMedia", "sendReaction"]) {
      if (req.url.startsWith(`/message/${kind}/`)) {
        evo[kind].push(json())
        return reply(201, { key: { id: `WA-${kind}-${evo[kind].length}` } })
      }
    }
    return reply(404, { message: "not stubbed: " + req.url })
  })
})
await new Promise((r) => evoSrv.listen(EVO_PORT, "127.0.0.1", r))

// ---- local SMTP sink: captures full RFC822 bodies ----
const SINK_PORT = 2664
const sinkLog = []
const sinkServer = net
  .createServer((sock) => {
    let buf = "",
      data = null,
      m = {}
    const say = (l) => sock.write(l + "\r\n")
    say("220 mh664-sink")
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

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh664-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
  colorScheme: "light",
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))

// ---- a throwaway account and workspace ----
const email = `mh664${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH664 " + Date.now())
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

// ---- WhatsApp channel: link a number through the stub ----
await page.goto(`${base}/en/w/${slug}/inbox/channels`, { waitUntil: "domcontentloaded" })
await page.click("[data-add-channel]")
await page.click('[data-add-type="whatsapp"]')
await page.waitForSelector("[data-whatsapp-state]", { timeout: 20000 })
await page.click('button:has-text("Link a number")')
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
const hook = evo.webhooks[0] ?? {}
check("webhook-registered", !!hook.url, hook)
const hookURL = hook.url.replace(/^https?:\/\/[^/]+/, base)
const token = hook.headers?.["X-Moharrik-Token"]

const postWebhook = (payload) =>
  fetch(hookURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Moharrik-Token": token },
    body: JSON.stringify(payload),
  })

const CUSTOMER = "201234567890@s.whatsapp.net"
const upsert = (key, message, pushName = "Dana") => ({ event: "messages.upsert", data: { key, pushName, message } })
const fromCustomer = (id, message) => upsert({ remoteJid: CUSTOMER, fromMe: false, id }, message)

const QUESTION = "Hi, is my order ready?"
check("wa-inbound-seeded", (await postWebhook(fromCustomer("in-1", { conversation: QUESTION }))).status === 200)
const waConvID = sql("SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp'", [wsID])
const inMsgID = sql("SELECT id FROM messages WHERE workspace_id = $1 AND external_id = 'in-1'", [wsID])
check("whatsapp-rows", !!waConvID && !!inMsgID, { waConvID, inMsgID })

await page.goto(`${base}/en/w/${slug}/inbox?c=${waConvID}`, { waitUntil: "domcontentloaded" })
await page.waitForSelector(`[data-message-id="${inMsgID}"]`, { timeout: 20000 })

const row = (id) => `[data-message-row="${id}"]`
async function reactVia(id, emoji, scope = row(id)) {
  await page.hover(`[data-message-id="${id}"]`)
  await page.click(`${scope} [data-action="react"]`)
  await page.waitForSelector("[data-reaction-bar]", { timeout: 10000 })
  const n = evo.sendReaction.length
  await page.click(`[data-reaction-bar] [data-quick-reaction="${emoji}"]`)
  // WhatsApp sends go through the stub; other channels just redraw a chip.
  await until(() => evo.sendReaction.length > n, 3000)
  await page.waitForSelector("[data-reaction-bar]", { state: "detached", timeout: 5000 }).catch(() => {})
  await sleep(300)
}

// ---- hovering shows React + Reply; a quick reaction goes out through sendReaction ----
await page.hover(`[data-message-id="${inMsgID}"]`)
await sleep(400) // the fade-in transition
const actionsOpacity = await page.$eval(`${row(inMsgID)} [data-message-actions]`, (el) =>
  getComputedStyle(el.parentElement).opacity,
)
check("hover-shows-actions", actionsOpacity === "1", actionsOpacity)
check("reply-button-present", (await page.locator(`${row(inMsgID)} [data-action="reply-to"]`).count()) === 1)
await page.click(`${row(inMsgID)} [data-action="react"]`)
await page.waitForSelector("[data-reaction-bar]", { timeout: 10000 })
check("six-quick-emoji", (await page.locator("[data-reaction-bar] [data-quick-reaction]").count()) === 6)
check("full-picker-button", (await page.locator('[data-reaction-bar] [data-action="react-more"]').count()) === 1)
await page.click('[data-reaction-bar] [data-quick-reaction="👍"]')
await page.waitForSelector("[data-reaction-bar]", { state: "detached", timeout: 5000 }).catch(() => {})
await page.waitForSelector(`${row(inMsgID)} [data-reaction="👍"][data-reaction-mine]`, { timeout: 10000 })
const r1 = evo.sendReaction[0]
check(
  "evo-sendReaction-exact",
  same(r1, { key: { remoteJid: CUSTOMER, fromMe: false, id: "in-1" }, reaction: "👍" }),
  r1,
)

// ---- a different emoji replaces it (one per member per message) ----
await reactVia(inMsgID, "❤️")
const chips1 = await page.locator(`${row(inMsgID)} [data-reaction]`).evaluateAll((els) => els.map((e) => e.dataset.reaction))
check("replace-one-chip", chips1.length === 1 && chips1[0] === "❤️", chips1)
check("evo-sendReaction-replace", evo.sendReaction[1]?.reaction === "❤️", evo.sendReaction[1])

// ---- clicking my own chip takes it back: sendReaction with "" ----
await page.click(`${row(inMsgID)} [data-reaction="❤️"]`)
await until(() => evo.sendReaction.length > 2)
await page.waitForSelector(`${row(inMsgID)} [data-reaction]`, { state: "detached", timeout: 5000 }).catch(() => {})
check("toggle-off-chip-gone", (await page.locator(`${row(inMsgID)} [data-reaction]`).count()) === 0)
check(
  "evo-sendReaction-remove-exact",
  same(evo.sendReaction[2], { key: { remoteJid: CUSTOMER, fromMe: false, id: "in-1" }, reaction: "" }),
  evo.sendReaction[2],
)

// ---- the "+" opens the full picker inside the reaction bar ----
await page.hover(`[data-message-id="${inMsgID}"]`)
await page.click(`${row(inMsgID)} [data-action="react"]`)
await page.click('[data-reaction-bar] [data-action="react-more"]')
await page.fill("[data-reaction-bar] [data-emoji-search]", "fire")
await page.click('[data-reaction-bar] [data-emoji-results] button')
await page.waitForSelector(`${row(inMsgID)} [data-reaction="🔥"]`, { timeout: 10000 })
check("full-picker-reacts", evo.sendReaction[3]?.reaction === "🔥", evo.sendReaction[3])

// ---- Reply: the composer shows the quote, the send carries `quoted` ----
await page.hover(`[data-message-id="${inMsgID}"]`)
await page.click(`${row(inMsgID)} [data-action="reply-to"]`)
await page.waitForSelector(`[data-reply-preview="${inMsgID}"]`, { timeout: 10000 })
check("composer-quote-text", (await page.locator("[data-reply-preview]").innerText()).includes(QUESTION))
const focused = await page.evaluate(() => document.activeElement?.id)
check("reply-focuses-composer", focused === "reply-body", focused)
await page.fill("#reply-body", "Yes — it ships today.")
await page.click('[data-form="reply"] [data-action="send"]')
await page.waitForFunction(() => !document.querySelector("[data-reply-preview]"), null, { timeout: 15000 })
await sleep(800)
const st = evo.sendText.at(-1)
check(
  "evo-sendText-quoted-exact",
  same(st?.quoted, { key: { id: "in-1" }, message: { conversation: QUESTION } }),
  st,
)
const outMsgID = sql(
  "SELECT id FROM messages WHERE workspace_id = $1 AND conversation_id = $2 AND direction = 'out' ORDER BY created_at DESC LIMIT 1",
  [wsID, waConvID],
)
const outExt = sql("SELECT external_id FROM messages WHERE id = $1", [outMsgID])
check("out-row-reply-to", sql("SELECT reply_to_message_id FROM messages WHERE id = $1", [outMsgID]) === inMsgID)
await page.waitForSelector(`[data-message-id="${outMsgID}"] [data-quote-id="${inMsgID}"]`, { timeout: 10000 })
check("sent-bubble-shows-quote", true)

// ---- a location reply quoting the same message: sendLocation carries `quoted` too ----
await page.hover(`[data-message-id="${inMsgID}"]`)
await page.click(`${row(inMsgID)} [data-action="reply-to"]`)
await page.click('[data-action="location"]')
await page.waitForSelector("[data-location-picker]", { timeout: 10000 })
await page.fill("#location-lat", "30.0444")
await page.fill("#location-lng", "31.2357")
await page.fill("#location-name", "HQ")
await page.click('[data-action="location-use"]')
await sleep(1200)
const sl = evo.sendLocation.at(-1)
check(
  "evo-sendLocation-quoted-exact",
  same(sl?.quoted, { key: { id: "in-1" }, message: { conversation: QUESTION } }),
  sl,
)
check("location-clears-reply", (await page.locator("[data-reply-preview]").count()) === 0)

// ---- Escape cancels a reply ----
await page.hover(`[data-message-id="${inMsgID}"]`)
await page.click(`${row(inMsgID)} [data-action="reply-to"]`)
await page.waitForSelector("[data-reply-preview]")
await page.press("#reply-body", "Escape")
check("escape-cancels-reply", (await page.locator("[data-reply-preview]").count()) === 0)

// ---- the customer reacts to our reply; the phone reacts to the question ----
const beforeRows = Number(sql("SELECT count(*) FROM messages WHERE conversation_id = $1", [waConvID]))
await postWebhook(
  fromCustomer("r-1", { reactionMessage: { key: { remoteJid: CUSTOMER, fromMe: true, id: outExt }, text: "🙏" } }),
)
await postWebhook(
  upsert({ remoteJid: CUSTOMER, fromMe: true, id: "r-2" }, {
    reactionMessage: { key: { remoteJid: CUSTOMER, fromMe: false, id: "in-1" }, text: "😂" },
  }),
)
const liveChip = await page
  .waitForSelector(`${row(outMsgID)} [data-reaction="🙏"]`, { timeout: 10000 })
  .then(() => true, () => false)
check("customer-reaction-realtime", liveChip)
await page.waitForSelector(`${row(inMsgID)} [data-reaction="😂"]`, { timeout: 10000 }).catch(() => {})
check("phone-reaction-shown", (await page.locator(`${row(inMsgID)} [data-reaction="😂"]`).count()) === 1)
const afterRows = Number(sql("SELECT count(*) FROM messages WHERE conversation_id = $1", [waConvID]))
check("reactions-are-not-messages", afterRows === beforeRows, { beforeRows, afterRows })

// ---- the customer quotes our reply; clicking the quote jumps to it ----
await postWebhook(
  fromCustomer("in-2", {
    extendedTextMessage: {
      text: "Great, thank you!",
      contextInfo: { stanzaId: outExt, quotedMessage: { conversation: "Yes — it ships today." } },
    },
  }),
)
const in2 = await page
  .waitForSelector(`[data-quote-id="${outMsgID}"]`, { timeout: 10000 })
  .then(() => true, () => false)
check("inbound-quote-shown", in2)
const in2ID = sql("SELECT id FROM messages WHERE workspace_id = $1 AND external_id = 'in-2'", [wsID])
check("inbound-quote-linked", sql("SELECT reply_to_message_id FROM messages WHERE id = $1", [in2ID]) === outMsgID)
await page.click(`[data-message-id="${in2ID}"] [data-quote-id="${outMsgID}"]`)
const hit = await page
  .waitForSelector(`[data-message-id="${outMsgID}"].mh-search-hit`, { timeout: 5000 })
  .then(() => true, () => false)
check("quote-click-jumps", hit)

// ---- email: in-app reaction (never sent), and a quote that sets In-Reply-To ----
const smtp = await call("POST", "/api/w/smtp-services", {
  name: "MH664 sink",
  host: "127.0.0.1",
  port: SINK_PORT,
  from_name: "MH664",
  from_email: "support@mh664.test",
  tls_mode: "none",
})
check("smtp-service", smtp.status === 201, JSON.stringify(smtp))
const mailbox = await call("POST", "/api/w/mailboxes", {
  name: "MH664 mailbox",
  host: "imap.mh664.test",
  port: 993,
  username: "support@mh664.test",
  password: "x",
  tls_mode: "tls",
  smtp_service_id: smtp.body?.service?.id,
  is_primary: true,
})
check("mailbox", mailbox.status === 201, JSON.stringify(mailbox))
const mailboxID = mailbox.body?.mailbox?.id
const emailConvID = sql(
  `INSERT INTO conversations (workspace_id, channel, mailbox_id, customer_name, customer_email, subject)
   VALUES ($1, 'email', $2, $3, $4, $5) RETURNING id`,
  [wsID, mailboxID, "Jordan Visitor", "jordan@mail.example", "Quote test"],
)
const firstMail = sql(
  `INSERT INTO messages (workspace_id, conversation_id, direction, mailbox_id, subject, body, external_id, created_at)
   VALUES ($1, $2, 'in', $3, 'Quote test', 'First question about pricing', 'orig-1@mail.example', now() - interval '2 hours') RETURNING id`,
  [wsID, emailConvID, mailboxID],
)
sql(
  `INSERT INTO messages (workspace_id, conversation_id, direction, mailbox_id, subject, body, external_id, created_at)
   VALUES ($1, $2, 'in', $3, 'Quote test', 'Second, unrelated note', 'orig-2@mail.example', now() - interval '1 hour')`,
  [wsID, emailConvID, mailboxID],
)
const reactionsBefore = evo.sendReaction.length
await page.goto(`${base}/en/w/${slug}/inbox?c=${emailConvID}`, { waitUntil: "domcontentloaded" })
await page.waitForSelector(`article[data-message-id="${firstMail}"]`, { timeout: 20000 })
await reactVia(firstMail, "👍", `article[data-message-id="${firstMail}"]`)
const emailChip = `article[data-message-id="${firstMail}"] [data-reaction="👍"]`
check("email-chip", (await page.locator(emailChip).count()) === 1)
check("email-chip-in-app", (await page.locator(`${emailChip}[data-reaction-in-app]`).count()) === 1)
check("email-reaction-not-sent", evo.sendReaction.length === reactionsBefore, evo.sendReaction.length)
await page.hover(emailChip)
const note = await page
  .waitForSelector("[data-reaction-in-app-note]", { timeout: 5000 })
  .then((el) => el.innerText(), () => "")
check("email-in-app-tooltip", /Shown here only — not delivered on Email/.test(note), note)

await page.hover(`article[data-message-id="${firstMail}"]`)
await page.click(`article[data-message-id="${firstMail}"] [data-action="reply-to"]`)
await page.waitForSelector(`[data-reply-preview="${firstMail}"]`)
await page.fill("#reply-body", "About pricing: see the attached sheet.")
await page.click('[data-form="reply"] [data-action="send"]')
await sleep(2000)
const raw = sinkLog.at(-1)?.data ?? ""
const irt = raw.match(/^In-Reply-To:\s*(.+)$/im)?.[1] ?? ""
check("email-in-reply-to-quoted", irt.includes("orig-1@mail.example"), irt)
const emailOut = sql(
  "SELECT id FROM messages WHERE conversation_id = $1 AND direction = 'out' ORDER BY created_at DESC LIMIT 1",
  [emailConvID],
)
await page.waitForSelector(`article[data-message-id="${emailOut}"] [data-quote-id="${firstMail}"]`, { timeout: 10000 })
  .then(() => check("email-card-shows-quote", true), () => check("email-card-shows-quote", false))

// ---- the docked chat: react + reply work there too ----
await page.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })
const launcher = page.locator("[data-chat-dock-launcher]")
await launcher.waitFor({ state: "visible", timeout: 20000 }).catch(() => {})
if (await launcher.count()) {
  await launcher.click()
  await page.waitForSelector("[data-dock-list]", { timeout: 10000 })
  const item = page.locator("[data-dock-list-item]", { hasText: "Dana" }).first()
  if (await item.count()) {
    await item.click()
    const dock = "[data-dock-window]"
    await page.waitForSelector(`${dock} [data-message-id="${in2ID}"]`, { timeout: 15000 })
    const n = evo.sendReaction.length
    await reactVia(in2ID, "😮", `${dock} ${row(in2ID)}`)
    check("dock-react-sends", evo.sendReaction[n]?.reaction === "😮" && evo.sendReaction[n]?.key?.id === "in-2", evo.sendReaction[n])
    await page.hover(`${dock} [data-message-id="${in2ID}"]`)
    await page.click(`${dock} ${row(in2ID)} [data-action="reply-to"]`)
    await page.waitForSelector(`${dock} [data-reply-preview="${in2ID}"]`, { timeout: 10000 })
    await page.fill(`${dock} #reply-body`, "You're welcome!")
    await page.click(`${dock} [data-form="reply"] [data-action="send"]`)
    await sleep(1200)
    check(
      "dock-sendText-quoted",
      same(evo.sendText.at(-1)?.quoted, { key: { id: "in-2" }, message: { conversation: "Great, thank you!" } }),
      evo.sendText.at(-1),
    )
    await page.screenshot({ path: path.join(shots, "en-light-1440-dock.png") })
  } else check("dock-list-has-item", false, "no dock item named Dana")
} else check("dock-launcher-present", false, "launcher not found")

// ---- en/ar × light/dark × 1440/390: chips, quotes, hover bar, no overflow ----
for (const scheme of ["light", "dark"])
  for (const loc of ["en", "ar"])
    for (const wd of [1440, 390]) {
      const k = `${loc}-${scheme}-${wd}`
      await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width: wd, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${waConvID}`, { waitUntil: "domcontentloaded" })
      await page.waitForSelector(`[data-message-id="${outMsgID}"]`, { timeout: 20000 })
      await page.waitForTimeout(500)
      await page.hover(`[data-message-id="${in2ID}"]`)
      const m = await page.evaluate(
        ({ outMsgID, in2ID }) => {
          const bubble = document.querySelector(`[data-message-id="${outMsgID}"]`).getBoundingClientRect()
          const chip = document.querySelector(`[data-message-row="${outMsgID}"] [data-reaction]`)?.getBoundingClientRect()
          return {
            dark: document.documentElement.classList.contains("dark"),
            rtl: document.documentElement.dir === "rtl",
            overflow: document.documentElement.scrollWidth > window.innerWidth + 1,
            chipUnderBubble: !!chip && chip.top >= bubble.bottom - 10 && chip.top <= bubble.bottom + 8,
            quote: !!document.querySelector(`[data-message-id="${in2ID}"] [data-quote-id="${outMsgID}"]`),
          }
        },
        { outMsgID, in2ID },
      )
      check(`${k}-theme`, m.dark === (scheme === "dark"), m.dark)
      check(`${k}-dir`, m.rtl === (loc === "ar"), m.rtl)
      check(`${k}-no-overflow`, !m.overflow, m)
      check(`${k}-chip-under-bubble`, m.chipUnderBubble, m)
      check(`${k}-quote`, m.quote, m)
      await page.screenshot({ path: path.join(shots, `mh664-${k}.png`) })
    }

check("no-page-errors", errs.length === 0, errs)
console.log(JSON.stringify({ ok: fail.length === 0, fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
await ctx.close()
evoSrv.close()
sinkServer.close()
process.exit(fail.length ? 1 : 0)
