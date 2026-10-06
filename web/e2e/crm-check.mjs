// CRM smoke check via dev.moharrik.com (or a base passed as argv[2]): companies, contacts,
// deals board (move), timeline (note+task), Arabic 390px overflow, and cross-workspace isolation.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://dev.moharrik.com"

async function newCtx(viewport = { width: 1440, height: 900 }) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "crm-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport,
    colorScheme: "dark",
  })
  return ctx
}

async function signUpAndEnterWorkspace(page, wsName) {
  const email = `crm${Date.now()}${Math.random().toString(36).slice(2, 6)}@x.test`
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

const out = {}
const ctxA = await newCtx()
const pageA = ctxA.pages()[0]
const errs = []
pageA.on("pageerror", (e) => errs.push(String(e).slice(0, 200)))
const { slug: slugA } = await signUpAndEnterWorkspace(pageA, "CRM A " + Date.now())
out.slugA = slugA

// ---- companies ----
await pageA.goto(`${base}/en/w/${slugA}/companies`, { waitUntil: "networkidle" })
out.addCompanyFormPresent = await pageA.evaluate(
  () => document.querySelector("[data-open=add-company]") != null,
)
await pageA.click('[data-open="add-company"]')
await pageA.fill("#company-name", "Acme Corp")
await pageA.fill("#company-domain", "acme.test")
await pageA.click("[data-form=add-company] button[type=submit]")
await pageA.waitForSelector('[data-company-row="Acme Corp"]', { timeout: 10000 })
out.companyCreated = true
await pageA.click('[data-company-row="Acme Corp"] a')
await pageA.waitForURL(/\/companies\/[^/]+$/, { timeout: 10000 })
const companyUrl = pageA.url()
const companyId = companyUrl.split("/companies/")[1]
// MH-1157: the record is tabbed: the form lives on Details, the timeline on Activity.
await pageA.goto(`${companyUrl}/details`, { waitUntil: "networkidle" })
await pageA.waitForSelector('[data-form="edit-company"]', { timeout: 10000 })
out.companyDetailLoaded = true
await pageA.fill("#edit-company-industry", "Software")
await pageA.click('[data-form="edit-company"] button[type=submit]')
await pageA.waitForTimeout(600)

// log a note + task on the company timeline
await pageA.goto(`${companyUrl}/activity`, { waitUntil: "networkidle" })
await pageA.click("[data-kind=note]")
await pageA.fill("#activity-body", "First contact made")
await pageA.click("[data-form=activity] button[type=submit]")
await pageA.waitForSelector("li[data-activity=note]", { timeout: 10000 })
await pageA.click("[data-kind=task]")
await pageA.fill("#activity-body", "Follow up next week")
await pageA.click("[data-form=activity] button[type=submit]")
await pageA.waitForSelector("li[data-activity=task]", { timeout: 10000 })
out.timelineHasNoteAndTask = await pageA.evaluate(
  () =>
    !!document.querySelector("li[data-activity=note]") &&
    !!document.querySelector("li[data-activity=task]"),
)
await pageA.click("[data-open-tasks] input[type=checkbox]")
await pageA
  .waitForFunction(() => !document.querySelector("[data-open-tasks]"), { timeout: 10000 })
  .catch(() => {})
out.taskMarkedDone = await pageA.evaluate(() => !document.querySelector("[data-open-tasks]"))

// ---- contacts: add one, link to the company ----
// MH-1156: lists live in a sideover opened from the header.
await pageA.goto(`${base}/en/w/${slugA}/contacts`, { waitUntil: "networkidle" })
await pageA.click('[data-open="lists"]')
await pageA.click('[data-open="create-list"]')
await pageA.fill("#list-name", "Prospects")
await pageA.click("[data-form=create-list] button[type=submit]")
await pageA.waitForSelector('[data-list-row="Prospects"]', { timeout: 10000 })
await pageA.keyboard.press("Escape")
await pageA.waitForSelector("[data-lists-sheet]", { state: "detached" })
await pageA.click('[data-open="add-contact"]')
await pageA.fill("#contact-email", "jane@acme.test")
await pageA.fill("#contact-name", "Jane Doe")
await pageA.click("[data-form=add-contact] button[type=submit]")
await pageA.waitForSelector('[data-contact-row="jane@acme.test"]', { timeout: 10000 })
await pageA.click('[data-contact-row="jane@acme.test"] a')
await pageA.waitForURL(/\/contacts\/[^/]+$/, { timeout: 10000 })
await pageA.goto(`${pageA.url()}/details`, { waitUntil: "networkidle" })
await pageA.waitForSelector('[data-form="edit-contact"]', { timeout: 10000 })
out.contactCompanySelectPresent = await pageA.evaluate(
  () => document.querySelector("#edit-contact-company") != null,
)
// A Base UI Select, not a native <select> — open it and click the option by text.
await pageA.click("#edit-contact-company")
await pageA.getByRole("option", { name: "Acme Corp" }).click({ timeout: 5000 })
await pageA.click('[data-form="edit-contact"] button[type=submit]')
await pageA.waitForTimeout(600)
out.contactLinkedToCompany = await pageA.evaluate(
  () => document.querySelector("#edit-contact-company")?.textContent?.includes("Acme") ?? false,
)

// ---- deals: create, verify on board, move via the "Move to" menu ----
await pageA.goto(`${base}/en/w/${slugA}/deals`, { waitUntil: "networkidle" })
await pageA.click('button:has-text("New deal")')
await pageA.waitForSelector("[data-form=new-deal]", { timeout: 10000 })
await pageA.fill("#new-deal-title", "Acme renewal")
await pageA.fill("#new-deal-value", "1500")
await pageA.click("#new-deal-company")
await pageA.getByRole("option", { name: "Acme Corp" }).click({ timeout: 5000 })
await pageA.click("[data-form=new-deal] button[type=submit]")
await pageA.waitForSelector('[data-deal-card="Acme renewal"]', { timeout: 10000 })
out.dealCreatedOnBoard = true
const stagesBefore = await pageA.evaluate(() =>
  [...document.querySelectorAll("[data-stage-id]")].map((s) => s.getAttribute("data-stage-id")),
)
const firstStage = stagesBefore[0]
const secondStage = stagesBefore[1]
const stageName = await pageA.evaluate(
  (id) => document.querySelector(`[data-stage-id="${id}"]`)?.getAttribute("data-stage-name"),
  secondStage,
)
await pageA.click(
  `[data-deal-card="Acme renewal"][data-deal-stage="${firstStage}"] [data-move-menu]`,
)
await pageA
  .getByRole("menuitem", { name: stageName ?? "" })
  .click({ timeout: 5000 })
  .catch(async () => {
    const items = await pageA.$$("[role=menuitem]")
    if (items.length) await items[items.length - 1].click()
  })
await pageA.waitForTimeout(800)
out.dealMoved = await pageA.evaluate(
  (id) => !!document.querySelector(`[data-deal-card="Acme renewal"][data-deal-stage="${id}"]`),
  secondStage,
)

// pipeline manage dialog opens with the current stages
await pageA.click('button:has-text("Edit pipeline")')
await pageA.waitForSelector("[data-stage-row]", { timeout: 10000 })
out.pipelineEditorStageRows = await pageA.evaluate(
  () => document.querySelectorAll("[data-stage-row]").length,
)
await pageA.keyboard.press("Escape")

// company detail should now list the deal
await pageA.goto(companyUrl, { waitUntil: "networkidle" })
out.companyShowsDeal = await pageA.evaluate(() => document.body.innerText.includes("Acme renewal"))

await ctxA.close()

// ---- Arabic 390px overflow ----
const ctxAr = await newCtx({ width: 390, height: 844 })
const pageAr = ctxAr.pages()[0]
const { slug: slugAr } = await signUpAndEnterWorkspace(pageAr, "AR Check " + Date.now())
for (const p of ["companies", "contacts", "deals"]) {
  await pageAr.goto(`${base}/ar/w/${slugAr}/${p}`, { waitUntil: "networkidle" })
  await pageAr.waitForTimeout(500)
  out[`ar390_${p}_overflow`] = await pageAr.evaluate(
    () => document.documentElement.scrollWidth > 390,
  )
}
await ctxAr.close()

// ---- cross-workspace isolation ----
const ctxB = await newCtx()
const pageB = ctxB.pages()[0]
const { slug: slugB } = await signUpAndEnterWorkspace(pageB, "CRM B " + Date.now())
await pageB.goto(`${base}/en/w/${slugB}/companies`, { waitUntil: "networkidle" })
out.workspaceBSeesNoCompanies = await pageB.evaluate(
  () => !document.body.innerText.includes("Acme Corp"),
)
const crossStatus = await pageB.evaluate(async (id) => {
  const r = await fetch(`/api/w/companies/${id}`, { headers: { "X-Workspace-Id": "" } })
  return r.status
}, companyId)
out.workspaceBApiStatusForWorkspaceACompanyNoHeader = crossStatus
await ctxB.close()

out.pageErrors = errs
console.log(JSON.stringify(out, null, 1))
