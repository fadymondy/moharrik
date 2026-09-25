// Production/live check for MH-584: paste a small vCard 3.0 fixture into the
// contacts vCard import page, preview it, commit it, and confirm the contact
// it created exists — then delete that contact via the API so this check
// leaves nothing behind. Fake data only: an obviously-fake E.164 number and
// an @example.test email, neither of which resolve to a real person.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/vcard-import-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "vcf-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const W = `${base}/en/w/${slug}`

const api = (url, init = {}) =>
  page.evaluate(
    async ([url, init, slug]) => {
      const ws = await (await fetch("/api/me/workspaces", { credentials: "include" })).json()
      const id = ((ws.workspaces ?? ws.items ?? ws).find((w) => w.slug === slug) ?? {}).id
      const headers = { "X-Workspace-ID": id, "Content-Type": "application/json" }
      if (init.method && init.method !== "GET") {
        headers["X-CSRF-Token"] = (
          await (
            await fetch("/api/auth/csrf", { credentials: "include", cache: "no-store" })
          ).json()
        ).csrf_token
      }
      const r = await fetch(url, { credentials: "include", ...init, headers })
      return { status: r.status, body: await r.text() }
    },
    [url, init, slug],
  )

const FAKE_NAME = "MH-584 vCard Check"
const FAKE_EMAIL = "mh584.vcard.check@example.test"
const FAKE_PHONE = "+20100000" + String(Date.now()).slice(-4) // unique-ish, still obviously fake
const fixture = [
  "BEGIN:VCARD",
  "VERSION:3.0",
  `FN:${FAKE_NAME}`,
  `N:Check;MH-584;;;`,
  `TEL;TYPE=CELL:${FAKE_PHONE}`,
  `EMAIL:${FAKE_EMAIL}`,
  `UID:mh584-e2e-${Date.now()}`,
  "END:VCARD",
  "",
].join("\r\n")

await page.goto(`${W}/contacts/vcard`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-testid=vcard-text-input]", { timeout: 30000 })
check("noPageErrorsOnLoad", errs.length === 0, errs)

await page.fill("[data-testid=vcard-text-input]", fixture)
if (shots) await page.screenshot({ path: path.join(shots, "mh584-vcard-pasted-en-light-1440.png") })

await page.click("[data-action=preview-vcard]")
await page.waitForSelector("[data-testid=vcard-preview-table]", { timeout: 15000 })
const newCountText = await page.locator("[data-testid=vcard-new-count]").innerText()
check("previewShowsOneNew", /\b1\b/.test(newCountText), newCountText)
if (shots)
  await page.screenshot({ path: path.join(shots, "mh584-vcard-preview-en-light-1440.png") })

await page.click("[data-action=commit-vcard]")
await page.waitForSelector("[data-testid=vcard-commit-result]", { timeout: 15000 })
const resultText = await page.locator("[data-testid=vcard-commit-result]").innerText()
check("commitReportsOneCreated", /\b1\b/.test(resultText), resultText)
if (shots)
  await page.screenshot({ path: path.join(shots, "mh584-vcard-committed-en-light-1440.png") })

// Confirm the contact actually landed, then clean it up.
const list = JSON.parse((await api(`/api/w/contacts?q=${encodeURIComponent(FAKE_EMAIL)}`)).body)
const contacts = list.contacts ?? list.items ?? []
const created = contacts.find((c) => c.email === FAKE_EMAIL)
check("contactCreatedWithFakeEmail", !!created, contacts)
if (created) {
  out.contact = { id: created.id, name: created.name, email: created.email, phone: created.phone }
  const del = await api(`/api/w/contacts/${created.id}`, { method: "DELETE" })
  check("cleanedUp", del.status < 300, del)
}

check("noPageErrors", errs.length === 0, errs)
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
