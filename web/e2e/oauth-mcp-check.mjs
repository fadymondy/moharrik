// OAuth for MCP, end to end (MH-391), with the audit trail it leaves (MH-392).
//
//   node e2e/oauth-mcp-check.mjs http://localhost:8510
//
// Acts as BOTH parties, over HTTP only, against a running server:
//
//   the connector (what claude.ai / ChatGPT do):
//     401 + WWW-Authenticate → protected-resource metadata → server metadata →
//     dynamic client registration → authorization request with PKCE (S256) →
//     code → token → MCP initialize + tools/list + a write → refresh (rotation,
//     reuse detection) → revocation
//   the member (what the consent page does):
//     signs up, makes two workspaces, reads the consent request, approves ONE
//     workspace with a narrowed set of scopes
//
// and asserts the token cannot reach the other workspace, a spent refresh
// token revokes the connection, and the audit log names the OAuth client.
// Every address it creates is on the reserved .example TLD.
import { createHash, randomBytes } from "node:crypto"

const base = (process.argv[2] ?? "http://localhost:8510").replace(/\/$/, "")
const ok = (cond, what) => {
  if (!cond) {
    console.error("FAIL:", what)
    process.exit(1)
  }
  console.log("ok  ", what)
}
const b64url = (buf) => buf.toString("base64").replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")

class Session {
  cookies = new Map()
  csrf = null
  async req(method, p, { json, ws } = {}) {
    const h = { Accept: "application/json" }
    if (json !== undefined) h["Content-Type"] = "application/json"
    if (ws) h["X-Workspace-ID"] = ws
    if (method !== "GET") {
      if (!this.csrf) {
        const r = await this.req("GET", "/api/auth/csrf")
        this.csrf = (await r.json()).csrf_token
      }
      h["X-CSRF-Token"] = this.csrf
    }
    if (this.cookies.size) h.Cookie = [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ")
    const res = await fetch(base + p, { method, headers: h, body: json !== undefined ? JSON.stringify(json) : undefined, redirect: "manual" })
    for (const c of res.headers.getSetCookie?.() ?? []) {
      const [pair] = c.split(";")
      const i = pair.indexOf("=")
      this.cookies.set(pair.slice(0, i).trim(), pair.slice(i + 1).trim())
    }
    return res
  }
  async json(method, p, opts, want) {
    const r = await this.req(method, p, opts)
    const body = await r.json().catch(() => ({}))
    if (want && r.status !== want) {
      console.error(method, p, r.status, body)
      process.exit(1)
    }
    return body
  }
}

async function mcp(token, method, params = {}) {
  const r = await fetch(base + "/api/mcp", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  })
  return { status: r.status, www: r.headers.get("www-authenticate"), body: await r.json().catch(() => ({})) }
}

async function form(p, fields) {
  const r = await fetch(base + p, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(fields).toString(),
  })
  return { status: r.status, body: await r.json().catch(() => ({})) }
}

// ---- discovery, as a connector does it ----------------------------------------------------
const first = await mcp("", "initialize", { protocolVersion: "2025-06-18" })
ok(first.status === 401, "an unauthenticated MCP call is 401")
const rm = /resource_metadata="([^"]+)"/.exec(first.www ?? "")?.[1]
ok(!!rm, `the 401 carries WWW-Authenticate resource_metadata (${rm})`)
const prm = await (await fetch(rm)).json()
ok(prm.resource === base + "/api/mcp" && prm.authorization_servers?.[0] === base, "protected-resource metadata names the resource and the issuer")
const asm = await (await fetch(base + "/.well-known/oauth-authorization-server")).json()
ok(asm.code_challenge_methods_supported?.join() === "S256", "server metadata: PKCE S256 only")
ok(!!asm.registration_endpoint && !!asm.token_endpoint && !!asm.revocation_endpoint, "server metadata: registration, token and revocation endpoints")

// ---- dynamic client registration -----------------------------------------------------------
const redirect = "http://127.0.0.1:33418/callback"
const bad = await fetch(asm.registration_endpoint, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ client_name: "Evil", redirect_uris: ["http://evil.example/cb"] }),
})
ok(bad.status === 400, "DCR refuses a plain-http, non-loopback redirect URI")
const reg = await (
  await fetch(asm.registration_endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "OAuth check client", redirect_uris: [redirect], token_endpoint_auth_method: "none" }),
  })
).json()
ok(reg.client_id?.startsWith("mhc_"), `DCR registered ${reg.client_id}`)

