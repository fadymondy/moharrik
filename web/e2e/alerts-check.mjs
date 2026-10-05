// MH-378 in a real, painting browser (headed Chrome): a member on the CONTACTS
// page is told the moment a visitor writes through the chat widget embedded on
// a page from another origin — popup, chime, bell badge — and clicking opens
// the thread via ?c=. Then: a desktop notification with the tab hidden, web push
// through the service worker (the Delivery tab's test and a real message with no
// Moharrik tab open), and a webhook item appearing live on the notifications
// page. Screenshots at 1440 and 390, light and dark, en and ar.
//
//   node e2e/alerts-check.mjs http://localhost:3378 http://localhost:8378 <shots dir>
//   node e2e/alerts-hidden-check.mjs http://localhost:3378     (the hidden-tab desktop notification)
import { chromium } from "playwright-core"
import { createHmac } from "node:crypto"
import { mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import http from "node:http"

const base = process.argv[2] ?? "http://localhost:3378"
const apiBase = process.argv[3] ?? "http://localhost:8378"
const shots = process.argv[4] ?? path.join(tmpdir(), "mh378-shots")
mkdirSync(shots, { recursive: true })
const SITE_PORT = 4378
const SITE = `http://127.0.0.1:${SITE_PORT}`
const CHROME = "C:/Program Files/Google/Chrome/Application/chrome.exe"

const out = { checks: {}, shots: [] }
const check = (k, v) => {
  out.checks[k] = v
  console.error(
    `${v ? "ok " : "FAIL"} ${k}${typeof v === "boolean" ? "" : " = " + JSON.stringify(v)}`,
  )
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// Instruments the page WITHOUT replacing behaviour: every call still goes
// through to the real API. It only records that the chime played and that a
// desktop notification was raised, which the page cannot otherwise report.
const instrument = () => {
  window.__alerts = { chimes: 0, desktop: [], pushes: [] }
  const Ctx = window.AudioContext
  if (Ctx) {
    window.AudioContext = class extends Ctx {
      constructor(...a) {
        super(...a)
        window.__alerts.chimes++
      }
    }
  }
  const N = window.Notification
  if (N) {
    const Wrapped = function (title, opts) {
      window.__alerts.desktop.push({ via: "page", title, body: opts?.body, tag: opts?.tag })
      return new N(title, opts)
    }
    Object.defineProperty(Wrapped, "permission", { get: () => N.permission })
    Wrapped.requestPermission = (...a) => N.requestPermission(...a)
    window.Notification = Wrapped
  }
  if (window.ServiceWorkerRegistration) {
    const show = ServiceWorkerRegistration.prototype.showNotification
    ServiceWorkerRegistration.prototype.showNotification = function (title, opts) {
      window.__alerts.desktop.push({ via: "sw", title, body: opts?.body, tag: opts?.tag })
      return show.call(this, title, opts)
    }
  }
  if (navigator.serviceWorker) {
    navigator.serviceWorker.addEventListener("message", (e) => window.__alerts.pushes.push(e.data))
  }
}

async function frames(page) {
  return page.evaluate(
    () =>
      new Promise((r) => {
        let n = 0
        const s = performance.now()
        const f = () => {
          n++
          if (performance.now() - s < 1000) requestAnimationFrame(f)
          else r(n)
        }
        requestAnimationFrame(f)
      }),
  )
}

async function apiCall(page, method, p, body) {
  return page.evaluate(
    async ({ method, p, body, wsId }) => {
      const c = await fetch("/api/auth/csrf", { credentials: "same-origin" }).then((r) => r.json())
      const res = await fetch(p, {
        method,
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": c.csrf_token,
          "X-Workspace-ID": wsId,
        },
        body: body ? JSON.stringify(body) : undefined,
      })
      return { status: res.status, body: await res.json().catch(() => null) }
    },
    { method, p, body, wsId: out.wsId },
  )
}

function servePage(port, key) {
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Rana's shop</title></head>
<body style="font-family:system-ui;padding:2rem"><h1>A customer's own site</h1><p>Served from ${SITE}, not from Moharrik.</p>
<script src="${base}/chat/v1.js" data-key="${key}" data-visitor-name="Rana" defer></script>
</body></html>`
  return new Promise((resolve) => {
    const srv = http.createServer((_, res) => {
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
      res.end(html)
    })
    srv.listen(port, "127.0.0.1", () => resolve(srv))
  })
}

// NB: never "networkidle" on a workspace page — its event stream is a request
// that stays open for as long as the page does (that is the feature).

// ---- the member: headed Chrome, persistent profile (push needs one) ---------
const member = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh378-")), {
  executablePath: CHROME,
  headless: false,
  viewport: { width: 1440, height: 900 },
  // Push registration talks to Google's push service; Playwright's default
  // flag turns that background networking off.
  ignoreDefaultArgs: ["--disable-background-networking"],
  args: ["--window-size=1460,1000"],
})
await member.grantPermissions(["notifications"], { origin: base })
await member.addInitScript(instrument)
const page = member.pages()[0]
const pageErrors = []
page.on("pageerror", (e) => pageErrors.push(String(e).slice(0, 300)))

const email = `mh378-${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "load" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "Rana Retail " + Date.now().toString(36))
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]
out.slug = slug
out.wsId = await page.evaluate(async (slug) => {
  const r = await fetch("/api/me/workspaces", { credentials: "same-origin" }).then((r) => r.json())
  return r.workspaces.find((w) => w.slug === slug).id
}, slug)

const widget = await apiCall(page, "POST", "/api/w/chat_widgets", {
  name: "Rana's shop",
  allowed_origins: [SITE],
})
const key = widget.body.widget.public_key
const hook = await apiCall(page, "POST", "/api/w/webhook_sources", { name: "CI" })
const hookID = hook.body.source.id
const hookSecret = hook.body.secret
const site = await servePage(SITE_PORT, key)

// ---- the visitor: another browser, another origin ---------------------------
const visitorBrowser = await chromium.launch({ executablePath: CHROME, headless: true })
const visitor = await visitorBrowser.newPage()
await visitor.goto(`${SITE}/`, { waitUntil: "networkidle" })
await visitor.waitForFunction(() => !!document.querySelector("[data-moharrik-chat]"), {
  timeout: 15000,
})
await visitor.evaluate(() =>
  document.querySelector("[data-moharrik-chat]").shadowRoot.querySelector(".btn").click(),
)
async function visitorWrites(text) {
  await visitor.evaluate((text) => {
    const sr = document.querySelector("[data-moharrik-chat]").shadowRoot
    sr.querySelector(".ta").value = text
    sr.querySelector(".form").dispatchEvent(
      new Event("submit", { cancelable: true, bubbles: true }),
    )
  }, text)
  // Sent = the widget shows it (it only does once the API accepted it).
  await visitor.waitForFunction(
    (text) =>
      [...document.querySelector("[data-moharrik-chat]").shadowRoot.querySelectorAll(".msg")].some(
        (m) => m.textContent.includes(text),
      ),
    text,
    { timeout: 15000 },
  )
}

// ---- 1. popup + badge + chime on the CONTACTS page ---------------------------
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "load" })
await page.waitForSelector("[data-bell]", { timeout: 15000 })
check("painting: rAF frames in 1s on the member tab", await frames(page))
await sleep(800) // the event stream is open
await visitorWrites("Hi! Is the blue jacket still in stock?")
const toast = await page.waitForSelector(".moharrik-alert", { timeout: 10000 }).catch(() => null)
check("popup appears on the contacts page", !!toast)
const toastText = toast ? await toast.innerText() : ""
check("popup names the sender", /Rana/.test(toastText))
check("popup shows the snippet", /blue jacket/.test(toastText))
await page.waitForSelector("[data-bell-count]", { timeout: 10000 })
check(
  "bell badge count",
  await page.$eval("[data-bell-count]", (e) => e.getAttribute("data-bell-count")),
)
check(
  "chime played (AudioContext created)",
  (await page.evaluate(() => window.__alerts.chimes)) >= 1,
)
check("still on contacts (no navigation)", page.url().endsWith("/contacts"))
await page.screenshot({ path: path.join(shots, "mh378-popup-en-light-1440.png") })
out.shots.push("mh378-popup-en-light-1440.png")

