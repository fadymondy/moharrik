// Inbox smoke check: create a chat widget, send a visitor message through the
// PUBLIC widget endpoint, see it in the agent inbox, reply, poll the public
// endpoint and see the reply — plus a wrong-Origin rejection, a disabled widget,
// Arabic at 390px, and cross-workspace isolation.
// NOTE: any page that mounts the workspace event stream (the inbox) never
// reaches Playwright's "networkidle" — the stream is a request that stays open
// on purpose. Those navigations use domcontentloaded and then wait on a
// selector, which is what actually matters here.
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
await pageA.goto(`${base}/en/w/${slugA}/inbox`, { waitUntil: "domcontentloaded" })
await pageA.waitForSelector("[data-conversation]", { timeout: 10000 })
out.conversationsInInbox = await pageA.evaluate(() => document.querySelectorAll("[data-conversation]").length)
out.arrivedUnread = await pageA.evaluate(() => document.querySelector("[data-conversation]")?.getAttribute("data-conversation-unread") === "true")
await pageA.click("[data-conversation]")
await pageA.waitForSelector("[data-messages]", { timeout: 10000 })
out.threadShowsVisitorMessage = await pageA.evaluate(() => document.body.innerText.includes("My invoice is wrong"))

// ---- realtime: a second visitor message appears WITHOUT a poll (MH-135) ----
// The agent tab stays where it is. Requests are counted first, so "it updated"
// cannot be satisfied by a timer that happens to fire: with polling removed,
// an idle tab should issue no /api/w/conversations requests at all until the
// event arrives.
let apiCalls = 0
const countCalls = (req) => {
  if (req.url().includes("/api/w/conversations")) apiCalls++
}
pageA.on("request", countCalls)
await pageA.waitForTimeout(4000)
out.idleConversationRequests = apiCalls

const t0 = Date.now()
await send({ body: "And it is urgent", visitor_token: visitorToken })
await pageA
  .waitForFunction(() => document.body.innerText.includes("And it is urgent"), { timeout: 8000 })
  .then(() => (out.realtimeArrived = true))
  .catch(() => (out.realtimeArrived = false))
out.realtimeLatencyMs = Date.now() - t0
pageA.off("request", countCalls)

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
  // domcontentloaded for the same reason as above: /inbox holds the stream open.
  await pageAr.goto(`${base}/ar/w/${slugAr}/${p}`, { waitUntil: "domcontentloaded" })
  await pageAr.waitForTimeout(1500)
  out[`ar390_${p.replace("/", "_")}_overflow`] = await pageAr.evaluate(() => document.documentElement.scrollWidth > 390)
}
await ctxAr.close()

// ---- cross-workspace isolation ----
const ctxB = await newCtx()
const pageB = ctxB.pages()[0]
const { slug: slugB } = await signUpAndEnterWorkspace(pageB, "IB B " + Date.now())
await pageB.goto(`${base}/en/w/${slugB}/inbox`, { waitUntil: "domcontentloaded" })
// domcontentloaded returns before the list has loaded, so wait for the state to
// render rather than reading an empty body and calling it "not empty".
await pageB.waitForFunction(() => /No conversations yet|data-conversation/.test(document.body.innerHTML), { timeout: 15000 }).catch(() => {})
out.workspaceBInboxEmpty = await pageB.evaluate(() => document.body.innerText.includes("No conversations yet"))
// B asking for A's event stream by id must be refused the same way every
// other workspace route refuses it — a stream is not a second door.
out.workspaceBStreamOfAStatus = await pageB.evaluate(async (ws) => {
  const r = await fetch("/api/w/events", { headers: { Accept: "text/event-stream", "X-Workspace-ID": ws } })
  await r.body?.cancel()
  return r.status
}, slugA)
out.workspaceBSeesNoWidget = await pageB.evaluate(async () => {
  const r = await fetch("/api/w/chat_widgets", { headers: { "X-Workspace-Id": "" } })
  return r.status
})
await ctxB.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