// ---- the member ------------------------------------------------------------------------------
const member = new Session()
const email = `oauth-${randomBytes(4).toString("hex")}@mail.example`
await member.json("POST", "/api/auth/register", { json: { email, password: "correct-horse-" + randomBytes(4).toString("hex") } }, 201)
const wsA = (await member.json("POST", "/api/me/workspaces", { json: { name: "OAuth A " + randomBytes(2).toString("hex") } }, 201)).workspace
const wsB = (await member.json("POST", "/api/me/workspaces", { json: { name: "OAuth B " + randomBytes(2).toString("hex") } }, 201)).workspace
// A marker contact in B the token must never see.
const markerB = "OnlyInB-" + randomBytes(3).toString("hex")
await member.json("POST", "/api/w/contacts", { ws: wsB.id, json: { email: `${markerB.toLowerCase()}@mail.example`, name: markerB } }, 201)

// ---- authorize (PKCE) ------------------------------------------------------------------------
const verifier = b64url(randomBytes(48))
const challenge = b64url(createHash("sha256").update(verifier).digest())
const state = b64url(randomBytes(12))
const params = {
  response_type: "code",
  client_id: reg.client_id,
  redirect_uri: redirect,
  code_challenge: challenge,
  code_challenge_method: "S256",
  state,
  scope: "members:read contacts:read contacts:write audit:read",
  resource: base + "/api/mcp",
}
const plain = await member.req("GET", "/api/oauth/authorize/request?" + new URLSearchParams({ ...params, code_challenge_method: "plain" }))
ok(plain.status === 400, "code_challenge_method=plain is refused")
const consent = await member.json("GET", "/api/oauth/authorize/request?" + new URLSearchParams(params), {}, 200)
ok(consent.client.name === "OAuth check client", "the consent request names the client")
ok(consent.workspaces.length === 2, "the consent request lists the member's two workspaces")
const inA = consent.workspaces.find((w) => w.id === wsA.id)
ok(inA.allowed_scopes.includes("audit:read"), "an owner may grant audit:read")
const wrongRedirect = await member.req("GET", "/api/oauth/authorize/request?" + new URLSearchParams({ ...params, redirect_uri: redirect + "x" }))
ok(wrongRedirect.status === 400, "a redirect URI that is not an exact match is refused")

const decision = await member.json(
  "POST",
  "/api/oauth/authorize/decision",
  { json: { ...params, approve: true, workspace: wsA.id, granted_scopes: ["members:read", "contacts:read", "contacts:write", "audit:read"] } },
  200,
)
const back = new URL(decision.redirect_to)
ok(back.origin + back.pathname === redirect && back.searchParams.get("state") === state && back.searchParams.get("iss") === base, "approval redirects with the code, state and iss")
const code = back.searchParams.get("code")

// ---- token ----------------------------------------------------------------------------------
const wrongVerifier = await form("/api/oauth/token", { grant_type: "authorization_code", code, redirect_uri: redirect, client_id: reg.client_id, code_verifier: b64url(randomBytes(48)) })
ok(wrongVerifier.status === 400 && wrongVerifier.body.error === "invalid_grant", "a wrong code_verifier is invalid_grant (and spends the code)")

// The code was spent by the wrong verifier; approve again for a fresh one.
const again = await member.json("POST", "/api/oauth/authorize/decision", { json: { ...params, approve: true, workspace: wsA.id, granted_scopes: ["members:read", "contacts:read", "contacts:write", "audit:read"] } }, 200)
const code2 = new URL(again.redirect_to).searchParams.get("code")
const tok = await form("/api/oauth/token", { grant_type: "authorization_code", code: code2, redirect_uri: redirect, client_id: reg.client_id, code_verifier: verifier, resource: base + "/api/mcp" })
ok(tok.status === 200 && tok.body.access_token?.startsWith("mho_at_") && tok.body.refresh_token?.startsWith("mho_rt_"), "the code is exchanged for an access and a refresh token")
ok(tok.body.expires_in === 3600, "the access token lives an hour")

// ---- MCP ------------------------------------------------------------------------------------
const init = await mcp(tok.body.access_token, "initialize", { protocolVersion: "2025-06-18" })
ok(init.status === 200 && init.body.result?.serverInfo?.name === "moharrik-mcp", "MCP initialize with the OAuth token")
const list = await mcp(tok.body.access_token, "tools/list")
ok(list.status === 200 && list.body.result.tools.length > 50, `tools/list returns ${list.body.result?.tools?.length} tools`)
const who = await mcp(tok.body.access_token, "tools/call", { name: "whoami", arguments: {} })
const whoText = who.body.result.content[0].text
ok(whoText.includes(wsA.id) && !whoText.includes(wsB.id), "whoami: the token acts in workspace A only")
ok(!whoText.includes("inbox:read"), "the token carries only the granted scopes")
const created = await mcp(tok.body.access_token, "tools/call", { name: "create_contact", arguments: { email: `from-oauth-${randomBytes(3).toString("hex")}@mail.example`, name: "ViaOAuth" } })
ok(!created.body.result.isError, "a write tool works through the OAuth token")
const search = await mcp(tok.body.access_token, "tools/call", { name: "search_contacts", arguments: { q: markerB } })
ok(!search.body.result.content[0].text.includes(markerB), "workspace B's contact is invisible to a token bound to A")
const inbox = await mcp(tok.body.access_token, "tools/call", { name: "list_conversations", arguments: {} })
ok(inbox.body.result.isError && inbox.body.result.content[0].text.includes("inbox:read"), "an ungranted scope is refused in-band")

