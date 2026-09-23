// MH-398: a recording that presses Enter, chooses from a dropdown and scrolls
// REPLAYS — recorded for real, saved as a workflow, and run in the real
// extension in a real Chromium.
//
// The recorder has always emitted press, select and scroll; the driver used to
// refuse all three ("this browser does not know the step"), so such a recording
// saved without complaint and then failed on every run. Unit tests
// (extension/test) cover the driver against jsdom; this covers what jsdom
// cannot: Chrome's own scripting, events, focus, scrolling and navigation.
//
// What happens:
//   1. The extension (a copy whose manifest may reach localhost) pairs through
//      its own popup; the pairing is approved over the API.
//   2. The operator's actions on a local page are RECORDED by the extension,
//      from trusted input: type, Enter, a dropdown choice, a wheel scroll, and
//      a click on a button that was below the fold.
//   3. The saved workflow is run. The page reports what happened to it back to
//      the local server, so the check sees the replay's effect on the page, not
//      only the driver's own account of it.
//   4. A hand-written run covers the rest of the vocabulary: Enter that really
//      navigates (the next step must run on the new page), select by value,
//      scroll-to-selector.
//
// Needs the API only — pairing, workflows and runs are all REST:
//   node e2e/replay-steps-check.mjs [apiBase]      (default http://127.0.0.1:8398)
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { connectExtension } from "./connect-helper.mjs"

const apiBase = (process.argv[2] ?? "http://127.0.0.1:8398").replace(/\/$/, "")
const here = path.dirname(fileURLToPath(import.meta.url))
// EXT_SRC runs the same check against another copy (e.g. the previous driver, as a control).
const SRC = path.resolve(process.env.EXT_SRC ?? path.resolve(here, "../../extension"))

// A COPY of the extension whose manifest declares the local hosts: the shipped
// one asks for any other host with a native Chrome prompt a check cannot click.
// The harness (node_modules, tests) stays behind; everything else is the real
// extension.
const EXT = mkdtempSync(path.join(tmpdir(), "ext-398-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) => !/[\\/](node_modules|test)([\\/]|$)/.test(path.relative(SRC, p) ? "/" + path.relative(SRC, p) : ""),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = ["http://localhost/*", "http://127.0.0.1/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
}

// ---- the page, and what it reports ----
const FIXTURE = readFileSync(path.join(SRC, "test/fixtures/replay.html"), "utf8")
// The page tells this server what became of it. A MutationObserver on the three
// outcome lines, so nothing in the fixture itself is test plumbing.
const REPORTER = `<script>
  new MutationObserver(() => {
    const read = (id) => document.getElementById(id).textContent
    fetch("/report", { method: "POST", body: JSON.stringify({
      at: Date.now(), searched: read("searched"), chosen: read("chosen"), clicked: read("clicked"),
    }) })
  }).observe(document.body, { subtree: true, childList: true, characterData: true })
</script>`
const SEARCH_THAT_NAVIGATES = `<!doctype html><title>Search</title>
  <form action="/results" method="get"><input id="q2" name="q"></form>
  <select id="size"><option value="s">Small</option><option value="m">Medium</option><option value="l">Large</option></select>
  <div style="height:2600px"></div><p id="bottom">the bottom of the page</p>`

let reports = []
const PAGE_PORT = Number(process.env.PAGE_PORT ?? 8793)
const PAGE = `http://127.0.0.1:${PAGE_PORT}`
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
  if (url.pathname === "/search") return res.end(SEARCH_THAT_NAVIGATES)
  if (url.pathname === "/results") {
    const q = url.searchParams.get("q") ?? ""
    return res.end(`<!doctype html><title>Results</title><h1 id="result">results for ${q.replace(/[<&]/g, "")}</h1>`)
  }
  res.end(FIXTURE.replace("</body>", REPORTER + "</body>"))
})
await new Promise((r) => site.listen(PAGE_PORT, "127.0.0.1", r))

const out = { checks: {} }
const check = (name, ok, detail) => {
  out.checks[name] = ok ? "PASS" : `FAIL${detail !== undefined ? ": " + JSON.stringify(detail).slice(0, 400) : ""}`
}
let ctx
process.on("exit", () => console.log(JSON.stringify(out, null, 1)))

