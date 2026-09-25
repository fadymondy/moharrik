// Contacts → Import/export vCard, the two importer surfaces added by MH-585:
// a Google CSV export (source: "google_csv", no OAuth) previewed/committed
// through the same routes as pasted vCard text, and the "Import from Google"
// panel (connect via OAuth against the FAKE Google, list, Sync now).
//
// Nothing here ever talks to real Google — the panel's connect button is
// followed only as far as the FAKE Google started below, the same fake
// internal/google/googletest plays for MH-439 (google-connect-check.mjs).
//
//   go run ./internal/google/googletest/fakegoogle -addr 127.0.0.1:18441 \
//     -seed-contacts-account contacts@brand.test -seed-contacts-name "Fake Sync" \
//     -seed-contacts-email fake-sync@example.test > fake.log
//   API: GOOGLE_API_BASE=http://127.0.0.1:18441
//        GOOGLE_INTEGRATIONS_CLIENT_ID=fake-client.apps.googleusercontent.com
//        GOOGLE_INTEGRATIONS_CLIENT_SECRET=fake-secret APP_URL=<this web origin>
//
//   node e2e/contacts-google-import.mjs http://localhost:3161 <shots dir>
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3161"
const shots = process.argv[3] ?? "."
const FAKE_HOST = "127.0.0.1:18441"
const RUN = Date.now()

let failures = 0
const ok = (cond, msg) => {
  console.log(`${cond ? "PASS" : "FAIL"} ${msg}`)
  if (!cond) failures++
}

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "gci-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1440, height: 900 },
})
const page = ctx.pages()[0] ?? (await ctx.newPage())
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))

// ---- an owner with a workspace ----------------------------------------------------
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
const vcardURL = (locale) => `${base}/${locale}/w/${slug}/contacts/vcard`

// The in-page fetch helper vcard-import-live.mjs uses, to check a commit's
// contact actually persisted rather than trusting the UI's own state.
const api = (url, init = {}) =>
  page.evaluate(
    async ([url, init, slug]) => {
      const ws = await (await fetch("/api/me/workspaces", { credentials: "include" })).json()
      const id = ((ws.workspaces ?? ws.items ?? ws).find((w) => w.slug === slug) ?? {}).id
      const headers = { "X-Workspace-ID": id, "Content-Type": "application/json" }
      if (init.method && init.method !== "GET") {
        headers["X-CSRF-Token"] = (await (await fetch("/api/auth/csrf", { credentials: "include", cache: "no-store" })).json()).csrf_token
      }
      const r = await fetch(url, { credentials: "include", ...init, headers })
      return { status: r.status, body: await r.text() }
    },
    [url, init, slug],
  )

// ---- 1) Google CSV import: preview -> commit -> persists after reload --------------
await page.goto(vcardURL("en"))
await page.waitForSelector("[data-testid=vcard-text-input]", { timeout: 30000 })
ok(errs.length === 0, "no page errors on load")

const csvEmail = `mh585.csv.${RUN}@example.test`
const csvPhone = "+2010000" + String(RUN).slice(-4)
const csv = ["First Name,Last Name,E-mail 1 - Value,Phone 1 - Value", `Google,Csv,${csvEmail},${csvPhone}`].join("\n")

await page.click("[data-action=source-google-csv]")
await page.fill("[data-testid=vcard-text-input]", csv)
await page.click("[data-action=preview-vcard]")
await page.waitForSelector("[data-testid=vcard-preview-table]", { timeout: 15000 })
ok(/\b1\b/.test(await page.locator("[data-testid=vcard-new-count]").innerText()), "Google CSV preview shows one new contact")
await page.screenshot({ path: path.join(shots, "mh585-csv-preview-en-light-1440.png") })

await page.click("[data-action=commit-vcard]")
await page.waitForSelector("[data-testid=vcard-commit-result]", { timeout: 15000 })
ok(/\b1\b/.test(await page.locator("[data-testid=vcard-commit-result]").innerText()), "Google CSV commit reports one created")

