// MH-478: Application autofill, with the REAL extension in a REAL Chromium,
// on a local job-application-shaped form.
//
//   - the popup's Application panel previews a mapping from the workspace
//     profile before anything is touched
//   - Fill sets exactly those fields with real input events, on the tab that
//     was already open — no navigation, no new tab
//   - a field the form already had an answer for is left alone
//   - nothing is ever submitted: the submit button is never clicked, the form
//     never navigates
//   - a site NOT on the browser's allowed sites refuses, in words, with no
//     page ever touched
//
//   node e2e/autofill-check.mjs [webBase] [apiBase] [--shots <card>]
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { connectExtension } from "./connect-helper.mjs"

const args = process.argv.slice(2)
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "e2e-shots"
const CHROME =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-autofill-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) =>
    !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?|[\\/]store[\\/]?/.test(p.slice(SRC.length)),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [`${base}/*`, `${apiBase}/*`, "http://*/*", "https://*/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
}

// A local, generic job-application-shaped form — the point of the generalised
// autofill is it needs no board-specific reader.
const PORT = 8798
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><title>Apply — Roaster Co</title>
<form id="app-form">
  <label for="fn">First name</label><input id="fn" name="fn"><br>
  <label for="ln">Last name</label><input id="ln" name="ln"><br>
  <label for="em">Email</label><input id="em" type="email" autocomplete="email"><br>
  <label for="ph">Phone</label><input id="ph" name="ph"><br>
  <label for="li">LinkedIn URL</label><input id="li" autocomplete="url"><br>
  <label for="cur">Current company</label><input id="cur" value="Already Filled Inc"><br>
  <label for="cover">Cover letter</label><textarea id="cover"></textarea><br>
  <input id="cv" type="file"><br>
  <button id="submit-btn" type="submit">Submit application</button>
</form>
<p id="submitted" hidden>SUBMITTED</p>
<script>
  document.getElementById("app-form").addEventListener("submit", (e) => {
    e.preventDefault()
    document.getElementById("submitted").hidden = false
  })
</script>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))
const FORM_URL = `http://roaster.test:${PORT}/apply`

