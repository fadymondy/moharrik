// Notification center smoke check (companies-check.mjs's shape): create a webhook
// source, deliver a signed test payload by curl-equivalent fetch, confirm it
// lands unread with the right severity, mark it read + saved, filter by source,
// check Arabic at 390px, and prove cross-workspace isolation both at the API and
// against an unknown/disabled source.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import crypto from "node:crypto"

const base = process.argv[2] ?? "https://dev.moharrik.com"

async function newCtx(viewport = { width: 1440, height: 900 }) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "nc-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `nc${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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
  const slug = page.url().split("/w/")[1].split("/")[0]
  return { email, slug }
}

function sign(secret, body) {
  return crypto.createHmac("sha256", secret).update(body).digest("hex")
}

// A generic source signs timestamp + "." + body and sends the timestamp (MH-381).
function genericHeaders(secret, body) {
  const ts = String(Math.floor(Date.now() / 1000))
  return {
    "Content-Type": "application/json",
    "X-Webhook-Timestamp": ts,
    "X-Webhook-Signature": `sha256=${sign(secret, `${ts}.${body}`)}`,
  }
}

const out = {}
const ctxA = await newCtx()
const pageA = ctxA.pages()[0]
const errs = []
pageA.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug: slugA } = await signUpAndEnterWorkspace(pageA, "NC A " + Date.now())
out.slugA = slugA

// ---- create webhook sources, capture each secret from the "shown once" dialog ----
await pageA.goto(`${base}/en/w/${slugA}/notifications/sources`, { waitUntil: "networkidle" })
async function createSource(name, kindLabel) {
  await pageA.click('button:has-text("Add source")')
  await pageA.waitForSelector("#source-name", { timeout: 10000 })
  await pageA.fill("#source-name", name)
  await pageA.click("#source-kind")
  await pageA.getByRole("option", { name: kindLabel, exact: true }).click({ timeout: 5000 })
  await pageA.click("form:has(#source-name) button[type=submit]")
  await pageA.waitForSelector("text=Webhook source created", { timeout: 10000 })
  // CopyField renders the value in a <code>, not an <input>.
  const codeVals = await pageA.evaluate(() =>
    [...document.querySelectorAll("[data-secret-dialog] code")].map(
      (c) => c.textContent?.trim() ?? "",
    ),
  )
  const url = codeVals.find((v) => v.includes("/api/hooks/"))
  const secret = codeVals.find((v) => /^[0-9a-f]{64}$/.test(v))
  await pageA.click('button:has-text("I\'ve saved it")')
  await pageA.waitForTimeout(300)
  const id = url?.split("/api/hooks/")[1]
  // deliver through the browser's base origin, not the public URL shown on the page
  return { url, secret, id, deliverURL: `${base}/api/hooks/${id}` }
}
// Two sources since MH-381 verifies per kind: GitHub signs its way, a generic
// sender signs a timestamp with the body.
const gh = await createSource("GitHub", "GitHub")
const mon = await createSource("Monitor", "Generic (JSON)")
out.gotUrlAndSecret = !!gh.url && !!gh.secret && !!mon.url && !!mon.secret
const { secret, id: sourceId, deliverURL } = gh

// ---- deliver a signed GitHub-shaped payload, and an unsigned/garbage one ----
const ghBody = JSON.stringify({
  action: "opened",
  repository: {
    full_name: "fadymondy/moharrik",
    html_url: "https://github.com/fadymondy/moharrik",
  },
  sender: { login: "octocat" },
})
const sig = sign(secret, ghBody)
const delivered = await fetch(deliverURL, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Hub-Signature-256": `sha256=${sig}`,
    "X-GitHub-Event": "pull_request",
  },
  body: ghBody,
})
out.deliveredStatus = delivered.status

const badSig = await fetch(deliverURL, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Hub-Signature-256": "sha256=deadbeef",
    "X-GitHub-Event": "issues",
  },
  body: JSON.stringify({ title: "forged" }),
})
out.badSignatureStatus = badSig.status

const unknown = await fetch(`${base}/api/hooks/00000000-0000-0000-0000-000000000000`, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Webhook-Signature": "sha256=whatever" },
  body: "{}",
})
out.unknownSourceStatus = unknown.status

// A second, generic-shaped delivery to the generic source, with an explicit
// severity, so severity + filter can be checked.
const genericBody = JSON.stringify({
  title: "Disk almost full",
  severity: "warning",
  actor: "monitor",
})
const generic = await fetch(mon.deliverURL, {
  method: "POST",
  headers: genericHeaders(mon.secret, genericBody),
  body: genericBody,
})
out.genericDeliveredStatus = generic.status
// The same body signed the pre-MH-381 way (body only, no timestamp): a new source refuses it.
const bodyOnly = await fetch(mon.deliverURL, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Webhook-Signature": `sha256=${sign(mon.secret, genericBody)}`,
  },
  body: genericBody,
})
out.genericBodyOnlyStatus = bodyOnly.status

