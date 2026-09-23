// A tiny OAuth MCP client whose consent step is done by a person in a real browser (MH-391).
//
//   node e2e/oauth-browser-client.mjs http://localhost:8510 [port]
//
// It does what a desktop MCP client does (RFC 8252 loopback redirect):
//   1. discovers the server from the 401 on /api/mcp,
//   2. registers itself (DCR) with http://127.0.0.1:<port>/callback,
//   3. prints the authorization URL — open it, sign in, pick the workspace, Approve,
//   4. receives the code on its loopback server, exchanges it (PKCE S256),
//   5. calls initialize + tools/list + whoami over MCP, refreshes, revokes,
//      and checks the revoked token is refused.
// The result is printed and also shown on the callback page, so a screenshot of the
// tab records the end of the flow. No secrets are printed: tokens are shown as prefixes.
import { createHash, randomBytes } from "node:crypto"
import http from "node:http"

const base = (process.argv[2] ?? "http://localhost:8510").replace(/\/$/, "")
const port = Number(process.argv[3] ?? 33418)
const redirect = `http://127.0.0.1:${port}/callback`
const b64url = (b) => b.toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
const short = (s) => (s ? `${s.slice(0, 10)}…` : "")

async function mcp(token, method, params = {}) {
  const r = await fetch(`${base}/api/mcp`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  })
  return { status: r.status, www: r.headers.get("www-authenticate"), body: await r.json().catch(() => ({})) }
}
async function form(url, fields) {
  const r = await fetch(url, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(fields) })
  return { status: r.status, body: await r.json().catch(() => ({})) }
}

const first = await mcp("", "initialize")
const rm = /resource_metadata="([^"]+)"/.exec(first.www ?? "")?.[1]
const prm = await (await fetch(rm)).json()
const asm = await (await fetch(`${prm.authorization_servers[0]}/.well-known/oauth-authorization-server`)).json()
const reg = await (
  await fetch(asm.registration_endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "Moharrik OAuth check (desktop)", redirect_uris: [redirect], token_endpoint_auth_method: "none" }),
  })
).json()
const verifier = b64url(randomBytes(48))
const state = b64url(randomBytes(12))
const auth = new URL(asm.authorization_endpoint)
for (const [k, v] of Object.entries({
  response_type: "code",
  client_id: reg.client_id,
  redirect_uri: redirect,
  code_challenge: b64url(createHash("sha256").update(verifier).digest()),
  code_challenge_method: "S256",
  state,
  scope: "members:read contacts:read contacts:write leads:read audit:read",
  resource: prm.resource,
}))
  auth.searchParams.set(k, v)
console.log("401 resource_metadata:", rm)
console.log("registered:", reg.client_id)
console.log("AUTHORIZE_URL=" + auth.toString())

const log = []
const note = (s) => {
  log.push(s)
  console.log(s)
}

const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, redirect)
  if (u.pathname !== "/callback") return res.writeHead(404).end()
  const html = (title) =>
    `<!doctype html><meta charset="utf-8"><title>${title}</title><body style="font:15px system-ui;max-width:720px;margin:40px auto"><h1>${title}</h1><pre style="white-space:pre-wrap">${log.join("\n")}</pre>`
  if (u.searchParams.get("error")) {
    note(`consent answered: error=${u.searchParams.get("error")} (${u.searchParams.get("error_description")})`)
    res.writeHead(200, { "Content-Type": "text/html" }).end(html("Moharrik refused the connection"))
    server.close()
    return
  }
  try {
    note(`callback: state ok=${u.searchParams.get("state") === state}, iss=${u.searchParams.get("iss")}`)
    const tok = await form(asm.token_endpoint, { grant_type: "authorization_code", code: u.searchParams.get("code"), redirect_uri: redirect, client_id: reg.client_id, code_verifier: verifier, resource: prm.resource })
    note(`token: ${tok.status} access=${short(tok.body.access_token)} refresh=${short(tok.body.refresh_token)} expires_in=${tok.body.expires_in} scope="${tok.body.scope}"`)
    const init = await mcp(tok.body.access_token, "initialize", { protocolVersion: "2025-06-18" })
    note(`initialize: ${init.status} ${init.body.result?.serverInfo?.name}`)
    const list = await mcp(tok.body.access_token, "tools/list")
    note(`tools/list: ${list.status} — ${list.body.result?.tools?.length} tools`)
    const who = await mcp(tok.body.access_token, "tools/call", { name: "whoami", arguments: {} })
    note(`whoami:\n${who.body.result?.content?.[0]?.text}`)
    const ref = await form(asm.token_endpoint, { grant_type: "refresh_token", refresh_token: tok.body.refresh_token, client_id: reg.client_id })
    note(`refresh: ${ref.status} rotated=${ref.body.refresh_token !== tok.body.refresh_token}`)
    const rv = await form(asm.revocation_endpoint, { token: ref.body.refresh_token, client_id: reg.client_id })
    note(`revoke: ${rv.status}`)
    const after = await mcp(ref.body.access_token, "tools/list")
    note(`after revoke: tools/list -> ${after.status} ${after.www?.includes("invalid_token") ? "(invalid_token)" : ""}`)
    res.writeHead(200, { "Content-Type": "text/html" }).end(html("Moharrik connected, used, refreshed and revoked"))
  } catch (e) {
    note(`failed: ${e}`)
    res.writeHead(500, { "Content-Type": "text/html" }).end(html("Failed"))
  }
  setTimeout(() => server.close(), 500)
})
server.listen(port, "127.0.0.1")
setTimeout(() => {
  console.log("timed out waiting for the consent")
  process.exit(1)
}, 15 * 60 * 1000).unref()
