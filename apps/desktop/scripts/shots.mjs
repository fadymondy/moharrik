// Screenshots of the desktop app against the local demo stack (apps/scripts/demo-stack.mjs), never a
// real workspace: builds if needed, runs Electron with MOHARRIK_SHOTS (src/main/shots.ts walks the
// screens), and when it writes `.ping` posts a visitor chat message so the native alert fires.
//
//   node scripts/shots.mjs [--out <dir>] [--server http://127.0.0.1:8130] [--api http://127.0.0.1:8131]
import { spawn, spawnSync } from "node:child_process"
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs"
import { createRequire } from "node:module"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const arg = (k, d) => {
  const i = process.argv.indexOf(`--${k}`)
  return i > 0 ? process.argv[i + 1] : d
}
const out = arg("out", process.env.MOHARRIK_SHOTS || join(root, "release", "shots"))
const server = arg("server", "http://127.0.0.1:8130")
const API = arg("api", "http://127.0.0.1:8131")
const statePath = join(root, "..", "scripts", ".demo-state.json")

if (!existsSync(statePath)) throw new Error(`no demo stack state at ${statePath}: run apps/scripts/demo-stack.mjs --seed first`)
const state = JSON.parse(readFileSync(statePath, "utf8"))

if (!existsSync(join(root, "dist", "main.cjs")) || process.argv.includes("--build")) {
  const r = spawnSync("npm", ["run", "build"], { cwd: root, stdio: "inherit", shell: true, env: { ...process.env, MOHARRIK_URL: server } })
  if (r.status !== 0) process.exit(r.status ?? 1)
}

/** The demo workspace's chat widget, read with the seeded user's session. */
async function widgetKey() {
  const headers = { Cookie: state.cookies.map(([k, v]) => `${k}=${v}`).join("; "), "X-Workspace-ID": state.workspace }
  const r = await fetch(`${API}/api/w/chat_widgets`, { headers })
  if (!r.ok) throw new Error(`chat widgets: ${r.status} ${await r.text()}`)
  const body = await r.json()
  const list = body.widgets ?? body.chat_widgets ?? body.items ?? []
  if (!list.length) throw new Error("the demo workspace has no chat widget")
  return list[0].public_key
}

async function visitorMessage() {
  const key = await widgetKey()
  const r = await fetch(`${API}/api/chat/${key}/messages`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: new URL(server).origin },
    body: JSON.stringify({ body: "مرحباً! هل يمكنني حجز عرض توضيحي غداً؟", name: "Layla Nabil", email: "layla@example.test" }),
  })
  if (r.status !== 201) throw new Error(`chat message: ${r.status} ${await r.text()}`)
  console.log("posted a visitor message to the demo widget")
}

mkdirSync(out, { recursive: true })
const userData = mkdtempSync(join(tmpdir(), "moharrik-shots-"))
const electron = createRequire(import.meta.url)("electron")

// The app touches this file once it is listening; polled, as a watch on the output folder can
// hold it open on Windows and fail the app's own writes there.
const ping = join(userData, "ping")
const watcher = setInterval(() => {
  if (!existsSync(ping)) return
  clearInterval(watcher)
  visitorMessage().catch((e) => console.error(String(e)))
}, 250)

// A parent that is itself Electron-as-Node (an IDE, a terminal app) must not pass that on.
const env = { ...process.env, MOHARRIK_E2E: "1", MOHARRIK_URL: server, MOHARRIK_SHOTS: out, MOHARRIK_USER_DATA: userData, MOHARRIK_PING: ping }
delete env.ELECTRON_RUN_AS_NODE
const child = spawn(electron, ["."], { cwd: root, stdio: "inherit", env })
child.on("exit", (code) => {
  clearInterval(watcher)
  rmSync(userData, { recursive: true, force: true })
  const result = join(out, "desktop-result.json")
  if (existsSync(result)) console.log(readFileSync(result, "utf8"))
  process.exit(code ?? 0)
})
