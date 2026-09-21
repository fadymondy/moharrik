// The chat widget script, driven on a page served from a DIFFERENT origin —
// which is the whole point: the Origin header the API checks is the customer's
// site, not ours. Serves a tiny page on :4123, embeds the widget, holds a
// two-way conversation, then checks the refusal path from a non-allowed origin.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import http from "node:http"

const base = process.argv[2] ?? "http://localhost:3122"
const SITE_PORT = 4123
const EVIL_PORT = 4124

function servePage(port, key) {
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Customer site</title></head>
<body><h1>A customer's own site</h1>
<script src="${base}/chat/v1.js" data-key="${key}" defer></script>
</body></html>`
  return new Promise((resolve) => {
    const srv = http.createServer((_, res) => {
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
      res.end(html)
    })
    srv.listen(port, "127.0.0.1", () => resolve(srv))
  })
}

async function newCtx() {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "wg-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1280, height: 900 },
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `wg${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
  return page.url().split("/w/")[1].split("/")[0]
}

const out = {}
const ctx = await newCtx()
const agent = ctx.pages()[0]
const slug = await signUpAndEnterWorkspace(agent, "WG " + Date.now())

// A widget that allows ONLY the customer site's origin.
await agent.goto(`${base}/en/w/${slug}/inbox/widgets`, { waitUntil: "networkidle" })
await agent.click('button:has-text("Add widget")')
await agent.waitForSelector("#widget-name", { timeout: 10000 })
await agent.fill("#widget-name", "Customer site")
await agent.fill("#widget-origins", `http://127.0.0.1:${SITE_PORT}`)
await agent.click('form:has(#widget-name) button[type=submit]')
await agent.waitForSelector('[data-widget-row="Customer site"]', { timeout: 10000 })
const embed = await agent.evaluate(() => [...document.querySelectorAll("code")].map((c) => c.textContent ?? "").find((v) => v.includes("data-key=")) ?? "")
const key = (embed.match(/data-key="([^"]+)"/) || [])[1]
out.widgetKey = !!key
out.embedPointsAtScript = /\/chat\/v1\.js/.test(embed)

const site = await servePage(SITE_PORT, key)
const evil = await servePage(EVIL_PORT, key)

// ---- the visitor, on their own site ----
const visitor = await ctx.newPage()
const visitorErrs = []
visitor.on("pageerror", (e) => visitorErrs.push(String(e).slice(0, 200)))
await visitor.goto(`http://127.0.0.1:${SITE_PORT}/`, { waitUntil: "networkidle" })
await visitor.waitForFunction(() => !!document.querySelector("[data-moharrik-chat]"), { timeout: 10000 })
out.widgetMounted = true
out.usesShadowRoot = await visitor.evaluate(() => !!document.querySelector("[data-moharrik-chat]")?.shadowRoot)

const launcher = () => visitor.evaluate(() => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".btn")?.textContent?.trim())
out.launcherLabel = await launcher()
await visitor.evaluate(() => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".btn").click())
await visitor.waitForTimeout(300)
out.panelOpened = await visitor.evaluate(() => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".panel").classList.contains("open"))

await visitor.evaluate(() => {
  const sr = document.querySelector("[data-moharrik-chat]").shadowRoot
  sr.querySelector(".ta").value = "Hello, my order is late"
  sr.querySelector(".form").dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }))
})
await visitor.waitForFunction(
  () => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg").length > 0,
  { timeout: 15000 },
)
out.visitorMessageShown = await visitor.evaluate(
  () => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".msg")?.textContent,
)
out.visitorTokenStored = await visitor.evaluate(() => Object.keys(localStorage).some((k) => k.startsWith("moharrik.chat.")))

// ---- the agent sees it and replies ----
await agent.goto(`${base}/en/w/${slug}/inbox`, { waitUntil: "networkidle" })
await agent.waitForSelector("[data-conversation]", { timeout: 15000 })
await agent.click("[data-conversation]")
await agent.waitForSelector("[data-messages]", { timeout: 10000 })
out.agentSeesVisitorMessage = await agent.evaluate(() => document.body.innerText.includes("my order is late"))
await agent.fill("#reply-body", "It ships today — sorry about the wait.")
await agent.click('[data-form="reply"] button[type=submit]')
await agent.waitForFunction(() => document.querySelectorAll('[data-message="out"]').length > 0, { timeout: 10000 })

// ---- the widget polls and shows the reply ----
await visitor.waitForFunction(
  () => [...document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg")].some((m) => m.textContent.includes("ships today")),
  { timeout: 20000 },
)
out.visitorSeesReply = true

// ---- a reload resumes the same thread from localStorage ----
await visitor.reload({ waitUntil: "networkidle" })
await visitor.evaluate(() => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".btn").click())
await visitor.waitForFunction(
  () => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg").length >= 2,
  { timeout: 20000 },
)
out.resumedAfterReload = await visitor.evaluate(
  () => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg").length,
)

// ---- the same widget on a NON-allowed origin is refused, visibly ----
const intruder = await ctx.newPage()
await intruder.goto(`http://127.0.0.1:${EVIL_PORT}/`, { waitUntil: "networkidle" })
await intruder.waitForFunction(() => !!document.querySelector("[data-moharrik-chat]"), { timeout: 10000 })
await intruder.evaluate(() => {
  const sr = document.querySelector("[data-moharrik-chat]").shadowRoot
  sr.querySelector(".btn").click()
  sr.querySelector(".ta").value = "let me in"
  sr.querySelector(".form").dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }))
})
await intruder.waitForTimeout(2500)
out.wrongOriginShowsError = await intruder.evaluate(() => {
  const e = document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".err")
  return e && !e.hidden ? e.textContent : ""
})
out.wrongOriginPostedNothing = await intruder.evaluate(
  () => document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg").length === 0,
)

out.visitorPageErrors = visitorErrs
site.close()
evil.close()
await ctx.close()
console.log(JSON.stringify(out, null, 1))
