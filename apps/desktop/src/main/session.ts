// The signed-in account, owned by the main process. The window never sees the token: it asks for
// data over IPC (the "api" channel) and gets data back.
//
// Sign-in is the shared app sign-in (@moharrik/client signin.ts) with a loopback redirect (RFC 8252 §7.3):
// the app listens on 127.0.0.1 on a port the OS picks, opens <server>/<locale>/app-signin in the
// person's own browser, and waits for the redirect to http://127.0.0.1:<port>/callback with a
// one-time code. The code and the PKCE verifier (which never left this process) are traded for this
// device's token, kept with safeStorage (store.ts).
import { net, shell } from "electron"
import { createHash, randomBytes } from "node:crypto"
import { createServer } from "node:http"
import type { AddressInfo } from "node:net"
import { hostname } from "node:os"

import { api, ApiError, finishSignIn, MoharrikClient, readCallback, startSignIn, type Api, type Locale, type PkceCrypto } from "@moharrik/client"

import type { CurrentWorkspace, SessionOut } from "../shared/types"
import { defaultServer, e2e } from "./config"
import * as store from "./store"

type Reason = "signed-in" | "signed-out" | "workspace" | "expired"
type Listener = (s: SessionOut | null, reason: Reason) => void

const VERSION = process.env.npm_package_version ?? "0"

let server = defaultServer()
let email = ""
let workspace: CurrentWorkspace | null = null
let token: string | null = null
let signedIn = false
let client = makeClient(server)
const listeners = new Set<Listener>()

const nodeCrypto: PkceCrypto = {
  randomBytes: (n) => new Uint8Array(randomBytes(n)),
  sha256: async (d) => new Uint8Array(createHash("sha256").update(d).digest()),
}

export const deviceName = () => `Moharrik for ${process.platform === "darwin" ? "Mac" : process.platform === "win32" ? "Windows" : "Linux"} — ${hostname()}`

function makeClient(url: string): MoharrikClient {
  const c = new MoharrikClient({
    baseUrl: url,
    // net.fetch goes through Chromium's network stack: the system proxy and certificate store.
    fetch: (input, init) => net.fetch(input as string, init),
    store: {
      get: async () => token,
      set: async (t) => {
        token = t
        persist()
      },
    },
    onSignedOut: () => expire(),
    clientName: `desktop/${VERSION} (${process.platform})`,
  })
  c.workspaceId = workspace?.id ?? null
  return c
}

export function onSession(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function emit(reason: Reason): void {
  const s = current()
  for (const fn of listeners) fn(s, reason)
}

function persist(): void {
  store.saveSession({ server, email, workspace }, token)
}

function expire(): void {
  if (!signedIn) return
  signedIn = false
  token = null
  persist()
  emit("expired")
}

export function current(): SessionOut | null {
  return signedIn ? { server, email, workspace } : null
}

export const getClient = () => client
export const getApi = (): Api | null => (signedIn ? api(client) : null)
export const getServer = () => server

/** On launch: resume the stored session if the token still works. Offline is not signed out. */
export async function restore(): Promise<void> {
  const s = store.loadSession()
  if (!s) return
  server = s.server || server
  email = s.email
  workspace = s.workspace
  token = store.readToken(s)
  client = makeClient(server)
  if (!token) return
  signedIn = true
  try {
    await api(client).me()
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      signedIn = false
      token = null
    }
  }
}

export function setServer(url: string): SessionOut | null {
  const next = url.trim().replace(/\/+$/, "")
  if (!/^https?:\/\/[^\s/]+/.test(next)) throw new Error("invalid server URL")
  if (next !== server) {
    // A device token belongs to the server that issued it: never send it to another one.
    const wasSignedIn = signedIn
    server = next
    token = null
    signedIn = false
    workspace = null
    client = makeClient(server)
    persist()
    if (wasSignedIn) emit("signed-out")
  }
  return current()
}

let pending: { cancel: () => void } | null = null

export function cancelSignIn(): void {
  pending?.cancel()
}

