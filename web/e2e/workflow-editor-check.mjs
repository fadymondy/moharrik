// MH-400 + MH-403, in a real Chromium with the real extension.
//
// MH-400 — a recorded workflow is opened in the editor and changed there:
//   1. The operator RECORDS a task on a page (the extension's recorder, from
//      trusted input): tick a box, type a message, press Send.
//   2. The site is redesigned: Send's test id changes, so the recorded
//      selector no longer finds it.
//   3. In the editor, that step's selector is RE-PICKED by clicking the live
//      page: the extension opens it, the click is swallowed, and the selector
//      comes back into the field.
//   4. Two steps are REORDERED by dragging, and a wait step is added with the
//      form — no JSON anywhere.
//   5. Saved as v2, then TEST-RUN from the editor: each row shows its step's
//      progress live (a row is seen "running", then every row "ok"), and the
//      page reports the task was done.
//   6. v1 is RESTORED — as v3, with v1's steps exactly.
//
// MH-403 — the schedule form:
//   7. It offers the saved workflows by name and asks for their values; a
//      secret is typed hidden.
//   8. Presets build the cron; the next five times are shown in the
//      workspace's time zone before saving.
//   9. The saved schedule shows the plain value and "password: set" — the
//      secret is never sent back — and "Run now" runs the workflow with the
//      secret filled in, while the run record keeps it redacted.
//
//   node e2e/workflow-editor-check.mjs [webBase] [apiBase]   (defaults :3506 / :8506)
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { execFileSync } from "node:child_process"
import { connectExtension } from "./connect-helper.mjs"

const web = (process.argv[2] ?? "http://localhost:3506").replace(/\/$/, "")
const apiBase = (process.argv[3] ?? "http://localhost:8506").replace(/\/$/, "")
const here = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(process.env.EXT_SRC ?? path.resolve(here, "../../extension"))
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const APP_DB = process.env.APP_DB ?? "moharrik_f"