// ---- 2. clicking Open lands on the thread via ?c= -----------------------------
await page
  .click(".moharrik-alert [data-button]")
  .catch(async () => page.click('.moharrik-alert button:has-text("Open")'))
await page.waitForURL(/\/inbox\?c=/, { timeout: 10000 })
const convId = new URL(page.url()).searchParams.get("c")
await page.waitForSelector(`[data-thread="${convId}"]`, { timeout: 10000 })
check("Open lands on /inbox?c=<id> with that thread open", !!convId)
check(
  "the thread shows the message",
  await page.evaluate(() => document.body.innerText.includes("blue jacket")),
)
// Reloading keeps the thread (it lives in the URL now).
await page.reload({ waitUntil: "load" })
check(
  "reload keeps the thread open",
  !!(await page.waitForSelector(`[data-thread="${convId}"]`, { timeout: 10000 }).catch(() => null)),
)

// ---- 3. no alert for the thread being viewed ----------------------------------
const chimesBefore = await page.evaluate(() => window.__alerts.chimes)
await visitorWrites("Size M please")
await page.waitForFunction(() => document.body.innerText.includes("Size M please"), {
  timeout: 10000,
})
await sleep(600)
check("viewing the thread: it updates live", true)
check(
  "viewing the thread: no popup and no chime",
  !(await page.$(".moharrik-alert")) &&
    (await page.evaluate(() => window.__alerts.chimes)) === chimesBefore,
)