/** Resolves true once signed in, false if cancelled or after ten minutes. */
export function signIn(locale: Locale, onReturn: () => void): Promise<boolean> {
  pending?.cancel()
  return new Promise<boolean>((resolve, reject) => {
    let settled = false
    const http = createServer()
    const finish = (ok: boolean, err?: unknown) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      pending = null
      http.close()
      if (err) reject(err)
      else resolve(ok)
    }
    const timer = setTimeout(() => finish(false), 10 * 60_000)
    pending = { cancel: () => finish(false) }
    let started: Awaited<ReturnType<typeof startSignIn>> | null = null

    http.on("request", (req, res) => {
      const u = new URL(req.url ?? "/", "http://127.0.0.1")
      if (u.pathname !== "/callback" || !started) {
        res.writeHead(404).end()
        return
      }
      let code: string
      try {
        // The state ties this redirect to the sign-in this app started; anything else is refused.
        code = readCallback(u.toString(), started)
      } catch {
        res.writeHead(400, { "Content-Type": "text/html; charset=utf-8" }).end(page(locale, false))
        return
      }
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }).end(page(locale, true))
      onReturn()
      const c = makeClient(server)
      finishSignIn(c, started, code)
        .then(async (r) => {
          client = c
          email = r.email
          signedIn = true
          // The remembered workspace only carries over when it is still one of this account's.
          const list = (await api(c).workspaces()).workspaces ?? []
          if (!workspace || !list.some((w) => w.id === workspace!.id)) workspace = list.length === 1 ? pick(list[0]!) : null
          c.workspaceId = workspace?.id ?? null
          persist()
          emit("signed-in")
          finish(true)
        })
        .catch((e) => finish(false, e))
    })

    // 127.0.0.1 only, on a port the OS picks: nothing off this machine can reach it.
    http.listen(0, "127.0.0.1", async () => {
      const port = (http.address() as AddressInfo).port
      try {
        started = await startSignIn({ serverUrl: server, redirectUri: `http://127.0.0.1:${port}/callback`, device: deviceName(), locale, crypto: nodeCrypto })
        // MOHARRIK_E2E: the local demo stack completes the browser step itself (apps/scripts/
        // demo-stack.mjs), so the scripted run follows the redirects here instead of opening a browser.
        if (e2e) void net.fetch(started.url).catch((e) => finish(false, e))
        else await shell.openExternal(started.url)
      } catch (e) {
        finish(false, e)
      }
    })
    http.on("error", (e) => finish(false, e))
  })
}

const pick = (w: CurrentWorkspace): CurrentWorkspace => ({ id: w.id, slug: w.slug, name: w.name, role: w.role, avatar_url: w.avatar_url })

export function selectWorkspace(w: CurrentWorkspace | null): SessionOut | null {
  workspace = w ? pick(w) : null
  client.workspaceId = workspace?.id ?? null
  persist()
  emit("workspace")
  return current()
}

export async function signOut(): Promise<void> {
  // Revoke this device's token server-side so it stops working everywhere, not just here.
  try {
    if (signedIn) await api(client).signOut()
  } catch {
    /* offline: signing out locally still happens */
  }
  signedIn = false
  token = null
  workspace = null
  persist()
  emit("signed-out")
}

/** What the browser tab shows after the redirect: done, go back to the app. */
function page(locale: Locale, ok: boolean): string {
  const ar = locale === "ar"
  const title = ok ? (ar ? "تمّ تسجيل الدخول" : "You're signed in") : ar ? "تعذّر تسجيل الدخول" : "Sign-in didn't complete"
  const body = ok
    ? ar
      ? "عد إلى تطبيق محرك. يمكنك إغلاق هذه الصفحة."
      : "Go back to the Moharrik app. You can close this tab."
    : ar
      ? "ابدأ تسجيل الدخول من جديد من تطبيق محرك."
      : "Start signing in again from the Moharrik app."
  return `<!doctype html><html lang="${ar ? "ar" : "en"}" dir="${ar ? "rtl" : "ltr"}"><head><meta charset="utf-8"><title>Moharrik</title>
<style>:root{color-scheme:light dark;--bg:#F0EBE1;--fg:#0E1A3C;--muted:#6E6551;--line:#DED5C4;--card:#F7F4EC}
@media(prefers-color-scheme:dark){:root{--bg:#0B1429;--fg:#F0EBE1;--muted:#8A97B8;--line:#25355C;--card:#0E1A3C}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:15px/1.6 Inter,"Segoe UI",system-ui,sans-serif}
main{border:1px solid var(--line);background:var(--card);border-radius:12px;padding:32px 40px;max-width:380px;text-align:center}
h1{font-size:18px;font-weight:600;margin:14px 0 6px}p{color:var(--muted);margin:0}</style></head>
<body><main><svg width="40" height="40" viewBox="0 0 400 400" aria-hidden="true"><rect x="80" y="40" width="80" height="80" fill="#00A0A8"/><rect x="240" y="40" width="80" height="80" fill="#C9A227"/><rect x="0" y="120" width="80" height="80" fill="#00A0A8"/><rect x="320" y="120" width="80" height="80" fill="#00A0A8"/><rect x="80" y="200" width="80" height="80" fill="#00A0A8"/><rect x="240" y="200" width="80" height="80" fill="#00A0A8"/><rect x="160" y="280" width="80" height="80" fill="#00A0A8"/></svg>
<h1>${title}</h1><p>${body}</p></main></body></html>`
}
