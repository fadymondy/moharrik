// The one fetch wrapper for the Moharrik API. Same origin (next.config.mjs proxies /api), session
// cookie `togo_session`, CSRF token on every mutating request, X-Workspace-ID when scoped.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = "ApiError"
  }
}

let csrfToken: string | null = null
let csrfPending: Promise<string> | null = null

async function getCsrf(force = false): Promise<string> {
  if (csrfToken && !force) return csrfToken
  if (!csrfPending || force) {
    csrfPending = fetch("/api/auth/csrf", { credentials: "same-origin", cache: "no-store" })
      .then(async (res) => {
        if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
        const data = (await res.json()) as { csrf_token?: string }
        if (!data.csrf_token) throw new ApiError(500, "The API did not return a CSRF token")
        csrfToken = data.csrf_token
        return csrfToken
      })
      .finally(() => {
        csrfPending = null
      })
  }
  return csrfPending
}

/** Forget the cached token, e.g. after sign-out (a new session gets a new token). */
export function resetCsrf() {
  csrfToken = null
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const text = await res.text()
    try {
      const data = JSON.parse(text) as { error?: string; message?: string; detail?: string }
      return data.error || data.message || data.detail || text || res.statusText
    } catch {
      return text || res.statusText || `Request failed (${res.status})`
    }
  } catch {
    return res.statusText || `Request failed (${res.status})`
  }
}

export type ApiOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  /** JSON body. */
  json?: unknown
  /** Multipart body (the browser sets the boundary). */
  form?: FormData
  /** Sends X-Workspace-ID for workspace-scoped endpoints. */
  workspaceId?: string | null
  signal?: AbortSignal
}

export async function api<T = unknown>(path: string, opts: ApiOptions = {}): Promise<T> {
  const method = opts.method ?? (opts.json !== undefined || opts.form ? "POST" : "GET")
  const mutating = method !== "GET"

  const send = async (forceCsrf: boolean) => {
    const headers: Record<string, string> = { Accept: "application/json" }
    if (opts.json !== undefined) headers["Content-Type"] = "application/json"
    if (opts.workspaceId) headers["X-Workspace-ID"] = opts.workspaceId
    if (mutating) headers["X-CSRF-Token"] = await getCsrf(forceCsrf)
    return fetch(path, {
      method,
      headers,
      body: opts.form ?? (opts.json !== undefined ? JSON.stringify(opts.json) : undefined),
      credentials: "same-origin",
      cache: "no-store",
      signal: opts.signal,
    })
  }

  let res = await send(false)
  if (!res.ok && mutating && res.status === 403) {
    const msg = await errorMessage(res.clone())
    // A stale token (new session, server restart): fetch a fresh one and retry exactly once.
    if (/csrf/i.test(msg)) res = await send(true)
  }

  if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
  if (res.status === 204) return undefined as T
  const type = res.headers.get("content-type") ?? ""
  return (type.includes("json") ? await res.json() : await res.text()) as T
}

// ── Contract types ───────────────────────────────────────────────────────

export type User = { id: string | number; email: string; roles?: string[] | null }

export type Role = "owner" | "admin" | "manager" | "member" | "client"
export const INVITABLE_ROLES = ["admin", "manager", "member", "client"] as const

export type WorkspaceSummary = { id: string; slug: string; name: string; role: Role; locale?: string }

export type CurrentWorkspace = {
  workspace: { id: string; slug: string; name: string; locale?: string; timezone?: string; currency?: string }
  role: Role
  can: string[]
}

export type Member = { user_id: string; email: string; role: Role; status: string; joined_at: string | null }

export type Invitation = { id: string; email: string; role: Role; expires_at: string; created_at: string }

export type InvitationPreview = {
  workspace_name: string
  email: string
  role: Role
  expired: boolean
  accepted: boolean
}

export type McpToken = {
  id: string
  name: string
  prefix: string
  scopes: string[]
  expires_at: string | null
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
}

export type McpScope = { scope: string; description: string; min_role: Role; allowed: boolean }

export type AuthMethods = { methods?: unknown; oauth?: unknown; providers?: unknown }

// ── Email marketing ──────────────────────────────────────────────────────

export type SubscriptionStatus = "pending" | "subscribed" | "unsubscribed" | "bounced"

export type MailList = {
  id: string
  name: string
  description: string
  double_opt_in: boolean
  subscribed: number
  pending: number
  created_at: string
}

export type Subscription = { id: string; list_id: string; list_name: string; status: SubscriptionStatus }

export type Contact = {
  id: string
  email: string
  name: string
  locale: "en" | "ar"
  source: string
  created_at: string
  subscriptions: Subscription[] | null
}

export type CampaignStatus = "draft" | "sending" | "sent" | "failed"

export type Campaign = {
  id: string
  list_id: string
  list_name: string
  subject: string
  body: string
  locale: "en" | "ar"
  status: CampaignStatus
  recipient_count: number
  sent_count: number
  failed_count: number
  last_error: string
  sent_at: string | null
  created_at: string
}

export type SmtpSettings = {
  configured: boolean
  host: string
  port: number
  username: string
  has_password: boolean
  from_name: string
  from_email: string
  tls_mode: "none" | "starttls" | "tls"
  updated_at: string | null
}
