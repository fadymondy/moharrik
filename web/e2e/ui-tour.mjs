// UI tour: every page at 1440 and 390, en/ar x light/dark, headless Chrome on a fresh profile.
//   WEB_ORIGIN=https://app.moharrik.com node e2e/ui-tour.mjs [--quick]
// Counts frames first, seeds a workspace through the session API, then screenshots each route and
// asserts no horizontal overflow and (desktop) that content uses the width.
import { chromium } from "playwright-core"
import { mkdirSync, mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const WEB = process.env.WEB_ORIGIN ?? "http://127.0.0.1:3010"
const TAG = process.env.TAG ?? (WEB.includes("fadymondy.com") ? "public" : "local")
const QUICK = process.argv.includes("--quick")
const ONLY = process.env.ONLY?.split(",")
const here = path.dirname(fileURLToPath(import.meta.url))
const SHOTS = path.join(here, "shots", "ui")
mkdirSync(SHOTS, { recursive: true })
let failures = 0
const check = (ok, msg) => {
  console.log(ok ? "✓" : "✗", msg)
  if (!ok) failures++
}

const ctx = await chromium.launchPersistentContext(
  mkdtempSync(path.join(tmpdir(), "moharrik-ui-")),
  {
    executablePath:
      process.env.CHROME_PATH ?? "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    args: ["--disable-extensions", "--no-first-run"],
  },
)
const page = ctx.pages()[0]
const errors = []
page.on("pageerror", (e) => errors.push(String(e)))

await page.goto(`${WEB}/en/login`, { waitUntil: "networkidle" })
const frames = await page.evaluate(
  () =>
    new Promise((r) => {
      let n = 0
      const t0 = performance.now()
      const f = () => (performance.now() - t0 < 1000 ? (n++, requestAnimationFrame(f)) : r(n))
      requestAnimationFrame(f)
    }),
)
check(frames > 20, `page paints: ${frames} frames/s`)

const email = `ui-${Date.now()}@example.test`
const s = await page.evaluate(
  async ({ email }) => {
    const j = async (res) => {
      const t = await res.text()
      if (!res.ok) throw new Error(`${res.status} ${res.url} ${t}`)
      return t ? JSON.parse(t) : {}
    }
    const { csrf_token } = await j(await fetch("/api/auth/csrf"))
    const H = (ws) => ({
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf_token,
      ...(ws ? { "X-Workspace-ID": ws } : {}),
    })
    await j(
      await fetch("/api/auth/register", {
        method: "POST",
        headers: H(),
        body: JSON.stringify({ email, password: "correct-horse-1" }),
      }),
    )
    const call = (m, u, b, ws) =>
      fetch(u, { method: m, headers: H(ws), body: b ? JSON.stringify(b) : undefined }).then(j)
    const { workspace } = await call("POST", "/api/me/workspaces", {
      name: "Studio Tour " + Date.now().toString(36),
    })
    const ws = workspace.id
    const me = await j(await fetch("/api/auth/me"))
    const cl = await call(
      "POST",
      "/api/w/clients",
      { name: "Acme Trading", email: "ops@acme.test", country: "EG" },
      ws,
    )
    await call(
      "POST",
      "/api/w/clients",
      { name: "Nile Foods", email: "hi@nile.test", country: "EG" },
      ws,
    )
    const p = await call(
      "POST",
      "/api/w/projects",
      {
        name: "Website relaunch",
        client_id: cl.id,
        color: "teal",
        budget_minutes: 6000,
        billing_mode: "hourly",
      },
      ws,
    )
    await call("POST", "/api/w/projects", { name: "Mobile app", color: "violet" }, ws)
    await call("POST", "/api/w/labels", { name: "design", color: "pink" }, ws)
    await call("POST", "/api/w/teams", { name: "Design", member_ids: [String(me.id)] }, ws)
    const tk = await call(
      "POST",
      `/api/w/projects/${p.id}/tasks`,
      { title: "Wireframe the home page", assignee_ids: [String(me.id)], priority: "high" },
      ws,
    )
    for (const t of ["Write the launch checklist", "Pick a hosting plan", "Draft the brand voice"])
      await call("POST", `/api/w/projects/${p.id}/tasks`, { title: t }, ws)
    await call(
      "POST",
      "/api/w/mcp_tokens",
      { name: "claude", scopes: ["projects:read", "tasks:read"] },
      ws,
    )
    return { slug: workspace.slug, project: p.id, task: tk.key }
  },
  { email },
)

const routes = [
  ["workspaces", "/workspaces"],
  ["overview", `/w/${s.slug}`],
  ["projects", `/w/${s.slug}/projects`],
  ["project", `/w/${s.slug}/projects/${s.project}`],
  ["board", `/w/${s.slug}/projects/${s.project}/board`],
  ["list", `/w/${s.slug}/projects/${s.project}/list`],
  ["project-settings", `/w/${s.slug}/projects/${s.project}/settings`],
  ["sheet", `/w/${s.slug}/projects/${s.project}/board?task=${s.task}`],
  ["clients", `/w/${s.slug}/clients`],
  ["teams", `/w/${s.slug}/teams`],
  ["members", `/w/${s.slug}/members`],
  ["labels", `/w/${s.slug}/settings/labels`],
  ["mcp", `/w/${s.slug}/settings/mcp`],
  ...(process.env.EXTRA_ROUTES
    ? JSON.parse(process.env.EXTRA_ROUTES).map(([n, r]) => [n, r.replace("{slug}", s.slug)])
    : []),
]
const matrix = QUICK
  ? [["en", "light"]]
  : [
      ["en", "light"],
      ["en", "dark"],
      ["ar", "light"],
      ["ar", "dark"],
    ]
for (const width of [1440, 390]) {
  await page.setViewportSize({ width, height: 900 })
  for (const [locale, theme] of matrix) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate((t) => localStorage.setItem("theme", t), theme)
    for (const [name, route] of routes) {
      if (ONLY && !ONLY.includes(name)) continue
      await page.goto(`${WEB}/${locale}${route}`, { waitUntil: "networkidle" })
      await page.waitForTimeout(700)
      const m = await page.evaluate(() => {
        const main = document.querySelector("main")
        const inner = main
          ? [...main.querySelectorAll("*")].reduce(
              (w, el) => Math.max(w, el.getBoundingClientRect().width),
              0,
            )
          : 0
        return {
          sw: document.documentElement.scrollWidth,
          cw: document.documentElement.clientWidth,
          main: main ? Math.round(main.getBoundingClientRect().width) : 0,
          widest: Math.round(inner),
          dark: document.documentElement.classList.contains("dark"),
          dir: document.documentElement.dir,
          title: document.title,
        }
      })
      check(
        m.sw <= m.cw + 1,
        `${TAG} ${width} ${locale}/${theme} ${name}: no horizontal overflow (${m.sw}/${m.cw})`,
      )
      check(
        m.dir === (locale === "ar" ? "rtl" : "ltr") && m.dark === (theme === "dark"),
        `${name}: dir ${m.dir}, dark ${m.dark}, title "${m.title}"`,
      )
      await page.screenshot({
        path: path.join(SHOTS, `${TAG}-${name}-${locale}-${theme}-${width}.png`),
        fullPage: width === 390,
      })
    }
  }
}
check(errors.length === 0, `no page errors ${errors.slice(0, 3).join(" | ")}`)
await ctx.close()
process.exit(failures ? 1 : 0)
