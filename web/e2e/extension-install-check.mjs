// MH-399: the extension can be installed from the app, and the installed
// extension works.
//
//   1. A new workspace: the schedules empty state says a paired browser is
//      needed and links to the install page (it used to say "Add a schedule"
//      with nothing to add it with).
//   2. The install page offers the packaged zip. It is DOWNLOADED through the
//      page's own button, and its sha256 is checked against the number the
//      page shows and the header the download sends.
//   3. The downloaded zip is unzipped and loaded UNPACKED into Chromium — the
//      file itself, not the source directory.
//   4. It pairs: the popup already knows the site (baked into the build), the
//      code is typed into the install page's own approve box, and the browser
//      comes online reporting the zip's version.
//   5. A workflow runs in it, and the page it drives reports the effect.
//
// Needs the web (which proxies /api) and an API with a packaged build:
//   node extension/build.mjs --site http://localhost:3506 --api http://localhost:8506 --dev
//   node e2e/extension-install-check.mjs [webBase]      (default http://localhost:3506)
import { chromium } from "playwright-core"
import { createHash } from "node:crypto"
import { createServer } from "node:http"
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import zlib from "node:zlib"
import { execFileSync } from "node:child_process"

const web = (process.argv[2] ?? "http://localhost:3506").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROMIUM =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"

const out = { checks: {} }
const check = (name, ok, detail) => {
  out.checks[name] = ok
    ? "PASS"
    : `FAIL${detail !== undefined ? ": " + JSON.stringify(detail).slice(0, 400) : ""}`
}
process.on("exit", () => console.log(JSON.stringify(out, null, 1)))

/** Unzip by the central directory: exactly what Chrome will see. */
function unzip(buf, dir) {
  const end = buf.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]))
  const count = buf.readUInt16LE(end + 10)
  let at = buf.readUInt32LE(end + 16)
  const names = []
  for (let i = 0; i < count; i++) {
    const method = buf.readUInt16LE(at + 10)
    const size = buf.readUInt32LE(at + 20)
    const nameLen = buf.readUInt16LE(at + 28)
    const local = buf.readUInt32LE(at + 42)
    const name = buf.subarray(at + 46, at + 46 + nameLen).toString("utf8")
    const localNameLen = buf.readUInt16LE(local + 26)
    const data = buf.subarray(local + 30 + localNameLen, local + 30 + localNameLen + size)
    const file = path.join(dir, name)
    mkdirSync(path.dirname(file), { recursive: true })
    writeFileSync(file, method === 8 ? zlib.inflateRawSync(data) : data)
    names.push(name)
    at += 46 + nameLen
  }
  return names
}

