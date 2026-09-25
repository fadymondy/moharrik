// Production check for MH-561 on the demo workspace: a contact can be created from an inbox
// thread with a phone and no email. The contact it creates is unlinked and deleted afterwards.
// Nothing is sent to anyone.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/contact-phone-only-live.mjs https://app.moharrik.com [shots]
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
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "cpo-")), {
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
await page.goto(`${W}/inbox`, { waitUntil: "domcontentloaded" })
await page.waitForSelector("[data-conversation]", { timeout: 30000 })

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

const list = JSON.parse((await api("/api/w/conversations?limit=50")).body).conversations
// a thread with no contact yet; prefer one whose customer gave no email, so nothing pre-fills
const open = list.filter((c) => !c.contact_id && !c.contact)
const conv = open.find((c) => !c.customer_email && !c.email) ?? open[0]
check("conversationWithoutContact", !!conv, list.length)
if (conv) {
  out.channel = conv.channel
  out.conversation = conv.id
  await page.goto(`${W}/inbox?c=${conv.id}`, { waitUntil: "domcontentloaded" })
  await page.locator("[data-crm-action=create]").click({ timeout: 30000 })
  const form = page.locator("[data-form=create-contact]")
  await form.waitFor()
  check(
    "emailNotRequired",
    (await page.locator("#new-contact-email").getAttribute("required")) === null,
  )
  const submit = form.locator("button[type=submit]")
  await page.fill("#new-contact-email", "")
  await page.fill("#new-contact-phone", "")
  check("submitDisabledWhenEmpty", await submit.isDisabled())
  await page.fill("#new-contact-name", "MH-561 phone-only check")
  await page.fill("#new-contact-phone", "+20 100 555 0199")
  check("submitEnabledWithPhoneOnly", !(await submit.isDisabled()))
  if (shots) await page.screenshot({ path: path.join(shots, "mh561-phone-only-en-light-1440.png") })
  await submit.click()
  await form.waitFor({ state: "detached", timeout: 15000 }).catch(() => {})
  check(
    "dialogClosed",
    (await form.count()) === 0,
    await form
      .locator("[role=alert], .text-destructive")
      .allTextContents()
      .catch(() => []),
  )
  const after = JSON.parse((await api(`/api/w/conversations/${conv.id}`)).body)
  const c = after.conversation ?? after
  const contactId = c.contact_id || c.contact?.id
  check("contactLinked", !!contactId, c.contact)
  // the conversation carries only a contact ref; the contact itself has the phone
  const got = contactId ? JSON.parse((await api(`/api/w/contacts/${contactId}`)).body) : null
  const ct = got?.contact ?? got
  out.contact = ct ? { name: ct.name, email: ct.email ?? "", phone: ct.phone ?? "" } : null
  check("contactHasNoEmail", !!ct && !ct.email, out.contact)
  check(
    "contactHasPhone",
    !!ct && (ct.phone ?? "").replace(/\D/g, "").endsWith("1005550199"),
    out.contact,
  )
  // clean up: unlink, then delete the contact this check created
  if (contactId) {
    out.unlink = (
      await api(`/api/w/conversations/${conv.id}`, {
        method: "PATCH",
        body: JSON.stringify({ contact_id: "" }),
      })
    ).status
    // only delete a contact this check made; a reused one (same phone) stays
    const mine = c.contact?.name === "MH-561 phone-only check"
    out.delete = mine
      ? (await api(`/api/w/contacts/${contactId}`, { method: "DELETE" })).status
      : "kept (reused)"
    check("cleanedUp", out.unlink < 300 && (!mine || out.delete < 300), {
      unlink: out.unlink,
      delete: out.delete,
    })
  }
}
check("noPageErrors", errs.length === 0, errs)
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
