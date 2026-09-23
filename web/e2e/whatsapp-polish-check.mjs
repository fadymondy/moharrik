// MH-413 in a real browser:
//   1. the WhatsApp link page polls while a QR / pairing code waits, and turns
//      "Linked" by itself; the pairing-code option; webhook health;
//   2. a campaign's audience narrowed to one language;
//   3. the campaigns and contacts pages update from workspace events.
//
//   node e2e/fakes-whatsapp-smtp.mjs <log-dir>    (Evolution stub :8914, SMTP sinks)
//   node e2e/whatsapp-polish-check.mjs http://localhost:3514 <log-dir>
//
// The "phone" is the stub: nothing is ever paired with a real number, and every
// email goes to a local sink.
import { call, fakeLog, launch, matrix, signIn, SHOTS } from "./batch-n-lib.mjs"

const base = (process.argv[2] ?? "http://localhost:3514").replace(/\/$/, "")
const logDir = process.argv[3]
const STUB = "http://127.0.0.1:8914"
const stub = (p, body) =>
  fetch(STUB + p, { method: body ? "POST" : "GET", headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined }).then((r) => r.json())
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const out = {}
const browser = await launch()
const { state, wsId, slug } = await signIn(browser, base)
const ctx = await browser.newContext({ storageState: state, viewport: { width: 1440, height: 900 } })
const page = await ctx.newPage()
const errors = []
page.on("pageerror", (e) => errors.push(String(e).slice(0, 200)))
let statusCalls = 0
page.on("request", (r) => {
  if (new URL(r.url()).pathname === "/api/w/whatsapp" && r.method() === "GET") statusCalls++
})

// ---- 1a. QR: polling, then linked without a reload ----------------------------
const waUrl = `${base}/en/w/${slug}/inbox/whatsapp`
await page.goto(waUrl, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-whatsapp-state]", { timeout: 60000 })
await call(page, "DELETE", "/api/w/whatsapp", undefined, wsId) // start unlinked
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-whatsapp-state=close]", { timeout: 30000 })
await page.getByRole("button", { name: /Link a number/ }).click()
await page.waitForSelector("[data-whatsapp-qr]", { timeout: 20000 })
out.qrPolling = await page.getAttribute("[data-whatsapp-polling]", "data-whatsapp-polling")
const before = statusCalls
await sleep(9000)
out.qrPolledTimes = statusCalls - before
// The phone "scans": the stub reports open (and posts connection.update to the hook).
await stub("/__ctl/state", { state: "open", wuid: "201055550000@s.whatsapp.net" })
const t0 = Date.now()
await page.waitForSelector("[data-whatsapp-state=open]", { timeout: 15000 })
out.linkedWithoutReloadMs = Date.now() - t0
out.qrGone = (await page.locator("[data-whatsapp-qr]").count()) === 0
out.pollingStopped = (await page.getAttribute("[data-whatsapp-polling]", "data-whatsapp-polling")) === "off"
out.webhook = await page.getAttribute("[data-webhook-status]", "data-webhook-status")

// ---- 1b. Pairing code instead of a QR ---------------------------------------------
await call(page, "DELETE", "/api/w/whatsapp", undefined, wsId)
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-whatsapp-state=close]", { timeout: 30000 })
await page.click("[data-toggle-pairing]")
await page.fill("#wa-link-number", "+20 17 0000 9999")
await page.getByRole("button", { name: /Get a pairing code/ }).click()
await page.waitForSelector("[data-pairing-code]", { timeout: 20000 })
out.pairingCode = await page.getAttribute("[data-pairing-code]", "data-pairing-code")
out.pairingNumberSent = (await stub("/__ctl/log")).pairingRequests.includes("201700009999") || (await stub("/__ctl/log")).created.length > 0
await page.screenshot({ path: `${SHOTS}/MH-413-pairing-code-en-light-1440.png` })
await stub("/__ctl/state", { state: "open", wuid: "201055550000@s.whatsapp.net" })
await page.waitForSelector("[data-whatsapp-state=open]", { timeout: 15000 })
out.pairingLinkedWithoutReload = (await page.locator("[data-pairing-code]").count()) === 0