// ---- the site the workflow drives, reporting what happened to it ----
let reports = []
const PAGE_PORT = Number(process.env.PAGE_PORT ?? 8796)
const PAGE = `http://127.0.0.1:${PAGE_PORT}`
const site = createServer((req, res) => {
  if (req.method === "POST" && req.url === "/report") {
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
  res.end(`<!doctype html><title>Signup</title>
    <input id="email" name="email"><button id="join" type="button">Join</button><p id="done">not yet</p>
    <script>document.getElementById("join").addEventListener("click", () => {
      document.getElementById("done").textContent = "joined as " + document.getElementById("email").value
      fetch("/report", { method: "POST", body: JSON.stringify({ done: document.getElementById("done").textContent }) })
    })</script>`)
})
await new Promise((r) => site.listen(PAGE_PORT, "127.0.0.1", r))

let plain, ext
try {
  // ---- 1. a signed-in owner and a new workspace, in a plain browser ----
  plain = await chromium.launch({ executablePath: CHROMIUM, headless: true })
  const ctx = await plain.newContext({
    viewport: { width: 1440, height: 1000 },
    acceptDownloads: true,
  })
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
  await call("POST", "/api/auth/register", {
    email: `mh399-${Date.now()}@x.test`,
    password: "Passw0rd!long-enough",
  })
  const { workspace } = await call("POST", "/api/me/workspaces", { name: `MH-399 ${Date.now()}` })
  out.workspace = workspace.slug

  const page = await ctx.newPage()
  await page.goto(`${web}/en/w/${workspace.slug}/automation`)
  await page.waitForSelector("[data-schedule-install]", { timeout: 60000 })
  check(
    "no paired browser: the schedules empty state says so and links to the install page",
    (await page.textContent("body")).includes("A schedule needs a paired browser") &&
      (await page.getAttribute("[data-schedule-install]", "href"))?.endsWith(
        "/automation/extension",
      ),
  )
  await page.locator("[data-schedule-install]").scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${SHOTS}/MH-403-empty-state-no-browser.png` })

  // ---- 2. the install page, and the download ----
  await page.click("[data-install-link]")
  await page.waitForSelector("[data-download-link]", { timeout: 60000 })
  const shownSum = await page.getAttribute("[data-download]  [data-sha256]", "data-sha256")
  const shownText = await page.locator("[data-download] code").first().textContent()
  const shownVersion = await page.getAttribute("[data-download] [data-version]", "data-version")
  out.page = { shownSum, shownVersion }
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.click("[data-download-link]"),
  ])
  const zipPath = path.join(
    mkdtempSync(path.join(tmpdir(), "mh399-dl-")),
    download.suggestedFilename(),
  )
  await download.saveAs(zipPath)
  const zip = readFileSync(zipPath)
  const fileSum = createHash("sha256").update(zip).digest("hex")
  const head = await http.get(`${web}/api/automation/extension/download`)
  out.download = {
    file: download.suggestedFilename(),
    bytes: zip.length,
    sha256: fileSum,
    header: head.headers()["x-checksum-sha256"],
  }
  check(
    "the downloaded file's sha256 is the one the page shows",
    fileSum === shownSum && shownText?.trim() === fileSum,
    out.download,
  )
  check(
    "the download sends the same checksum in a header",
    head.headers()["x-checksum-sha256"] === fileSum,
  )
  check(
    "the file is named for its version",
    download.suggestedFilename().startsWith(`moharrik-extension-${shownVersion}`),
  )

  // ---- 3. unzip and load it unpacked ----
  const dir = mkdtempSync(path.join(tmpdir(), "mh399-unpacked-"))
  const names = unzip(zip, dir)
  out.unzipped = names
  check(
    "the zip holds the extension and not its test harness",
    names.includes("manifest.json") &&
      names.includes("driver.js") &&
      !names.some((n) => /node_modules|^test\/|package\.json/.test(n)),
  )
  // The same check someone following the page would make, with certutil.
  try {
    const cert = execFileSync("certutil", ["-hashfile", zipPath, "SHA256"], { encoding: "utf8" })
    check("certutil agrees with the page", cert.replace(/\s/g, "").toLowerCase().includes(shownSum))
  } catch (e) {
    out.certutil = String(e.message).slice(0, 200)
  }

  ext = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh399-profile-")), {
    executablePath: CHROMIUM,
    headless: false,
    viewport: { width: 1280, height: 800 },
    args: [`--disable-extensions-except=${dir}`, `--load-extension=${dir}`, "--no-first-run"],
  })
  let worker = ext.serviceWorkers()[0]
  if (!worker) worker = await ext.waitForEvent("serviceworker", { timeout: 30000 })
  const extensionID = new URL(worker.url()).host
  const loaded = await worker.evaluate(() => chrome.runtime.getManifest())
  check("Chromium loaded the unzipped extension", loaded.version === shownVersion, {
    manifest: loaded.version,
  })

  // ---- 4. pair it ----
  const popup = await ext.newPage()
  await popup.goto(`chrome-extension://${extensionID}/popup.html`)
  const baked = { site: await popup.inputValue("#site"), api: await popup.inputValue("#api") }
  out.popup = baked
  check(
    "the popup already knows which site it belongs to (baked into the build)",
    baked.site === web && baked.api !== "",
    baked,
  )
  await popup.fill("#workspace", workspace.slug)
  await popup.click("#start")
  await popup.waitForSelector("#codebox:not([hidden])", { timeout: 20000 })
  const code = (await popup.textContent("#code")).replace(/\s/g, "")

  await page.bringToFront()
  await page.fill("#pair-code", code)
  await page.fill("#pair-name", "MH-399 Chromium")
  await page.fill("#pair-hosts", "127.0.0.1")
  await page.click('form[data-form="approve"] button[type="submit"]')
  let agent
  for (let i = 0; i < 40 && !agent; i++) {
    const { browsers } = await call("GET", "/api/w/browsers", undefined, workspace.id)
    agent = browsers.find((b) => b.status === "online")
    if (!agent) await new Promise((r) => setTimeout(r, 1500))
  }
  check("approved on the install page, the browser comes online by itself", !!agent)
  if (!agent) throw new Error("the extension never came online")
  check(
    "it reports the version it was installed at, and is not flagged outdated",
    agent.extension_version === shownVersion && agent.update_available === false,
    agent,
  )
  await page.waitForSelector("[data-paired-count]", { timeout: 20000 })
  await page.screenshot({
    path: `${SHOTS}/MH-399-install-paired-en-light-1440.png`,
    fullPage: true,
  })

  // ---- 5. run a workflow in it ----
  const { workflow } = await call(
    "POST",
    "/api/w/workflows",
    {
      name: "Join the list",
      steps: [
        { command: "navigate", args: { url: `${PAGE}/signup` } },
        { command: "type", args: { selector: "#email", text: "{{email}}" } },
        { command: "click", args: { selector: "#join" } },
      ],
      params: [{ name: "email", required: true }],
    },
    workspace.id,
  )
  reports = []
  const { run_id } = await call(
    "POST",
    `/api/w/workflows/${workflow.id}/run`,
    { agent_id: agent.id, values: { email: "a@b.test" } },
    workspace.id,
  )
  let run
  for (let i = 0; i < 60; i++) {
    run = await call("GET", `/api/w/runs/${run_id}`, undefined, workspace.id)
    if (!["queued", "running"].includes(run.run.status)) break
    await new Promise((r) => setTimeout(r, 1000))
  }
  out.run = {
    status: run.run.status,
    error: run.run.error,
    steps: run.steps.map((s) => `${s.command} ${s.status}`),
  }
  check("a workflow runs in the installed extension", run.run.status === "ok", out.run)
  check(
    "and the page it drove saw it happen",
    reports.some((r) => r.done === "joined as a@b.test"),
    reports,
  )

  // ---- the outdated flag, as the page shows it ----
  // The extension reports its version on connect; an older one is simulated
  // on the row, which is what the page reads.
  execFileSync("docker", [
    "exec",
    "pg",
    "psql",
    "-U",
    "postgres",
    "-d",
    process.env.APP_DB ?? "moharrik_f",
    "-c",
    `UPDATE browser_agents SET extension_version = '0.1.2' WHERE id = '${agent.id}'`,
  ])
  await page.goto(`${web}/en/w/${workspace.slug}/automation`)
  await page.waitForSelector("[data-update-available]", { timeout: 20000 })
  check(
    "a browser on an older extension is flagged, linking to the install page",
    (await page.getAttribute("[data-update-available]", "href"))?.endsWith("/automation/extension"),
  )
  await page.screenshot({ path: `${SHOTS}/MH-399-browser-outdated-en-light-1440.png` })
} catch (err) {
  out.error = String(err?.stack || err)
} finally {
  const failed = Object.values(out.checks).filter((v) => v !== "PASS").length
  out.summary = out.error ? "ERROR" : failed ? `${failed} FAILED` : "ALL PASS"
  await ext?.close().catch(() => {})
  await plain?.close().catch(() => {})
  site.close()
  process.exitCode = out.error || failed ? 1 : 0
}