try {
  const userDir = mkdtempSync(path.join(tmpdir(), "ext-398-profile-"))
  ctx = await chromium.launchPersistentContext(userDir, {
    executablePath: process.env.EXT_CHROME ?? "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe",
    headless: false,
    viewport: { width: 1280, height: 800 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
  })
  let worker = ctx.serviceWorkers()[0]
  if (!worker) worker = await ctx.waitForEvent("serviceworker", { timeout: 30000 })
  const extensionID = new URL(worker.url()).host
  const workerErrors = []
  worker.on("console", (m) => m.type() === "error" && workerErrors.push(m.text().slice(0, 200)))
  check("extension loaded", !!extensionID)

  // ---- an account and a workspace, over the API ----
  const http = ctx.request
  const csrf = async () => (await (await http.get(`${apiBase}/api/auth/csrf`)).json()).csrf_token
  const call = async (method, p, data, wsID) => {
    const headers = { "X-CSRF-Token": await csrf() }
    if (wsID) headers["X-Workspace-ID"] = wsID
    const res = await http.fetch(`${apiBase}${p}`, { method, headers, data })
    const text = await res.text()
    if (!res.ok()) throw new Error(`${method} ${p} → ${res.status()} ${text.slice(0, 300)}`)
    return text ? JSON.parse(text) : null
  }
  await call("POST", "/api/auth/register", { email: `mh398-${Date.now()}@x.test`, password: "Passw0rd!long-enough" })
  const { workspace } = await call("POST", "/api/me/workspaces", { name: `MH-398 ${Date.now()}` })
  out.workspace = workspace.slug

  // ---- connect through the extension's own popup (MH-478) ----
  const { browser: connected, online, popup } = await connectExtension({
    ctx, extensionID, site: apiBase, api: apiBase, call, workspaceID: workspace.id,
    name: "MH-398 Chromium", hosts: ["127.0.0.1"],
  })
  const agentID = connected.id
  check("extension paired and connected by itself", online)
  if (!online) throw new Error("the extension never came online")
  // hello.ok carries the allow-list the recorder checks; give it a moment to land.
  await new Promise((r) => setTimeout(r, 1000))

  // ---- RECORD, from trusted input ----
  const page = await ctx.newPage()
  await page.goto(`${PAGE}/replay.html`)
  await page.bringToFront()
  const started = await popup.evaluate(() => chrome.runtime.sendMessage({ type: "record.start" }))
  check("recording started on the page", started?.ok === true, started)
  await page.waitForTimeout(500)

  await page.click("#q")
  await page.keyboard.type("moharrik")
  await page.keyboard.press("Enter")
  await page.waitForTimeout(300)
  await page.selectOption("#country", "uk")
  await page.waitForTimeout(300)
  await page.mouse.move(640, 400)
  await page.mouse.wheel(0, 2300)
  await page.waitForTimeout(1500) // the recorder settles a scroll after 700ms
  await page.click("#deep")
  await page.waitForTimeout(500)
  out.recordedPage = await page.evaluate(() => ({
    searched: document.getElementById("searched").textContent,
    chosen: document.getElementById("chosen").textContent,
    clicked: document.getElementById("clicked").textContent,
  }))

  const stopped = await popup.evaluate(() => chrome.runtime.sendMessage({ type: "record.stop" }))
  check("recording saved as a workflow", stopped?.ok === true && !!stopped.saved?.workflow_id, stopped)
  const workflowID = stopped.saved.workflow_id

  const { workflows } = await call("GET", "/api/w/workflows", undefined, workspace.id)
  const wf = workflows.find((w) => w.id === workflowID)
  out.recordedSteps = wf.steps
  const byCommand = (c) => wf.steps.filter((s) => s.command === c)
  check("the recording holds a press {key: Enter}", byCommand("press").some((s) => s.args?.key === "Enter"), wf.steps)
  check(
    "the recording holds a select {selector, value: <label>}",
    byCommand("select").some((s) => s.args?.selector && s.args?.value === "United Kingdom"),
    wf.steps,
  )
  check("the recording holds a scroll {dy}", byCommand("scroll").some((s) => Number(s.args?.dy) >= 300), wf.steps)
  await page.close()

  // ---- REPLAY the recorded workflow ----
  const waitForRun = async (runID) => {
    for (let i = 0; i < 90; i++) {
      const r = await call("GET", `/api/w/runs/${runID}`, undefined, workspace.id)
      if (!["queued", "running"].includes(r.run.status)) return r
      await new Promise((res) => setTimeout(res, 1000))
    }
    throw new Error(`run ${runID} did not finish`)
  }
  reports = []
  const { run_id: replayID } = await call("POST", `/api/w/workflows/${workflowID}/run`, { agent_id: agentID }, workspace.id)
  const replay = await waitForRun(replayID)
  out.replay = {
    status: replay.run.status,
    error: replay.run.error,
    steps: replay.steps.map((s) => `${s.position} ${s.command} ${s.status}: ${s.summary || s.error}`),
  }
  check("the recorded workflow replays: run ok", replay.run.status === "ok", out.replay)
  check("every step of the replay ok", replay.steps.every((s) => s.status === "ok"), out.replay.steps)
  const last = reports[reports.length - 1] ?? {}
  out.replayedPage = last
  check("replay: Enter submitted the search", last.searched === "searched: moharrik", last)
  check("replay: the dropdown choice reached the page's change handler", last.chosen === "chosen: uk", last)
  const clickedAt = Number((last.clicked || "").match(/scrollY (\d+)/)?.[1] ?? -1)
  check("replay: the page was scrolled before the deep button was clicked", clickedAt >= 1500, last)

  // ---- the rest of the vocabulary, hand-written ----
  const { run_id: handID } = await call(
    "POST",
    "/api/w/runs",
    {
      agent_id: agentID,
      steps: [
        { command: "goto", args: { url: `${PAGE}/search` } },
        { command: "select", args: { selector: "#size", value: "l" } },
        { command: "extract", args: { selector: "#size", attribute: "id" } },
        { command: "scroll", args: { selector: "#bottom" } },
        { command: "type", args: { selector: "#q2", text: "real navigation" } },
        { command: "press", args: { key: "Enter", selector: "#q2" } },
        // Only right if the press waited for the page Enter navigated to.
        { command: "extract", args: { selector: "#result" } },
        { command: "press", args: { key: "Escape" } },
      ],
    },
    workspace.id,
  )
  const hand = await waitForRun(handID)
  out.handWritten = {
    status: hand.run.status,
    error: hand.run.error,
    steps: hand.steps.map((s) => `${s.position} ${s.command} ${s.status}: ${s.summary || s.error}`),
  }
  check("hand-written run ok", hand.run.status === "ok", out.handWritten)
  check("select by value chose Large", /chose "Large" in #size/.test(hand.steps[1]?.summary ?? ""), hand.steps[1])
  check("scroll to a selector", /scrolled #bottom into view/.test(hand.steps[3]?.summary ?? ""), hand.steps[3])
  check(
    "Enter navigated and the next step ran on the new page",
    (hand.steps[6]?.summary ?? "") === "results for real navigation",
    hand.steps[6],
  )

  // ---- a step that cannot work still says why ----
  const { run_id: badID } = await call(
    "POST",
    "/api/w/runs",
    {
      agent_id: agentID,
      steps: [
        { command: "goto", args: { url: `${PAGE}/replay.html` } },
        { command: "select", args: { selector: "#country", value: "Atlantis" } },
      ],
    },
    workspace.id,
  )
  const bad = await waitForRun(badID)
  check(
    "a select that cannot be done fails the run, in the browser's words",
    bad.run.status === "failed" && /has no option "Atlantis"/.test(bad.steps[1]?.error ?? ""),
    { status: bad.run.status, error: bad.steps[1]?.error },
  )

  out.extensionErrors = workerErrors
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  const failed = Object.values(out.checks).filter((v) => v !== "PASS").length
  out.summary = out.error ? "ERROR" : failed ? `${failed} FAILED` : "ALL PASS"
  await ctx?.close().catch(() => {})
  site.close()
  process.exitCode = out.error || failed ? 1 : 0
}
