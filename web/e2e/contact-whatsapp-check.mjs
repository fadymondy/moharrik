// MH-410 in a real browser: "Message on WhatsApp" on a contact's page.
//
//   node e2e/fakes-whatsapp-smtp.mjs <log-dir>        (Evolution stub on :8914)
//   node e2e/contact-whatsapp-check.mjs http://localhost:3514
//
// The API's EVOLUTION_API_URL must point at the stub: the "WhatsApp" here is a
// local fake, and every seeded phone number is in an unallocated range. No
// real number is linked, paired or messaged.
import { call, launch, matrix, signIn } from "./batch-n-lib.mjs"

const base = (process.argv[2] ?? "http://localhost:3514").replace(/\/$/, "")
const STUB = "http://127.0.0.1:8914"
const stub = (p, body) =>
  fetch(STUB + p, {
    method: body ? "POST" : "GET",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  }).then((r) => r.json())

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
await page.goto(`${base}/en/w/${slug}`, { waitUntil: "domcontentloaded" })

// ---- the workspace's number: linked against the stub, then "scanned" ----
const link = await call(page, "POST", "/api/w/whatsapp/link", {}, wsId)
out.link = link.status
out.connected = (
  await stub("/__ctl/state", { state: "open", wuid: "201055550000@s.whatsapp.net" })
).hook

// ---- two contacts: one who opted in, one who did not ----
// A fresh contact each run, so the first message always opens a new thread.
// The number is in Egypt's unallocated +20 16 range (see e2e/seed-demo.mjs).
async function freshContact(name, optIn) {
  const email = `wa-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}@mail.example`
  const c = (await call(page, "POST", "/api/w/contacts", { email, name }, wsId)).body.contact
  const phone = "+20 16 " + String(Math.floor(1e7 + Math.random() * 8e7))
  await call(page, "PATCH", `/api/w/contacts/${c.id}`, { phone }, wsId)
  if (optIn) await call(page, "POST", `/api/w/contacts/${c.id}/whatsapp/opt-in`, {}, wsId)
  return { ...c, phone }
}
const optedIn = await freshContact("Salma Fawzy", true)
const notOptedIn = await freshContact("Karim Nabil", false)
const third = await freshContact("Mona Adel", true)
out.contacts = { optedIn: optedIn?.name, notOptedIn: notOptedIn?.name }

// ---- no opt-in: the button is disabled and says why ----
await page.goto(`${base}/en/w/${slug}/contacts/${notOptedIn.id}/profile`, {
  waitUntil: "domcontentloaded",
})
await page.waitForSelector("[data-contact-whatsapp]:not([data-contact-whatsapp=loading])", {
  timeout: 60000,
})
out.notOptedIn = {
  state: await page.getAttribute("[data-contact-whatsapp]", "data-contact-whatsapp"),
  buttonDisabled: await page.locator("[data-message-whatsapp]").isDisabled(),
  reason: await page.locator("[data-whatsapp-disabled-reason]").innerText(),
}

// ---- opted in: write, confirm (new thread), sent, and it is in the inbox ----
await stub("/__ctl/reset", {})
await page.goto(`${base}/en/w/${slug}/contacts/${optedIn.id}/profile`, {
  waitUntil: "domcontentloaded",
})
await page.waitForSelector("[data-contact-whatsapp=ready]", { timeout: 60000 })
const text = `Following up on our call about the green-coffee order. ${Date.now().toString(36)}`
await page.click("[data-message-whatsapp]")
await page.fill("#wa-text", text)
await page.click('[data-form="whatsapp-message"] button[type=submit]')
await page.waitForSelector("[data-whatsapp-confirm]", { timeout: 15000 })
out.confirmStepShown = true
out.sentBeforeConfirm = (await stub("/__ctl/log")).sent.length
await page
  .locator("[data-whatsapp-confirm]")
  .getByRole("button", { name: /Yes, send it/ })
  .click()
await page.waitForSelector("[data-whatsapp-sent]", { timeout: 20000 })
const log = await stub("/__ctl/log")
const digits = optedIn.phone.replace(/\D/g, "")
out.stubReceived = log.sent.filter((s) => s.number === digits && s.text === text).length
// Into the inbox thread from the sheet.
await page.click("[data-whatsapp-sent] [data-open-thread]")
await page.waitForURL(/\/inbox\?c=/, { timeout: 20000 })
await page.waitForSelector(`text=${text.slice(0, 40)}`, { timeout: 20000 })
out.inInboxThread = true
await page.screenshot({
  path: "e2e-shots/MH-410-inbox-thread-en-light-1440.png",
})

// A second message continues the thread: no confirm step.
await page.goto(`${base}/en/w/${slug}/contacts/${optedIn.id}/profile`, {
  waitUntil: "domcontentloaded",
})
await page.waitForSelector("[data-contact-whatsapp=ready]", { timeout: 60000 })
await page.click("[data-message-whatsapp]")
await page.fill("#wa-text", "The samples ship tomorrow.")
await page.click('[data-form="whatsapp-message"] button[type=submit]')
await page.waitForSelector("[data-whatsapp-sent]", { timeout: 20000 })
out.secondWithoutConfirm = (await page.locator("[data-whatsapp-confirm]").count()) === 0
out.stubTotal = (await stub("/__ctl/log")).sent.filter((s) => s.number === digits).length

out.pass =
  out.notOptedIn.buttonDisabled &&
  out.notOptedIn.state === "not_opted_in" &&
  out.sentBeforeConfirm === 0 &&
  out.stubReceived === 1 &&
  out.secondWithoutConfirm &&
  out.stubTotal === 2

// ---- screenshots ----
const shots = []
shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-410",
    name: "contact-ready",
    url: `/w/${slug}/contacts/${optedIn.id}/profile`,
    ready: "[data-contact-whatsapp=ready]",
    prepare: async (p) => {
      await p
        .locator("[data-whatsapp-log] summary")
        .click()
        .catch(() => {})
    },
  })),
)
shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-410",
    name: "contact-no-optin",
    url: `/w/${slug}/contacts/${notOptedIn.id}/profile`,
    ready: "[data-whatsapp-disabled-reason]",
  })),
)
shots.push(
  ...(await matrix(browser, state, {
    base,
    card: "MH-410",
    name: "confirm",
    url: `/w/${slug}/contacts/${third.id}/profile`,
    ready: "[data-contact-whatsapp=ready]",
    fullPage: false,
    prepare: async (p, { locale }) => {
      await p.click("[data-message-whatsapp]")
      await p.fill(
        "#wa-text",
        locale === "ar"
          ? "مرحبًا، نتابع طلب البن الأخضر."
          : "Hello, following up on the green-coffee order.",
      )
      await p.click('[data-form="whatsapp-message"] button[type=submit]')
      await p.waitForSelector("[data-whatsapp-confirm]", { timeout: 15000 }).catch(() => {})
    },
  })),
)
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
out.stubSentAfterShots = (await stub("/__ctl/log")).sent.length
console.log(JSON.stringify(out, null, 2))
await browser.close()
