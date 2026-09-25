// MH-412 in a real browser: two SMTP services, a campaign sent through B while
// a confirmation email goes through A, verified in each fake provider's log.
//
//   node e2e/fakes-whatsapp-smtp.mjs <log-dir>          (sinks A :2614, B :2615)
//   node e2e/smtp-services-check.mjs http://localhost:3514 <log-dir>
//
// Every address is on .example / mail.example and every "provider" is a local
// sink: nothing leaves the machine.
import { call, fakeLog, launch, matrix, signIn } from "./batch-n-lib.mjs"

const base = (process.argv[2] ?? "http://localhost:3514").replace(/\/$/, "")
const logDir = process.argv[3]
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
const stamp = Date.now().toString(36)

// ---- start clean: remove services from an earlier run (default last) ----
await page.goto(`${base}/en/w/${slug}/settings/email`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-smtp-state]", { timeout: 60000 })
let existing =
  (await call(page, "GET", "/api/w/smtp-services", undefined, wsId)).body.services ?? []
for (const s of [...existing].sort((a, b) => Number(a.is_default) - Number(b.is_default))) {
  await call(page, "DELETE", `/api/w/smtp-services/${s.id}`, undefined, wsId)
}
await page.reload({ waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-smtp-state]", { timeout: 30000 })
out.emptyState = await page.getAttribute("[data-smtp-state]", "data-smtp-state")

// ---- add A and B through the sheet ----
async function addService(name, port, user, pass, from) {
  await page.click("[data-add-service]")
  await page.waitForSelector("[data-service-sheet] #smtp-name")
  await page.fill("#smtp-name", name)
  await page.fill("#smtp-host", "127.0.0.1")
  await page.fill("#smtp-port", String(port))
  await page.click("#smtp-tls")
  await page.getByRole("option", { name: /None/ }).click()
  await page.fill("#smtp-username", user)
  await page.fill("#smtp-password", pass)
  await page.fill("#smtp-from-name", "Nile Coffee")
  await page.fill("#smtp-from-email", from)
  await page.click('[data-form="smtp"] button[type=submit]')
  await page.waitForSelector(`[data-service="${name}"]`, { timeout: 15000 })
}
await addService("Transactional A", 2614, "a-user", "a-pass-SECRET", "hello@nilecoffee.example")
await addService("Bulk B", 2615, "b-user", "b-pass-SECRET", "news@nilecoffee.example")
out.aRoles = await page.$$eval('[data-service="Transactional A"] [data-role]', (els) =>
  els.map((e) => e.getAttribute("data-role")),
)
out.bRoles = await page.$$eval('[data-service="Bulk B"] [data-role]', (els) =>
  els.map((e) => e.getAttribute("data-role")),
)

// The list never carries a password.
const listed = await call(page, "GET", "/api/w/smtp-services", undefined, wsId)
out.noPasswordInList = !JSON.stringify(listed.body).includes("pass-SECRET")
const idA = listed.body.services.find((s) => s.name === "Transactional A").id
const idB = listed.body.services.find((s) => s.name === "Bulk B").id

// ---- test B from the page ----
await page.click("#smtp-test-service")
await page.getByRole("option", { name: "Bulk B" }).click()
await page.fill("#smtp-test-to", `me-${stamp}@mail.example`)
await page.click('[data-form="smtp-test"] button[type=submit]')
await page.waitForSelector('[data-service="Bulk B"] [data-test-ok="yes"]', { timeout: 15000 })
out.bTestedOk = true

// ---- a campaign through B, drafted in the form with the picker ----
const lists = (await call(page, "GET", "/api/w/mail/lists", undefined, wsId)).body.lists
const target = lists.filter((l) => l.subscribed > 0).sort((a, b) => b.subscribed - a.subscribed)[0]
const doiList = lists.find((l) => l.double_opt_in)
out.campaignList = { name: target.name, subscribed: target.subscribed }
await page.goto(`${base}/en/w/${slug}/campaigns`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("#campaign-list", { timeout: 60000 })
await page.click("#campaign-list")
await page.getByRole("option", { name: target.name }).click()
await page.waitForSelector("[data-service-picker]")
await page.click("#campaign-service")
await page.getByRole("option", { name: "Bulk B" }).click()
const subject = `MH-412 check ${stamp}`
await page.fill("#campaign-subject", subject)
await page
  .locator('[data-form="new-campaign"] textarea, [data-form="new-campaign"] [contenteditable=true]')
  .first()
  .fill("Fresh beans this week.")
await page.click('[data-form="new-campaign"] button[type=submit]')
const row = page.locator(`[data-campaign-row="${subject}"]`)
await row.waitFor({ timeout: 15000 })
out.rowSaysViaB =
  (await row.locator("[data-campaign-service]").getAttribute("data-campaign-service")) === "Bulk B"
await row.getByRole("button", { name: /Send/ }).click()
out.sendDialogNamesB = (await page.locator("[role=alertdialog]").innerText()).includes("Bulk B")
await page
  .locator("[role=alertdialog]")
  .getByRole("button", { name: /^Send$/ })
  .click()

// ---- meanwhile, a confirmation email: somebody joins a double-opt-in list ----
const joiner = `joiner-${stamp}@mail.example`
const added = await call(
  page,
  "POST",
  "/api/w/contacts",
  { email: joiner, list_id: doiList.id },
  wsId,
)
out.confirmation = added.body.confirmation

// ---- wait for the send, then read each provider's own log ----
for (let i = 0; i < 60; i++) {
  const c = (await call(page, "GET", "/api/w/campaigns", undefined, wsId)).body.campaigns.find(
    (x) => x.subject === subject,
  )
  if (c && c.status !== "sending" && c.status !== "draft") {
    out.campaign = {
      status: c.status,
      sent: c.sent_count,
      failed: c.failed_count,
      via: c.smtp_service_name,
    }
    break
  }
  await page.waitForTimeout(500)
}
const logA = fakeLog(logDir, "smtp-A.json") ?? []
const logB = fakeLog(logDir, "smtp-B.json") ?? []
const campaignInB = logB.filter((m) => m.subject === subject)
const campaignInA = logA.filter((m) => m.subject === subject)
const confirmInA = logA.filter((m) => m.to === joiner)
const confirmInB = logB.filter((m) => m.to === joiner)
out.providerLogs = {
  B_campaignMails: campaignInB.length,
  B_campaignAuthUser: [...new Set(campaignInB.map((m) => m.user))],
  B_campaignFrom: [...new Set(campaignInB.map((m) => m.from))],
  A_campaignMails: campaignInA.length,
  A_confirmation: confirmInA.map((m) => ({ user: m.user, from: m.from, subject: m.subject })),
  B_confirmation: confirmInB.length,
}
out.pass =
  out.campaign?.status === "sent" &&
  campaignInB.length === target.subscribed &&
  campaignInA.length === 0 &&
  confirmInA.length === 1 &&
  confirmInB.length === 0 &&
  out.noPasswordInList

// ---- screenshots: the services, the edit sheet, the campaign picker ----
out.shots = []
out.shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-412",
    name: "services",
    url: `/w/${slug}/settings/email`,
    ready: "[data-smtp-services]",
  })),
)
out.shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-412",
    name: "edit-sheet",
    url: `/w/${slug}/settings/email`,
    ready: "[data-smtp-services]",
    fullPage: false,
    prepare: async (p) => {
      await p.click('[data-edit-service="Bulk B"]')
      await p.waitForSelector("[data-service-sheet] #smtp-name")
      await p.waitForTimeout(300)
    },
  })),
)
out.shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-412",
    name: "campaigns",
    url: `/w/${slug}/campaigns`,
    ready: "#campaign-list",
    prepare: async (p) => {
      await p.waitForSelector("[data-service-picker]", { timeout: 10000 })
    },
  })),
)
out.shotProblems = out.shots.filter(
  (s) =>
    s.overflow > 0 ||
    s.errors.length ||
    s.rawKeys.length ||
    (s.locale === "ar") !== (s.dir === "rtl") ||
    (s.theme === "dark") !== s.dark,
)
out.shotCount = out.shots.length
delete out.shots
out.pageErrors = errors
console.log(JSON.stringify(out, null, 2))
await browser.close()
void idA
void idB
