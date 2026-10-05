// MH-660: realtime typing/recording indicators, both ways — WhatsApp presence
// and the chat widget visitor. Everything here runs against LOCAL servers
// only (this worktree's :3145/:8145 by default) and a STUB Evolution server
// (never the real one, and never a real phone number).
//
//   node web/e2e/mh660-typing.mjs [webBase] [evolutionStubPort]
//
// Covers:
//   a) a fresh link's webhook subscribes PRESENCE_UPDATE (see whatsapp-check.mjs
//      for the full link flow + the auto-widen path for already-linked
//      instances, which is exercised by go's TestMissingEvents).
//   b) a customer's "composing"/"recording" shows in the thread header AND the
//      conversation list row, and clears on "paused" and after ~25s of silence.
//   c) delivered over the existing realtime stream, never persisted (nothing
//      here ever asserts a DB row for a typing event).
//   d) the agent's composer sends throttled "composing" + a "paused" on
//      stop/send to WhatsApp (via the stub) and to the widget visitor.
//   e) a widget visitor's typing reaches the inbox (header + list row).
//   f) the same indicator shows in the chat dock.
//   g) en/ar, light/dark, 1440/390 screenshots.
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3145"
if (/moharrik\.com/i.test(base)) {
  throw new Error("refusing to run mh660-typing.mjs against moharrik.com — local servers only")
}
const STUB_PORT = Number(process.argv[3] ?? 8788)

const FAKE_QR =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

const stub = { created: [], webhooks: [], sent: [], presence: [], state: "connecting" }

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
    // MH-660: the endpoint AgentTyping's WhatsApp half calls.
    if (req.url.startsWith("/chat/sendPresence/")) {
      const b = json()
      stub.presence.push({
        instance: req.url.split("/").pop(),
        number: b.number,
        presence: b.presence,
        at: Date.now(),
      })
      return reply(200, {})
    }
    if (req.url.startsWith("/instance/delete/")) {
      stub.deleted = req.url.split("/").pop()
      return reply(200, { status: "SUCCESS" })
    }
    return reply(404, { message: "not stubbed: " + req.url })
  })
})
await new Promise((r) => srv.listen(STUB_PORT, "127.0.0.1", r))

