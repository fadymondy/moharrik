// MH-411 in a real browser: a member verifies their number, then a failing
// automation run and a new lead arrive on WhatsApp in one batched message, a
// burst of 5 arrives as one, and quiet hours hold a message until they end.
//
//   node e2e/fakes-whatsapp-smtp.mjs <log-dir>        (Evolution stub on :8914)
//   node e2e/whatsapp-notify-check.mjs http://localhost:3514 <database>
//
// Runs against the API's real 30-second notifier. Failed automation runs are
// written straight into the LOCAL database named by <database> (a run needs a
// paired browser extension, which a check cannot pair); the lead goes through
// the public inquiry route like a visitor's. The member's number is in an
// unallocated range and the "WhatsApp" is the stub: nothing reaches a phone.
import { execFileSync } from "node:child_process"
import { call, launch, matrix, signIn } from "./batch-n-lib.mjs"

const base = (process.argv[2] ?? "http://localhost:3514").replace(/\/$/, "")
const db = process.argv[3] ?? "moharrik_n"
const STUB = "http://127.0.0.1:8914"
const ME = "201700004242"
const stub = (p, body) =>
  fetch(STUB + p, {
    method: body ? "POST" : "GET",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  }).then((r) => r.json())
const sql = (q) =>
  execFileSync("docker", ["exec", "pg", "psql", "-U", "postgres", "-d", db, "-tAc", q], {
    encoding: "utf8",
  }).trim()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const toMe = async () => (await stub("/__ctl/log")).sent.filter((s) => s.number === ME)
async function waitFor(pred, ms, what) {
  const until = Date.now() + ms
  while (Date.now() < until) {
    const v = await pred()
    if (v) return v
    await sleep(2000)
  }
  throw new Error("timed out waiting for " + what)
}

const out = {}
const browser = await launch()
const { state, wsId, slug } = await signIn(browser, base)
const ctx = await browser.newContext({
  storageState: state,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errors = []
page.on("pageerror", (e) => errors.push(String(e).slice(0, 200)))
await page.goto(`${base}/en/w/${slug}/settings/delivery`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-wa-notify]", { timeout: 60000 })

// The workspace's number, linked against the stub and "open".
await call(page, "POST", "/api/w/whatsapp/link", {}, wsId)
await stub("/__ctl/state", { state: "open", wuid: "201055550000@s.whatsapp.net" })
// Start from nothing: an earlier run's number is removed.
await call(page, "DELETE", "/api/w/notify/whatsapp", undefined, wsId)
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-wa-notify=unverified]", { timeout: 60000 })

const verifyShots = await matrix(browser, state, {
  base,
  card: "MH-411",
  name: "delivery-verify",
  url: `/w/${slug}/settings/delivery`,
  ready: "[data-wa-notify=unverified]",
})

// ---- verify the member's own number ----
const before = (await toMe()).length
await page.fill("#wa-notify-phone", "+20 17 0000 4242")
await page.getByRole("button", { name: /Send code/ }).click()
await page.waitForSelector("[data-wa-notify=code-pending]", { timeout: 20000 })
const codeMsg = (await toMe()).slice(before)[0]?.text ?? ""
const code = (codeMsg.match(/\b\d{6}\b/) ?? [""])[0]
out.codeArrived = !!code
await page.fill("#wa-notify-code", "000000" === code ? "111111" : "000000")
await page.click("[data-wa-notify-confirm]")
await page.waitForSelector("text=/not right|expired/", { timeout: 10000 })
out.wrongCodeRefused = true
await page.fill("#wa-notify-code", code)
await page.click("[data-wa-notify-confirm]")
await page.waitForSelector("[data-wa-notify=verified]", { timeout: 20000 })
out.verified = true

// Settings for the check: failures + leads, no quiet hours, a 1-minute window.
const put = (over = {}) =>
  call(
    page,
    "PUT",
    "/api/w/notify/whatsapp",
    {
      enabled: true,
      kinds: ["automation_failed", "lead"],
      quiet_start: 0,
      quiet_end: 0,
      timezone: "",
      daily_cap: 20,
      batch_minutes: 1,
      locale: "en",
      ...over,
    },
    wsId,
  )
await put()
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-wa-notify=verified]", { timeout: 60000 })
// Let one tick set the watermarks before anything happens.
await sleep(35000)

// ---- a failing automation run and a new lead ----
const agent =
  sql(`SELECT id FROM browser_agents WHERE workspace_id = '${wsId}' LIMIT 1`) ||
  sql(
    `INSERT INTO browser_agents (workspace_id, name, token_hash, created_by) VALUES ('${wsId}', 'Roastery laptop', 'check-${Date.now()}', '') RETURNING id`,
  ).split("\n")[0]
