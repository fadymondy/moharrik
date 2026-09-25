// MH-379 on the demo workspace: create a contact from a chat (prefilled from the visitor),
// see the chat in the contact's timeline, open a deal with the transcript as its first note.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
const base = process.argv[2] ?? "https://app.moharrik.com",
  shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "icl-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
const W = `${base}/en/w/${slug}`
await page.goto(`${W}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })
const n = await page.locator("[data-conversation]").count()
out.threads = []
let picked = -1
for (let i = 0; i < n; i++) {
  await page.locator("[data-conversation]").nth(i).click()
  await page.waitForSelector("[data-thread-crm]", { timeout: 20000 })
  await page.waitForTimeout(400)
  const st = await page.evaluate(() => ({
    contact: document.querySelector("[data-crm-contact]")?.getAttribute("data-crm-contact") ?? null,
    auto: !!document.querySelector("[data-crm-auto]"),
    deal: !!document.querySelector("[data-crm-deal]"),
    canCreate: !!document.querySelector('[data-crm-action="create"]'),
    dealEnabled: !document.querySelector('[data-crm-action="deal"]')?.disabled,
    subject: document.querySelector("[data-thread-subject]")?.textContent?.slice(0, 60),
  }))
  out.threads.push(st)
  if (picked < 0 && st.canCreate && st.dealEnabled) picked = i
}
check("foundUnlinkedThread", picked >= 0, out.threads)
if (picked >= 0) {
  await page.locator("[data-conversation]").nth(picked).click()
  await page.waitForSelector('[data-crm-action="create"]')
  await page.click('[data-crm-action="create"]')
  await page.waitForSelector('form[data-form="create-contact"]')
  out.prefill = {
    name: await page.inputValue("#new-contact-name"),
    email: await page.inputValue("#new-contact-email"),
    phone: await page.inputValue("#new-contact-phone"),
  }
  check("prefilled", !!(out.prefill.name || out.prefill.email || out.prefill.phone), out.prefill)
  if (!out.prefill.email) await page.fill("#new-contact-email", `visitor${Date.now()}@example.com`)
  if (!out.prefill.name) await page.fill("#new-contact-name", "Demo visitor")
  if (shots) await page.screenshot({ path: path.join(shots, "create-contact.png") })
  await page.click('form[data-form="create-contact"] button[type=submit]')
  await page.waitForSelector("[data-crm-contact]:not([data-crm-contact=''])", { timeout: 15000 })
  const cid = await page.getAttribute(
    "[data-crm-contact]:not([data-crm-contact=''])",
    "data-crm-contact",
  )
  check("contactLinked", !!cid)
  // deal
  await page.click('[data-crm-action="deal"]')
  await page.waitForSelector('form[data-form="open-deal"]')
  await page.click('form[data-form="open-deal"] button[type=submit]')
  await page.waitForSelector("[data-crm-deal]", { timeout: 15000 })
  const did = await page.getAttribute("[data-crm-deal]", "data-crm-deal")
  check("dealOpened", !!did)
  if (shots) await page.screenshot({ path: path.join(shots, "thread-linked.png") })
  // contact timeline
  await page.goto(`${W}/contacts/${cid}`, { waitUntil: "domcontentloaded" })
  await page.waitForSelector("[data-timeline-conversation]", { timeout: 20000 }).catch(() => {})
  check(
    "contactTimelineShowsChat",
    (await page.locator("[data-timeline-conversation]").count()) > 0,
  )
  if (shots)
    await page.screenshot({ path: path.join(shots, "contact-timeline.png"), fullPage: true })
  // deal: transcript as first note
  await page.goto(`${W}/deals/${did}`, { waitUntil: "domcontentloaded" })
  await page.waitForTimeout(2500)
  const body = await page.evaluate(() => document.body.innerText)
  check("dealHasTranscript", /transcript|conversation|محادثة/i.test(body), body.slice(0, 400))
  if (shots) await page.screenshot({ path: path.join(shots, "deal.png"), fullPage: true })
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? "FAILED: " + fail.join(", ") : "ALL CHECKS PASSED")
await ctx.close()
process.exit(fail.length ? 1 : 0)
