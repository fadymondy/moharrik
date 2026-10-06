// Webhook sources check (MH-381): the kind selector, the per-kind instructions
// in the created-secret dialog, the generic curl example actually working (it
// is run verbatim through bash), a Sentry source taking its Client Secret, and
// a legacy source flagged "replayable" and switched to timestamped signing —
// in light and dark, English at 1440 and Arabic (RTL) at 390.
//
//   node e2e/webhook-sources-check.mjs http://localhost:3131 "<owner DATABASE_URL>"
//
// The database URL is only used to mark one source legacy_signing, which is
// what 30_webhook_signing.sql does to every source that existed before MH-381
// (the API cannot create one, on purpose). Needs psql and bash on PATH.
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { createHmac } from "node:crypto"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3131"
const dbURL = process.argv[3] ?? ""
const shots = process.argv[4] ?? tmpdir()

async function newCtx(viewport, colorScheme) {
  return chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "wh-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme,
  })
}

const out = {}
const fail = (msg) => {
  out.failures = [...(out.failures ?? []), msg]
}
const check = (cond, msg) => {
  if (!cond) fail(msg)
}

const ctx = await newCtx({ width: 1440, height: 900 }, "light")
const page = ctx.pages()[0]
const errs = []
page.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))

// ---- sign up, workspace ----
// WH_EMAIL / WH_PASSWORD reuse an account instead of registering one (register is
// rate-limited per IP, so repeated runs otherwise start failing with a 429).
const email = process.env.WH_EMAIL ?? `wh${Date.now()}@x.test`
const password = process.env.WH_PASSWORD ?? "Passw0rd!long-enough"
await page.goto(`${base}/en/${process.env.WH_EMAIL ? "login" : "register"}`, {
  waitUntil: "networkidle",
})
await page.fill("#email", email)
await page.fill("#password", password)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MH-381 UI " + Date.now())
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]
const sourcesURL = (locale) => `${base}/${locale}/w/${slug}/notifications/sources`

async function pickKind(label) {
  await page.click("#source-kind")
  await page.getByRole("option", { name: label, exact: true }).click()
}

async function createSource(name, kindLabel) {
  await page.click('button:has-text("Add source")')
  await page.waitForSelector("#source-name", { timeout: 10000 })
  await page.fill("#source-name", name)
  await pickKind(kindLabel)
  const hint = await page.textContent("[data-kind-hint]")
  await page.click("form:has(#source-name) button[type=submit]")
  await page.waitForSelector("[data-secret-dialog]", { timeout: 10000 })
  return hint
}

const codes = () =>
  page.$$eval("[data-secret-dialog] code", (els) => els.map((e) => e.textContent ?? ""))

await page.goto(sourcesURL("en"), { waitUntil: "networkidle" })

// ---- generic: timestamped instructions and a curl that works as pasted ----
out.genericHint = await createSource("UI generic", "Generic (JSON)")
check(/5 minutes/.test(out.genericHint), "generic kind hint does not mention the 5-minute window")
out.genericHowTo = await page.textContent('[data-how-to="generic"]')
let [url, secret, curl] = await codes()
check(
  /X-Webhook-Timestamp/.test(curl) && /\$TS\.|%s\.%s/.test(curl),
  "generic curl example is not the timestamped scheme",
)
check(curl.includes(secret) && curl.includes(url), "curl example does not carry the URL and secret")
await page.screenshot({ path: path.join(shots, "wh-generic-dialog-en-light.png") })
// Run the example exactly as a user would paste it.
const curlOut = execFileSync("bash", ["-c", curl + ' -w " HTTP=%{http_code}"'], {
  encoding: "utf8",
})
out.curlExample = curlOut.trim()
check(/HTTP=202/.test(curlOut), "the dialog's curl example did not deliver (" + curlOut + ")")
await page.click('button:has-text("I\'ve saved it")')
const genericURL = url
const genericSecret = secret

// ---- github: GitHub instructions, no curl ----
out.githubHint = await createSource("UI github", "GitHub")
out.githubHowTo = await page.textContent('[data-how-to="github"]')
check(
  /X-Hub-Signature-256/.test(out.githubHowTo),
  "github instructions do not name X-Hub-Signature-256",
)
check((await codes()).length === 2, "github dialog should show URL + secret only, no curl")
await page.click('button:has-text("I\'ve saved it")')

// ---- sentry: no generated secret; paste the Client Secret ----
out.sentryHint = await createSource("UI sentry", "Sentry")
check(/paste/i.test(out.sentryHint), "sentry kind hint does not say the secret is pasted")
out.sentryHowTo = await page.textContent('[data-how-to="sentry"]')
check(
  (await codes()).length === 1,
  "sentry dialog should show only the URL (Sentry makes the secret)",
)
check(
  !(await page.isVisible('[data-secret-dialog] [role="alert"]')),
  "sentry dialog shows a 'secret shown once' warning for a secret it never made",
)
await page.screenshot({ path: path.join(shots, "wh-sentry-dialog-en-light.png") })
await page.click('button:has-text("Later")')
await page.waitForSelector('tr:has([data-source-row="UI sentry"]) [data-no-secret]', {
  timeout: 10000,
})
out.sentryNoSecretFlag = true
await page.click('tr:has([data-source-row="UI sentry"]) button:has-text("Set client secret")')
const clientSecret = "sentry-client-secret-" + Date.now().toString(16) + "abcdef0123456789"
await page.fill('input[id^="client-secret-"]', clientSecret)
await page.click('button:has-text("Save secret")')
await page.waitForSelector('tr:has([data-source-row="UI sentry"]) [data-no-secret]', {
  state: "detached",
  timeout: 10000,
})
out.sentrySecretSaved = true