const out = {}
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-autofill-profile-")),
  {
    executablePath: CHROME,
    headless: false,
    viewport: { width: 1280, height: 900 },
    args: [
      `--disable-extensions-except=${EXT}`,
      `--load-extension=${EXT}`,
      "--no-first-run",
      "--host-resolver-rules=MAP *.test 127.0.0.1",
    ],
  },
)
try {
  const worker =
    ctx.serviceWorkers()[0] || (await ctx.waitForEvent("serviceworker", { timeout: 30000 }))
  const extensionID = new URL(worker.url()).host
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  await page.goto(`${base}/en/register`, { waitUntil: "load" })
  await page.fill("#email", `autofill-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Autofill ${Date.now().toString(36)}`)
    await page.click("button[type=submit]")
    await page.waitForURL(/\/w\//, { timeout: 30000 })
  }
  const slug = page.url().split("/w/")[1].split("/")[0]
  const call = async (method, p, data, wsID) => {
    const csrf = (await (await page.request.get(`${base}/api/auth/csrf`)).json()).csrf_token
    const res = await page.request.fetch(`${base}${p}`, {
      method,
      headers: { "X-CSRF-Token": csrf, ...(wsID ? { "X-Workspace-ID": wsID } : {}) },
      data,
    })
    const text = await res.text()
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${text}`)
    return text ? JSON.parse(text) : null
  }
  const wsID = (await call("GET", "/api/me/workspaces")).workspaces.find((w) => w.slug === slug).id

  // The workspace profile, set on Settings → Application profile's own route.
  await page.goto(`${base}/en/w/${slug}/settings/automation-profile`, { waitUntil: "load" })
  await page.waitForSelector("[data-form='profile']", { timeout: 30000 })
  out.settingsPageHasTheForm = true
  await page.fill("#profile-first_name", "Mona")
  await page.fill("#profile-last_name", "Adel")
  await page.fill("#profile-email", "mona@candidate.test")
  await page.fill("#profile-phone", "201000000009")
  await page.fill("#profile-linkedin", "linkedin.com/in/monaadel")
  await page.click("[data-save-profile]")
  await page.waitForSelector("[data-save-profile]:disabled", { timeout: 15000 }).catch(() => {})
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-settings-profile.png` })

  const { online, browser } = await connectExtension({
    ctx,
    extensionID,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "Office Chrome",
    hosts: ["roaster.test"],
  })
  out.connected = online

  // ---- 1. off the allowed sites: the popup refuses, in words, no page touched ----
  const offSite = createServer((_, res) => res.end("off list")).listen(0)
  const offPort = offSite.address().port
  const offPage = await ctx.newPage()
  await offPage.goto(`http://127.0.0.1:${offPort}/`, { waitUntil: "load" })
  await offPage.bringToFront()
  const popupOff = await ctx.newPage()
  await popupOff.setViewportSize({ width: 360, height: 700 })
  await popupOff.goto(`chrome-extension://${extensionID}/popup.html`)
  await popupOff.waitForSelector("[data-autofill]:not([hidden])", { timeout: 20000 })
  await popupOff.click("[data-autofill-preview]")
  await popupOff.waitForFunction(
    () => document.getElementById("autofill-note").textContent.length > 0,
    null,
    { timeout: 15000 },
  )
  const offNote = await popupOff.textContent("#autofill-note")
  out.offListRefused = /not on this browser's allowed sites/.test(offNote)
  out.offListMatchesEmpty = (await popupOff.locator("#autofill-list li").count()) === 0
  await popupOff.close()
  await offPage.close()
  offSite.close()

  // ---- 2. on the local form, the tab already open ----
  const formPage = await ctx.newPage()
  await formPage.goto(FORM_URL, { waitUntil: "load" })
  await formPage.bringToFront()

  const popup = await ctx.newPage()
  await popup.setViewportSize({ width: 360, height: 760 })
  await popup.goto(`chrome-extension://${extensionID}/popup.html`)
  await popup.waitForSelector("[data-autofill]:not([hidden])", { timeout: 20000 })
  if (card) await popup.screenshot({ path: `${SHOTS}/${card}-popup-ready.png` })

  await popup.click("[data-autofill-preview]")
  await popup.waitForSelector("[data-autofill-list] li", { timeout: 20000 })
  const matched = await popup.locator("[data-autofill-list] li").allInnerTexts()
  out.previewedBeforeFilling = matched.length > 0 && (await formPage.inputValue("#fn")) === ""
  out.previewShowsMapping =
    matched.some((t) => /→\s*Mona/.test(t)) &&
    matched.some((t) => /→\s*mona@candidate\.test/.test(t))
  out.previewSkipsAlreadyFilled = !matched.some((t) => /Current company/.test(t))
  if (card) await popup.screenshot({ path: `${SHOTS}/${card}-popup-preview.png` })

  await popup.click("[data-autofill-run]")
  await popup.waitForFunction(
    () => /field\(s\) filled/.test(document.getElementById("autofill-note").textContent),
    null,
    { timeout: 20000 },
  )
  if (card) {
    await popup.screenshot({ path: `${SHOTS}/${card}-popup-filled.png` })
    await popup.emulateMedia({ colorScheme: "dark" })
    await popup.waitForTimeout(200)
    await popup.screenshot({ path: `${SHOTS}/${card}-popup-filled-dark.png` })
    await popup.emulateMedia({ colorScheme: "light" })
  }
  await formPage.bringToFront()
  if (card) await formPage.screenshot({ path: `${SHOTS}/${card}-form-filled.png` })

  out.firstNameFilled = (await formPage.inputValue("#fn")) === "Mona"
  out.lastNameFilled = (await formPage.inputValue("#ln")) === "Adel"
  out.emailFilled = (await formPage.inputValue("#em")) === "mona@candidate.test"
  out.linkedinFilled = (await formPage.inputValue("#li")) === "linkedin.com/in/monaadel"
  out.currentCompanyLeftAlone = (await formPage.inputValue("#cur")) === "Already Filled Inc"
  out.formStillOnSamePage = formPage.url() === FORM_URL
  out.neverSubmitted = await formPage.isHidden("#submitted")

  // ---- 3. never touches file inputs or submit ----
  out.fileInputEmpty =
    (await formPage.evaluate(() => document.getElementById("cv").files.length)) === 0

  // ---- 4. re-preview after manual edit: already-typed fields are skipped ----
  await formPage.fill("#cover", "I would love to roast coffee for you.")
  await popup.reload()
  await popup.waitForSelector("[data-autofill]:not([hidden])", { timeout: 15000 })
  await popup.click("[data-autofill-preview]")
  await popup.waitForFunction(
    () => document.getElementById("autofill-note").textContent.length > 0,
    null,
    { timeout: 15000 },
  )
  out.secondPreviewHasNoMoreMatches =
    (await popup.locator("[data-autofill-list] li").count()) === 0 ||
    (await popup.textContent("#autofill-note")).includes("Nothing")
  // ---- 5. the new Settings page: en/ar x light/dark x 1440/390 ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({ executablePath: CHROME })
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({
            storageState: state,
            viewport: { width, height: 900 },
            colorScheme: theme,
          })
          const p = await c.newPage()
          await p.goto(`${base}/${locale}/w/${slug}/settings/automation-profile`, {
            waitUntil: "load",
          })
          await p.waitForSelector("[data-form='profile']", { timeout: 30000 })
          await p.waitForTimeout(600)
          await p.screenshot({
            path: `${SHOTS}/${card}-settings-${locale}-${theme}-${width}.png`,
            fullPage: true,
          })
          out[`settings-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(
            () => document.documentElement.scrollWidth - innerWidth === 0,
          )
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  await ctx.close().catch(() => {})
  site.close()
}

console.log(JSON.stringify(out, null, 2))
const failed = Object.entries(out).filter(([k, v]) => v === false || k === "error")
if (failed.length) {
  console.error("FAILED:", failed.map(([k]) => k).join(", "))
  process.exit(1)
}
