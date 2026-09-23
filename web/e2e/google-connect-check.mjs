// Settings → Integrations → Google, end to end against the FAKE Google (MH-439).
//
// Nothing here talks to Google. The fake (internal/google/googletest/fakegoogle)
// plays the consent screen: pick the account, untick scopes, allow or cancel.
// Its log prints every revoke, which is how "disconnect revokes" is checked
// from outside the app.
//
//   go run ./internal/google/googletest/fakegoogle -addr 127.0.0.1:18439 > fake.log
//   API: GOOGLE_API_BASE=http://127.0.0.1:18439 GOOGLE_INTEGRATIONS_CLIENT_ID=fake-client.apps.googleusercontent.com
//        GOOGLE_INTEGRATIONS_CLIENT_SECRET=fake-secret APP_URL=<this web origin>
//   node e2e/google-connect-check.mjs http://localhost:3439 <shots dir> <fake.log> <owner DATABASE_URL>
import { chromium } from "playwright-core"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3439"
const shots = process.argv[3] ?? "."
const fakeLog = process.argv[4] ?? ""
const RUN = Date.now()
const FAKE = "127.0.0.1:18439"

let failures = 0
const ok = (cond, msg) => {
  console.log(`${cond ? "PASS" : "FAIL"} ${msg}`)
  if (!cond) failures++
}
const revokes = () => (fakeLog ? (readFileSync(fakeLog, "utf8").match(/REVOKED/g) ?? []).length : -1)

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "g-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())

// ---- an owner with a workspace --------------------------------------------------
await page.goto(`${base}/en/login`)
const owner = `owner-${RUN}@brand.test`
const slug = await page.evaluate(async (email) => {
  const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
  const post = (u, b) =>
    fetch(u, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify(b),
    })
  await post("/api/auth/register", { email, password: "correct-horse-battery" })
  const ws = await (await post("/api/me/workspaces", { name: `Brand Co ${email.split("@")[0].slice(-6)}` })).json()
  return ws.workspace.slug
}, owner)
const url = (locale) => `${base}/${locale}/w/${slug}/settings/integrations`

async function consent({ email, untick = [] }) {
  await page.waitForURL((u) => u.host === FAKE, { timeout: 15000 })
  await page.fill('input[name="email"]', email)
  for (const s of untick) await page.uncheck(`input[value="${s}"]`)
  await page.click('button[value="allow"]')
  await page.waitForURL((u) => u.pathname.endsWith("/settings/integrations"), { timeout: 15000 })
}

// ---- empty page ------------------------------------------------------------------
await page.goto(url("en"))
await page.waitForSelector('[data-google-configured="true"]')
ok((await page.locator("[data-connect]").count()) === 3, "three Connect buttons, one per product")
ok(await page.getByText("No Google accounts connected").isVisible(), "empty state says no accounts")
await page.screenshot({ path: `${shots}/mh439-empty-en-light-1440.png`, fullPage: true })

// ---- connect Analytics -------------------------------------------------------------
await page.click('[data-connect="analytics"]')
await consent({ email: "marketing@brand.test" })
await page.waitForSelector('[data-google-result="connected"]')
ok((await page.locator('[data-google-result="connected"]').innerText()).includes("Google Analytics is connected"), "banner: Analytics connected")
ok(
  await page
    .waitForURL((u) => !u.search.includes("google="), { timeout: 5000 })
    .then(
      () => true,
      () => false,
    ),
  "the result is cleared from the address bar",
)
await page.waitForSelector('[data-google-connection="marketing@brand.test"]')
ok((await page.locator('[data-google-connection="marketing@brand.test"]').innerText()).includes("Google Analytics"), "listed with its product")

// ---- connect YouTube with revenue, another account ---------------------------------
await page.getByRole("checkbox", { name: /revenue/i }).click()
ok((await page.getByRole("checkbox", { name: /revenue/i }).getAttribute("aria-checked")) === "true", "revenue checkbox ticks")
await page.click('[data-connect="youtube"]')
await consent({ email: "channel@brand.test" })
await page.waitForSelector('[data-google-connection="channel@brand.test"]')
const yt = await page.locator('[data-google-connection="channel@brand.test"]').innerText()
ok(yt.includes("YouTube") && yt.includes("Revenue"), "YouTube listed with revenue")
ok((await page.locator("[data-google-connection]").count()) === 2, "two accounts listed")

// ---- an unticked scope comes back as a readable error, and is revoked ----------------
let before = revokes()
await page.click('[data-connect="search_console"]')
await consent({ email: "seo@brand.test", untick: ["https://www.googleapis.com/auth/webmasters"] })
await page.waitForSelector('[data-google-result="scope"]')
const scopeMsg = await page.locator('[data-google-result="scope"]').innerText()
ok(scopeMsg.includes("Search Console needs every box"), `readable error: "${scopeMsg.trim()}"`)
ok((await page.locator('[data-google-connection="seo@brand.test"]').count()) === 0, "the refused account is not listed")
await page.waitForTimeout(800)
ok(fakeLog === "" || revokes() === before + 1, "the refused grant was revoked at the fake Google")
await page.screenshot({ path: `${shots}/mh439-error-scope-en-light-1440.png`, fullPage: true })

