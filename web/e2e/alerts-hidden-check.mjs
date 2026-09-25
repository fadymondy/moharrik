// MH-378: the desktop notification a member gets while their Moharrik tab is in
// the BACKGROUND — driven over raw CDP, because a page Playwright drives always
// reports document.visibilityState === "visible" (it was tried: minimising the
// window and opening another tab both left it "visible"). A plain Chrome, a real
// second tab in the same window, and the member's tab is genuinely hidden.
//
// --disable-backgrounding-occluded-windows keeps an unattended desktop (other
// windows on top, a locked session) from marking the whole window hidden — the
// hiding under test is the tab switch, not occlusion.
//
//   node e2e/alerts-hidden-check.mjs http://localhost:3378
import { chromium } from "playwright-core"
import { spawn } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import http from "node:http"
import WebSocket from "ws"

const base = process.argv[2] ?? "http://localhost:3378"
const PORT = 9380
const SITE_PORT = 4379
const SITE = `http://127.0.0.1:${SITE_PORT}`
const CHROME = "C:/Program Files/Google/Chrome/Application/chrome.exe"
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const out = {}
const check = (k, v) => {
  out[k] = v
  console.error(
    `${v ? "ok " : "FAIL"} ${k}${typeof v === "boolean" ? "" : " = " + JSON.stringify(v)}`,
  )
}

const proc = spawn(
  CHROME,
  [
    `--remote-debugging-port=${PORT}`,
    `--user-data-dir=${mkdtempSync(path.join(tmpdir(), "mh378-hidden-"))}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-backgrounding-occluded-windows",
    "--window-size=1440,900",
    "about:blank",
  ],
  { stdio: "ignore" },
)
await sleep(3000)

function cdp(url) {
  const ws = new WebSocket(url)
  let id = 0
  const pending = new Map()
  ws.on("message", (m) => {
    const d = JSON.parse(m)
    if (d.id && pending.has(d.id)) {
      pending.get(d.id)(d)
      pending.delete(d.id)
    }
  })
  const ready = new Promise((r) => ws.on("open", r))
  const send = async (method, params = {}) => {
    await ready
    return new Promise((resolve, reject) => {
      const i = ++id
      pending.set(i, (d) =>
        d.error ? reject(new Error(`${method}: ${d.error.message}`)) : resolve(d.result),
      )
      ws.send(JSON.stringify({ id: i, method, params }))
    })
  }
  return { send, close: () => ws.close() }
}

const version = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json()
const browser = cdp(version.webSocketDebuggerUrl)
await browser.send("Browser.grantPermissions", { permissions: ["notifications"], origin: base })
const target = (await (await fetch(`http://127.0.0.1:${PORT}/json`)).json()).find(
  (t) => t.type === "page",
)
const tab = cdp(target.webSocketDebuggerUrl)
await tab.send("Page.enable")
await tab.send("Runtime.enable")

// Records (and still performs) every desktop notification the page raises.
await tab.send("Page.addScriptToEvaluateOnNewDocument", {
  source: `(() => {
    window.__desktop = []
    const N = window.Notification
    if (N) {
      const W = function (title, opts) { window.__desktop.push({ via: "page", title, body: opts && opts.body, tag: opts && opts.tag }); return new N(title, opts) }
      Object.defineProperty(W, "permission", { get: () => N.permission })
      W.requestPermission = (...a) => N.requestPermission(...a)
      window.Notification = W
    }
    if (window.ServiceWorkerRegistration) {
      const show = ServiceWorkerRegistration.prototype.showNotification
      ServiceWorkerRegistration.prototype.showNotification = function (title, opts) { window.__desktop.push({ via: "sw", title, body: opts && opts.body, tag: opts && opts.tag }); return show.call(this, title, opts) }
    }
  })()`,
})

const evaluate = async (expression) => {
  const r = await tab.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  })
  if (r.exceptionDetails)
    throw new Error(r.exceptionDetails.exception?.description ?? "evaluate failed")
  return r.result.value
}
const goto = async (url) => {
  await tab.send("Page.navigate", { url })
  await sleep(500)
  for (let i = 0; i < 60 && (await evaluate("document.readyState")) !== "complete"; i++)
    await sleep(250)
}
const waitFor = async (expression, ms = 15000) => {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await evaluate(expression).catch(() => false)) return true
    await sleep(250)
  }
  return false
}

