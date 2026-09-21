// Inbox smoke check: create a chat widget, send a visitor message through the
// PUBLIC widget endpoint, see it in the agent inbox, reply, poll the public
// endpoint and see the reply — plus a wrong-Origin rejection, a disabled widget,
// Arabic at 390px, and cross-workspace isolation.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3122"

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ib-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `ib${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
const ctxA = await newCtx()
const pageA = ctxA.pages()[0]
const errs = []
pageA.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug: slugA } = await signUpAndEnterWorkspace(pageA, "IB A " + Date.now())
out.slugA = slugA

// ---- create a chat widget with an allowed origin ----
await pageA.goto(`${base}/en/w/${slugA}/inbox/widgets`, { waitUntil: "networkidle" })
await pageA.click('button:has-text("Add widget")')
await pageA.waitForSelector("#widget-name", { timeout: 10000 })
await pageA.fill("#widget-name", "Marketing site")
await pageA.fill("#widget-origins", base)
await pageA.click('form:has(#widget-name) button[type=submit]')
await pageA.waitForSelector('[data-widget-row="Marketing site"]', { timeout: 10000 })
const embed = await pageA.evaluate(() => [...document.querySelectorAll("code")].map((c) => c.textContent ?? "").find((v) => v.includes("data-key=")) ?? "")
const widgetKey = (embed.match(/data-key="([^"]+)"/) || [])[1]
out.gotWidgetKey = !!widgetKey

const send = (body, origin = base) =>
  fetch(`${base}/api/chat/${widgetKey}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: origin },
    body: JSON.stringify(body),
  })

// ---- a visitor writes ----
const first = await send({ body: "My invoice is wrong", name: "Dana" })
out.visitorSendStatus = first.status
const visitorToken = first.status === 201 ? (await first.json()).visitor_token : null
out.gotVisitorToken = !!visitorToken

// ---- wrong Origin is refused ----
out.wrongOriginStatus = (await send({ body: "from elsewhere" }, "https://evil.test")).status
// ---- unknown key is refused ----
out.unknownKeyStatus = (
  await fetch(`${base}/api/chat/cwk_0000000000000000000000000000000000000000000000000000000000000000/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: base },
    body: JSON.stringify({ body: "x" }),
  })
).status

// ---- it lands in the agent inbox, unread ----
await pageA.goto(`${base}/en/w/${slugA}/inbox`, { waitUntil: "networkidle" })
await pageA.waitForSelector("[data-conversation]", { timeout: 10000 })
out.conversationsInInbox = await pageA.evaluate(() => document.querySelectorAll("[data-conversation]").length)
out.arrivedUnread = await pageA.evaluate(() => document.querySelector("[data-conversation]")?.getAttribute("data-conversation-unread") === "true")
await pageA.click("[data-conversation]")
await pageA.waitForSelector("[data-messages]", { timeout: 10000 })
out.threadShowsVisitorMessage = await pageA.evaluate(() => document.body.innerText.includes("My invoice is wrong"))

// ---- the team replies ----
await pageA.fill("#reply-body", "Sorry about that — fixing it now.")
await pageA.click('[data-form="reply"] button[type=submit]')
await pageA.waitForFunction(() => document.querySelectorAll('[data-message="out"]').length > 0, { timeout: 10000 })
out.replySaved = await pageA.evaluate(() => document.querySelectorAll('[data-message="out"]').length)

// ---- the visitor polls and sees the reply ----
const poll = await fetch(`${base}/api/chat/${widgetKey}/messages?visitor_token=${encodeURIComponent(visitorToken)}`, { headers: { Origin: base } })
out.visitorPollStatus = poll.status
const polled = poll.status === 200 ? await poll.json() : { messages: [] }
out.visitorSeesReply = (polled.messages || []).some((m) => m.direction === "out" && m.body.includes("Sorry about that"))
out.visitorSeesNoStaffEmail = (polled.messages || []).every((m) => !m.author_email)

// ---- another visitor token is refused ----
out.foreignVisitorTokenStatus = (
  await fetch(`${base}/api/chat/${widgetKey}/messages?visitor_token=cvt_dead`, { headers: { Origin: base } })
).status

// ---- closing, then a disabled widget refuses delivery ----
await pageA.click('button:has-text("Close")')
await pageA.waitForTimeout(600)
await pageA.goto(`${base}/en/w/${slugA}/inbox/widgets`, { waitUntil: "networkidle" })
await pageA.click('[data-widget-row="Marketing site"] [role="switch"]')
await pageA.waitForTimeout(600)
out.disabledWidgetStatus = (await send({ body: "after disable" })).status

await ctxA.close()

// ---- Arabic at 390px ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "IB AR " + Date.now())
for (const p of ["inbox", "inbox/widgets"]) {
  await pageAr.goto(`${base}/ar/w/${slugAr}/${p}`, { waitUntil: "networkidle" })
  await pageAr.waitForTimeout(500)
  out[`ar390_${p.replace("/", "_")}_overflow`] = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
}
await ctxAr.close()

// ---- cross-workspace isolation ----
const ctxB = await newCtx()
const pageB = ctxB.pages()[0]
const { slug: slugB } = await signUpAndEnterWorkspace(pageB, "IB B " + Date.now())
await pageB.goto(`${base}/en/w/${slugB}/inbox`, { waitUntil: "networkidle" })
out.workspaceBInboxEmpty = await pageB.evaluate(() => document.body.innerText.includes("No conversations yet"))
out.workspaceBSeesNoWidget = await pageB.evaluate(async () => {
  const r = await fetch("/api/w/chat_widgets", { headers: { "X-Workspace-Id": "" } })
  return r.status
})
await ctxB.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