// ---- 4. own reply alerts nobody ------------------------------------------------
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "load" })
await sleep(800)
await apiCall(page, "POST", `/api/w/conversations/${convId}/reply`, {
  body: "Yes — size M is in stock.",
})
await sleep(1200)
check("own reply: no popup", !(await page.$(".moharrik-alert")))

// ---- 5. desktop notification while the tab is hidden ---------------------------
// Not here: Playwright keeps every page it drives reporting "visible" (minimised
// or behind another tab), so a hidden tab cannot be produced through it at all.
// e2e/alerts-hidden-check.mjs drives a plain Chrome over raw CDP for that step.

// ---- 6. web push: enable on the Delivery tab, test push -----------------------
await page.goto(`${base}/en/w/${slug}/settings/delivery`, { waitUntil: "load" })
await page.waitForSelector("[data-push-enable], [data-push-disable]", { timeout: 15000 })
await page.click("[data-push-enable]")
const enabled = await page
  .waitForSelector("[data-push-state=on]", { timeout: 30000 })
  .catch(() => null)
check("push enabled on this device", !!enabled)
if (enabled) {
  const endpoint = await page.evaluate(
    async () =>
      (await (await navigator.serviceWorker.ready).pushManager.getSubscription())?.endpoint,
  )
  check("push endpoint host", endpoint ? new URL(endpoint).host : null)
  await page.screenshot({ path: path.join(shots, "mh378-delivery-en-light-1440.png") })
  out.shots.push("mh378-delivery-en-light-1440.png")

  // FCM sometimes answers 410 for a subscription made seconds earlier in an
  // automated Chrome profile. That is the cleanup path working (the row is
  // deleted), and enabling again makes a FRESH subscription (lib/alerts.ts);
  // both are recorded here rather than hidden by a silent retry.
  let toastOk = null
  const attempts = []
  for (let attempt = 1; attempt <= 3 && !toastOk; attempt++) {
    await page.click("[data-push-test]")
    const t = await page
      .waitForSelector(
        '[data-sonner-toast]:has-text("Test sent"), [data-sonner-toast]:has-text("removed as expired")',
        { timeout: 20000 },
      )
      .catch(() => null)
    const text = t ? await t.innerText() : "no answer"
    attempts.push(text.replace(/\s+/g, " ").trim())
    if (/Test sent/.test(text)) toastOk = t
    else {
      await page.waitForSelector("[data-push-enable]", { timeout: 10000 })
      await page.click("[data-push-enable]")
      await page.waitForSelector("[data-push-state=on]", { timeout: 30000 })
      await sleep(1500)
    }
  }
  check("test push attempts", attempts)
  check("test push accepted by the push service", !!toastOk)
  const got = await page
    .waitForFunction(() => window.__alerts.pushes.some((p) => p?.payload?.kind === "test"), null, {
      timeout: 30000,
    })
    .then(() => true)
    .catch(() => false)
  check("test push arrived through the service worker", got)
  // The worker tells open tabs first and shows the notification after, so poll.
  const testShown = await page
    .waitForFunction(
      async () => {
        const list = (
          await (await navigator.serviceWorker.ready).getNotifications({ tag: "moharrik-test" })
        ).map((n) => n.title)
        return list.length ? list : null // [] is truthy: it would end the wait at once
      },
      null,
      { timeout: 10000, polling: 250 },
    )
    .then((h) => h.jsonValue())
    .catch(() => [])
  check("test push shown by the service worker", testShown.length ? testShown : false)
  await page.screenshot({ path: path.join(shots, "mh378-delivery-tested-en-light-1440.png") })
  out.shots.push("mh378-delivery-tested-en-light-1440.png")

  // ---- 7. a real message with NO Moharrik tab open ---------------------------
  const blank = await member.newPage()
  await blank.goto("about:blank")
  await page.close()
  await sleep(800)
  await visitorWrites("Also, do you deliver to Maadi?")
  await sleep(8000)
  const again = await member.newPage()
  await again.goto(`${base}/en/w/${slug}/settings/delivery`, { waitUntil: "load" })
  const pushed = await again.evaluate(async () =>
    (await (await navigator.serviceWorker.ready).getNotifications()).map((n) => ({
      title: n.title,
      body: n.body,
      tag: n.tag,
    })),
  )
  check("real message pushed with no tab open (SW notifications)", pushed)
  check(
    "…including this message",
    pushed.some((n) => /Maadi/.test(n.body) && /Rana/.test(n.title)),
  )
  await blank.close()
  var live = again
} else {
  var live = page
}

