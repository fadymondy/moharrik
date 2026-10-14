// The typed Moharrik API client every app shares.
//
// One origin serves both the web and the API (app.moharrik.com proxies /api), so the apps take a
// single server URL. Auth is this install's device token (a personal access token minted by app
// sign-in, signin.ts) sent as a Bearer, short-lived and renewed with its refresh token on a 401;
// the workspace is chosen per request with X-Workspace-ID, exactly as the web does. Request and
// response types come from openapi.d.ts, generated from the Go handlers, so a server change that
// breaks an app fails its typecheck.
import type { paths } from "./openapi"

type Method = "get" | "post" | "put" | "patch" | "delete"
type Op<P, M extends Method> = P extends keyof paths ? NonNullable<paths[P][M]> : never
type JsonOf<R> = R extends { content: { "application/json": infer J } } ? J : undefined
type Ok<O> = O extends { responses: infer R }
  ? R extends { 200: infer A }
    ? JsonOf<A>
    : R extends { 201: infer B }
      ? JsonOf<B>
      : undefined
  : never
type BodyOf<O> = O extends { requestBody: { content: { "application/json": infer B } } }
  ? B
  : O extends { requestBody?: { content: { "application/json": infer B } } }
    ? B
    : never
type QueryOf<O> = O extends { parameters: { query?: infer Q } } ? (Q extends undefined ? never : Q) : never
type PathOf<O> = O extends { parameters: { path: infer P } } ? (P extends undefined ? never : P) : never

/** Paths that have a given method. */
export type PathsWith<M extends Method> = { [P in keyof paths]: paths[P][M] extends undefined | never ? never : P }[keyof paths]

export type Options<O> = {
  path?: PathOf<O>
  query?: QueryOf<O>
  body?: BodyOf<O>
  /** Override the client's current workspace for this call. */
  workspace?: string | null
  signal?: AbortSignal
}

/** Where the device token lives: Keychain/Keystore on phones, safeStorage on the desktop. */
export interface TokenStore {
  get(): Promise<string | null>
  set(token: string | null): Promise<void>
}

/** A refused request, with the API's problem detail (huma's RFC 9457 body) as the message. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body?: unknown,
  ) {
    super(message)
    this.name = "ApiError"
  }
}

export interface ClientOptions {
  /** The server origin, e.g. https://app.moharrik.com — never hard-coded in an app (config.ts). */
  baseUrl: string
  store: TokenStore
  /**
   * Where the refresh token lives, beside the device token. A 401 trades it once at
   * POST /api/auth/app/refresh and retries; without one (or when the server refuses it) the
   * client signs out.
   */
  refreshStore?: TokenStore
  fetch?: typeof fetch
  /** Called once when the server refuses the stored token (revoked, or signed out everywhere). */
  onSignedOut?: () => void
  /** Sent as X-Moharrik-Client, e.g. "mobile/1.0.0 (ios)". */
  clientName?: string
}

export class MoharrikClient {
  readonly baseUrl: string
  workspaceId: string | null = null
  private readonly store: TokenStore
  private readonly refreshStore?: TokenStore
  private refreshing: Promise<boolean> | null = null
  private readonly fetcher: typeof fetch
  private readonly onSignedOut?: () => void
  private readonly clientName?: string
  private cached: string | null | undefined

  constructor(o: ClientOptions) {
    this.baseUrl = o.baseUrl.replace(/\/+$/, "")
    this.store = o.store
    this.refreshStore = o.refreshStore
    this.fetcher = o.fetch ?? ((...a) => fetch(...a))
    this.onSignedOut = o.onSignedOut
    this.clientName = o.clientName
  }

  async token(): Promise<string | null> {
    if (this.cached === undefined) this.cached = await this.store.get()
    return this.cached
  }

  /** Sets the device token; null signs out (and forgets the refresh token too). */
  async setToken(token: string | null): Promise<void> {
    this.cached = token
    await this.store.set(token)
    if (token === null) await this.refreshStore?.set(null)
  }

  /** Stores a sign-in's device token and refresh token (every app sign-in returns both). */
  async setSession(s: { token: string; refresh_token?: string | null }): Promise<void> {
    await this.setToken(s.token)
    if (s.refresh_token) await this.refreshStore?.set(s.refresh_token)
  }