async function newCtx(opts = {}) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh660-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    ...opts,
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `mh660-${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
const ctx = await newCtx({ colorScheme: "light" })
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "MH660 " + Date.now())
out.slug = slug

// ---- (a) link WhatsApp against the stub: assert the fresh subscription includes PRESENCE_UPDATE ----
// The old dedicated /inbox/whatsapp page is now just a redirect shim to
// /settings/integrations?type=whatsapp, where WhatsAppChannelForm renders inside a
// sheet opened from the "Add channel" dropdown (see
// web/components/channels/whatsapp-channel-form.tsx and
// web/app/[locale]/w/[slug]/settings/integrations/page.tsx).
await page.goto(`${base}/en/w/${slug}/settings/integrations`, { waitUntil: "domcontentloaded" })
await page.click("[data-add-channel]")
await page.click('[data-add-type="whatsapp"]')
await page.waitForSelector('[data-add-channel-sheet="whatsapp"] [data-whatsapp-state]', { timeout: 20000 })
await page.click('[data-add-channel-sheet="whatsapp"] button:has-text("Link a number")')
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
const hook = stub.webhooks[0] ?? {}
out.freshLinkSubscribesPresence = (hook.events ?? []).includes("PRESENCE_UPDATE")
const hookURL = hook.url.replace(/^https?:\/\/[^/]+/, base)
const token = hook.headers["X-Moharrik-Token"]

const post = (payload, headers = {}) =>
  fetch(hookURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Moharrik-Token": token, ...headers },
    body: JSON.stringify(payload),
  })

const CUSTOMER = "201234567890"

// ---- a customer message opens a conversation to attach presence to ----
await post({
  event: "messages.upsert",
  data: {
    key: { remoteJid: `${CUSTOMER}@s.whatsapp.net`, fromMe: false, id: "WA-" + Date.now() },
    pushName: "Dana",
    message: { conversation: "hi, quick question" },
  },
})

await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 20000 })
await page.waitForTimeout(300) // let the virtualized list settle before clicking a row
await page.click("[data-conversation]")
try {
  await page.waitForSelector("[data-messages]", { timeout: 5000 })
} catch {
  // The row's first click can land before the list settles from an SWR
  // revalidation replacing the node; try once more.
  await page.click("[data-conversation]")
  await page.waitForSelector("[data-messages]", { timeout: 15000 })
}

// ---- (b)/(c) a composing presence.update shows "typing…" in header AND list row ----
await post({ event: "presence.update", data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "composing" } } } })
await page
  .waitForSelector('[data-thread-title] ~ * [data-typing-indicator], [data-typing-indicator]', {
    timeout: 10000,
  })
  .then(() => (out.headerShowsTyping = true))
  .catch(() => (out.headerShowsTyping = false))
out.headerTypingText = await page
  .locator("[data-typing-indicator]")
  .first()
  .textContent()
  .catch(() => null)
out.listRowShowsTyping = await page.evaluate(
  () => document.querySelectorAll("[data-typing-indicator]").length >= 2,
)

// paused clears both
await post({ event: "presence.update", data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "paused" } } } })
await page
  .waitForFunction(() => document.querySelectorAll("[data-typing-indicator]").length === 0, {
    timeout: 10000,
  })
  .then(() => (out.clearsOnPaused = true))
  .catch(() => (out.clearsOnPaused = false))

// recording -> "recording audio…"
await post({ event: "presence.update", data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "recording" } } } })
await page.waitForSelector("[data-typing-indicator]", { timeout: 10000 })
out.recordingText = await page.locator("[data-typing-indicator]").first().textContent()

// ---- the ~25s safety-net timeout, with no paused event at all ----
await page.waitForFunction(() => document.querySelectorAll("[data-typing-indicator]").length === 0, {
  timeout: 30000,
})
out.clearsOnTimeout = true

// ---- (d) the agent's composer: throttled composing, then paused on send ----
await post({ event: "presence.update", data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "composing" } } } })
await page.waitForSelector("[data-typing-indicator]", { timeout: 10000 })
await post({ event: "presence.update", data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "paused" } } } })
await page.waitForFunction(() => document.querySelectorAll("[data-typing-indicator]").length === 0, {
  timeout: 10000,
})

const presenceCountBefore = stub.presence.length
await page.fill("#reply-body", "One moment")
await page
  .waitForFunction(
    (n) => window.__mh660_presence_count === undefined || true,
    presenceCountBefore,
    { timeout: 100 },
  )
  .catch(() => {})
// give the client-side throttle (agentTyping fires immediately on first keystroke) a beat
await page.waitForTimeout(500)
out.agentComposingSentToStub = stub.presence.some(
  (p) => p.presence === "composing" && p.number === CUSTOMER,
)
await page.click('[data-form="reply"] button[type=submit]')
await page
  .waitForFunction(() => document.querySelectorAll('[data-message="out"]').length > 0, {
    timeout: 15000,
  })
  .catch(() => {})
await page.waitForTimeout(300)
out.agentPausedSentOnSend = stub.presence.some(
  (p) => p.presence === "paused" && p.number === CUSTOMER,
)

// ---- (e)/(d widget half) a chat widget: visitor typing reaches the inbox, agent typing reaches the visitor ----
const SITE_PORT = 4188
function servePage(port) {
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Customer site</title></head><body><h1>site</h1></body></html>`
  return new Promise((resolve) => {
    const s = createServer((_, res) => {
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
      res.end(html)
    })
    s.listen(port, "127.0.0.1", () => resolve(s))
  })
}

// Chat widgets moved under the unified Channels page too (WidgetChannelForm,
// see web/components/channels/widget-channel-form.tsx) — same restructure as
// WhatsApp above. Create it there, then open the row's edit sheet
// (WidgetSettingsSheet) to read the embed snippet's data-key.
await page.goto(`${base}/en/w/${slug}/settings/integrations`, { waitUntil: "domcontentloaded" })
await page.click("[data-add-channel]")
await page.click('[data-add-type="chat"]')
await page.waitForSelector("#widget-name", { timeout: 10000 })
await page.fill("#widget-name", "MH660 widget")
await page.fill("#widget-origins", `http://127.0.0.1:${SITE_PORT}`)
await page.click('[data-form="chat-widget"] button[type=submit]')
await page.waitForSelector('[data-channel-row][data-channel-type="chat"]', { timeout: 10000 })
await page.click('[data-channel-row][data-channel-type="chat"] [data-edit-channel]')
await page.waitForSelector("code", { timeout: 10000 })
const embed = await page.evaluate(
  () =>
    [...document.querySelectorAll("code")].map((c) => c.textContent ?? "").find((v) => v.includes("data-key=")) ??
    "",
)
const widgetKey = (embed.match(/data-key="([^"]+)"/) || [])[1]
out.widgetKeyFound = !!widgetKey