// ---- 8. a webhook item appears live on the notifications page -----------------
await live.goto(`${base}/en/w/${slug}/notifications`, { waitUntil: "load" })
await live.evaluate(() => (window.__noReload = true))
await sleep(800)
const body = JSON.stringify({ title: "Deploy failed on main", severity: "error" })
const ts = Math.floor(Date.now() / 1000).toString()
const sig = createHmac("sha256", hookSecret)
  .update(ts + "." + body)
  .digest("hex")
const hres = await fetch(`${apiBase}/api/hooks/${hookID}`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Webhook-Timestamp": ts,
    "X-Webhook-Signature": "sha256=" + sig,
  },
  body,
})
check("webhook accepted", hres.status)
const appeared = await live
  .waitForFunction(() => document.body.innerText.includes("Deploy failed on main"), null, {
    timeout: 10000,
  })
  .then(() => true)
  .catch(() => false)
check(
  "webhook item appears on the notifications page without reload",
  appeared && (await live.evaluate(() => window.__noReload === true)),
)

// ---- 9. screenshots: 1440 / 390 × light / dark × en / ar ---------------------
for (const locale of ["en", "ar"]) {
  for (const theme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      await live.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await live.emulateMedia({ colorScheme: theme })
      await live.goto(`${base}/${locale}/w/${slug}/contacts`, { waitUntil: "load" })
      await live.evaluate((theme) => localStorage.setItem("theme", theme), theme)
      // Theme applied, THEN reload before anything is captured (CLAUDE.md).
      await live.reload({ waitUntil: "load" })
      await live.waitForSelector("[data-bell]")
      await sleep(900)
      await visitorWrites(
        locale === "ar" ? "مرحبا، هل المقاس الكبير متوفر؟" : "Hello again — is size L available?",
      )
      await live.waitForSelector(".moharrik-alert", { timeout: 10000 })
      await sleep(700)
      const a = `mh378-popup-${locale}-${theme}-${width}.png`
      await live.screenshot({ path: path.join(shots, a) })
      out.shots.push(a)
      if (locale === "ar")
        check(
          `ar ${theme} ${width}: html dir`,
          await live.evaluate(() => document.documentElement.dir),
        )
      await live.click("[data-bell] > button")
      await live.waitForSelector("[data-slot=sheet-content] li", { timeout: 10000 })
      await sleep(500)
      const b = `mh378-bell-${locale}-${theme}-${width}.png`
      await live.screenshot({ path: path.join(shots, b) })
      out.shots.push(b)
      if (width === 390) {
        const box = await live.$eval("[data-slot=sheet-content]", (e) => {
          const r = e.getBoundingClientRect()
          return { left: Math.round(r.left), right: Math.round(r.right) }
        })
        check(
          `${locale} ${theme} 390: bell panel inside the viewport`,
          box.left >= 0 && box.right <= 390 ? box : false,
        )
      }
      await live.keyboard.press("Escape")
      await live.goto(`${base}/${locale}/w/${slug}/settings/delivery`, { waitUntil: "load" })
      await live.waitForSelector("[data-section=this-device]")
      await sleep(400)
      const c = `mh378-delivery-${locale}-${theme}-${width}.png`
      await live.screenshot({ path: path.join(shots, c), fullPage: true })
      out.shots.push(c)
      // The visitor's per-thread limit is 12 a minute.
      await sleep(4000)
    }
  }
}

out.pageErrors = pageErrors
site.close()
await visitorBrowser.close()
await member.close()
console.log(JSON.stringify(out, null, 1))