// ---- legacy: mark the generic source as a pre-MH-381 one, then switch it ----
const genericID = genericURL.split("/api/hooks/")[1]
if (dbURL) {
  execFileSync("psql", [
    dbURL,
    "-Atc",
    `UPDATE webhook_sources SET legacy_signing = true WHERE id = '${genericID}'`,
  ])
  // A body-only delivery, as its old sender would send: accepted, and stamped.
  const body = JSON.stringify({ title: "legacy sender" })
  const sig = createHmac("sha256", genericSecret).update(body).digest("hex")
  const r = await fetch(`${base}/api/hooks/${genericID}`, {
    method: "POST",
    headers: { "X-Webhook-Signature": "sha256=" + sig },
    body,
  })
  out.legacyBodyOnlyStatus = r.status
  check(r.status === 202, "legacy source refused its old body-only signature")

  await page.reload({ waitUntil: "networkidle" })
  const legacy = 'tr:has([data-source-row="UI generic"]) [data-legacy-signing]'
  await page.waitForSelector(legacy, { timeout: 10000 })
  out.legacyText = (await page.textContent(legacy)).replace(/\s+/g, " ").trim()
  check(/Legacy signing, replayable/.test(out.legacyText), "legacy badge text missing")
  check(/Last legacy-signed delivery/.test(out.legacyText), "last legacy delivery line missing")
  // Colour comes from the warn token, in this theme, not a hard-coded value.
  out.legacyBadgeColour = await page.$eval(
    `${legacy} [data-slot="badge"], ${legacy} span`,
    (el) => {
      const cs = getComputedStyle(el)
      const probe = document.createElement("span")
      probe.style.color = "var(--grid-warn-text)"
      document.body.appendChild(probe)
      const want = getComputedStyle(probe).color
      probe.remove()
      return { color: cs.color, token: want, borderColor: cs.borderTopColor }
    },
  )
  check(
    out.legacyBadgeColour.color === out.legacyBadgeColour.token,
    "legacy badge text is not --grid-warn-text in light",
  )
  await page.screenshot({ path: path.join(shots, "wh-legacy-row-en-light.png") })

  await page.click(`${legacy} button:has-text("Switch to timestamped signing")`)
  await page.waitForSelector('[role="alertdialog"]', { timeout: 10000 })
  out.switchDialog = (await page.textContent('[role="alertdialog"]')).replace(/\s+/g, " ").trim()
  await page.click('[role="alertdialog"] button:has-text("Switch")')
  await page.waitForSelector(legacy, { state: "detached", timeout: 10000 })
  out.legacySwitchedOff = true
  const r2 = await fetch(`${base}/api/hooks/${genericID}`, {
    method: "POST",
    headers: { "X-Webhook-Signature": "sha256=" + sig },
    body,
  })
  out.bodyOnlyAfterSwitch = r2.status
  check(r2.status === 401, "body-only signature still accepted after switching off")
  // Re-mark it for the dark / Arabic passes below.
  execFileSync("psql", [
    dbURL,
    "-Atc",
    `UPDATE webhook_sources SET legacy_signing = true WHERE id = '${genericID}'`,
  ])
}
out.pageErrorsLight = errs.slice()
await ctx.close()

// ---- dark, English 1440; Arabic RTL 390 (light + dark) ----
for (const [locale, width, scheme] of [
  ["en", 1440, "dark"],
  ["ar", 390, "light"],
  ["ar", 390, "dark"],
]) {
  const c = await newCtx({ width, height: 900 }, scheme)
  const p = c.pages()[0]
  const perr = []
  p.on("pageerror", (e) => perr.push(String(e).slice(0, 200)))
  await p.goto(`${base}/en/login`, { waitUntil: "networkidle" })
  await p.fill("#email", email)
  await p.fill("#password", password)
  await p.click("button[type=submit]")
  await p.waitForURL(/\/w\/|workspaces/, { timeout: 30000 })
  await p.goto(`${base}/${locale}/w/${slug}/notifications/sources`, { waitUntil: "networkidle" })
  const key = `${locale}-${width}-${scheme}`
  out[key] = await p.evaluate(() => {
    const legacy = document.querySelector("[data-legacy-signing]")
    const badge = legacy?.querySelector("span")
    const probe = document.createElement("span")
    probe.style.color = "var(--grid-warn-text)"
    document.body.appendChild(probe)
    const token = getComputedStyle(probe).color
    probe.remove()
    return {
      dir: document.documentElement.dir,
      legacyText: legacy?.textContent?.replace(/\s+/g, " ").trim().slice(0, 90) ?? null,
      badgeColour: badge ? getComputedStyle(badge).color : null,
      token,
      overflowX: document.documentElement.scrollWidth > window.innerWidth,
    }
  })
  if (scheme && dbURL)
    check(out[key].badgeColour === out[key].token, `legacy badge is not the warn token in ${key}`)
  if (locale === "ar") check(out[key].dir === "rtl", `${key} is not RTL`)
  await p.screenshot({ path: path.join(shots, `wh-sources-${key}.png`), fullPage: true })
  // The create dialog in this locale: kind hint follows the selection.
  await p.click('button:has-text("' + (locale === "ar" ? "إضافة مصدر" : "Add source") + '")')
  await p.waitForSelector("#source-kind", { timeout: 10000 })
  await p.click("#source-kind")
  await p.getByRole("option", { name: "Sentry", exact: true }).click()
  out[key].sentryHint = (await p.textContent("[data-kind-hint]"))?.slice(0, 80)
  await p.screenshot({ path: path.join(shots, `wh-create-${key}.png`) })
  out[key].pageErrors = perr
  await c.close()
}

out.ok = !out.failures && errs.length === 0
console.log(JSON.stringify(out, null, 2))
process.exit(out.ok ? 0 : 1)