await page.reload({ waitUntil: "domcontentloaded" })
const afterReload = JSON.parse((await api(`/api/w/contacts?q=${encodeURIComponent(csvEmail)}`)).body)
const csvContacts = afterReload.contacts ?? afterReload.items ?? []
const csvCreated = csvContacts.find((c) => c.email === csvEmail)
ok(!!csvCreated, `the Google CSV import persisted after reload (${JSON.stringify(csvContacts)})`)

// ---- 2) "Import from Google" panel: not-connected state ----------------------------
await page.goto(vcardURL("en"))
await page.waitForSelector("[data-testid=google-contacts-panel]", { timeout: 15000 })
ok(await page.getByTestId("google-contacts-empty").isVisible(), "not-connected state shown before any account is linked")
// The admin flag (ws.can) resolves a render cycle after the workspace loads,
// so the button can still be attaching when the empty state above already
// painted -- wait for it rather than sampling isVisible() immediately.
const connectBtn = page.locator("[data-action=connect-google-contacts]")
await connectBtn.waitFor({ state: "visible", timeout: 10000 })
ok(true, "a Connect a Google account button is offered")
await page.screenshot({ path: path.join(shots, "mh585-google-not-connected-en-light-1440.png") })

// The button starts an OAuth flow (GET /api/w/google/connect?product=contacts),
// answering a `url` to navigate to -- never followed to real Google, only to
// the FAKE Google this check started.
await connectBtn.click()
await page.waitForURL((u) => u.host === FAKE_HOST, { timeout: 15000 })
ok(true, "connect sends the browser to the fake Google, never the real one")
const connectURL = new URL(page.url())
ok(connectURL.pathname.includes("/o/oauth2") || connectURL.pathname.includes("/auth"), `oauth start URL looks right (${connectURL.pathname})`)

// ---- 3) consent as the seeded account, then Sync now against the fake --------------
// The callback always lands on the workspace's integrations page (never
// back to vcard -- internal/google's callback route is generic across
// every product), so the panel's own consent round trip is checked from
// there, then the check returns to vcard to drive "Sync now".
const googleEmail = "contacts@brand.test"
await page.fill('input[name="email"]', googleEmail)
await page.click('button[value="allow"]')
await page.waitForURL((u) => u.pathname.endsWith("/settings/integrations"), { timeout: 15000 })
ok(new URL(page.url()).searchParams.get("google") === "connected", "consent comes back as connected")

await page.goto(vcardURL("en"))
await page.waitForSelector(`[data-google-contacts-connection="${googleEmail}"]`, { timeout: 15000 })
ok(true, "the connected account is listed after consent")

await page.click("[data-action=sync-google-contacts]")
await page.waitForSelector("[data-testid=google-contacts-sync-result]", { timeout: 20000 })
const syncText = await page.locator("[data-testid=google-contacts-sync-result]").innerText()
ok(/\b1\b/.test(syncText), `sync now shows the seeded contact synced (${syncText})`)
await page.screenshot({ path: path.join(shots, "mh585-google-synced-en-light-1440.png") })

// ---- 4) the sync actually landed a real contact --------------------------------------
const seeded = JSON.parse((await api(`/api/w/contacts?q=${encodeURIComponent("fake-sync@example.test")}`)).body)
const seededContacts = seeded.contacts ?? seeded.items ?? []
ok(seededContacts.some((c) => c.email === "fake-sync@example.test"), "the synced Google contact actually persisted")

// ---- 5) en/ar x light/dark x 1440/390, no horizontal overflow ------------------------
for (const locale of ["en", "ar"]) {
  for (const scheme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await page.emulateMedia({ colorScheme: scheme })
      // Navigate AFTER switching: a theme change measured in place can read a frozen transition.
      await page.goto(vcardURL(locale))
      await page.waitForSelector("[data-testid=google-contacts-panel]", { timeout: 15000 })
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
      await page.screenshot({ path: path.join(shots, `mh585-${locale}-${scheme}-${width}.png`), fullPage: true })
    }
  }
}

ok(errs.length === 0, "no page errors across the whole run")
await ctx.close()
console.log(failures ? `${failures} FAILED` : "all passed")
process.exit(failures ? 1 : 0)