const failRun = (why) =>
  sql(
    `INSERT INTO automation_runs (workspace_id, agent_id, status, error, finished_at) VALUES ('${wsId}', '${agent}', 'failed', '${why}', now()) RETURNING id`,
  )
failRun("the supplier price page asked for a captcha")
const forms = (await call(page, "GET", "/api/w/subscribe-forms", undefined, wsId)).body.forms ?? []
const inquiry = forms.find((f) => f.kind === "inquiry" && f.enabled)
const origin = (inquiry.allowed_origins ?? [])[0] ?? "https://nilecoffee.example"
const lead = await fetch(`${base}/api/inquiry/${inquiry.public_key}`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Origin: origin },
  body: JSON.stringify({
    email: `buyer-${Date.now().toString(36)}@mail.example`,
    name: "Hany Saleh",
    company: "Saleh Cafes",
    message: "Quote for 40kg a month",
  }),
})
out.leadStatus = lead.status
const n0 = (await toMe()).length
const first = await waitFor(async () => (await toMe()).slice(n0)[0], 200000, "the batched message")
out.firstMessage = first.text
out.firstBatched =
  /2 updates/.test(first.text) &&
  /Automation failed/.test(first.text) &&
  /New lead: Hany Saleh/.test(first.text)

// ---- a burst of 5 is one message ----
const n1 = (await toMe()).length
for (let i = 1; i <= 5; i++) failRun(`nightly price check ${i} failed`)
const burst = await waitFor(async () => (await toMe()).slice(n1)[0], 200000, "the burst")
await sleep(40000) // one more tick: nothing else may arrive
const afterBurst = (await toMe()).slice(n1)
out.burst = {
  messages: afterBurst.length,
  fiveInOne: /5 updates/.test(burst.text) && (burst.text.match(/• /g) ?? []).length === 5,
}

// ---- quiet hours hold a message until they end ----
const tzNow = new Date()
const mins = tzNow.getUTCHours() * 60 + tzNow.getUTCMinutes()
await put({
  timezone: "UTC",
  quiet_start: (mins + 1440 - 30) % 1440,
  quiet_end: (mins + 60) % 1440,
})
const n2 = (await toMe()).length
failRun("the quiet-hours run failed")
await sleep(95000) // past the window and three ticks
out.quiet = { heldDuringQuiet: (await toMe()).length === n2 }
const held = await call(page, "GET", "/api/w/notify/whatsapp", undefined, wsId)
out.quiet.pendingOnPage = held.body.pending
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-wa-notify=verified]", { timeout: 60000 })
out.quiet.pendingShown = await page.getAttribute(
  "[data-wa-notify-pending]",
  "data-wa-notify-pending",
)
await page.screenshot({
  path: "E:/Sites/fadymondy.com-v2/.setup/shots/MH-411-held-in-quiet-hours-en-light-1440.png",
  fullPage: true,
})
// "Morning": the quiet window ends now.
await put({
  timezone: "UTC",
  quiet_start: (mins + 1440 - 30) % 1440,
  quiet_end: (new Date().getUTCHours() * 60 + new Date().getUTCMinutes()) % 1440,
})
const morning = await waitFor(async () => (await toMe()).slice(n2)[0], 120000, "the held message")
out.quiet.sentWhenQuietEnded = /quiet-hours run failed/.test(morning.text)

// Back to calm settings for the screenshots.
await put({ quiet_start: 22 * 60, quiet_end: 7 * 60, batch_minutes: 5 })

out.pass =
  out.codeArrived &&
  out.wrongCodeRefused &&
  out.verified &&
  out.firstBatched &&
  out.burst.messages === 1 &&
  out.burst.fiveInOne &&
  out.quiet.heldDuringQuiet &&
  out.quiet.pendingOnPage === 1 &&
  out.quiet.sentWhenQuietEnded

// ---- screenshots: the verified settings with the log ----
const shots = await matrix(browser, state, {
  base,
  card: "MH-411",
  name: "delivery",
  url: `/w/${slug}/settings/delivery`,
  ready: "[data-wa-notify=verified]",
})
shots.push(...verifyShots)
out.shotProblems = shots.filter(
  (s) =>
    s.overflow > 0 ||
    s.errors.length ||
    s.rawKeys.length ||
    (s.locale === "ar") !== (s.dir === "rtl") ||
    (s.theme === "dark") !== s.dark,
)
out.shotCount = shots.length
out.pageErrors = errors
console.log(JSON.stringify(out, null, 2))
await browser.close()