// ---- a member with a workspace and a chat widget ------------------------------
await goto(`${base}/en/login`)
const setup = await evaluate(`(async () => {
  const csrf = async () => (await fetch("/api/auth/csrf", { credentials: "same-origin" }).then(r => r.json())).csrf_token
  const post = async (p, body, ws) => fetch(p, { method: "POST", credentials: "same-origin",
    headers: Object.assign({ "Content-Type": "application/json", "X-CSRF-Token": await csrf() }, ws ? { "X-Workspace-ID": ws } : {}),
    body: JSON.stringify(body) }).then(async r => ({ status: r.status, body: await r.json().catch(() => null) }))
  const email = "mh378-hidden-" + Date.now() + "@x.test"
  const reg = await post("/api/auth/register", { email, password: "Passw0rd!long-enough" })
  const ws = await post("/api/me/workspaces", { name: "Hidden tab " + Date.now().toString(36) })
  const w = ws.body.workspace
  const widget = await post("/api/w/chat_widgets", { name: "Shop", allowed_origins: ["${SITE}"] }, w.id)
  return { reg: reg.status, slug: w.slug, key: widget.body.widget.public_key }
})()`)
check("member signed up with a workspace and widget", setup.reg === 201 && !!setup.key)

const srv = await new Promise((resolve) => {
  const s = http.createServer((_, res) => {
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
    res.end(
      `<!doctype html><html><body><h1>Shop</h1><script src="${base}/chat/v1.js" data-key="${setup.key}" data-visitor-name="Rana" defer></script></body></html>`,
    )
  })
  s.listen(SITE_PORT, "127.0.0.1", () => resolve(s))
})

// ---- the member is on Contacts, then switches to another tab -----------------
await goto(`${base}/en/w/${setup.slug}/contacts`)
check("bell rendered on contacts", await waitFor(`!!document.querySelector("[data-bell]")`))
check(
  "painting: rAF frames in 1s",
  await evaluate(
    `new Promise(r => { let n = 0; const s = performance.now(); const f = () => { n++; performance.now() - s < 1000 ? requestAnimationFrame(f) : r(n) }; requestAnimationFrame(f) })`,
  ),
)
await sleep(1000) // the event stream is open
await browser.send("Target.createTarget", {
  url: "about:blank",
  newWindow: false,
  background: false,
})
check(
  "member tab is hidden after switching tabs",
  await waitFor(`document.visibilityState === "hidden"`, 5000),
)

// ---- the visitor writes, from another origin -----------------------------------
const vb = await chromium.launch({ executablePath: CHROME, headless: true })
const visitor = await vb.newPage()
await visitor.goto(`${SITE}/`, { waitUntil: "networkidle" })
await visitor.waitForFunction(() => !!document.querySelector("[data-moharrik-chat]"))
await visitor.evaluate(() => {
  const sr = document.querySelector("[data-moharrik-chat]").shadowRoot
  sr.querySelector(".btn").click()
  sr.querySelector(".ta").value = "Are you open on Friday?"
  sr.querySelector(".form").dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }))
})
await visitor.waitForFunction(() =>
  [...document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg")].some(
    (m) => m.textContent.includes("Friday"),
  ),
)

// ---- the hidden tab raised a desktop notification ------------------------------
const raised = await waitFor(`window.__desktop.length > 0`, 10000)
const desktop = await evaluate(`window.__desktop`)
check("desktop notification raised by the hidden tab", raised ? desktop : false)
check(
  "…from Rana, with the message",
  raised && desktop.some((d) => /Rana/.test(d.title) && /Friday/.test(d.body ?? "")),
)
check("still hidden when it fired", await evaluate(`document.visibilityState`))
const onScreen = await evaluate(`(async () => {
  const reg = await navigator.serviceWorker.getRegistration("/")
  return reg ? (await reg.getNotifications()).map(n => ({ title: n.title, body: n.body, tag: n.tag })) : "page Notification (no service worker registered in this profile)"
})()`)
check("on screen", onScreen)

srv.close()
await vb.close()
tab.close()
browser.close()
proc.kill()
console.log(JSON.stringify(out, null, 1))
