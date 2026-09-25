// Read-only production check for MH-603: the leads, marketing, research and browser/workflow
// MCP tools answer JSON in the shared shape instead of prose. Signs in as the demo user, mints a
// short-lived token on the demo workspace, calls only list_* tools with no arguments, and revokes
// the token. The token is never printed.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mh603-mcp-json-live.mjs https://app.moharrik.com
const base = process.argv[2] ?? "https://app.moharrik.com"
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const jar = new Map()
async function rest(method, path, body, ws) {
  const headers = {
    "Content-Type": "application/json",
    Cookie: [...jar].map(([k, v]) => `${k}=${v}`).join("; "),
  }
  if (ws) headers["X-Workspace-ID"] = ws
  if (method !== "GET") headers["X-CSRF-Token"] = jar.get("__csrf") ?? ""
  const res = await fetch(base + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })
  for (const c of res.headers.getSetCookie?.() ?? []) {
    const [kv] = c.split(";")
    const i = kv.indexOf("=")
    jar.set(kv.slice(0, i), kv.slice(i + 1))
  }
  let json = {}
  try {
    json = JSON.parse(await res.text())
  } catch {}
  return { status: res.status, json }
}
jar.set("__csrf", (await rest("GET", "/api/auth/csrf")).json.csrf_token)
const login = await rest("POST", "/api/auth/login", {
  email: process.env.DEMO_EMAIL,
  password: process.env.DEMO_PASSWORD,
})
const wss = (await rest("GET", "/api/me/workspaces")).json
const ws = (wss.workspaces ?? wss.items ?? wss).find?.((w) => w.slug === slug)?.id
if (!ws) {
  console.log(JSON.stringify({ login: login.status, workspaces: Object.keys(wss) }))
  process.exit(1)
}
const scopes = (await rest("GET", "/api/w/mcp_tokens", null, ws)).json.scopes.map((s) => s.scope)
const minted = (
  await rest("POST", "/api/w/mcp_tokens", { name: `MH-603 live check ${Date.now()}`, scopes }, ws)
).json
const TOOLS = [
  "list_leads",
  "list_mail_lists",
  "list_campaigns",
  "list_research",
  "list_workflows",
  "list_browsers",
]
let out = {}
try {
  let id = 0
  for (const name of TOOLS) {
    const res = await fetch(`${base}/api/mcp`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${minted.plaintext}`,
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: ++id,
        method: "tools/call",
        params: { name, arguments: {} },
      }),
    })
    const r = (await res.json()).result ?? {}
    const text = r.content?.find((c) => c.type === "text")?.text ?? ""
    let keys = null
    try {
      const j = JSON.parse(text)
      keys = j && typeof j === "object" && !Array.isArray(j) ? Object.keys(j) : null
    } catch {}
    out[name] = { status: res.status, isError: !!r.isError, json: !!keys, keys }
  }
} finally {
  const del = await rest("DELETE", `/api/w/mcp_tokens/${minted.token.id}`, null, ws)
  out.revoked = del.status < 300
}
console.log(JSON.stringify(out, null, 1))
const ok =
  TOOLS.every((n) => out[n].status === 200 && !out[n].isError && out[n].json) && out.revoked
console.log(ok ? "ALL OK" : "FAIL")
process.exit(ok ? 0 : 1)
