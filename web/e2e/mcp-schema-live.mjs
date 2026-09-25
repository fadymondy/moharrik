// Read-only production check for MH-557: every tool on the live MCP server publishes an
// inputSchema strict clients (ChatGPT, Claude Code) accept — type "object" and an object of
// properties, never null. Signs in as the demo user, mints a short-lived token on the demo
// workspace, lists the tools, and revokes the token. The token is never printed.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/mcp-schema-live.mjs https://app.moharrik.com
const base = process.argv[2] ?? "https://app.moharrik.com"
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const jar = new Map()
async function rest(method, path, body, ws) {
  const headers = { "Content-Type": "application/json", Cookie: [...jar].map(([k, v]) => `${k}=${v}`).join("; ") }
  if (ws) headers["X-Workspace-ID"] = ws
  if (method !== "GET") headers["X-CSRF-Token"] = jar.get("__csrf") ?? ""
  const res = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined })
  for (const c of res.headers.getSetCookie?.() ?? []) {
    const [kv] = c.split(";"); const i = kv.indexOf("="); jar.set(kv.slice(0, i), kv.slice(i + 1))
  }
  let json = {}; try { json = JSON.parse(await res.text()) } catch {}
  return { status: res.status, json }
}
jar.set("__csrf", (await rest("GET", "/api/auth/csrf")).json.csrf_token)
const login = await rest("POST", "/api/auth/login", { email: process.env.DEMO_EMAIL, password: process.env.DEMO_PASSWORD })
const wss = (await rest("GET", "/api/me/workspaces")).json
const ws = (wss.workspaces ?? wss.items ?? wss).find?.((w) => w.slug === slug)?.id
if (!ws) { console.log(JSON.stringify({ login: login.status, workspaces: Object.keys(wss) })); process.exit(1) }
const scopes = (await rest("GET", "/api/w/mcp_tokens", null, ws)).json.scopes.map((s) => s.scope)
const minted = (await rest("POST", "/api/w/mcp_tokens", { name: `MH-557 live check ${Date.now()}`, scopes }, ws)).json
let out = {}
try {
  const res = await fetch(`${base}/api/mcp`, { method: "POST",
    headers: { Authorization: `Bearer ${minted.plaintext}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }) })
  const tools = (await res.json()).result?.tools ?? []
  const bad = tools.filter((t) => { const s = t.inputSchema
    return !s || s.type !== "object" || s.properties === null || typeof s.properties !== "object" || Array.isArray(s.properties) ||
      Object.values(s.properties).some((p) => !p || typeof p !== "object") || (s.required && !Array.isArray(s.required)) }).map((t) => t.name)
  const raw = JSON.stringify(tools)
  // MH-528: browser_plugin_run says it waits and answers with the data, polling via browser_result.
  const pr = tools.find((t) => t.name === "browser_plugin_run")?.description ?? ""
  out = { status: res.status, tools: tools.length, bad, nullProperties: /"properties":null/.test(raw),
    pluginRunDescribed: /25 seconds/.test(pr) && /browser_result/.test(pr) && !/get_browser_run/.test(pr) }
} finally {
  const del = await rest("DELETE", `/api/w/mcp_tokens/${minted.token.id}`, null, ws); out.revoked = del.status < 300
}
console.log(JSON.stringify(out))
console.log(out.status === 200 && out.tools > 0 && out.bad.length === 0 && !out.nullProperties && out.pluginRunDescribed && out.revoked ? "ALL OK" : "FAIL")
