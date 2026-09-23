// MH-422: a run stops the moment it leaves the browser's allowed sites,
// however it got there — proved in the REAL extension, in a real Chromium,
// against a real API.
//
// The allow-list used to be checked only for goto steps, on the server. A click
// on a link, an Enter that submits a form, or a redirect moved the tab off the
// list, and every later step acted there. extension/test covers the driver
// against jsdom; this covers what jsdom cannot: Chrome's own navigation,
// redirects, webNavigation events, documentId-pinned injection, and the
// server's handling of the stop.
//
// Hosts: Chromium resolves every *.localhost name to loopback, so one local
// server plays several sites by Host header:
//   allowed.localhost (and its subdomains)  — the browser's ONLY allowed site
//   evil.localhost, idp.evil.localhost      — not on the list
//
// The extension copy is given host permission for ALL of *.localhost —
// including evil.localhost — so nothing here is stopped by Chrome refusing to
// inject. What stops a run is the allow-list check, which is the point.
//
// Every off-list page reports back to this server: that it was reached (so the
// case is real, not a navigation that never happened), and anything typed into
// it (which must be nothing).
//
//   node e2e/allowed-sites-check.mjs [apiBase]      (default http://127.0.0.1:8422)
//
// With WEB_BASE set (a `next dev` whose API_ORIGIN is apiBase, on the same
// host so the session cookie is shared), it also opens the run's detail page
// and checks the reason is what a person reads there.
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { connectExtension } from "./connect-helper.mjs"

const apiBase = (process.argv[2] ?? "http://127.0.0.1:8422").replace(/\/$/, "")
const webBase = (process.env.WEB_BASE ?? "").replace(/\/$/, "")
const here = path.dirname(fileURLToPath(import.meta.url))
// EXT_SRC runs the same check against another copy (e.g. the previous driver, as a control).
const SRC = path.resolve(process.env.EXT_SRC ?? path.resolve(here, "../../extension"))