  /**
   * Trades the refresh token for a new pair. One at a time: calls that fail together wait for the
   * same refresh. False when there is none or the server refuses it; a network failure throws.
   */
  private refresh(): Promise<boolean> {
    this.refreshing ??= (async () => {
      try {
        const rt = await this.refreshStore?.get()
        if (!rt) return false
        const res = await this.fetcher(this.baseUrl + "/api/auth/app/refresh", {
          method: "POST",
          headers: { Accept: "application/json", "Content-Type": "application/json" },
          body: JSON.stringify({ refresh_token: rt }),
        })
        if (res.status >= 500) throw await problem(res)
        if (!res.ok) return false
        const s = (await res.json()) as { token?: string; refresh_token?: string }
        if (!s.token) return false
        await this.setSession({ token: s.token, refresh_token: s.refresh_token })
        return true
      } finally {
        this.refreshing = null
      }
    })()
    return this.refreshing
  }

  /** The absolute URL of an API path (for images and downloads). */
  url(path: string): string {
    return path.startsWith("http") ? path : this.baseUrl + path
  }

  /** Headers for a raw request made outside call(): the SSE stream, an authenticated image. */
  async headers(workspace: string | null = this.workspaceId): Promise<Record<string, string>> {
    const h: Record<string, string> = { Accept: "application/json" }
    const t = await this.token()
    if (t) h.Authorization = `Bearer ${t}`
    if (workspace) h["X-Workspace-ID"] = workspace
    if (this.clientName) h["X-Moharrik-Client"] = this.clientName
    return h
  }

  async call<M extends Method, P extends PathsWith<M>>(
    method: M,
    path: P,
    opts: Options<Op<P, M>> = {},
  ): Promise<Ok<Op<P, M>>> {
    return this.raw<Ok<Op<P, M>>>(method, path as string, opts as Options<unknown>)
  }

  /** An untyped call, for the few routes outside the OpenAPI document (togo's /api/auth/me). */
  async raw<T>(method: Method, path: string, opts: Options<unknown> = {}): Promise<T> {
    const url = this.baseUrl + fill(path, opts.path as Record<string, unknown> | undefined) + qs(opts.query as object | undefined)
    const headers = await this.headers(opts.workspace === undefined ? this.workspaceId : opts.workspace)
    const init: RequestInit = { method: method.toUpperCase(), headers, signal: opts.signal }
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json"
      init.body = JSON.stringify(opts.body)
    }
    let res = await this.fetcher(url, init)
    if (res.status === 401 && headers.Authorization) {
      // The device token expired or was refused: renew it once (unless another call already
      // did) and retry with the new one.
      const now = await this.token()
      if ((now && `Bearer ${now}` !== headers.Authorization) || (await this.refresh())) {
        const t = await this.token()
        if (t) {
          headers.Authorization = `Bearer ${t}`
          res = await this.fetcher(url, init)
        }
      }
    }
    if (res.status === 401 && headers.Authorization) {
      // Still refused: the session is gone server-side (signed out on another device, revoked,
      // "sign out everywhere", the account deleted). Forget it so the app returns to sign-in
      // instead of failing every call.
      await this.setToken(null)
      this.onSignedOut?.()
    }
    if (!res.ok) throw await problem(res)
    if (res.status === 204) return undefined as T
    const text = await res.text()
    return (text ? JSON.parse(text) : undefined) as T
  }
}

function fill(path: string, params?: Record<string, unknown>): string {
  return path.replace(/\{(\w+)\}/g, (_, k: string) => {
    const v = params?.[k]
    if (v === undefined || v === null || v === "") throw new Error(`missing path parameter ${k}`)
    return encodeURIComponent(String(v))
  })
}

export function qs(q?: object): string {
  if (!q) return ""
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue
    p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ""
}

async function problem(res: Response): Promise<ApiError> {
  let body: unknown
  let msg = `${res.status} ${res.statusText}`.trim()
  try {
    body = await res.json()
    const b = body as { error?: string; detail?: string; title?: string; errors?: { message?: string }[] }
    msg = b.errors?.[0]?.message || b.error || b.detail || b.title || msg
  } catch {
    // Not JSON (a proxy's page): keep the status line.
  }
  return new ApiError(res.status, msg, body)
}
