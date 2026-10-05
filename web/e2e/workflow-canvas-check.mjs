// The workflow canvas (MH-480), with the REAL extension in a REAL Chromium,
// against a local test site on the browser's allowed sites.
//
//   node e2e/workflow-canvas-check.mjs [webBase] [apiBase] [--shots <card>] [--video]
//
// What it proves, in a real browser, end to end:
//   - a new workflow is built on the canvas from nothing: node picker, side
//     panel, "+" after the last node and "+" on an edge;
//   - "Test workflow" runs it in the connected browser and the nodes light up
//     WHILE it runs (a node is seen "running" before the run ends), with item
//     counts on the edges and per-node output;
//   - it saves (the steps are exactly what was built; the layout is kept apart
//     from them), and moving a node alone is saved without a new version;
//   - a workflow saved before the canvas opens as a readable graph and still
//     runs unchanged;
//   - a past execution replays on the canvas with each node's data;
//   - version history previews an old version;
//   - the list page, the canvas and the executions view in en/ar × light/dark
//     × 1440/390 (with --shots), the flow mirrored in Arabic.
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { copyFileSync, cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { connectExtension } from "./connect-helper.mjs"

const args = process.argv.slice(2)
const flag = (name) => {
  const i = args.indexOf(`--${name}`)
  if (i < 0) return false
  args.splice(i, 1)
  return true
}
const opt = (name, d) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 ? args.splice(i, 2)[1] : d
}
const card = opt("shots", "")
const video = flag("video")
const base = (args[0] ?? "http://localhost:3515").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8515").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROME =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))

const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-canvas-"))
cpSync(SRC, EXT, {
  recursive: true,
  filter: (p) => !/node_modules|[\\/]test[\\/]?|[\\/]dist[\\/]?/.test(p.slice(SRC.length)),
})
{
  const m = JSON.parse(readFileSync(path.join(EXT, "manifest.json"), "utf8"))
  m.host_permissions = [`${base}/*`, `${apiBase}/*`, "http://localhost/*", "http://127.0.0.1/*"]
  writeFileSync(path.join(EXT, "manifest.json"), JSON.stringify(m, null, 2))
  const cfg = readFileSync(path.join(EXT, "config.js"), "utf8")
    .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(base)}`)
    .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(apiBase)}`)
  writeFileSync(path.join(EXT, "config.js"), cfg)
}

// ---- the site the runs drive: 127.0.0.1 is on the browser's allowed sites ----
const PORT = 18515 // batch O's own, next to its API (:8515)
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><html><head><title>Orders</title></head><body>
    <h1 id="title">Orders for today</h1>
    <ul id="orders">
      <li class="order"><a href="/o/1">Order 1</a> <span class="total">120</span></li>
      <li class="order"><a href="/o/2">Order 2</a> <span class="total">80</span></li>
      <li class="order"><a href="/o/3">Order 3</a> <span class="total">45</span></li>
    </ul>
    <button id="menu" aria-label="Open the menu">Menu</button><p id="log">nothing yet</p>
    <input id="q" name="q" value="">
    <script>
      document.getElementById("menu").addEventListener("click", (e) => {
        document.getElementById("log").textContent = e.isTrusted ? "opened by a real click" : "ignored an untrusted click"
      })
    </script></body></html>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))
const SITE = `http://127.0.0.1:${PORT}/`

const out = {}
// Compared as values: Postgres keeps a step's arguments as jsonb, which does
// not keep the order the keys were written in.
const canon = (v) =>
  Array.isArray(v)
    ? v.map(canon)
    : v && typeof v === "object"
      ? Object.fromEntries(
          Object.keys(v)
            .sort()
            .map((k) => [k, canon(v[k])]),
        )
      : v