// A copy of the extension whose manifest may reach the local hosts without
// Chrome's per-site prompt (which a check cannot click). Everything else is
// the real extension.
const EXT = mkdtempSync(path.join(tmpdir(), "ext-400-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) => !/[\\/](node_modules|test|dist)([\\/]|$)/.test(path.relative(SRC, p) ? "/" + path.relative(SRC, p) : ""),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = ["http://localhost/*", "http://127.0.0.1/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
}

// ---- the site, which gets redesigned halfway through ----
let redesigned = false
let reports = []
const PAGE_PORT = Number(process.env.PAGE_PORT ?? 8797)
const PAGE = `http://127.0.0.1:${PAGE_PORT}`
const REPORT = `<script>
  function report(data) { fetch("/report", { method: "POST", body: JSON.stringify(data) }) }
</script>`
const composePage = () => `<!doctype html><title>Compose</title>${REPORT}
  <style>body{font:15px/1.5 system-ui;margin:24px} .actions{margin-top:12px}</style>
  <label><input type="checkbox" id="agree"> I agree to the terms</label><br>
  <textarea id="message" name="message" rows="3"></textarea>
  <div class="actions">
    <button type="button" class="btn">Cancel</button>
    <button type="button" class="btn" data-testid="${redesigned ? "compose-send" : "send-button"}">Send</button>
  </div>
  <p id="out">not sent</p>
  <script>
    document.querySelector(".actions").addEventListener("click", (e) => {
      const b = e.target.closest("button"); if (!b || b.textContent !== "Send") return
      const text = "sent: " + document.getElementById("message").value + (document.getElementById("agree").checked ? " (agreed)" : "")
      document.getElementById("out").textContent = text
      report({ out: text })
    })
  </script>`
const loginPage = `<!doctype html><title>Login</title>${REPORT}
  <input id="user" name="user"><input id="pw" name="pw" type="password"><button id="login" type="button">Log in</button>
  <script>document.getElementById("login").addEventListener("click", () =>
    report({ user: document.getElementById("user").value, pw: document.getElementById("pw").value }))</script>`
const site = createServer((req, res) => {
  const url = new URL(req.url, PAGE)
  if (req.method === "POST" && url.pathname === "/report") {
    let body = ""
    req.on("data", (c) => (body += c))
    req.on("end", () => {
      try {
        reports.push(JSON.parse(body))
      } catch {}
      res.end("ok")
    })
    return
  }
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" })
  res.end(url.pathname === "/login" ? loginPage : composePage())
})
await new Promise((r) => site.listen(PAGE_PORT, "127.0.0.1", r))

const out = { checks: {} }
const check = (name, ok, detail) => {
  out.checks[name] = ok ? "PASS" : `FAIL${detail !== undefined ? ": " + JSON.stringify(detail).slice(0, 500) : ""}`
}
process.on("exit", () => console.log(JSON.stringify(out, null, 1)))
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let ctx
try {
  ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "ext-400-profile-")), {
    executablePath: process.env.EXT_CHROME ?? "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe",
    headless: false,
    viewport: { width: 1440, height: 1000 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
  })
  let worker = ctx.serviceWorkers()[0]
  if (!worker) worker = await ctx.waitForEvent("serviceworker", { timeout: 30000 })
  const extensionID = new URL(worker.url()).host
  out.extensionVersion = await worker.evaluate(() => chrome.runtime.getManifest().version)

  // The API through the web origin, sharing the browser's cookies, so the
  // pages opened below are signed in as the same person.
  const http = ctx.request
  const csrf = async () => (await (await http.get(`${web}/api/auth/csrf`)).json()).csrf_token
  const call = async (method, p, data, wsID) => {
    const headers = { "X-CSRF-Token": await csrf() }
    if (wsID) headers["X-Workspace-ID"] = wsID
    const res = await http.fetch(`${web}${p}`, { method, headers, data })
    const text = await res.text()
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${text.slice(0, 300)}`)
    return text ? JSON.parse(text) : null
  }
  await call("POST", "/api/auth/register", { email: `mh400-${Date.now()}@x.test`, password: "Passw0rd!long-enough" })
  const { workspace } = await call("POST", "/api/me/workspaces", { name: `MH-400 ${Date.now()}` })
  out.workspace = workspace.slug
  // The workspace's own zone, which the schedule form must use by default.
  execFileSync("docker", ["exec", "pg", "psql", "-U", "postgres", "-d", APP_DB, "-c", `UPDATE workspaces SET timezone = 'Africa/Cairo' WHERE id = '${workspace.id}'`])

  // ---- connect through the extension's own popup (MH-478) ----
  const { browser: connected, online, popup } = await connectExtension({
    ctx, extensionID, site: web, api: apiBase, call, workspaceID: workspace.id,
    name: "MH-400 Chromium", hosts: ["127.0.0.1"],
  })
  const browser = connected
  check("extension paired and online", online)
  if (!online) throw new Error("never came online")
  await sleep(1000)

  // ---- 1. record ----
  const site1 = await ctx.newPage()
  await site1.goto(`${PAGE}/compose`)
  await site1.bringToFront()
  const started = await popup.evaluate(() => chrome.runtime.sendMessage({ type: "record.start" }))
  check("recording started", started?.ok === true, started)
  await site1.waitForTimeout(500)
  await site1.click("#agree")
  await site1.click("#message")
  await site1.keyboard.type("hello from the recorder")
  await site1.waitForTimeout(300)
  await site1.click('[data-testid="send-button"]')
  await site1.waitForTimeout(600)
  const stopped = await popup.evaluate(() => chrome.runtime.sendMessage({ type: "record.stop" }))
  check("the recording was saved as a workflow", !!stopped?.saved?.workflow_id, stopped)
  const workflowID = stopped.saved.workflow_id
  await site1.close()
  const v1 = (await call("GET", `/api/w/workflows/${workflowID}`, undefined, workspace.id)).workflow
  out.recorded = v1.steps
  check("the recording is version 1", v1.version === 1)

  // ---- 2. the site changes under the recording ----
  redesigned = true

  // ---- open it in the editor ----
  const page = await ctx.newPage()
  await page.goto(`${web}/en/w/${workspace.slug}/automation`)
  await page.click(`[data-workflow] [data-edit-workflow]`)
  await page.waitForSelector('[data-form="workflow-editor"][data-version="1"]', { timeout: 60000 })
  check("the recorded workflow opens in the visual editor, one form per step", (await page.locator("[data-step-row]").count()) === v1.steps.length)
  check("no JSON textarea in the editor", (await page.locator('[data-form="workflow-editor"] textarea').count()) === 0)
  await page.screenshot({ path: `${SHOTS}/MH-400-editor-opened-en-light-1440.png`, fullPage: true })

  const rows = async () =>
    page.$$eval("[data-step-row]", (els) =>
      els.map((el) => ({
        command: el.getAttribute("data-step-command"),
        status: el.getAttribute("data-step-status"),
        inputs: [...el.querySelectorAll("input:not([type=checkbox])")].map((i) => i.value),
      })),
    )

  // ---- 3. re-pick the Send step's selector on the live page ----
  const before = await rows()
  const sendIndex = before.findIndex((r) => r.command === "click" && r.inputs.some((v) => v.includes("send-button")))
  check("the recorded Send step used the old selector", sendIndex > 0, before)
  await page.locator(`[data-step-row="${sendIndex}"] [data-pick]`).click()
  await page.waitForSelector("[data-pick-popover]")
  const prefilled = await page.inputValue("[data-pick-popover] input")
  check("the picker offers the step's page", prefilled.startsWith(`${PAGE}/compose`), prefilled)
  const [pickTab] = await Promise.all([ctx.waitForEvent("page", { timeout: 20000 }), page.click("[data-pick-start]")])
  await pickTab.waitForLoadState("domcontentloaded")
  await pickTab.waitForFunction(() => !!document.getElementById("__moh_pick_overlay__"), undefined, { timeout: 20000 })
  reports = []
  await pickTab.click('[data-testid="compose-send"]') // a trusted click, as a person's
  await page.bringToFront()
  await page.waitForFunction(
    (i) => [...document.querySelectorAll(`[data-step-row="${i}"] input`)].some((x) => x.value.includes("compose-send")),
    sendIndex,
    { timeout: 30000 },
  )
  const picked = (await rows())[sendIndex].inputs.find((v) => v.includes("compose-send"))
  out.picked = picked
  check("re-picked on the live page: the new selector is in the field", !!picked, await rows())
  await sleep(500)
  check("the picking click never reached the page", !reports.some((r) => r.out), reports)
  check("the picking tab was closed afterwards", pickTab.isClosed())

  // ---- 4. reorder by dragging, and add a step with the form ----
  const agreeIndex = before.findIndex((r) => r.command === "click" && r.inputs.some((v) => v.includes("agree")))
  const typeIndex = before.findIndex((r) => r.command === "type")
  const handle = page.locator(`[data-step-row="${typeIndex}"] [data-drag-handle]`)
  const target = page.locator(`[data-step-row="${agreeIndex}"]`)
  const hb = await handle.boundingBox()
  const tb = await target.boundingBox()
  await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2)
  await page.mouse.down()
  await page.mouse.move(hb.x + hb.width / 2, hb.y - 10, { steps: 5 })
  await page.mouse.move(hb.x + hb.width / 2, tb.y + 8, { steps: 15 })
  await page.mouse.up()
  await sleep(400)
  const reordered = await rows()
  check("dragging the type step above the checkbox step reordered them", reordered[agreeIndex].command === "type" && reordered[agreeIndex + 1].inputs.some((v) => v.includes("agree")), reordered)

  // A wait before Send, written with the form, so the live progress has a step
  // slow enough to be seen running.
  await page.click("[data-add-step]")
  const last = (await rows()).length - 1
  await page.locator(`[data-step-row="${last}"] button[role="combobox"], [data-step-row="${last}"] [data-slot="select-trigger"]`).first().click()
  await page.getByRole("option", { name: "Wait", exact: true }).click()
  await page.fill(`[data-step-row="${last}"] [data-field="ms"] input`, "2500")
  // Move it up above Send with the arrow buttons (the keyboard path).
  await page.locator(`[data-step-row="${last}"] button[aria-label="Move up"]`).click()
  const edited = await rows()
  out.editedOrder = edited.map((r) => r.command)
  check("the wait step was added and moved before Send", edited[edited.length - 2].command === "wait" && edited[edited.length - 1].inputs.some((v) => v.includes("compose-send")), edited)

  // ---- 5. save as v2, then test-run ----
  await page.click("[data-save]")
  await page.waitForSelector('[data-form="workflow-editor"][data-version="2"]', { timeout: 20000 })
  const v2 = (await call("GET", `/api/w/workflows/${workflowID}`, undefined, workspace.id)).workflow
  check("saved as v2", v2.version === 2 && v2.steps.some((s) => s.command === "wait") && JSON.stringify(v2.steps).includes("compose-send"), v2.steps)

  reports = []
  await page.click("[data-test-run]")
  const seen = new Set()
  for (let i = 0; i < 120; i++) {
    const r = await rows()
    r.forEach((x, n) => x.status && seen.add(`${n}:${x.status}`))
    const status = await page.getAttribute("[data-test-status]", "data-test-status").catch(() => null)
    if (status && !["queued", "running"].includes(status)) break
    await sleep(250)
  }
  const afterTest = await rows()
  out.progressSeen = [...seen]
  check("live progress: a step was shown running while the run went", [...seen].some((s) => s.endsWith(":running")), out.progressSeen)
  check("the test run finished ok, every row ok", (await page.getAttribute("[data-test-status]", "data-test-status")) === "ok" && afterTest.every((r) => r.status === "ok"), afterTest)
  await sleep(500)
  check("the page saw the task done, in the new order", reports.some((r) => r.out === "sent: hello from the recorder (agreed)"), reports)
  await page.screenshot({ path: `${SHOTS}/MH-400-editor-tested-en-light-1440.png`, fullPage: true })

  // ---- 6. restore v1 ----
  await page.click('[data-restore="1"]')
  await page.click("[data-restore-confirm]")
  await page.waitForSelector('[data-form="workflow-editor"][data-version="3"]', { timeout: 20000 })
  const v3 = (await call("GET", `/api/w/workflows/${workflowID}`, undefined, workspace.id)).workflow
  const { versions } = await call("GET", `/api/w/workflows/${workflowID}/versions`, undefined, workspace.id)
  check("restoring v1 made v3 with v1's steps exactly", v3.version === 3 && JSON.stringify(v3.steps) === JSON.stringify(v1.steps), { v1: v1.steps, v3: v3.steps })
  check("history kept all three: restored, edited, recorded", versions.map((v) => `${v.version}:${v.note}`).join(",") === "3:restored,2:edited,1:recorded", versions.map((v) => `${v.version}:${v.note}`))
  check("the editor shows v1's steps again", (await page.locator("[data-step-row]").count()) === v1.steps.length)
  await page.screenshot({ path: `${SHOTS}/MH-400-editor-restored-en-light-1440.png`, fullPage: true })

  // ---- 7-9. the schedule form ----
  const password = `pw-${Date.now()}`
  await call(
    "POST",
    "/api/w/workflows",
    {
      name: "Morning login",
      steps: [
        { command: "navigate", args: { url: `${PAGE}/login` } },
        { command: "type", args: { selector: "#user", text: "{{user}}" } },
        { command: "type", args: { selector: "#pw", text: "{{password}}" } },
        { command: "click", args: { selector: "#login" } },
      ],
      params: [
        { name: "user", label: "Account", required: true },
        { name: "password", label: "Password", required: true, secret: true },
      ],
    },
    workspace.id,
  )
  await page.goto(`${web}/en/w/${workspace.slug}/automation`)
  // The guided flow: setup folds into a status row once a browser is paired
  // and may go somewhere, and a schedule is added from its section.
  await page.waitForSelector("[data-browser-status]", { timeout: 60000 })
  check("with a browser paired and allowed somewhere, setup folds into one status row", (await page.locator('[data-flow-step="install"]').count()) === 0)
  await page.locator("[data-new-schedule]").first().click()
  await page.waitForSelector('form[data-form="schedule"]', { timeout: 60000 })
  const form = page.locator('form[data-form="schedule"]')
  await form.scrollIntoViewIfNeeded()
  // The newest workflow is first: Morning login.
  check("the form offers saved workflows by name", (await page.textContent("#sc-workflow")).includes("Morning login"))
  check("it asks for the workflow's values; the secret is a password field", (await page.getAttribute("#sc-val-password", "type")) === "password" && (await page.locator("#sc-val-user").count()) === 1)
  await page.fill("#sc-name", "Weekday login")
  await page.fill("#sc-val-user", "fady")
  await page.fill("#sc-val-password", password)
  // Weekdays at 07:00 is the default preset; the preview is in Cairo time.
  await page.waitForSelector('[data-preview="valid"] [data-next-runs] li:nth-child(5)', { timeout: 20000 })
  const preview = await page.$$eval("[data-next-runs] li", (els) => els.map((e) => e.textContent))
  out.preview = preview
  const previewZone = await page.textContent('[data-preview="valid"]')
  check(
    "the next five runs are shown before saving, weekdays at 07:00 in the workspace's zone",
    preview.length === 5 && preview.every((p) => /07:00/.test(p) && !/Saturday|Sunday/.test(p)) && previewZone.includes("Africa/Cairo"),
    { preview, previewZone },
  )
  // A custom expression that is not one yet says so, and blocks saving.
  await page.locator("#sc-preset").click()
  await page.getByRole("option", { name: "Custom (cron)" }).click()
  await page.fill("#sc-cron", "0 7 *")
  await page.waitForSelector('[data-preview="invalid"]', { timeout: 10000 })
  check("a half-typed cron is explained, and Add is off", await form.locator('button[type="submit"]').isDisabled())
  await page.fill("#sc-cron", "30 6 * * 1-5")
  await page.waitForSelector('[data-preview="valid"] [data-next-runs] li:nth-child(5)', { timeout: 10000 })
  check("custom cron previewed too", (await page.$$eval("[data-next-runs] li", (els) => els.map((e) => e.textContent))).every((p) => /06:30/.test(p)))
  await page.screenshot({ path: `${SHOTS}/MH-403-schedule-form-en-light-1440.png`, fullPage: true })
  await form.locator('button[type="submit"]').click()
  await page.waitForSelector('[data-schedule="Weekday login"] [data-schedule-workflow]', { timeout: 20000 })
  const rowText = await page.textContent('[data-schedule="Weekday login"]')
  check("the saved schedule names its workflow and shows the plain value, and the secret only as set", rowText.includes("Morning login") && rowText.includes("user: fady") && rowText.includes("password: set") && !rowText.includes(password), rowText)
  const listRaw = await (await http.get(`${web}/api/w/schedules`, { headers: { "X-Workspace-ID": workspace.id } })).text()
  check("the secret is never sent back by the API", !listRaw.includes(password))

  reports = []
  await page.locator('[data-schedule="Weekday login"] button', { hasText: "Run now" }).click()
  let run
  for (let i = 0; i < 60; i++) {
    const { schedules } = await call("GET", "/api/w/schedules", undefined, workspace.id)
    const sc = schedules.find((s) => s.name === "Weekday login")
    if (sc?.last_run_id) {
      run = await call("GET", `/api/w/runs/${sc.last_run_id}`, undefined, workspace.id)
      if (!["queued", "running"].includes(run.run.status)) break
    }
    await sleep(1000)
  }
  await sleep(500)
  check("Run now runs the workflow with the secret filled in for the browser", run?.run.status === "ok" && reports.some((r) => r.user === "fady" && r.pw === password), { run: run?.run, reports })
  check("the run record keeps the secret redacted", !JSON.stringify(run).includes(password))
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  const failed = Object.values(out.checks).filter((v) => v !== "PASS").length
  out.summary = out.error ? "ERROR" : failed ? `${failed} FAILED` : "ALL PASS"
  await ctx?.close().catch(() => {})
  site.close()
  process.exitCode = out.error || failed ? 1 : 0
}
