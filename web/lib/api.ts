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

/**
 * Download a file the API streams (a CSV export) and hand it to the browser as a
 * download. A plain link cannot do this: the workspace travels in a header.
 *
 * Read as a Blob, never as text: Response.text() decodes UTF-8 and silently drops
 * the byte-order mark, and the BOM is what makes Excel read Arabic correctly.
 */
export async function downloadFile(path: string, opts: { workspaceId?: string | null; fallbackName: string }): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/csv, */*" }
  if (opts.workspaceId) headers["X-Workspace-ID"] = opts.workspaceId
  const res = await fetch(path, { headers, credentials: "same-origin", cache: "no-store" })
  if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement("a")
    a.href = url
    a.download = fileNameFrom(res.headers.get("content-disposition")) ?? opts.fallbackName
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }
}

/** The name in Content-Disposition, preferring the UTF-8 filename* form (an Arabic list name). */
function fileNameFrom(header: string | null): string | null {
  if (!header) return null
  const star = /filename\*=UTF-8''([^;]+)/i.exec(header)
  if (star) {
    try {
      return decodeURIComponent(star[1])
    } catch {
      /* fall through to the ASCII name */
    }
  }
  const plain = /filename="([^"]+)"/i.exec(header)
  return plain ? plain[1] : null
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

export type Subscription = {
  id: string
  list_id: string
  list_name: string
  status: SubscriptionStatus
  confirmed_at: string | null
  unsubscribed_at: string | null
  /** A member took the address off this list (MH-408); who is on the contact's timeline. */
  unsubscribed_by_workspace: boolean
}

export type StatusCounts = Record<SubscriptionStatus | "all", number>

/** One page of GET /api/w/contacts (MH-408): keyset-paged, with the filter's total. */
export type ContactPage = {
  contacts: Contact[] | null
  /** Every contact matching the filter, across all pages. */
  total: number
  limit: number
  /** Fetches the next page; empty on the last one. */
  next_cursor: string
  /** One list's contacts by status, under the same search. Null without a list. */
  counts: StatusCounts | null
}

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

/**
 * A pipeline stage. deal_count and value_minor cover the WHOLE stage (value in the workspace
 * currency), not just the cards sent; next_cursor (board view) fetches the stage's next cards.
 */
export type Stage = {
  id: string
  name: string
  position: number
  kind: StageKind
  deal_count: number
  value_minor: number
  next_cursor?: string
}

/** One page of a keyset-paged list (MH-408, MH-467). */
export type Page = { total: number; limit: number; next_cursor: string }

/** The first page of a record's timeline, carried on the record's own page. */
export type TimelinePage = {
  activities: Activity[]
  activities_total: number
  activities_next_cursor: string
  /** Linked inbox conversations in the first page's stretch of time (MH-379); older pages carry the rest. */
  conversations: ConversationRef[]
}

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
  /** A WhatsApp customer's number; "" for other channels. */
  customer_phone: string
  /** The CRM contact it is linked to (MH-379). */
  contact: { id: string; name: string; email: string; company: Ref | null } | null
  /** auto: matched on the exact email/phone when filed; manual: a member linked (or unlinked) it. */
  contact_link: "" | "auto" | "manual"
  /** The deal it was turned into. */
  deal: { id: string; title: string } | null
  /** When a snoozed conversation wakes (MH-383); null otherwise. */
  snoozed_until: string | null
  closed_at: string | null
}

export type VisitorGuess = { name: string; email: string; phone: string; locale: "en" | "ar" }

export type ConversationDetail = {
  conversation: Conversation
  messages: InboxMessage[]
  /** What the visitor gave, to prefill a new contact. */
  visitor: VisitorGuess
  /** The language to answer in; canned replies insert this variant. */
  reply_language: "en" | "ar"
}

/** An inbox conversation as a CRM timeline shows it (MH-379). */
export type ConversationRef = {
  id: string
  channel: ConversationChannel
  subject: string
  status: ConversationStatus
  customer_name: string
  preview: string
  message_count: number
  contact: ContactRef | null
  deal_id: string | null
  last_message_at: string
  created_at: string
}

export type CannedReply = {
  id: string
  title: string
  body_en: string
  body_ar: string
  position: number
  created_at: string
  updated_at: string
}

export type ChannelStats = {
  channel: ConversationChannel | "all"
  conversations: number
  answered: number
  awaiting: number
  first_response_median_seconds: number | null
  first_response_mean_seconds: number | null
  resolved: number
  resolution_rate: number | null
  resolution_median_seconds: number | null
}