// ---- 2. A campaign for Arabic-speaking contacts only ------------------------------
// A list with subscribers in BOTH languages, so the filter has something to
// leave out: made here (no double opt-in; every address is mail.example).
const stamp = Date.now().toString(36)
const target = (await call(page, "POST", "/api/w/mail/lists", { name: `MH-413 mixed ${stamp}`, double_opt_in: false }, wsId)).body.list
const arabic = [], english = []
for (const [i, l] of ["en", "en", "ar", "ar", "ar"].entries()) {
  const email = `mixed-${stamp}-${i}@mail.example`
  await call(page, "POST", "/api/w/contacts", { email, locale: l, list_id: target.id }, wsId)
  ;(l === "ar" ? arabic : english).push(email)
}
arabic.sort()
target.subscribed = arabic.length + english.length
out.list = { name: target.name, subscribed: target.subscribed, arabic: arabic.length, english: english.length }
await page.goto(`${base}/en/w/${slug}/campaigns`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("#campaign-list", { timeout: 60000 })
await page.click("#campaign-list")
await page.getByRole("option", { name: target.name }).click()
await page.waitForSelector(`[data-email-reachable="${target.subscribed}"]`, { timeout: 15000 })
await page.click("#campaign-audience")
await page.getByRole("option", { name: /Arabic-speaking/ }).click()
await page.waitForSelector(`[data-email-reachable="${arabic.length}"]`, { timeout: 15000 })
out.reachShownForArabic = arabic.length
const subject = `MH-413 Arabic only ${Date.now().toString(36)}`
await page.fill("#campaign-subject", subject)
await page.locator('[data-form="new-campaign"] textarea').first().fill("قهوة طازجة هذا الأسبوع.")
await page.click('[data-form="new-campaign"] button[type=submit]')
const row = page.locator(`[data-campaign-row="${subject}"]`)
await row.waitFor({ timeout: 15000 })
out.rowSaysArabicOnly = (await row.locator("[data-campaign-audience]").getAttribute("data-campaign-audience")) === "ar"

// ---- 3a. Live campaign progress: sent from ANOTHER tab, watched here -------------
const other = await ctx.newPage()
await other.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })
let campaignFetches = 0
page.on("request", (r) => {
  if (new URL(r.url()).pathname === "/api/w/campaigns" && r.method() === "GET") campaignFetches++
})
const id = (await call(other, "GET", "/api/w/campaigns", undefined, wsId)).body.campaigns.find((c) => c.subject === subject).id
const t1 = Date.now()
await call(other, "POST", `/api/w/campaigns/${id}/send`, {}, wsId)
await row.locator("text=/^Sent$/").waitFor({ timeout: 20000 })
out.liveCampaign = { sentShownMs: Date.now() - t1, refetches: campaignFetches }
const logs = [...(fakeLog(logDir, "smtp-A.json") ?? []), ...(fakeLog(logDir, "smtp-B.json") ?? [])]
const got = logs.filter((m) => m.subject.includes("MH-413") || m.data.includes(subject)).map((m) => m.to).sort()
out.audienceDelivered = {
  to: got.length,
  onlyArabic: got.length === arabic.length && JSON.stringify(got) === JSON.stringify(arabic),
  englishReached: got.filter((e) => english.includes(e)).length,
}

// ---- 3b. Live contacts: a visitor subscribes while the page is open ---------------
const forms = (await call(page, "GET", "/api/w/subscribe-forms", undefined, wsId)).body.forms
const form = forms.find((f) => f.kind === "subscribe" && f.enabled)
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("table, [data-empty]", { timeout: 60000 })
const visitor = `live-${Date.now().toString(36)}@mail.example`
const t2 = Date.now()
await fetch(`${base}/api/subscribe/${form.public_key}`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Origin: (form.allowed_origins ?? [])[0] ?? "https://nilecoffee.example" },
  body: JSON.stringify({ email: visitor, name: "Live Visitor" }),
})
await page.waitForSelector(`text=${visitor}`, { timeout: 12000 })
out.liveContactMs = Date.now() - t2

out.pass =
  out.qrPolling === "on" && out.qrPolledTimes >= 2 && out.qrGone && out.pollingStopped && out.webhook === "ok" &&
  !!out.pairingCode && out.pairingLinkedWithoutReload && out.rowSaysArabicOnly && out.audienceDelivered.onlyArabic &&
  out.liveCampaign.sentShownMs < 20000 && out.audienceDelivered.englishReached === 0 && out.liveContactMs < 12000

// ---- screenshots ------------------------------------------------------------------------
const shots = []
shots.push(...(await matrix(browser, state, { base, card: "MH-413", name: "whatsapp-linked", url: `/w/${slug}/inbox/whatsapp`, ready: "[data-webhook-status]" })))
shots.push(
  ...(await matrix(browser, state, {
    base, card: "MH-413", name: "campaign-audience", url: `/w/${slug}/campaigns`, ready: "#campaign-list",
    prepare: async (p, { locale }) => {
      await p.click("#campaign-audience")
      await p.getByRole("option").nth(2).click()
      await p.waitForSelector("[data-email-reachable]", { timeout: 10000 }).catch(() => {})
      void locale
    },
  })),
)
out.shotProblems = shots.filter((s) => s.overflow > 0 || s.errors.length || s.rawKeys.length || (s.locale === "ar") !== (s.dir === "rtl") || (s.theme === "dark") !== s.dark)
out.shotCount = shots.length
out.pageErrors = errors
console.log(JSON.stringify(out, null, 2))
await browser.close()
