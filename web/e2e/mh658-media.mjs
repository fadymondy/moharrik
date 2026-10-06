// Inbox: inbound WhatsApp media — images, video, a voice note, a document, a
// sticker, a location and a contact card (MH-658), end to end against a STUB
// Evolution server. Never touches a real Evolution server and never sends
// anything for real: every "download" below is this file's own local stub
// answering its own local fake getBase64FromMediaMessage endpoint.
//
// Run the API with EVOLUTION_API_URL pointing at this stub BEFORE running this
// script (LoadConfig() in internal/whatsapp/routes.go reads env once at boot):
//   EVOLUTION_API_URL=http://127.0.0.1:8788 EVOLUTION_API_KEY=stub-key-658 \
//   WHATSAPP_HOOK_BASE=http://localhost:8141 go run ./cmd/api
//
// node web/e2e/mh658-media.mjs http://localhost:3141 8788
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { mkdtempSync, mkdirSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))

const base = process.argv[2] ?? "http://localhost:3141"
const STUB_PORT = Number(process.argv[3] ?? 8788)
const STUB_KEY = "stub-key-658"

if (base.includes("moharrik.com")) {
  console.error("refusing to run against moharrik.com — this script sends fake WhatsApp webhooks")
  process.exit(1)
}

// A 1x1 PNG — reused for every image-ish fixture (photo, sticker). <img>
// sniffs actual bytes, not the declared mimetype, so a "webp" sticker backed
// by real PNG bytes still renders.
const PNG_1PX =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// A minimal, genuinely playable WAV: 44-byte header + 1s of 8-bit PCM silence
// at 8000Hz mono, so <audio> in headless Chrome has a real duration and
// currentTime genuinely advances during playback — no proprietary codec, no
// fabricated waveform.
function buildWav(seconds = 1, sampleRate = 8000) {
  const numSamples = seconds * sampleRate
  const buf = Buffer.alloc(44 + numSamples)
  buf.write("RIFF", 0)
  buf.writeUInt32LE(36 + numSamples, 4)
  buf.write("WAVE", 8)
  buf.write("fmt ", 12)
  buf.writeUInt32LE(16, 16)
  buf.writeUInt16LE(1, 20) // PCM
  buf.writeUInt16LE(1, 22) // mono
  buf.writeUInt32LE(sampleRate, 24)
  buf.writeUInt32LE(sampleRate, 28) // byte rate (1 byte/sample * rate)
  buf.writeUInt16LE(1, 32) // block align
  buf.writeUInt16LE(8, 34) // bits per sample
  buf.write("data", 36)
  buf.writeUInt32LE(numSamples, 40)
  for (let i = 0; i < numSamples; i++) buf.writeUInt8(128, 44 + i) // silence (unsigned 8-bit midpoint)
  return buf
}
const WAV_B64 = buildWav(2).toString("base64")

// ---- media fixtures, keyed by the WhatsApp message id ("key.id") the fake
// getBase64FromMediaMessage handler receives inside body.message.key.id ----
const MEDIA_FIXTURES = {
  "img-1": { base64: PNG_1PX, mimetype: "image/png", fileName: "photo.png" },
  "sticker-1": { base64: PNG_1PX, mimetype: "image/webp", fileName: "sticker.webp" },
  "voice-1": { base64: WAV_B64, mimetype: "audio/wav", fileName: "voice.wav" },
  "vid-1": {
    base64: Buffer.from("not-really-mp4-bytes").toString("base64"),
    mimetype: "video/mp4",
    fileName: "clip.mp4",
  },
  "doc-1": {
    base64: Buffer.from("%PDF-1.4 fake").toString("base64"),
    mimetype: "application/pdf",
    fileName: "invoice.pdf",
  },
  "fail-1": { status: 500, message: "media expired" },
  "img-ar-1": { base64: PNG_1PX, mimetype: "image/png", fileName: "photo.png" },
}

const stub = { created: [], webhooks: [], sent: [], state: "connecting" }

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
    if (req.headers["apikey"] !== STUB_KEY) return reply(401, { message: "bad apikey" })

    if (req.url === "/instance/create") {
      stub.created.push(json().instanceName)
      return reply(201, { qrcode: { base64: "data:image/png;base64," + PNG_1PX, code: "2@abc" } })
    }
    if (req.url.startsWith("/webhook/set/")) {
      stub.webhooks.push({ instance: req.url.split("/").pop(), ...json().webhook })
      return reply(200, { webhook: { enabled: true } })
    }
    if (req.url.startsWith("/instance/connectionState/")) {
      return reply(200, { instance: { state: stub.state } })
    }
    if (req.url.startsWith("/instance/connect/")) {
      return reply(200, { base64: "data:image/png;base64," + PNG_1PX, code: "2@abc" })
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
    if (req.url.startsWith("/chat/getBase64FromMediaMessage/")) {
      const b = json()
      const id = b.message?.key?.id
      const fx = MEDIA_FIXTURES[id]
      if (!fx) return reply(404, { message: "no fixture for " + id })
      if (fx.status && fx.status !== 200)
        return reply(fx.status, { message: fx.message || "failed" })
      return reply(200, { base64: fx.base64, mimetype: fx.mimetype, fileName: fx.fileName })
    }
    return reply(404, { message: "not stubbed: " + req.url })
  })
})
await new Promise((r) => srv.listen(STUB_PORT, "127.0.0.1", r))