// ---- the audit log names the client ---------------------------------------------------------
const auditPage = await member.json("GET", "/api/w/audit?actor_kind=oauth", { ws: wsA.id }, 200)
const row = auditPage.entries.find((e) => e.entity_type === "contacts" && e.action === "create")
ok(row && row.actor_name === "OAuth check client" && row.operation === "mcp:create_contact" && row.via === "mcp", "the audit log records the contact as created by the OAuth client through mcp:create_contact")
const viaTool = await mcp(tok.body.access_token, "tools/call", { name: "audit_log", arguments: { actor_kind: "oauth", limit: 5 } })
ok(!viaTool.body.result.isError && viaTool.body.result.content[0].text.includes("mcp:create_contact"), "the audit_log MCP tool reads the same log")

// ---- connected apps --------------------------------------------------------------------------
const grants = await member.json("GET", "/api/w/oauth_grants", { ws: wsA.id }, 200)
ok(grants.grants.length === 1 && grants.grants[0].client_name === "OAuth check client", "Settings lists the connected app in A")
const grantsB = await member.json("GET", "/api/w/oauth_grants", { ws: wsB.id }, 200)
ok(grantsB.grants.length === 0, "and not in B")

// ---- refresh: rotation and reuse detection --------------------------------------------------
const r1 = await form("/api/oauth/token", { grant_type: "refresh_token", refresh_token: tok.body.refresh_token, client_id: reg.client_id })
ok(r1.status === 200 && r1.body.refresh_token !== tok.body.refresh_token, "refresh rotates the refresh token")
const widen = await form("/api/oauth/token", { grant_type: "refresh_token", refresh_token: r1.body.refresh_token, client_id: reg.client_id, scope: "inbox:read" })
ok(widen.status === 400, "a refresh cannot widen the scopes (and that attempt spent the token)")
const reuse = await form("/api/oauth/token", { grant_type: "refresh_token", refresh_token: tok.body.refresh_token, client_id: reg.client_id })
ok(reuse.status === 400 && reuse.body.error === "invalid_grant", "reusing a spent refresh token is invalid_grant")
const dead = await mcp(r1.body.access_token, "tools/list")
ok(dead.status === 401 && /invalid_token/.test(dead.www ?? ""), "…and it revoked the whole connection: the newest access token is dead too")

// ---- a fresh connection, revoked by the client (RFC 7009) and by the member ------------------
async function connect() {
  const d = await member.json("POST", "/api/oauth/authorize/decision", { json: { ...params, approve: true, workspace: wsA.id, granted_scopes: ["members:read"] } }, 200)
  const c = new URL(d.redirect_to).searchParams.get("code")
  return (await form("/api/oauth/token", { grant_type: "authorization_code", code: c, redirect_uri: redirect, client_id: reg.client_id, code_verifier: verifier })).body
}
const t2 = await connect()
ok((await mcp(t2.access_token, "tools/list")).status === 200, "a second connection works")
const rv = await form("/api/oauth/revoke", { token: t2.refresh_token, client_id: reg.client_id })
ok(rv.status === 200, "RFC 7009 revocation answers 200")
ok((await mcp(t2.access_token, "tools/list")).status === 401, "revoking the refresh token ends the connection")

const t3 = await connect()
const g3 = (await member.json("GET", "/api/w/oauth_grants", { ws: wsA.id }, 200)).grants.find((g) => !g.revoked_at)
await member.json("DELETE", `/api/w/oauth_grants/${g3.id}`, { ws: wsA.id })
ok((await mcp(t3.access_token, "tools/list")).status === 401, "disconnecting in Settings cuts the connector off")

const denied = await member.json("POST", "/api/oauth/authorize/decision", { json: { ...params, approve: false } }, 200)
ok(new URL(denied.redirect_to).searchParams.get("error") === "access_denied", "Deny redirects with error=access_denied")

console.log("\nall OAuth checks passed against", base)