const site = await servePage(SITE_PORT)
const visitor = await ctx.newPage()
await visitor.goto(`http://127.0.0.1:${SITE_PORT}/`, { waitUntil: "networkidle" })

// The real widget SDK (an external repo, not in this worktree — web/public/chat/v1.js
// is a generated do-not-edit artifact with no typing support) does not send or
// render typing yet, so the visitor side here talks to the public API directly —
// exactly what a future widget build would do.
const startResp = await visitor.evaluate(
  async ({ base, widgetKey, SITE_PORT }) => {
    const r = await fetch(`${base}/api/chat/${encodeURIComponent(widgetKey)}/messages`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: `http://127.0.0.1:${SITE_PORT}` },
      body: JSON.stringify({ body: "hello from the widget" }),
    })
    return r.json()
  },
  { base, widgetKey, SITE_PORT },
)
const visitorToken = startResp.visitor_token
out.visitorConversationStarted = !!visitorToken

// visitor -> inbox: the inbox must be open (and its realtime SSE stream
// subscribed) BEFORE the typing POST fires — the hub has no replay buffer,
// so an event sent before anyone is listening is simply gone. Open + select
// the conversation first, then post the visitor's "composing".
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector('[data-conversation]:has-text("hello from the widget"), [data-conversation]', {
  timeout: 15000,
})
// select the widget conversation (the newest one)
const widgetConvBtn = page.locator("[data-conversation]").first()
await page.waitForTimeout(300) // let the virtualized list settle before clicking a row
await widgetConvBtn.click()
try {
  await page.waitForSelector("[data-messages]", { timeout: 5000 })
} catch {
  await widgetConvBtn.click()
  await page.waitForSelector("[data-messages]", { timeout: 15000 })
}
await page.waitForTimeout(500) // let this tab's SSE stream finish subscribing
await visitor.evaluate(
  async ({ base, widgetKey, SITE_PORT, visitorToken }) => {
    await fetch(`${base}/api/chat/${encodeURIComponent(widgetKey)}/typing`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: `http://127.0.0.1:${SITE_PORT}` },
      body: JSON.stringify({ visitor_token: visitorToken, presence: "composing" }),
    })
  },
  { base, widgetKey, SITE_PORT, visitorToken },
)
out.widgetVisitorTypingReachesInbox = await page
  .waitForSelector("[data-typing-indicator]", { timeout: 8000 })
  .then(() => true)
  .catch(() => false)

// clear it so the next assertion is not stale
await visitor.evaluate(
  async ({ base, widgetKey, SITE_PORT, visitorToken }) => {
    await fetch(`${base}/api/chat/${encodeURIComponent(widgetKey)}/typing`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: `http://127.0.0.1:${SITE_PORT}` },
      body: JSON.stringify({ visitor_token: visitorToken, presence: "" }),
    })
  },
  { base, widgetKey, SITE_PORT, visitorToken },
)
await page.waitForFunction(() => document.querySelectorAll("[data-typing-indicator]").length === 0, {
  timeout: 8000,
})