// ---- feed shows both, unread, with the right severity ----
await pageA.goto(`${base}/en/w/${slugA}/notifications`, { waitUntil: "networkidle" })
await pageA.waitForSelector("[data-notification]", { timeout: 10000 })
out.itemsOnFeed = await pageA.evaluate(
  () => document.querySelectorAll("[data-notification]").length,
)
out.bothUnread = await pageA.evaluate(() =>
  [...document.querySelectorAll("[data-notification]")].every(
    (li) => li.getAttribute("data-notification-read") === "false",
  ),
)
out.hasWarningBadge = await pageA.evaluate(() => document.body.innerText.includes("Warning"))
out.hasPRTitle = await pageA.evaluate(
  () =>
    document.body.innerText.includes("pull_request") ||
    document.body.innerText.includes("fadymondy/moharrik"),
)

// mark the first one read, save the second
const rows = await pageA.$$("[data-notification]")
await rows[0].$eval('button[aria-label="Mark read"]', (b) => b.click())
await pageA.waitForTimeout(500)
out.oneMarkedRead = await pageA.evaluate(() =>
  [...document.querySelectorAll("[data-notification]")].some(
    (li) => li.getAttribute("data-notification-read") === "true",
  ),
)
const rows2 = await pageA.$$("[data-notification]")
await rows2[1].$eval('button[aria-label="Save for later"]', (b) => b.click())
await pageA.waitForTimeout(500)

// filter: saved only shows 1
await pageA.click('[data-filter="saved"]')
await pageA.waitForTimeout(500)
out.savedFilterCount = await pageA.evaluate(
  () => document.querySelectorAll("[data-notification]").length,
)
await pageA.click('[data-filter="saved"]')

// mark all read
await pageA.waitForTimeout(300)
await pageA.click('button:has-text("Mark all read")')
await pageA.waitForTimeout(500)
await pageA.click('[data-filter="unread"]')
await pageA.waitForTimeout(500)
out.unreadAfterMarkAll = await pageA.evaluate(
  () => document.querySelectorAll("[data-notification]").length,
)

// disable the source, confirm further deliveries are refused
await pageA.goto(`${base}/en/w/${slugA}/notifications/sources`, { waitUntil: "networkidle" })
await pageA.click('tr:has([data-source-row="GitHub"]) [role="switch"]')
await pageA.waitForTimeout(500)
const disabledDelivery = await fetch(deliverURL, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-Hub-Signature-256": `sha256=${sign(secret, "{}")}`,
    "X-GitHub-Event": "ping",
  },
  body: "{}",
})
out.disabledSourceDeliveryStatus = disabledDelivery.status

await ctxA.close()

// ---- Arabic at 390px, no overflow ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "NC AR " + Date.now())
for (const p of ["notifications", "notifications/sources"]) {
  await pageAr.goto(`${base}/ar/w/${slugAr}/${p}`, { waitUntil: "networkidle" })
  await pageAr.waitForTimeout(500)
  out[`ar390_${p.replace("/", "_")}_overflow`] = await pageAr.evaluate(
    () => document.documentElement.scrollWidth > 390,
  )
}
await ctxAr.close()

// ---- cross-workspace isolation ----
const ctxB = await newCtx()
const pageB = ctxB.pages()[0]
const { slug: slugB } = await signUpAndEnterWorkspace(pageB, "NC B " + Date.now())
await pageB.goto(`${base}/en/w/${slugB}/notifications`, { waitUntil: "networkidle" })
out.workspaceBFeedEmpty = await pageB.evaluate(() =>
  document.body.innerText.includes("No notifications yet"),
)
// Authenticated AS workspace B (its own CSRF token + X-Workspace-ID) reaching for
// workspace A's source id — the real cross-workspace check. A bare fetch with
// neither header only proves the CSRF gate exists, not that isolation holds.
const crossFetchStatus = await pageB.evaluate(
  async ({ sid, ws }) => {
    const csrf = await (await fetch("/api/auth/csrf")).json()
    const r = await fetch(`/api/w/webhook_sources/${sid}`, {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": csrf.csrf_token,
        "X-Workspace-ID": ws,
      },
      body: JSON.stringify({ name: "stolen" }),
    })
    return r.status
  },
  { sid: sourceId, ws: slugB },
)
out.workspaceBPatchWorkspaceASourceStatus = crossFetchStatus
await ctxB.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