export type InboxStats = {
  days: number
  since: string
  channels: ChannelStats[]
  total: ChannelStats
  open_now: number
  snoozed_now: number
  unread_now: number
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

export type FeedProvider = "feed" | "hn" | "devto"

export type FeedSource = {
  id: string
  name: string
  url: string
  kind: "rss" | "atom" | "api"
  /** feed = RSS/Atom; hn and devto are read through their public APIs. */
  provider: FeedProvider
  params: Record<string, string>
  catalog_key: string
  enabled: boolean
  interval_minutes: 360 | 720 | 1440
  /** "" until the first fetch. */
  last_status: "" | "ok" | "failed" | "over_budget"
  last_added: number
  last_fetched_at: string | null
  next_run_at: string | null
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
  outlet: string
  provider: FeedProvider
  title: string
  url: string
  summary: string
  author: string
  topic_ids: string[]
  cluster_id: string | null
  rank: number
  points: number
  comments: number
  published_at: string
  saved_at: string | null
}

export type NewsPage = { items: NewsItem[]; total: number; has_more: boolean; next_cursor?: string }

/** One part of a trending topic's score. The score is the sum of its parts. */
export type TrendReason = {
  kind: "sources" | "velocity" | "relevance"
  points: number
  /** English, for API clients; the page renders kind + the structured fields. */
  label: string
  detail: string[]
  count?: number
  signal?: "burst" | "hn" | "devto"
  position?: number
  metric?: number
}

export type TrendState = "open" | "saved" | "reviewed" | "dismissed"

/** A trending topic: items from any number of sources about one story. */
export type TrendTopic = {
  id: string
  day: string
  title: string
  summary: string
  summary_source: string
  cluster_method: string
  source_count: number
  item_count: number
  outlets: string[]
  source_ids: string[]
  score: number
  reasons: TrendReason[]
  first_seen_at: string
  last_seen_at: string
  state: TrendState
  reviewed_at: string | null
  dismissed_at: string | null
  saved_at: string | null
  items: NewsItem[]
}

export type TrendCounts = { open: number; saved: number; reviewed: number; dismissed: number; all: number }

export type TrendTopicPage = {
  topics: TrendTopic[]
  has_more: boolean
  next_cursor?: string
  counts: TrendCounts
  days: { day: string; count: number }[]
}

export type TrendTopicDetail = { topic: TrendTopic; outlets: { outlet: string; count: number }[] }

export type FeedCatalogEntry = {
  key: string
  name: string
  description: string
  category: "technology" | "business" | "world" | "industry"
  language: "en" | "ar"
  provider: FeedProvider
  url: string
  params?: Record<string, string>
  interval_minutes: number
  /** This workspace's source for the entry, when it has added it. */
  source_id: string | null
}

export type TrendSettings = {
  fetch_budget_per_day: number
  fetches_today: number
  budget_day: string
  scheduled_per_day: number
  max_budget: number
}

// ── Automation ──────────────────────────────────────────────────────────

export type BrowserAgent = {
  id: string
  name: string
  platform: string
  browser_version: string
  extension_version: string
  /** Older than the extension this server offers (MH-399). */
  update_available: boolean
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
  /** The saved workflow this schedule runs, when it runs one (MH-403). */
  workflow_id: string
  workflow_name: string
  /** Only the plain values; secret ones are never sent back. */
  values: Record<string, string>
  /** Which secret values are set. */
  secret_params: string[]
  next_run_at: string | null
  last_run_at: string | null
  /** running | ok | failed | missed | waiting — "missed" and "waiting" are first-class. */
  last_status: string
  last_error: string
  last_run_id: string
  created_at: string
}


/** The downloadable extension (MH-399). */
export type ExtensionInfo = {
  available: boolean
  reason?: string
  version?: string
  file?: string
  bytes?: number
  sha256?: string
  site?: string
  dev?: boolean
  built_at?: string
  commit?: string
  download_url?: string
}

export type WorkflowParam = {
  name: string
  label?: string
  required?: boolean
  /** Redacted from the run record. */
  secret?: boolean
  default?: string
}

export type WorkflowStep = { command: string; args?: Record<string, unknown>; continue_on_error?: boolean }

export type AutomationWorkflow = {
  id: string
  name: string
  description: string
  steps: WorkflowStep[]
  params: WorkflowParam[]
  /** Every {{placeholder}} the steps actually use. */
  placeholders: string[]
  run_count: number
  /** The current version; every save adds one (MH-400). */
  version: number
  created_at: string
  updated_at: string
}

export type WorkflowVersion = {
  version: number
  name: string
  description: string
  steps: WorkflowStep[]
  params: WorkflowParam[]
  note: "created" | "recorded" | "edited" | "restored"
  restored_from?: number
  created_at: string
}

/** The element picker (MH-400). */
export type ElementPick = {
  id: string
  agent_id: string
  status: "waiting" | "picked" | "failed"
  result?: { selector: string; fragile?: boolean; tag?: string; text?: string; host?: string }
  error?: string
  started_at: string
}

export type SchedulePreview = { timezone: string; valid: boolean; error?: string; times: string[] }

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
  /** What the visitor typed as a budget, as typed ("" when left out). */
  budget: string
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

// ── Integrations: Google (MH-439) ───────────────────────────────────────

export type GoogleProduct = "analytics" | "search_console" | "youtube"
export const GOOGLE_PRODUCTS: GoogleProduct[] = ["analytics", "search_console", "youtube"]

/** A connected Google account. No token of any kind is ever in it. */
export type GoogleConnection = {
  id: string
  email: string
  /** Derived from what Google actually granted, not from what was asked for. */
  products: GoogleProduct[] | null
  scopes: string[] | null
  /** YouTube revenue reporting was granted. */
  revenue: boolean
  /** "" = working; "reauth" = Google refused the stored grant. */
  status: "" | "reauth"
  auth_error: string
  connected_by: string
  connected_by_email: string
  created_at: string
  updated_at: string
}

export type GoogleConnections = {
  /** false = this server has no Google OAuth client configured. */
  configured: boolean
  redirect_url: string
  connections: GoogleConnection[] | null
}

export type GoogleDisconnected = { revoked: boolean; shared: boolean; revoke_error?: string }

// ── Google resources (MH-440) ────────────────────────────────────────────

export type GoogleKind = "ga4_property" | "gsc_site" | "yt_channel"
export const GOOGLE_KINDS: GoogleKind[] = ["ga4_property", "gsc_site", "yt_channel"]

/** A GA4 property, Search Console site or YouTube channel a connected account can see. */
export type GoogleResource = {
  id: string
  connection_id: string
  /** The account it is read through — only for integrations:manage. */
  connection_email?: string
  kind: GoogleKind
  /** As Google names it: properties/123, sc-domain:example.com, UC… */
  external_id: string
  display_name: string
  /** The GA4 account, or the site's permission level. */
  parent_name: string
  details: { handle?: string; avatar_url?: string; permission_level?: string; account?: string; property_type?: string } | null
  /** Still returned by the latest discovery. */
  available: boolean
  enabled: boolean
  auto_enabled: boolean
  last_synced_at: string | null
  next_sync_at: string
  syncing: boolean
  /** Google asked to slow down; nothing is fetched on demand before this. */
  backoff_until: string | null
  /** The last sync's failure, in plain words ("" = fine). */
  last_error: string
  /** The account needs reconnecting. */
  reauth: boolean
}

export type GoogleDiscovery = {
  connection_id: string
  product: GoogleProduct
  discovered_at: string
  found: number
  error: string
}

export type GoogleResources = {
  resources: GoogleResource[] | null
  discoveries: GoogleDiscovery[] | null
  needs_choice: GoogleKind[] | null
  available: GoogleKind[] | null
  can_manage: boolean
}

// ── Analytics: GA4 (MH-444) ──────────────────────────────────────────────

export type GaTotals = {
  /** activeUsers over the whole window — GA4's own total, not a sum of days. */
  users: number
  new_users: number
  sessions: number
  views: number
  engaged_sessions: number
  /** 0..1 */
  engagement_rate: number
  engagement_seconds: number
  avg_engagement_seconds_per_user: number
}

export type GaReport = {
  days: number
  start_date: string
  end_date: string
  time_zone: string
  /** GA4 has no data for this property in this window. */
  empty: boolean
  totals: GaTotals
  daily: { date: string; users: number; sessions: number; views: number }[] | null
  top_pages: { path: string; views: number; users: number }[] | null
  sources: { source: string; medium: string; sessions: number; users: number }[] | null
  fetched_at: string
  stale: boolean
  note?: string
}

export type GaReportOut = {
  resource: GoogleResource | null
  report: GaReport | null
  error?: string
}

// ── Search Console (MH-445) ──────────────────────────────────────────────

export type GscTotals = { clicks: number; impressions: number; ctr: number; position: number | null }
/** One day. Null fields: the day was never imported (unknown), unlike 0 (imported, no data). */
export type GscPoint = { date: string; clicks: number | null; impressions: number | null; ctr: number | null; position: number | null }
export type GscRow = { key: string; clicks: number; impressions: number; ctr: number; position: number | null }

export type GscPerformance = {
  days: number
  start_date: string
  end_date: string
  /** site = the site's own totals; queries = sums of query rows (anonymised queries left out). */
  basis: "site" | "queries"
  totals: GscTotals
  previous: GscTotals | null
  previous_start_date?: string
  previous_end_date?: string
  daily: GscPoint[] | null
  previous_daily: GscPoint[] | null
  top_queries: GscRow[] | null
  top_pages: GscRow[] | null
  countries: string[] | null
  devices: string[] | null
  coverage: { imported_days: number; first_date: string | null; last_data_date: string | null; last_import_at: string | null; truncated_days: number }
  empty: boolean
  nothing_yet: boolean
  /** Days of the window not imported yet: totals over them are partial. */
  missing_days: number
  previous_missing_days: number
}

export type GscPerformanceOut = { resource: GoogleResource | null; performance: GscPerformance | null }

// ── YouTube (MH-441) ─────────────────────────────────────────────────────

export type YtChannel = {
  channel_id: string
  title: string
  handle: string
  avatar_url: string
  subscribers: number
  /** The channel hides its subscriber count. */
  subscribers_hidden: boolean
  videos: number
  views: number
  uploads_complete: boolean
  updated_at: string
  comments_seen_until: string | null
  url: string
}

export type YtQuota = { day: string; workspace_used: number; workspace_limit: number; global_used: number; global_limit: number }

export type YtChannelOut = { resource: GoogleResource | null; channel: YtChannel | null; quota: YtQuota }

export type YtVideo = {
  id: string
  title: string
  description: string
  tags: string[] | null
  thumbnail_url: string
  published_at: string | null
  scheduled_at: string | null
  scheduled: boolean
  privacy_status: string
  duration_seconds: number
  views: number
  /** Null when the video hides its likes. */
  likes: number | null
  /** Null when comments are turned off. */
  comments: number | null
  comments_disabled: boolean
  synced_at: string
  url: string
  studio_url: string
}

export type YtVideoPage = { videos: YtVideo[] | null; has_more: boolean; next_cursor?: string; total: number; counts: Record<string, number> | null }

export type YtComment = {
  id: string
  video_id: string
  author: string
  author_avatar_url: string
  text: string
  likes: number
  replies: number
  answered: boolean
  published_at: string | null
  reply_url: string
}

export type YtVideoDetail = YtVideo & { top_comments: YtComment[] | null; comment_note?: string }

// ── YouTube Analytics (MH-442) ───────────────────────────────────────────

export type YtAnalytics = {
  scope: string
  days: number
  start_date: string
  end_date: string
  empty: boolean
  totals: { views: number; watch_minutes: number; avg_view_duration_seconds: number; subscribers_gained: number; subscribers_lost: number; likes: number; comments: number }
  /** Null values: YouTube has not published that day yet. */
  daily: { day: string; views: number | null; watch_minutes: number | null; subscribers_gained: number | null; subscribers_lost: number | null }[] | null
  top_videos: { video_id: string; title: string; thumbnail_url: string; views: number; watch_minutes: number }[] | null
  traffic_sources: { source: string; views: number }[] | null
  fetched_at: string
  stale: boolean
  note?: string
}

export type YtAnalyticsOut = { resource: GoogleResource | null; analytics: YtAnalytics | null; error?: string }

// ── Alerts and web push (MH-378) ─────────────────────────────────────────

export type BellItem = {
  kind: "conversation" | "notification"
  id: string
  /** The customer (conversation) or the item's title (notification). */
  title: string
  /** The latest message (conversation) or the source's name (notification). */
  snippet: string
  channel?: "chat" | "email" | "whatsapp"
  severity?: NotificationSeverity
  unread: boolean
  at: string
  assignee_user_id?: string | null
}

export type Bell = {
  unread_conversations: number
  unread_notifications: number
  can_inbox: boolean
  can_notifications: boolean
  items: BellItem[] | null
}

export type PushDevice = {
  id: string
  endpoint: string
  user_agent: string
  locale: "en" | "ar"
  created_at: string
  last_ok_at: string | null
  last_error: string
  last_error_at: string | null
}

export type PushStatus = { configured: boolean; public_key: string; devices: PushDevice[] | null }

export type PushResult = { sent: number; removed: number; failed: number; devices: number }

// ── Overview (MH-462) ───────────────────────────────────────────────────
// The workspace's front page in one read. A null section is one the viewer's
// role cannot read (hidden), never "empty": an empty list is [].

export type OverviewLeadCounts = Record<LeadStatus, number>

export type OverviewToday = {
  leads: OverviewLeadCounts | null
  /** Since midnight in the workspace's time zone. */
  leads_received_today: number | null
  unread_conversations: number | null
  unread_notifications: number | null
  research: { live: number; latest: { id: string; topic: string; status: "queued" | "running" } | null } | null
}

export type OverviewStage = { id: string; name: string; deal_count: number; value_minor: number }

export type OverviewDeal = {
  id: string
  title: string
  value_minor: number
  currency: string
  stage_name: string
  company_name: string | null
  /** YYYY-MM-DD */
  expected_close: string
}

export type OverviewPipeline = {
  /** Open stages in board order. Values are in the workspace currency only. */
  stages: OverviewStage[]
  open_deals: number
  open_value_minor: number
  /** Open deals in another currency: counted, never summed. */
  other_currency_deals: number
  won_this_month: { since: string; count: number; value_minor: number; other_currency_deals: number }
  next_closes: OverviewDeal[]
}

export type OverviewLead = {
  id: string
  name: string
  email: string
  company: string
  status: LeadStatus
  origin: string
  attribution: Pick<LeadAttribution, "utm_source" | "utm_medium" | "referrer" | "gclid">
  created_at: string
}

export type OverviewConversation = {
  id: string
  channel: ConversationChannel
  status: ConversationStatus
  customer_name: string
  customer_email: string
  subject: string
  unread: boolean
  preview: string
  last_message_at: string
}

export type OverviewNotification = {
  id: string
  severity: NotificationSeverity
  title: string
  source_name: string
  url: string
  read: boolean
  occurred_at: string
}

export type OverviewMarketing = {
  /** null without contacts:read. */
  lists: { id: string; name: string; subscribed: number; pending: number }[] | null
  draft_count: number
  drafts: { id: string; subject: string; channel: CampaignChannel; list_name: string; created_at: string }[]
}

export type OverviewTrends = {
  topics: number
  matched: number
  items: { id: string; title: string; url: string; source_name: string; topics: string[]; published_at: string }[]
}

export type SetupKey = "inquiry_form" | "smtp" | "chat_widget" | "whatsapp" | "google" | "browser"

export type Overview = {
  currency: string
  timezone: string
  today: OverviewToday
  pipeline: OverviewPipeline | null
  leads: OverviewLead[] | null
  conversations: OverviewConversation[] | null
  notifications: OverviewNotification[] | null
  marketing: OverviewMarketing | null
  trends: OverviewTrends | null
  /** Only the items the viewer's role can see. available=false: this server cannot do it at all. */
  setup: { key: SetupKey; done: boolean; available: boolean }[] | null
  /** Null without analytics:read, or when no GA4 property / Search Console site is enabled. */
  traffic?: OverviewTraffic | null
  /** Each enabled YouTube channel (MH-441/442). */
  youtube?: OverviewYouTube[] | null
}

export type OverviewYouTube = {
  resource_id: string
  title: string
  subscribers: number | null
  videos: number | null
  /** YouTube Analytics views over the last 28 days; null until fetched. */
  views_28: number | null
  views_28_to: string | null
  synced_at: string | null
  last_error: string
}

/** The last 28 days of each enabled GA4 property (MH-444). A null figure was never fetched. */
export type OverviewTraffic = {
  properties: {
    resource_id: string
    name: string
    users: number | null
    sessions: number | null
    start_date: string | null
    end_date: string | null
    fetched_at: string | null
    last_error: string
  }[] | null
  /** Each enabled Search Console site's clicks over the 28 days ending on its newest day with data (MH-445). */
  sites?: {
    resource_id: string
    name: string
    clicks: number | null
    impressions: number | null
    start_date: string | null
    end_date: string | null
    last_error: string
  }[] | null
}