// ---- cancel at Google ------------------------------------------------------------------
await page.click('[data-connect="analytics"]')
await page.waitForURL((u) => u.host === FAKE)
await page.click('button[value="deny"]')
await page.waitForSelector('[data-google-result="access_denied"]')
ok(true, "cancel at Google comes back as access_denied")

// ---- reauth → Reconnect ------------------------------------------------------------------
if (process.argv[5]) {
  execFileSync("docker", [
    "exec",
    "pg",
    "psql",
    "-U",
    "postgres",
    "-d",
    new URL(process.argv[5]).pathname.slice(1),
    "-c",
    `UPDATE google_connections SET status='reauth', auth_error='google token endpoint: 400 invalid_grant' WHERE email='marketing@brand.test'`,
  ])
  await page.goto(url("en"))
  const row = page.locator('[data-google-connection="marketing@brand.test"]')
  await row.waitFor()
  ok((await row.getAttribute("data-status")) === "reauth", "reauth shown")
  ok(await row.getByRole("button", { name: "Reconnect" }).isVisible(), "Reconnect offered")
  await page.screenshot({ path: `${shots}/mh439-reauth-en-light-1440.png`, fullPage: true })
  await row.getByRole("button", { name: "Reconnect" }).click()
  await page.waitForURL((u) => u.host === FAKE)
  ok(new URL(page.url()).searchParams.get("login_hint") === "marketing@brand.test", "reconnect pre-selects the account")
  await consent({ email: "marketing@brand.test" })
  await page.waitForSelector('[data-google-result="connected"]')
  ok((await page.locator('[data-google-connection="marketing@brand.test"]').getAttribute("data-status")) === "ok", "reconnect clears reauth")
}

// ---- the screenshot matrix -------------------------------------------------------------------
for (const locale of ["en", "ar"]) {
  for (const scheme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await page.emulateMedia({ colorScheme: scheme })
      // Navigate AFTER switching: a theme change measured in place can read a frozen transition.
      await page.goto(url(locale))
      await page.waitForSelector("[data-google-connection]")
      const html = await page.evaluate(() => ({
        dark: document.documentElement.classList.contains("dark"),
        dir: document.documentElement.dir,
      }))
      ok(
        html.dark === (scheme === "dark") && html.dir === (locale === "ar" ? "rtl" : "ltr"),
        `${locale}/${scheme}/${width}: theme and direction applied`,
      )
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      ok(overflow <= 0, `${locale}/${scheme}/${width}: no horizontal page overflow (${overflow}px)`)
      // The table can scroll inside its own box without the page overflowing, so check the
      // row's action itself is on screen: a Disconnect you have to find by scrolling is a bug.
      const action = await page.locator("[data-google-connection]").first().locator("button").last().boundingBox()
      ok(action && action.x >= 0 && action.x + action.width <= width, `${locale}/${scheme}/${width}: the row's Disconnect is on screen`)
      await page.screenshot({
        path: `${shots}/mh439-${locale}-${scheme}-${width}.png`,
        fullPage: true,
      })
    }
  }
}

// ---- disconnect revokes ----------------------------------------------------------------------
await page.setViewportSize({ width: 1440, height: 900 })
await page.emulateMedia({ colorScheme: "light" })
await page.goto(url("en"))
const row = page.locator('[data-google-connection="channel@brand.test"]')
await row.waitFor()
before = revokes()
await row.getByRole("button", { name: "Disconnect" }).click()
const dialog = page.getByRole("alertdialog")
ok((await dialog.innerText()).includes("Disconnect channel@brand.test?"), "disconnect asks for confirmation")
// The dialog animates in: wait until it has finished before the picture, or it is half transparent.
await page.waitForFunction(() => {
  const d = document.querySelector('[role="alertdialog"]')
  return d && getComputedStyle(d).opacity === "1" && !d.hasAttribute("data-starting-style")
})
await page.screenshot({ path: `${shots}/mh439-disconnect-confirm-en-light-1440.png` })
await dialog.getByRole("button", { name: "Disconnect" }).click()
await page.getByText("Disconnected, and access revoked at Google.").waitFor()
await row.waitFor({ state: "detached" })
ok(true, "disconnected: toast shown and the row is gone")
await page.waitForTimeout(800)
ok(fakeLog === "" || revokes() === before + 1, `the fake Google saw the revoke (${before} → ${revokes()})`)
await page.screenshot({
  path: `${shots}/mh439-after-disconnect-en-light-1440.png`,
  fullPage: true,
})

await ctx.close()
console.log(failures ? `${failures} FAILED` : "all passed")
process.exit(failures ? 1 : 0)