const EXT = mkdtempSync(path.join(tmpdir(), "ext-422-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) => !/[\\/](node_modules|test)([\\/]|$)/.test(path.relative(SRC, p) ? "/" + path.relative(SRC, p) : ""),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = ["http://localhost/*", "http://127.0.0.1/*", "http://*.localhost/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
}

// ---- the sites ----
const PORT = Number(process.env.PAGE_PORT ?? 8794)
const at = (host, p = "/") => `http://${host}:${PORT}${p}`
const ALLOWED = "allowed.localhost"
const EVIL = "evil.localhost"

// What each off-list page reports, and every request that reached an off-list host.
let offList = []
let reports = []
const REPORTER = `<script>
  const report = (what, extra) => fetch("/report", { method: "POST", body: JSON.stringify({ what, host: location.host, path: location.pathname, ...extra }) })
  report("reached")
  document.addEventListener("input", (e) => report("typed", { value: e.target.value }), true)
  document.addEventListener("click", (e) => report("clicked", { target: e.target.id }), true)
</script>`

const pages = {
  [`${ALLOWED} /start`]: `<!doctype html><title>Start</title>
    <a id="away" href="${at(EVIL, "/landing")}">elsewhere</a>
    <a id="bounce" href="/out">a tracked link</a>
    <a id="sub" href="${at("app." + ALLOWED, "/inside")}">the app</a>
    <a id="lookalike" href="${at(ALLOWED + ".evil.localhost", "/")}">look-alike</a>
    <form id="search" action="${at(EVIL, "/search")}" method="get"><input id="q" name="q"></form>
    <form id="local" action="/results" method="get"><input id="q2" name="q"></form>
    <p id="here">allowed start</p>`,
  [`${ALLOWED} /results`]: `<!doctype html><title>Results</title><h1 id="result">results</h1>`,
  [`${ALLOWED} /drifts`]: `<!doctype html><title>Drifts</title><p id="here">drifting</p>
    <script>setTimeout(() => { location.href = "${at(EVIL, "/landing")}" }, 400)</script>`,
  [`app.${ALLOWED} /inside`]: `<!doctype html><title>App</title><p id="here">inside the app</p>`,
  [`www.${ALLOWED} /home`]: `<!doctype html><title>Home</title><p id="here">signed in</p>`,
  [`${EVIL} /landing`]: `<!doctype html><title>Evil</title><input id="field"><p id="here">evil</p>${REPORTER}`,
  [`${EVIL} /search`]: `<!doctype html><title>Evil search</title><input id="field"><p id="here">evil search</p>${REPORTER}`,
  [`${ALLOWED}.evil.localhost /`]: `<!doctype html><title>Look-alike</title><input id="field">${REPORTER}`,
}
const redirects = {
  // A chain that ENDS off the list, through an allowed host.
  [`${ALLOWED} /out`]: at("hop." + ALLOWED, "/next"),
  [`hop.${ALLOWED} /next`]: at(EVIL, "/landing"),
  // A chain that passes THROUGH an off-list host and ends back on the list.
  [`${ALLOWED} /sso`]: at("idp." + EVIL, "/bounce"),
  [`idp.${EVIL} /bounce`]: at("www." + ALLOWED, "/home"),
}

const handler = (req, res) => {
  const host = String(req.headers.host || "").replace(/:\d+$/, "")
  const url = new URL(req.url, `http://${req.headers.host}`)
  const key = `${host} ${url.pathname}`
  if (!/(^|\.)allowed\.localhost$/.test(host) || host.endsWith(".evil.localhost")) {
    if (url.pathname !== "/favicon.ico") offList.push(`${req.method} ${host}${url.pathname}${url.search}`)
  }
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
  if (redirects[key]) {
    res.writeHead(302, { Location: redirects[key], "Cache-Control": "no-store" })
    return res.end()
  }
  if (pages[key]) {
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" })
    return res.end(pages[key])
  }
  res.writeHead(404, { "Content-Type": "text/plain" })
  res.end("not found")
}
// Both loopbacks: Chromium may resolve *.localhost to either.
const servers = [createServer(handler), createServer(handler)]
await new Promise((r) => servers[0].listen(PORT, "127.0.0.1", r))
await new Promise((r) => servers[1].listen(PORT, "::1", r).on("error", () => r()))

const out = { checks: {} }
const check = (name, ok, detail) => {
  out.checks[name] = ok ? "PASS" : `FAIL${detail !== undefined ? ": " + JSON.stringify(detail).slice(0, 600) : ""}`
}
let ctx
process.on("exit", () => console.log(JSON.stringify(out, null, 1)))

try {
  const userDir = mkdtempSync(path.join(tmpdir(), "ext-422-profile-"))
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
  const version = await worker.evaluate(() => chrome.runtime.getManifest().version)
  out.extensionVersion = version
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
  await call("POST", "/api/auth/register", { email: `mh422-${Date.now()}@x.test`, password: "Passw0rd!long-enough" })
  const { workspace } = await call("POST", "/api/me/workspaces", { name: `MH-422 ${Date.now()}` })
  out.workspace = workspace.slug

  // ---- connect through the extension's own popup (MH-478) ----
  const { browser: connected, online, popup } = await connectExtension({
    ctx, extensionID, site: apiBase, api: apiBase, call, workspaceID: workspace.id,
    name: "MH-422 Chromium", hosts: [ALLOWED],
  })
  const agentID = connected.id
  out.reportedVersion = (await call("GET", "/api/w/browsers", undefined, workspace.id)).browsers.find((b) => b.id === agentID)?.extension_version
  check("extension paired and connected by itself", online)
  if (!online) throw new Error("the extension never came online")
  check("the browser reports the bumped extension version", out.reportedVersion === version && version.split(".").map(Number).reduce((acc, n) => acc * 1000 + n, 0) >= 1002, {
    reported: out.reportedVersion,
    manifest: version,
  })
  await popup.close()

  const waitForRun = async (runID) => {
    for (let i = 0; i < 90; i++) {
      const r = await call("GET", `/api/w/runs/${runID}`, undefined, workspace.id)
      if (!["queued", "running"].includes(r.run.status)) return r
      await new Promise((res) => setTimeout(res, 500))
    }
    throw new Error(`run ${runID} did not finish`)
  }
  const run = async (name, steps) => {
    offList = []
    reports = []
    const { run_id } = await call("POST", "/api/w/runs", { agent_id: agentID, steps }, workspace.id)
    const r = await waitForRun(run_id)
    // Let any late page report land before judging what the off-list page saw.
    await new Promise((res) => setTimeout(res, 700))
    const summary = {
      id: run_id,
      status: r.run.status,
      error: r.run.error,
      steps: r.steps.map((s) => `${s.position + 1} ${s.command} ${s.status}: ${s.error || s.summary}`),
      offList: [...offList],
      reports: [...reports],
    }
    out[name] = summary
    return { r, summary }
  }
  const stopped = (name, r, stepNo, host, { opener = "went to" } = {}) => {
    const reason = `step ${stepNo} ${opener} ${host}, which isn't on this browser's allowed sites`
    check(`${name}: the run failed with the reason naming step ${stepNo} and ${host}`, r.r.run.status === "failed" && r.r.run.error === reason, {
      want: reason,
      got: r.summary,
    })
    const after = r.r.steps.filter((s) => s.position + 1 > stepNo)
    check(`${name}: every later step was skipped, never run`, after.length > 0 && after.every((s) => s.status === "skipped"), r.summary.steps)
    const typed = r.summary.reports.filter((x) => x.what === "typed" || x.what === "clicked")
    check(`${name}: nothing was typed or clicked on the off-list page`, typed.length === 0, r.summary.reports)
  }

  // ---- 1. a link to an off-list host ----
  const link = await run("link", [
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "click", args: { selector: "#away" } },
    { command: "type", args: { selector: "#field", text: "the-password" } },
    { command: "extract", args: { selector: "body" } },
  ])
  check("link: the click really reached evil.localhost", link.summary.offList.some((x) => x.startsWith(`GET ${EVIL}/landing`)), link.summary.offList)
  stopped("link", link, 2, EVIL)

  if (webBase) {
    // What the operator sees: the run detail page shows the reason.
    const page = await ctx.newPage()
    await page.goto(`${webBase}/en/w/${workspace.slug}/automation/runs/${link.summary.id}`)
    const reason = "step 2 went to evil.localhost, which isn't on this browser's allowed sites"
    const shown = await page
      .getByText(reason, { exact: true })
      .first()
      .waitFor({ timeout: 60000 })
      .then(() => true, () => false)
    out.runPage = { url: page.url(), shown }
    if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT, fullPage: true })
    check("the run detail page shows the reason, naming the step and the host", shown, out.runPage)
    await page.close()
  }

  // ---- 2. Enter submitting a form to an off-list action ----
  const enter = await run("enterSubmit", [
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "type", args: { selector: "#q", text: "moharrik" } },
    { command: "press", args: { key: "Enter" } },
    { command: "type", args: { selector: "#field", text: "the-password" } },
  ])
  check("enter: the form really submitted to evil.localhost", enter.summary.offList.some((x) => x.startsWith(`GET ${EVIL}/search?q=moharrik`)), enter.summary.offList)
  stopped("enter", enter, 3, EVIL)

  // ---- 3. a redirect chain that ends off the list ----
  const redirect = await run("redirectClick", [
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "click", args: { selector: "#bounce" } },
    { command: "type", args: { selector: "#field", text: "the-password" } },
  ])
  check("redirect: the chain really ended on evil.localhost", redirect.summary.offList.some((x) => x.startsWith(`GET ${EVIL}/landing`)), redirect.summary.offList)
  stopped("redirect (click)", redirect, 2, EVIL)

  const gotoRedirect = await run("redirectGoto", [
    { command: "goto", args: { url: at(ALLOWED, "/out") } },
    { command: "extract", args: { selector: "body" } },
  ])
  stopped("redirect (goto)", gotoRedirect, 1, EVIL)

  // ---- 4. continue_on_error does not carry a run past leaving ----
  const carry = await run("continueOnError", [
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "click", args: { selector: "#lookalike" }, continue_on_error: true },
    { command: "type", args: { selector: "#field", text: "the-password" }, continue_on_error: true },
  ])
  stopped("look-alike host, continue_on_error", carry, 2, `${ALLOWED}.evil.localhost`)

  // ---- 5. a page that moves off the list by itself, during a wait ----
  const drift = await run("drift", [
    { command: "goto", args: { url: at(ALLOWED, "/drifts") } },
    { command: "wait", args: { ms: 5000 } },
    { command: "type", args: { selector: "#field", text: "the-password" } },
  ])
  stopped("page moved by itself", drift, 2, EVIL)

  // ---- 6. an allowed flow still completes: subdomain, port, redirect through an off-list IdP, on-list Enter ----
  const fine = await run("allowedFlow", [
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "click", args: { selector: "#sub" } },
    { command: "extract", args: { selector: "#here" } },
    { command: "goto", args: { url: at(ALLOWED, "/sso") } },
    { command: "extract", args: { selector: "#here" } },
    { command: "goto", args: { url: at(ALLOWED, "/start") } },
    { command: "type", args: { selector: "#q2", text: "fine" } },
    { command: "press", args: { key: "Enter", selector: "#q2" } },
    { command: "extract", args: { selector: "#result" } },
  ])
  check("allowed flow: run ok", fine.r.run.status === "ok", fine.summary)
  check("allowed flow: the subdomain page was acted on", fine.r.steps[2]?.summary === "inside the app", fine.r.steps[2])
  check("allowed flow: a redirect through idp.evil.localhost that ends on www.allowed.localhost continues", fine.r.steps[4]?.summary === "signed in", fine.r.steps[4])
  check("allowed flow: Enter to an on-list action continues", fine.r.steps[8]?.summary === "results", fine.r.steps[8])

  // ---- 7. the kill switch still stops a run in flight ----
  offList = []
  const { run_id: longID } = await call(
    "POST",
    "/api/w/runs",
    {
      agent_id: agentID,
      steps: [
        { command: "goto", args: { url: at(ALLOWED, "/start") } },
        { command: "wait", args: { ms: 3000 } },
        { command: "extract", args: { selector: "#here" } },
      ],
    },
    workspace.id,
  )
  await new Promise((res) => setTimeout(res, 1500))
  await call("POST", "/api/w/automation/settings", { paused: true, reason: "MH-422 check" }, workspace.id)
  await waitForRun(longID)
  // The stop marks the run cancelled at once; the dispatcher finishes it a
  // moment later, when the step in flight comes back "not connected". Judge
  // the record after that, not before.
  await new Promise((res) => setTimeout(res, 1500))
  const halted = await call("GET", `/api/w/runs/${longID}`, undefined, workspace.id)
  out.killSwitch = { status: halted.run.status, error: halted.run.error, steps: halted.steps.map((s) => `${s.position + 1} ${s.command} ${s.status}`) }
  check("kill switch: a run in flight is cancelled", halted.run.status === "cancelled", out.killSwitch)
  await call("POST", "/api/w/automation/settings", { paused: false }, workspace.id).catch((e) => (out.resumeError = String(e)))

  out.extensionErrors = workerErrors
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  const failed = Object.values(out.checks).filter((v) => v !== "PASS").length
  out.summary = out.error ? "ERROR" : failed ? `${failed} FAILED` : "ALL PASS"
  await ctx?.close().catch(() => {})
  servers.forEach((s) => s.close())
  process.exitCode = out.error || failed ? 1 : 0
}