// agent -> widget visitor: open the visitor's own SSE stream, then type in the composer
const sseFrames = []
const sseTask = visitor.evaluate(
  async ({ base, widgetKey, SITE_PORT, visitorToken }) => {
    const res = await fetch(`${base}/api/chat/${encodeURIComponent(widgetKey)}/events?visitor_token=${encodeURIComponent(visitorToken)}`, {
      headers: { Accept: "text/event-stream", Origin: `http://127.0.0.1:${SITE_PORT}` },
    })
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ""
    const frames = []
    const deadline = Date.now() + 8000
    while (Date.now() < deadline) {
      const { value, done } = await Promise.race([
        reader.read(),
        new Promise((r) => setTimeout(() => r({ done: false, value: undefined }), 500)),
      ])
      if (done) break
      if (value) buf += decoder.decode(value, { stream: true })
      let idx
      while ((idx = buf.indexOf("\n\n")) !== -1) {
        const frame = buf.slice(0, idx)
        buf = buf.slice(idx + 2)
        const line = frame.split("\n").find((l) => l.startsWith("data:"))
        if (line) frames.push(line.slice(5).trim())
      }
      if (frames.some((f) => f.includes('"typing"'))) break
    }
    try {
      reader.cancel()
    } catch {}
    return frames
  },
  { base, widgetKey, SITE_PORT, visitorToken },
)
await page.waitForTimeout(300)
await page.fill("#reply-body", "typing to the visitor")
await sseTask.then((frames) => sseFrames.push(...frames))
out.agentTypingReachesWidgetVisitor = sseFrames.some(
  (f) => f.includes('"typing"') && f.includes('"composing"'),
)

// (MH-1147: the floating chat dock is gone — the inbox itself is the dock now, so the
// thread checks above already cover it.)
out.pageErrors = errs
await ctx.close()

// ---- screenshots: en/ar x light/dark x 1440/390 ----
const shots = []
async function shot(locale, scheme, width, height, name) {
  const c = await newCtx({ colorScheme: scheme, viewport: { width, height } })
  const p = c.pages()[0]
  const { slug: s } = await signUpAndEnterWorkspace(p, "MH660shot " + Date.now() + name)
  await p.goto(`${base}/${locale}/w/${s}/settings/integrations`, { waitUntil: "domcontentloaded" })
  await p.click("[data-add-channel]")
  await p.click('[data-add-type="whatsapp"]')
  await p.waitForSelector('[data-add-channel-sheet="whatsapp"] [data-whatsapp-state]', { timeout: 20000 })
  await p
    .click('[data-add-channel-sheet="whatsapp"] button:has-text("Link a number"), [data-add-channel-sheet="whatsapp"] button:has-text("ربط رقماً")')
    .catch(() => {})
  await p.waitForTimeout(500)
  const hookRow = stub.webhooks.at(-1)
  if (hookRow) {
    const hurl = hookRow.url.replace(/^https?:\/\/[^/]+/, base)
    const tok = hookRow.headers["X-Moharrik-Token"]
    await fetch(hurl, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Moharrik-Token": tok },
      body: JSON.stringify({
        event: "messages.upsert",
        data: {
          key: { remoteJid: `${CUSTOMER}@s.whatsapp.net`, fromMe: false, id: "shot-" + Date.now() },
          pushName: "Dana",
          message: { conversation: "hi" },
        },
      }),
    })
    await p.goto(`${base}/${locale}/w/${s}/inbox`, { waitUntil: "domcontentloaded" })
    await p.waitForSelector("[data-conversation]", { timeout: 15000 })
    await p.waitForTimeout(300) // let the virtualized list settle before clicking a row
    await p.click("[data-conversation]")
    try {
      await p.waitForSelector("[data-messages]", { timeout: 5000 })
    } catch {
      await p.click("[data-conversation]")
      await p.waitForSelector("[data-messages]", { timeout: 15000 })
    }
    await fetch(hurl, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Moharrik-Token": tok },
      body: JSON.stringify({
        event: "presence.update",
        data: { id: `${CUSTOMER}@s.whatsapp.net`, presences: { [`${CUSTOMER}@s.whatsapp.net`]: { lastKnownPresence: "composing" } } },
      }),
    })
    await p.waitForSelector("[data-typing-indicator]", { timeout: 10000 }).catch(() => {})
    await p.waitForTimeout(600) // let the pulse animation and theme settle
  }
  const file = path.resolve(process.cwd(), `../.setup/shots/mh660-${name}.png`)
  await p.screenshot({ path: file }).catch(() => {})
  shots.push(file)
  await c.close()
}

await shot("en", "light", 1440, 900, "en-light-1440")
await shot("en", "dark", 1440, 900, "en-dark-1440")
await shot("ar", "light", 390, 844, "ar-light-390")
await shot("ar", "dark", 390, 844, "ar-dark-390")

out.shots = shots
srv.close()
site.close()
console.log(JSON.stringify(out, null, 1))
