// Phase 4 (MH-478): fadymondy.com's browser_* MCP tools, called over the real
// MCP endpoint with a workspace token, driving the REAL extension in a REAL
// Chromium — on a local page and on news.google.com.
//
//   node e2e/mcp-browser-check.mjs [webBase] [apiBase]
import { chromium } from "playwright-core"
import { createServer } from "node:http"
import { cpSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { connectExtension } from "./connect-helper.mjs"

const args = process.argv.slice(2)
const base = (args[0] ?? "http://localhost:3508").replace(/\/$/, "")
const apiBase = (args[1] ?? "http://localhost:8508").replace(/\/$/, "")
const CHROME =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"
const here = path.dirname(fileURLToPath(import.meta.url))

const SRC = path.resolve(here, "../../extension")
const EXT = mkdtempSync(path.join(tmpdir(), "ext-mcp-"))
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

const PORT = 8797
const site = createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" })
  res.end(`<!doctype html><title>Roaster</title><ul><li><b class="n">Ethiopia Yirgacheffe</b><i class="p">520</i></li><li><b class="n">Brazil Santos</b><i class="p">380</i></li></ul>
    <button id="more" onclick="document.body.insertAdjacentHTML('beforeend','<p id=extra>Kenya AA</p>')">More</button>`)
})
await new Promise((r) => site.listen(PORT, "127.0.0.1", r))

const out = {}
const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "ext-mcp-profile-")),
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
  await page.fill("#email", `mcp-${Date.now()}@x.test`)
  await page.fill("#password", "Passw0rd!long-enough")
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
  if (!/\/w\//.test(page.url())) {
    await page.fill("#ws-name", `MCP ${Date.now().toString(36)}`)
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
  const { online } = await connectExtension({
    ctx,
    extensionID,
    site: base,
    api: apiBase,
    call,
    workspaceID: wsID,
    name: "Office Chrome",
    hosts: ["roaster.test", "news.google.com"],
  })
  out.connected = online

  const tok = await call(
    "POST",
    "/api/w/mcp_tokens",
    { name: "e2e", scopes: ["automation:read", "automation:run", "automation:manage"] },
    wsID,
  )
  const mcp = async (name, argsIn) => {
    const res = await fetch(`${apiBase}/api/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${tok.plaintext}` },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: { name, arguments: argsIn },
      }),
    })
    const body = await res.json()
    return body.result?.content?.[0]?.text ?? JSON.stringify(body)
  }

  const agents = await mcp("browser_agents", {})
  out.agentsListsIt = /Office Chrome .* — online/.test(agents) && agents.includes("roaster.test")

  const local = await mcp("browser_extract", {
    url: `http://roaster.test:${PORT}/`,
    list: "li",
    fields: { name: ".n", price: ".p" },
  })
  out.extractLocal =
    local.includes("(2/2 steps ok)") &&
    local.includes("Ethiopia Yirgacheffe") &&
    local.includes("380")

  const run = await mcp("browser_run", {
    steps: [
      { command: "navigate", args: { url: `http://roaster.test:${PORT}/` } },
      { command: "click", args: { selector: "#more" } },
      { command: "extract", args: { selector: "#extra" } },
    ],
  })
  out.runInteracts = run.includes("(3/3 steps ok)") && run.includes("Kenya AA")
  const key = run.split(" ")[1]
  out.resultByKey = (await mcp("browser_result", { run: key })).includes("3. extract — ok")

  const refused = await mcp("browser_extract", { url: "https://example.org/", fields: { h: "h1" } })
  out.offListRefusedAndNamed =
    refused.includes("example.org") && refused.includes("Nothing was run")
  const widened = await mcp("browser_set_allowed_sites", { add: ["example.org"] })
  const allowed = await mcp("browser_extract", { url: "https://example.org/", fields: { h: "h1" } })
  out.adminWidensTheList = widened.includes("Added: example.org")
  out.thenItRuns = allowed.includes("(2/2 steps ok)") && allowed.includes("Example Domain")
  if (!out.thenItRuns) out.allowedAnswer = allowed.slice(0, 400)

  // Google News with the selectors fadymondy.com's google-news-topic plugin uses.
  const news = await mcp("browser_extract", {
    url: "https://news.google.com/headlines/section/topic/TECHNOLOGY?hl=en-US&gl=US&ceid=US:en",
    wait_for: "[jscontroller=BoFkO] time",
    list: "[jscontroller=BoFkO][data-n-tid]",
    fields: { headline: 'a[tabindex="0"][href^="./read/"]', source: '[data-n-tid="9"]' },
    limit: 5,
  })
  out.realGoogleNews = news.includes("(3/3 steps ok)") && /"headline":"[^"]{10,}/.test(news)
  if (!out.realGoogleNews) out.newsAnswer = news.slice(0, 600)
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
