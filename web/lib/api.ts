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
  phone: string
  title: string
  company_id: string | null
  company_name: string
  created_at: string
  subscriptions: Subscription[] | null
  /** WhatsApp consent (MH-320) — its own, never implied by a list. */
  whatsapp_opt_in_at: string | null
  /** Set when the contact said STOP. Only an explicit reversal clears it. */
  whatsapp_opt_out_at: string | null
}

export type CampaignStatus = "draft" | "sending" | "sent" | "failed"

export type CampaignChannel = "email" | "whatsapp"

export type Campaign = {
  id: string
  list_id: string
  list_name: string
  channel: CampaignChannel
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

export type FormKind = "subscribe" | "inquiry"

export type SubscribeForm = {
  id: string
  /** subscribe puts an address on a list; inquiry files a lead (MH-390). */
  kind: FormKind
  /** Empty for an inquiry form. */
  list_id: string
  list_name: string
  name: string
  public_key: string
  /** Empty allows NO site to submit. */
  allowed_origins: string[] | null
  enabled: boolean
  thanks_en: string
  thanks_ar: string
  created_at: string
  /** Where a customer's page posts. */
  endpoint: string
  /** A minimal HTML form, built by the API (it knows its public URL). */
  embed: string
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

// ── CRM ──────────────────────────────────────────────────────────────────

export type Ref = { id: string; name: string }
export type ContactRef = { id: string; email: string; name: string; title: string }

export type CompanySize = "" | "1-10" | "11-50" | "51-200" | "201-1000" | "1000+"
export const COMPANY_SIZES: CompanySize[] = ["1-10", "11-50", "51-200", "201-1000", "1000+"]

export type Company = {
  id: string
  name: string
  domain: string
  industry: string
  size: CompanySize
  notes: string
  owner_user_id: string | null
  contact_count: number
  open_deals: number
  created_at: string
}

export type StageKind = "open" | "won" | "lost"

export type Stage = { id: string; name: string; position: number; kind: StageKind; deal_count: number; value_minor: number }

export type Deal = {
  id: string
  title: string
  value_minor: number
  currency: string
  stage_id: string
  stage_name: string
  stage_kind: StageKind
  position: number
  contact: ContactRef | null
  company: Ref | null
  owner_user_id: string | null
  expected_close: string | null
  closed_at: string | null
  created_at: string
}

export type ActivityKind = "note" | "call" | "meeting" | "task"
export const ACTIVITY_KINDS: ActivityKind[] = ["note", "call", "meeting", "task"]

export type Activity = {
  id: string
  kind: ActivityKind
  body: string
  due_at: string | null
  done_at: string | null
  contact: ContactRef | null
  company: Ref | null
  deal: Ref | null
  created_by: string
  created_at: string
}

export type ContactDetail = {
  id: string
  email: string
  name: string
  locale: "en" | "ar"
  source: string
  phone: string
  title: string
  tags: string[]
  company: Ref | null
  owner_user_id: string | null
  created_at: string
  subscriptions: Subscription[]
}

// ── Notification center ─────────────────────────────────────────────────

export type WebhookSourceKind = "generic" | "github" | "sentry"

export type WebhookSource = {
  id: string
  name: string
  kind: WebhookSourceKind
  enabled: boolean
  url: string
  delivery_count: number
  last_delivery_at: string | null
  last_error: string
  last_error_at: string | null
  /** Predates MH-381: still accepts the body-only signature, which is replayable. */
  legacy_signing: boolean
  /** Last delivery that verified only by the legacy scheme. */
  last_legacy_at: string | null
  /** False for a Sentry source whose Client Secret has not been pasted yet. */
  has_secret: boolean
  rotated_at: string
  created_at: string
}

export type NotificationSeverity = "info" | "warning" | "error"

export type NotificationItem = {
  id: string
  source_id: string | null
  source_name: string
  kind: string
  severity: NotificationSeverity
  title: string
  body: string
  actor: string
  url: string
  payload: string
  occurred_at: string
  read_at: string | null
  saved_at: string | null
}

// ── Inbox ───────────────────────────────────────────────────────────────

export type ConversationChannel = "chat" | "email" | "whatsapp"
export type ConversationStatus = "open" | "snoozed" | "closed"

export type Conversation = {
  id: string
  channel: ConversationChannel
  subject: string
  status: ConversationStatus
  customer_name: string
  customer_email: string
  contact_id: string | null
  assignee_user_id: string | null
  unread: boolean
  last_message_at: string
  created_at: string
  preview: string
  message_count: number
  /** Where a chat visitor opened the thread from; "" for other channels. */
  page_url: string
  user_agent: string
  locale: string
}

export type InboxMessage = {
  id: string
  direction: "in" | "out"
  author_user_id: string | null
  author_email: string
  body: string
  created_at: string
}

export type ChatWidget = {
  id: string
  name: string
  public_key: string
  allowed_origins: string[]
  enabled: boolean
  greeting: string
  embed: string
  created_at: string
}

// ── Trends ──────────────────────────────────────────────────────────────

export type FeedSource = {
  id: string
  name: string
  url: string
  kind: "rss" | "atom"
  enabled: boolean
  last_fetched_at: string | null
  last_error: string
  last_error_at: string | null
  item_count: number
  created_at: string
}

export type Topic = {
  id: string
  name: string
  keywords: string[]
  enabled: boolean
  item_count: number
  created_at: string
}

export type NewsItem = {
  id: string
  source_id: string | null
  source_name: string
  title: string
  url: string
  summary: string
  author: string
  topic_ids: string[]
  published_at: string
  saved_at: string | null
}

// ── Automation ──────────────────────────────────────────────────────────

export type BrowserAgent = {
  id: string
  name: string
  platform: string
  browser_version: string
  extension_version: string
  /** Live, from the socket registry — not the last value written to the row. */
  status: "online" | "offline"
  /** Empty allows nothing. */
  allowed_hosts: string[]
  token_prefix: string
  last_seen_at: string
  revoked_at: string | null
  created_at: string
}

export type AgentPairing = {
  id: string
  requested_name: string
  platform: string
  ip: string
  status: string
  expires_at: string
  created_at: string
}

export type AutomationSettings = {
  paused: boolean
  reason: string
  paused_at: string | null
  paused_by: string
}

export type RunStep = {
  position: number
  command: string
  args: string
  status: "pending" | "ok" | "failed" | "skipped"
  summary: string
  result: string
  error: string
  duration_ms: number
}

export type AutomationRun = {
  id: string
  agent_id: string
  agent_name: string
  trigger: string
  requested_by: string
  status: "queued" | "running" | "ok" | "failed" | "cancelled"
  error: string
  step_count: number
  ok_count: number
  started_at: string | null
  finished_at: string | null
  created_at: string
}

// ── Inbox: the email channel ────────────────────────────────────────────

export type MailboxSettings = {
  configured: boolean
  host: string
  port: number
  username: string
  /** The password is write-only; this only says whether one is stored. */
  has_password: boolean
  tls_mode: "tls" | "starttls" | "none"
  folder: string
  enabled: boolean
  last_polled_at: string | null
  last_error: string
  message_count: number
}

export type WhatsAppLink = {
  linked: boolean
  /** Read live from the WhatsApp server, not from our row. */
  state: "open" | "connecting" | "close" | string
  phone: string
  qr_base64?: string
  qr_code?: string
  last_error: string
  linked_at: string | null
  /** false when this deployment has no WhatsApp server configured at all. */
  available: boolean
}

export type AutomationSchedule = {
  id: string
  name: string
  agent_id: string
  agent_name: string
  cron: string
  timezone: string
  catch_up: "skip" | "once"
  enabled: boolean
  steps: { command: string; args?: Record<string, unknown>; continue_on_error?: boolean }[]
  next_run_at: string | null
  last_run_at: string | null
  /** running | ok | failed | missed | waiting — "missed" and "waiting" are first-class. */
  last_status: string
  last_error: string
  last_run_id: string
  created_at: string
}


export type WorkflowParam = {
  name: string
  label?: string
  required?: boolean
  /** Redacted from the run record. */
  secret?: boolean
  default?: string
}

export type AutomationWorkflow = {
  id: string
  name: string
  description: string
  steps: { command: string; args?: Record<string, unknown> }[]
  params: WorkflowParam[]
  /** Every {{placeholder}} the steps actually use. */
  placeholders: string[]
  run_count: number
  created_at: string
}

// ── Research (MH-227) ───────────────────────────────────────────────────

export type ResearchStatus = "queued" | "running" | "ok" | "failed" | "cancelled"

export type ResearchRun = {
  id: string
  topic: string
  query: string
  status: ResearchStatus
  /** Trimmed to 400 characters in the list; whole on the detail endpoint. */
  summary: string
  /** The provider that wrote the summary, empty when nothing did. */
  model: string
  /** On an "ok" run this is why there is no summary (no model, nothing readable). */
  error: string
  /** Written when the run finishes; 0 while it is still going. */
  found_count: number
  read_count: number
  requested_by: string
  started_at: string | null
  finished_at: string | null
  created_at: string
}

export type ResearchSource = {
  /** 0-based rank in the search results; the summary cites it as [position + 1]. */
  position: number
  url: string
  title: string
  snippet: string
  /** Only present when the run is fetched with ?content=true. */
  content?: string
  fetched: boolean
  /** Why the page could not be read, when it could not. */
  error?: string
}

export type ResearchList = {
  runs: ResearchRun[]
  can_search: boolean
  can_read: boolean
  can_summarise: boolean
}

export type ResearchDetail = { run: ResearchRun; sources: ResearchSource[] }

// ── Leads (MH-390) ──────────────────────────────────────────────────────

export type LeadStatus = "new" | "contacted" | "qualified" | "converted" | "spam"
export const LEAD_STATUSES: LeadStatus[] = ["new", "contacted", "qualified", "converted", "spam"]

export type LeadRef = { id: string; name: string }

export type LeadAttribution = {
  utm_source: string
  utm_medium: string
  utm_campaign: string
  utm_term: string
  utm_content: string
  referrer: string
  gclid: string
  landing_page: string
}

export type Lead = {
  id: string
  status: LeadStatus
  name: string
  email: string
  phone: string
  /** What the visitor typed; the CRM record is converted_company. */
  company: string
  message: string
  locale: string
  form_id: string | null
  form_name: string
  /** The allowed site the form was posted from. */
  origin: string
  attribution: LeadAttribution
  converted_contact: LeadRef | null
  converted_company: LeadRef | null
  converted_deal: LeadRef | null
  converted_at: string | null
  converted_by: string | null
  created_at: string
  updated_at: string
}

export type LeadConversion = {
  lead: Lead
  contact_id: string
  contact_created: boolean
  company_id: string
  company_created: boolean
  /** The email is free mail, so no company was made. */
  free_mail: boolean
  deal_id: string
  activity_id: string
}