const same = (a, b) => JSON.stringify(canon(a)) === JSON.stringify(canon(b))
const log = (m) => console.error(`· ${m}`)
const videoDir = video ? mkdtempSync(path.join(tmpdir(), "canvas-video-")) : undefined
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-canvas-profile-")),
  {
    executablePath: CHROME,
    headless: false,
    viewport: { width: 1440, height: 900 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`, "--no-first-run"],
    ...(video ? { recordVideo: { dir: videoDir, size: { width: 1440, height: 900 } } } : {}),
  },
)

let recorded = null
try {
  let worker = ctx.serviceWorkers()[0]
  if (!worker) worker = await ctx.waitForEvent("serviceworker", { timeout: 30000 })
  const extensionID = new URL(worker.url()).host

  // ---- a new account, workspace and connected browser ----
  const page = ctx.pages()[0] ?? (await ctx.newPage())
  recorded = page.video()
  await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
  await page.fill("#email", `canvas-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `Canvas ${Date.now().toString(36)}`)
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
  const { browser, online, popup } = await connectExtension({
    ctx,
    extensionID,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "Canvas Chromium",
    hosts: ["127.0.0.1"],
  })
  await popup.close()
  out.browserConnected = online
  log(`workspace ${slug}, browser ${online ? "online" : "OFFLINE"}`)

  // ---- a workflow saved before the canvas existed: aliases, a placeholder ----
  const legacySteps = [
    { command: "goto", args: { url: "http://127.0.0.1:{{port}}/" } },
    { command: "fill", args: { selector: "#q", text: "orders" }, continue_on_error: true },
    { command: "read", args: { selector: "#title" } },
    { command: "extract", args: { selector: "li.order .total", all: true } },
  ]
  const legacy = (
    await call(
      "POST",
      "/api/w/workflows",
      {
        name: "Legacy orders check",
        description: "Saved before the canvas",
        steps: legacySteps,
        params: [{ name: "port", required: true, default: String(PORT) }],
      },
      wsID,
    )
  ).workflow

  const base_ = `${base}/en/w/${slug}/automation`

  // ---- the list page, and "Create workflow" ----
  await page.bringToFront()
  await page.goto(`${base_}/workflows`, { waitUntil: "load" })
  await page.waitForSelector(`[data-workflow-row="Legacy orders check"] [data-chain="4"]`, {
    timeout: 30000,
  })
  out.listShowsTheChain = true
  await page.click("[data-create-workflow]")
  await page.waitForURL(/\/workflows\/new/, { timeout: 30000 })
  await page.waitForSelector("[data-workflow-canvas]")
  out.newCanvasStartsEmpty =
    (await page.$$("[data-canvas-node=step]")).length === 0 &&
    !!(await page.$("[data-canvas-node=trigger]"))

  // ---- build it from nothing ----
  await page.fill("[data-workflow-name]", "Orders on the canvas")
  const pick = async (search, kind) => {
    await page.waitForSelector("[data-node-picker]")
    await page.fill("[data-picker-search]", search)
    await page.click(`[data-pick-kind=${kind}]`)
    await page.waitForSelector(`[data-step-panel][data-step-kind=${kind}]`)
  }
  // 1. open the page — found by searching
  await page.click("[data-add-end]")
  await pick("open", "navigate")
  await page.fill("#node-0-url", SITE)
  // 2. read the rows — "+" after the last node again
  await page.click("[data-add-end]")
  await pick("extract", "extract_data")
  await page.fill("#node-1-list", "li.order")
  await page.fill('[data-fields-row="0"] input >> nth=0', "name")
  await page.fill('[data-fields-row="0"] input >> nth=1', "a")
  await page.click("[data-add-field]")
  await page.fill('[data-fields-row="1"] input >> nth=0', "href")
  await page.fill('[data-fields-row="1"] input >> nth=1', "a|@href")
  await page.click("[data-add-field]")
  await page.fill('[data-fields-row="2"] input >> nth=0', "total")
  await page.fill('[data-fields-row="2"] input >> nth=1', ".total")
  // 3. a screenshot at the end
  await page.click("[data-add-end]")
  await pick("screen", "screenshot")
  // 4. and a click BETWEEN the page and the rows: the "+" on an edge
  await page.click("[data-panel-close]")
  await page.click('[data-insert-at="1"]')
  await pick("click", "click")
  await page.fill("#node-1-selector", 'role=button[name="Open the menu"]')
  await page.click("[data-panel-close]")
  const kinds = await page.$$eval("[data-canvas-node=step]", (els) =>
    els.map((e) => e.getAttribute("data-node-kind")),
  )
  out.builtFromNothing = kinds.join(",") === "navigate,click,extract_data,screenshot"
  log(`built: ${kinds.join(" → ")}`)
  // Drawn left to right, each node after the one before it.
  const xs = await page.$$eval("[data-canvas-node]", (els) =>
    els.map((e) => [e.getAttribute("data-canvas-node"), e.getBoundingClientRect().x]),
  )
  out.flowsLeftToRight = xs
    .filter(([k]) => k !== "add")
    .every(([, x], i, a) => i === 0 || x > a[i - 1][1])
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-build-en-light-1440.png` })

  // ---- test it: the nodes light up WHILE the run goes ----
  await page.waitForSelector("[data-test-run]:not([disabled])", { timeout: 20000 })
  const trace = []
  const t0 = Date.now()
  if (process.env.CANVAS_DEBUG) {
    page.on("response", async (r) => {
      if (!/\/api\/w\/runs\/[^/]+$/.test(new URL(r.url()).pathname)) return
      try {
        const b = await r.json()
        trace.push(`${Date.now() - t0}ms ${b.run.status} ${b.steps.map((x) => x.status).join(",")}`)
      } catch {}
    })
  }
  await page.click("[data-test-run]")
  const seen = new Set()
  const deadline = Date.now() + 120000
  let status = ""
  while (Date.now() < deadline) {
    const now = await page.$$eval("[data-canvas-node=step]", (els) =>
      els.map((e) => e.getAttribute("data-node-status") || "-"),
    )
    now.forEach((s, i) => seen.add(`${i}:${s}`))
    // Read without waiting: getAttribute would block until the run is over.
    status = await page.evaluate(
      () => document.querySelector("[data-test-status]")?.getAttribute("data-test-status") ?? "",
    )
    if (status) break
    await page.waitForTimeout(150)
  }
  out.nodesLitWhileRunning = [...seen].some((s) => s.endsWith(":running"))
  out.testRunFinishedOk = status === "ok"
  out.everyNodeOk = await page.$$eval("[data-canvas-node=step]", (els) =>
    els.every((e) => e.getAttribute("data-node-status") === "ok"),
  )
  out.edgeCountsItems = !!(await page.$('[data-edge-items="3"]'))
  if (trace.length)
    log(
      `fetches: ${trace.join(" | ")}; visibility ${await page.evaluate(() => document.visibilityState)}`,
    )
  log(`test run ${status}; seen ${[...seen].filter((s) => !s.endsWith(":-")).join(" ")}`)
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-ran-en-light-1440.png` })

  // per-node output: the rows the extract brought back, as a table
  await page.click('[data-canvas-node=step][data-node-index="2"]')
  await page.click('[data-panel-tab="output"]')
  await page.waitForSelector("[data-output-table]")
  out.nodeOutputTable = (await page.$$eval("[data-output-table] tbody tr", (r) => r.length)) === 3
  await page.click('[data-canvas-node=step][data-node-index="3"]')
  await page.click('[data-panel-tab="output"]')
  out.nodeOutputScreenshot = !!(await page
    .waitForSelector("[data-node-shot] img", { timeout: 20000 })
    .catch(() => null))
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-output-en-light-1440.png` })
  await page.click("[data-panel-close]")

  // ---- save ----
  await page.click("[data-save]")
  await page.waitForURL(/\/workflows\/(?!new)[^/?]+$/, { timeout: 30000 })
  const id = page.url().split("/workflows/")[1].split("?")[0]
  const saved = (await call("GET", `/api/w/workflows/${id}`, undefined, wsID)).workflow
  out.savedStepsAreWhatWasBuilt = same(saved.steps, [
    { command: "navigate", args: { url: SITE } },
    { command: "click", args: { selector: 'role=button[name="Open the menu"]' } },
    {
      command: "extract",
      args: { fields: { href: "a|@href", name: "a", total: ".total" }, list: "li.order" },
    },
    { command: "screenshot" },
  ])
  if (!out.savedStepsAreWhatWasBuilt) log(`saved steps: ${JSON.stringify(saved.steps)}`)
  const layout = await call("GET", `/api/w/workflows/${id}/layout`, undefined, wsID)
  out.layoutKeptApartFromSteps =
    layout.signature === "navigate|click|extract|screenshot" &&
    layout.layout.nodes.length === 4 &&
    !JSON.stringify(saved.steps).includes('"x"')

  // ---- moving a node alone is saved without a new version ----
  await page.waitForSelector("[data-workflow-canvas]")
  const node = await page.$('[data-canvas-node=step][data-node-index="3"]')
  const box = await node.boundingBox()
  await page.mouse.move(box.x + 48, box.y + 48)
  await page.mouse.down()
  await page.mouse.move(box.x + 48, box.y + 170, { steps: 8 })
  await page.mouse.up()
  await page.waitForSelector('[data-canvas-page][data-dirty="true"]')
  await page.click("[data-save]")
  await page.waitForSelector('[data-canvas-page][data-dirty="false"]', { timeout: 20000 })
  const after = (await call("GET", `/api/w/workflows/${id}`, undefined, wsID)).workflow
  const moved = await call("GET", `/api/w/workflows/${id}/layout`, undefined, wsID)
  out.layoutOnlySaveKeepsVersion =
    after.version === saved.version && moved.layout.nodes[3].y > layout.layout.nodes[3].y + 60
  await page.reload({ waitUntil: "load" })
  await page.waitForSelector('[data-canvas-node=step][data-node-index="3"]')
  const y0 = (await (await page.$('[data-canvas-node=step][data-node-index="2"]')).boundingBox()).y
  const y3 = (await (await page.$('[data-canvas-node=step][data-node-index="3"]')).boundingBox()).y
  out.movedNodeStaysAfterReload = y3 > y0 + 40

  // ---- an edit, saved as version 2, for the history ----
  await page.click('[data-canvas-node=step][data-node-index="3"]')
  await page.click('[data-panel-tab="parameters"]')
  await page.click('[data-field="full_page"]')
  await page.click("[data-save]")
  await page.waitForSelector('[data-canvas-page][data-version="2"]', { timeout: 20000 })
  out.editSavedAsVersion2 = true

  // ---- the legacy workflow opens as a readable graph and runs unchanged ----
  await page.goto(`${base_}/workflows/${legacy.id}?view=editor`, { waitUntil: "load" })
  await page.waitForSelector("[data-canvas-node=step]")
  const legacyKinds = await page.$$eval("[data-canvas-node=step]", (els) =>
    els.map((e) => e.getAttribute("data-node-kind")),
  )
  out.legacyOpensAsGraph = legacyKinds.join(",") === "navigate,type,extract,extract"
  out.legacyOpensClean =
    (await page.getAttribute("[data-canvas-page]", "data-dirty")) === "false" &&
    (await page.isDisabled("[data-save]"))
  const { run_id: legacyRun } = await call(
    "POST",
    `/api/w/workflows/${legacy.id}/run`,
    { agent_id: browser.id, values: {} },
    wsID,
  )
  let lr
  for (let i = 0; i < 90; i++) {
    lr = await call("GET", `/api/w/runs/${legacyRun}`, undefined, wsID)
    if (!["queued", "running"].includes(lr.run.status)) break
    await page.waitForTimeout(1000)
  }
  const legacyAfter = (await call("GET", `/api/w/workflows/${legacy.id}`, undefined, wsID)).workflow
  out.legacyRunsUnchanged =
    lr.run.status === "ok" &&
    same(legacyAfter.steps, legacySteps) &&
    lr.steps.every((s) => s.status === "ok")
  log(`legacy run ${lr.run.status}: ${lr.steps.map((s) => s.status).join(" ")}`)

  // ---- replay a past execution with per-node data ----
  await page.goto(`${base_}/workflows/${id}?view=executions`, { waitUntil: "load" })
  await page.waitForSelector("[data-execution]")
  await page.waitForSelector("[data-replay] [data-canvas-node=step]")
  out.executionDrawnAsItRan =
    (await page.$$eval("[data-replay] [data-canvas-node=step]", (els) =>
      els.map((e) => e.getAttribute("data-node-status")).join(","),
    )) === "ok,ok,ok,ok"
  await page.click("[data-replay-button]")
  let replaySawRunning = false
  for (let i = 0; i < 60 && !replaySawRunning; i++) {
    replaySawRunning = !!(await page.$(
      '[data-replay] [data-canvas-node=step][data-node-status="running"]',
    ))
    await page.waitForTimeout(100)
  }
  out.replayLightsNodesInTurn = replaySawRunning
  await page.waitForSelector('[data-replay][data-replay-playing="false"]', { timeout: 30000 })
  await page.click('[data-replay] [data-canvas-node=step][data-node-index="2"]')
  await page.waitForSelector("[data-replay] ~ * [data-output-table], [data-output-table]")
  out.replayNodeData = (await page.$$eval("[data-output-table] tbody tr", (r) => r.length)) === 3
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-replay-en-light-1440.png` })

  // ---- version history previews an old version ----
  await page.goto(`${base_}/workflows/${id}?view=editor`, { waitUntil: "load" })
  await page.click("[data-open-versions]")
  await page.click('[data-preview-version="1"]')
  out.versionPreview =
    !!(await page
      .waitForSelector('[data-preview-banner="1"]', { timeout: 10000 })
      .catch(() => null)) && !!(await page.$('[data-workflow-canvas][data-read-only="true"]'))
  if (card) await page.screenshot({ path: `${SHOTS}/${card}-versions-en-light-1440.png` })

  // ---- the list's Executions tab ----
  await page.goto(`${base_}/workflows?tab=executions`, { waitUntil: "load" })
  await page.waitForSelector("[data-execution-row]", { timeout: 20000 })
  out.listExecutions = (await page.$$("[data-execution-row]")).length >= 2

  // ---- every locale, scheme and width, in a browser that is not the one driven ----
  if (card) {
    const state = await ctx.storageState()
    const shooter = await chromium.launch({ executablePath: CHROME })
    const pages = [
      ["list", `/automation/workflows`, "[data-workflow-row]"],
      ["canvas", `/automation/workflows/${id}?view=editor`, "[data-canvas-node=step]"],
      [
        "executions",
        `/automation/workflows/${id}?view=executions`,
        "[data-replay] [data-canvas-node=step]",
      ],
    ]
    for (const locale of ["en", "ar"]) {
      for (const theme of ["light", "dark"]) {
        for (const width of [1440, 390]) {
          const c = await shooter.newContext({
            storageState: state,
            viewport: { width, height: 900 },
            colorScheme: theme,
          })
          const p = await c.newPage()
          for (const [name, route, ready] of pages) {
            await p.goto(`${base}/${locale}/w/${slug}${route}`, { waitUntil: "load" })
            // A narrow screen lists the executions first; one is chosen to see it.
            if (name === "executions" && width < 768)
              await p.click("[data-execution]", { timeout: 60000 })
            await p.waitForSelector(ready, { timeout: 60000 })
            await p.waitForTimeout(900)
            if (name === "canvas" && width === 1440) {
              // With a node open, so the side panel is in the picture.
              await p.click('[data-canvas-node=step][data-node-index="2"]')
              await p.waitForSelector("[data-step-panel]")
              await p.waitForTimeout(300)
            }
            await p.screenshot({
              path: `${SHOTS}/${card}-${name}-${locale}-${theme}-${width}.png`,
            })
            out[`${name}-${locale}-${theme}-${width}-noOverflow`] = await p.evaluate(
              () => document.documentElement.scrollWidth - innerWidth <= 0,
            )
            if (name === "canvas" && locale === "ar" && width === 1440) {
              // The flow mirrors: the trigger on the right, each step to its left.
              const xs = await p.$$eval(
                "[data-canvas-node=trigger], [data-canvas-node=step]",
                (els) => els.map((e) => e.getBoundingClientRect().x),
              )
              out[`canvas-ar-${theme}-flowsRightToLeft`] = xs.every(
                (x, i) => i === 0 || x < xs[i - 1],
              )
            }
          }
          await c.close()
        }
      }
    }
    await shooter.close()
  }
} catch (e) {
  out.error = String(e?.stack || e).slice(0, 1200)
} finally {
  await ctx.close()
  site.close()
  if (recorded && card) {
    // The recording of the page that was driven; written out once the context closes.
    copyFileSync(await recorded.path(), `${SHOTS}/${card}-build.webm`)
    out.video = `${SHOTS}/${card}-build.webm`
  }
  console.log(JSON.stringify(out, null, 1))
  const bad = Object.entries(out).filter(([k, v]) => v === false || k === "error")
  process.exit(bad.length ? 1 : 0)
}