async function newCtx(viewport, colorScheme) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh658-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme,
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `m658${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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

const out = { pageErrors: [] }
const shotsDir = path.join(__dirname, "shots", "mh658")
mkdirSync(shotsDir, { recursive: true })

const ctx = await newCtx({ width: 1440, height: 900 }, "light")
const page = ctx.pages()[0]
page.on("pageerror", (e) => out.pageErrors.push(String(e).slice(0, 200)))
const { slug } = await signUpAndEnterWorkspace(page, "MH658 " + Date.now())

// ---- link a number so the webhook + token exist ----
// MH-571 folded the dedicated /inbox/whatsapp page into a unified channels
// list; adding a WhatsApp channel is now: open the channels page, "Add
// channel", pick WhatsApp from the dropdown, then the same form as before.
await page.goto(`${base}/en/w/${slug}/settings/integrations`, { waitUntil: "domcontentloaded" })
await page.click("[data-add-channel]")
await page.click('[data-add-type="whatsapp"]')
await page.waitForSelector("[data-whatsapp-state]", { timeout: 20000 })
await page.click('button:has-text("Link a number")')
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
const hook = stub.webhooks[0] ?? {}
out.webhookRegistered = !!hook.url
const hookURL = hook.url.replace(/^https?:\/\/[^/]+/, base)
const token = hook.headers?.["X-Moharrik-Token"]

const post = (payload, headers = {}) =>
  fetch(hookURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Moharrik-Token": token, ...headers },
    body: JSON.stringify(payload),
  })

const msg = (id, message, extra = {}) => ({
  event: "messages.upsert",
  data: {
    key: { remoteJid: "201234567890@s.whatsapp.net", fromMe: false, id },
    pushName: "Dana",
    message,
    ...extra,
  },
})

// ---- send one webhook per media kind — media-only, no caption, exactly the
// shape this card fixes from being dropped ----
out.postStatuses = {}
out.postStatuses.image = (
  await post(msg("img-1", { imageMessage: { mimetype: "image/png" } }))
).status
out.postStatuses.sticker = (
  await post(msg("sticker-1", { stickerMessage: { mimetype: "image/webp" } }))
).status
out.postStatuses.voice = (
  await post(msg("voice-1", { audioMessage: { ptt: true, seconds: 2, mimetype: "audio/wav" } }))
).status
out.postStatuses.video = (
  await post(msg("vid-1", { videoMessage: { mimetype: "video/mp4" } }))
).status
out.postStatuses.document = (
  await post(
    msg("doc-1", { documentMessage: { fileName: "invoice.pdf", mimetype: "application/pdf" } }),
  )
).status
out.postStatuses.location = (
  await post(
    msg("loc-1", {
      locationMessage: {
        degreesLatitude: 30.05,
        degreesLongitude: 31.23,
        name: "HQ",
        address: "Cairo",
      },
    }),
  )
).status
out.postStatuses.contact = (
  await post(
    msg("contact-1", {
      contactMessage: { displayName: "Sam", vcard: "BEGIN:VCARD\nTEL:+201111111111\nEND:VCARD" },
    }),
  )
).status
out.postStatuses.contactsArray = (
  await post(
    msg("contacts-arr-1", {
      contactsArrayMessage: {
        contacts: [
          { displayName: "Group Contact", vcard: "BEGIN:VCARD\nTEL:+201222222222\nEND:VCARD" },
        ],
      },
    }),
  )
).status
out.postStatuses.failedDownload = (
  await post(msg("fail-1", { imageMessage: { mimetype: "image/png" } }))
).status

// ---- open the conversation ----
await page.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 20000 })
await page.click("[data-conversation]")
await page.waitForSelector("[data-messages]", { timeout: 15000 })
await page.waitForFunction(
  () => document.querySelectorAll("[data-attachment-images], [data-attachment-sticker]").length > 0,
  { timeout: 15000 },
)

out.imageRendered = await page.evaluate(
  () => !!document.querySelector("[data-attachment-images] img"),
)
out.stickerRendered = await page.evaluate(
  () => !!document.querySelector("[data-attachment-sticker]"),
)
out.videoRendered = await page.evaluate(() => {
  const v = document.querySelector("[data-attachment-video]")
  return !!v && !!v.getAttribute("src")
})
out.documentRendered = await page.evaluate(() => {
  const a = document.querySelector("[data-attachment-files] a")
  return !!a && /invoice\.pdf/.test(a.textContent || "")
})
out.locationRendered = await page.evaluate(() => {
  const a = document.querySelector("[data-attachment-location]")
  return (
    !!a &&
    a.getAttribute("href")?.includes("openstreetmap.org") &&
    a.getAttribute("href")?.includes("30.05")
  )
})
out.contactRendered = await page.evaluate(() => {
  const cards = [...document.querySelectorAll("[data-attachment-contact]")]
  return (
    cards.some((c) => /Sam/.test(c.textContent || "")) &&
    cards.some((c) => /Group Contact/.test(c.textContent || ""))
  )
})
out.failedChipRendered = await page.evaluate(
  () => !!document.querySelector("[data-attachment-failed]"),
)

// sticker bubble has no chrome: its wrapper carries data-sticker-only and no background
out.stickerBubbleHasNoBackground = await page.evaluate(() => {
  const wrap = document.querySelector("[data-sticker-only]")
  if (!wrap) return false
  const bg = getComputedStyle(wrap).backgroundColor
  return bg === "rgba(0, 0, 0, 0)" || bg === "transparent"
})

// ---- the image lightbox opens on click ----
await page.click("[data-attachment-images] button")
await page.waitForSelector('[data-slot="dialog-content"]', { timeout: 10000 })
out.lightboxOpened = await page.evaluate(
  () => !!document.querySelector('[data-slot="dialog-content"] img'),
)
await page.keyboard.press("Escape")
await page.waitForTimeout(300)

// ---- the voice player actually advances currentTime ----
// Count rAF frames first (project rule): a hidden/non-painting tab never lets
// a real <audio> element's currentTime move meaningfully during a short wait.
out.framesPainted = await page.evaluate(
  () =>
    new Promise((resolve) => {
      let n = 0
      function tick() {
        n++
        if (n < 5) requestAnimationFrame(tick)
        else resolve(n)
      }
      requestAnimationFrame(tick)
    }),
)
await page.click("[data-attachment-voice] button")
await page.waitForTimeout(700)
out.voiceCurrentTimeAdvanced = await page.evaluate(() => {
  const el = document.querySelector("[data-attachment-voice] audio")
  return !!el && el.currentTime > 0
})

await page.screenshot({ path: path.join(shotsDir, "en-light-1440.png") })

// ---- theme + locale + viewport sweep: dark, then Arabic at 390 ----
await page.emulateMedia({ colorScheme: "dark" })
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-messages]", { timeout: 15000 })
out.dark1440_overflow = await page.evaluate(() => document.documentElement.scrollWidth > 1440 + 1)
await page.screenshot({ path: path.join(shotsDir, "en-dark-1440.png") })
await ctx.close()

const ctxAr = await newCtx({ width: 390, height: 844 }, "dark")
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "MH658 AR " + Date.now())
await pageAr.goto(`${base}/en/w/${slugAr}/settings/integrations`, { waitUntil: "domcontentloaded" })
await pageAr.click("[data-add-channel]")
await pageAr.click('[data-add-type="whatsapp"]')
await pageAr.waitForSelector("[data-whatsapp-state]", { timeout: 20000 })
await pageAr.click('button:has-text("Link a number")')
await pageAr.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
const hookAr = stub.webhooks.at(-1) ?? {}
const hookURLAr = hookAr.url.replace(/^https?:\/\/[^/]+/, base)
const tokenAr = hookAr.headers?.["X-Moharrik-Token"]
const postAr = (payload) =>
  fetch(hookURLAr, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Moharrik-Token": tokenAr },
    body: JSON.stringify(payload),
  })
await postAr(msg("img-ar-1", { imageMessage: { mimetype: "image/png" } }))
await postAr(
  msg("loc-ar-1", {
    locationMessage: { degreesLatitude: 30.05, degreesLongitude: 31.23, name: "HQ" },
  }),
)
await postAr(
  msg("contact-ar-1", {
    contactMessage: { displayName: "سام", vcard: "BEGIN:VCARD\nTEL:+201111111111\nEND:VCARD" },
  }),
)

await pageAr.goto(`${base}/ar/w/${slugAr}/inbox`, { waitUntil: "domcontentloaded" })
await pageAr.waitForSelector("[data-conversation]", { timeout: 20000 })
await pageAr.click("[data-conversation]")
await pageAr.waitForSelector("[data-messages]", { timeout: 15000 })
await pageAr.waitForFunction(
  () => document.querySelectorAll("[data-attachment-images]").length > 0,
  { timeout: 15000 },
)
out.ar390_overflow = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
out.ar390_locationRendered = await pageAr.evaluate(
  () => !!document.querySelector("[data-attachment-location]"),
)
out.ar390_contactRendered = await pageAr.evaluate(
  () => !!document.querySelector("[data-attachment-contact]"),
)
await pageAr.evaluate(() => document.querySelector("[data-messages]")?.scrollIntoView())
await pageAr.waitForTimeout(300)
await pageAr.screenshot({ path: path.join(shotsDir, "ar-dark-390.png") })
await ctxAr.close()

srv.close()
console.log(JSON.stringify(out, null, 1))
