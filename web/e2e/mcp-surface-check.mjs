// The MCP surface (MH-380, MH-386, MH-393, MH-396, MH-409) over a REAL MCP
// client session: JSON-RPC 2.0 over the streamable-HTTP transport at
// <base>/api/mcp, with a bearer token minted through the settings page's own
// route. Nothing is called in-process — this is what Claude Code or Cursor do.
//
// The outside world is faked here: an RSS feed (any free port) and an Evolution
// API server for WhatsApp on EVO_PORT, which the API must have been started
// against (EVOLUTION_API_URL=http://localhost:<EVO_PORT>, EVOLUTION_API_KEY=x,
// WHATSAPP_HOOK_BASE=<api origin>). Without it the WhatsApp checks report the
// server as not configured, which they also assert.
//
//   node web/e2e/mcp-surface-check.mjs http://localhost:3503
import http from "node:http"
import crypto from "node:crypto"

const base = process.argv[2] ?? "http://localhost:3503"
const EVO_PORT = Number(process.env.EVO_PORT ?? 8593)
const results = []
const ok = (name, pass, detail = "") => {
  results.push({ name, pass })
  console.log(`${pass ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`)
}

// ---- fakes -----------------------------------------------------------------
let polls = 0
const feed = http.createServer((req, res) => {
  // /wire2.xml is a second outlet carrying the first two headlines at its own
  // addresses, so the trends center clusters them into cross-source stories.
  if (req.url === "/wire2.xml") {
    let items = ""
    for (let i = 1; i <= 2; i++) {
      const at = new Date(Date.now() - 30 * 60e3).toUTCString()
      items += `<item><title>Nile coffee exporters report record agents story ${i}</title><link>https://wire2.example/${i}</link><guid>w2-${i}</guid><description>Second outlet ${i}</description><pubDate>${at}</pubDate></item>`
    }
    res.setHeader("Content-Type", "application/rss+xml")
    return res.end(
      `<?xml version="1.0"?><rss version="2.0"><channel><title>Second Wire</title>${items}</channel></rss>`,
    )
  }
  polls++
  const n = polls + 2
  let items = ""
  for (let i = 1; i <= n; i++) {
    const at = new Date(Date.now() - (n - i) * 3600e3).toUTCString()
    items += `<item><title>Nile coffee exporters report record agents story ${i}</title><link>https://news.example/${i}</link><guid>e2e-${i}</guid><description>Summary ${i}</description><pubDate>${at}</pubDate></item>`
  }
  res.setHeader("Content-Type", "application/rss+xml")
  res.end(
    `<?xml version="1.0"?><rss version="2.0"><channel><title>E2E Wire</title>${items}</channel></rss>`,
  )
})
await new Promise((r) => feed.listen(0, "127.0.0.1", r))
const feedURL = `http://127.0.0.1:${feed.address().port}/rss.xml`

const evo = { hook: "", sent: [] }
const evoServer = http.createServer(async (req, res) => {
  let body = ""
  for await (const c of req) body += c
  const j = body ? JSON.parse(body) : {}
  res.setHeader("Content-Type", "application/json")
  const p = req.url
  if (req.method === "POST" && p === "/instance/create")
    return res.end(
      JSON.stringify({
        qrcode: {
          code: "2@E2E-QR",
          base64: "data:image/png;base64,iVBOR",
          pairingCode: j.number ? "E2E-" + j.number.slice(-4) : "",
        },
      }),
    )
  if (p.startsWith("/instance/connect/"))
    return res.end(JSON.stringify({ code: "2@E2E-QR", base64: "data:image/png;base64,iVBOR" }))
  if (p.startsWith("/webhook/set/")) {
    evo.hook = j.webhook?.url ?? ""
    return res.end("{}")
  }
  if (p.startsWith("/webhook/find/"))
    return res.end(evo.hook ? JSON.stringify({ enabled: true, url: evo.hook }) : "null")
  if (p.startsWith("/instance/connectionState/"))
    return res.end(JSON.stringify({ instance: { state: "open" } }))
  if (p.startsWith("/message/sendText/")) {
    evo.sent.push({ ...j, at: Date.now() })
    return res.end(JSON.stringify({ key: { id: "E2E-" + evo.sent.length } }))
  }
  res.statusCode = 404
  res.end('{"message":"not found"}')
})
const evoUp = await new Promise((r) =>
  evoServer.listen(EVO_PORT, "127.0.0.1", () => r(true)).on("error", () => r(false)),
)

// ---- a browser-less session for the REST setup -------------------------------
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
  const text = await res.text()
  let json = {}
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: res.status, json }
}
const csrf = await rest("GET", "/api/auth/csrf")
jar.set("__csrf", csrf.json.csrf_token)
const email = `mcp-e2e-${Date.now()}@example.test`
await rest("POST", "/api/auth/register", { email, password: "correct-horse-e2e" })
const ws = (await rest("POST", "/api/me/workspaces", { name: `MCP surface e2e ${Date.now()}` }))
  .json.workspace.id
const scopes = (await rest("GET", "/api/w/mcp_tokens", null, ws)).json.scopes.map((s) => s.scope)
const mint = async (name, sc) =>
  (await rest("POST", "/api/w/mcp_tokens", { name, scopes: sc }, ws)).json

// ---- the MCP client ------------------------------------------------------------
let rpcID = 0
async function rpc(token, method, params) {
  const res = await fetch(`${base}/api/mcp`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: ++rpcID, method, params }),
  })
  return { status: res.status, body: await res.json().catch(() => ({})) }
}
async function tool(token, name, args = {}) {
  const r = await rpc(token, "tools/call", { name, arguments: args })
  const text = r.body.result?.content?.[0]?.text ?? r.body.error?.message ?? ""
  let json = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: r.status, isError: !!r.body.result?.isError, text, json }
}

const full = await mint("e2e full", scopes)
const T = full.plaintext
const init = await rpc(T, "initialize", {
  protocolVersion: "2025-06-18",
  capabilities: {},
  clientInfo: { name: "mcp-surface-check", version: "1" },
})
ok("initialize negotiates 2025-06-18", init.body.result?.protocolVersion === "2025-06-18")
await rpc(T, "notifications/initialized", {})
const list = await rpc(T, "tools/list", {})
const names = (list.body.result?.tools ?? []).map((t) => t.name)
const wanted = [
  "list_conversations",
  "get_conversation",
  "reply",
  "close_conversation",
  "assign_conversation",
  "list_notifications",
  "get_notification",
  "saved_notifications",
  "mark_notification",
  "save_notification",
  "mark_all_notifications_read",
  "list_webhook_sources",
  "create_webhook_source",
  "rotate_webhook_source",
  "disable_webhook_source",
  "enable_webhook_source",
  "get_contact",
  "search_contacts",
  "create_contact",
  "update_contact",
  "get_company",
  "search_companies",
  "update_company",
  "get_deal",
  "update_deal",
  "company_timeline",
  "list_activities",
  "snooze_conversation",
  "link_conversation_contact",
  "create_contact_from_conversation",
  "open_deal_from_conversation",
  "list_canned_replies",
  "inbox_stats",
  "list_trend_sources",
  "list_trend_items",
  "list_saved_trend_items",
  "mark_trend_item",
  "collect_now",
  "add_trend_source",
  "update_trend_source",
  "list_trend_catalog",
  "trend_budget",
  "list_trending_topics",
  "get_trending_topic",
  "review_trending_topic",
  "whatsapp_status",
  "whatsapp_link",
  "send_whatsapp",
]
ok(
  `tools/list offers all ${wanted.length} new tools (${names.length} in total)`,
  wanted.every((n) => names.includes(n)),
  wanted.filter((n) => !names.includes(n)).join(", "),
)
ok(
  "every tool has a description and its scope",
  (list.body.result?.tools ?? []).every(
    (t) => t.description.length > 20 && /\(scope: [a-z]+:[a-z_]+\)$/.test(t.description),
  ),
)

// ---- CRM (MH-393) ----
const me = (await tool(T, "list_members")).json.members[0]
const co = (
  await tool(T, "create_company", {
    name: "Nile Robotics",
    domain: "nile-robotics.example",
    industry: "Robotics",
  })
).json.company
ok("create_company returns an id", !!co?.id)
const found = (await tool(T, "search_companies", { q: "nile-robotics" })).json
ok(
  "search_companies finds it by domain, with a total",
  found.total === 1 && found.companies[0]?.id === co.id,
)
const lc = (await tool(T, "list_companies", { limit: 1 })).json
ok(
  "list_companies pages with a total and ids",
  lc.total >= 1 && !!lc.companies[0]?.id && lc.companies.length === 1,
)
const ld = (await tool(T, "list_deals", { limit: 1 })).json
ok("list_deals pages with a total and ids", typeof ld.total === "number" && Array.isArray(ld.deals))
ok(
  "update_company sets the owner by email",
  (await tool(T, "update_company", { id: co.id, size: "11-50", owner: me.email })).json.company
    .owner_user_id === me.user_id,
)
const ct = (
  await tool(T, "create_contact", {
    email: "salma@nile-robotics.example",
    name: "Salma",
    phone: "+20 100 777 1234",
    title: "COO",
    company_id: co.id,
  })
).json.contact
ok(
  "create_contact keeps phone, title and company",
  ct.phone === "+20 100 777 1234" && ct.company?.id === co.id,
)
ok(
  "search_contacts by phone",
  (await tool(T, "search_contacts", { q: "777 1234" })).json.contacts[0]?.id === ct.id,
)
ok(
  "search_contacts by company",
  (await tool(T, "search_contacts", { q: "Nile Robotics" })).json.contacts[0]?.id === ct.id,
)
ok(
  "update_contact",
  (await tool(T, "update_contact", { id: ct.id, tags: ["vip"] })).json.contact.tags[0] === "vip",
)
const deal = (
  await tool(T, "create_deal", {
    title: "Nile pilot",
    value: 4200,
    contact_id: ct.id,
    company_id: co.id,
  })
).json.deal
await tool(T, "log_activity", { kind: "meeting", body: "Kick-off", deal_id: deal.id })
ok(
  "get_deal has its timeline",
  (await tool(T, "get_deal", { id: deal.id })).json.activities.length === 1,
)
const won = (await tool(T, "update_deal", { id: deal.id, stage: "Won" })).json.deal
ok("update_deal to Won closes it", won.stage.kind === "won" && !!won.closed_at)
ok("get_contact", (await tool(T, "get_contact", { id: ct.id })).json.deals.length === 1)
ok(
  "list_contacts returns ids",
  !!(await tool(T, "list_contacts", { limit: 5 })).json.contacts[0].id,
)

// ---- inbox (MH-380) ----
const origin = "https://e2e.example"
const widget = (
  await rest("POST", "/api/w/chat_widgets", { name: "E2E", allowed_origins: [origin] }, ws)
).json.widget
const sent = await fetch(`${base}/api/chat/${widget.public_key}/messages`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Origin: origin },
  body: JSON.stringify({
    body: "Is the pilot on?",
    name: "Salma",
    email: "salma@nile-robotics.example",
  }),
})
const visitorToken = (await sent.json()).visitor_token
const convs = (await tool(T, "list_conversations", { unread: true })).json
const conv = convs.conversations[0]
ok(
  "list_conversations shows the visitor's chat, unread",
  convs.unread_count === 1 && conv.channel === "chat",
)
ok(
  "get_conversation reads the thread and marks it read",
  (await tool(T, "get_conversation", { id: conv.id })).json.conversation.unread === false,
)
const rep = (await tool(T, "reply", { id: conv.id, body: "Yes, Tuesday." })).json
const poll = await (
  await fetch(`${base}/api/chat/${widget.public_key}/messages?visitor_token=${visitorToken}`, {
    headers: { Origin: origin },
  })
).json()
ok("reply reaches the visitor's widget", rep.sent && poll.messages.at(-1)?.body === "Yes, Tuesday.")
ok(
  "assign_conversation by email",
  (await tool(T, "assign_conversation", { id: conv.id, assignee: me.email })).json.conversation
    .assignee_user_id === me.user_id,
)
ok(
  "close_conversation",
  (await tool(T, "close_conversation", { id: conv.id })).json.conversation.status === "closed",
)
await rest("PATCH", `/api/w/conversations/${conv.id}`, { contact_id: ct.id }, ws)
const tl = (await tool(T, "company_timeline", { id: co.id })).json
const kinds = new Set(tl.entries.map((e) => `${e.type}:${e.kind}`))
ok(
  "company_timeline merges activities, conversations and deals",
  ["activity:meeting", "conversation:chat", "deal:opened", "deal:won"].every((k) => kinds.has(k)),
  [...kinds].join(" "),
)
ok(
  "list_activities pages a record's timeline",
  (await tool(T, "list_activities", { deal_id: deal.id })).json.total === 1,
)

// ---- the inbox on MH-379/383: snooze, CRM links, canned replies, stats ----
const until = new Date(Date.now() + 48 * 3600e3).toISOString().replace(/\.\d+Z$/, "Z")
ok(
  "snooze_conversation until a time",
  (await tool(T, "snooze_conversation", { id: conv.id, until })).json.conversation.status ===
    "snoozed",
)
ok(
  "snooze_conversation wake",
  (await tool(T, "snooze_conversation", { id: conv.id, wake: true })).json.conversation.status ===
    "open",
)
const made = (
  await tool(T, "create_contact_from_conversation", {
    id: conv.id,
    email: "salma@nile-robotics.example",
  })
).json
ok(
  "create_contact_from_conversation links the existing contact",
  made.contact_id === ct.id && made.contact_created === false,
)
const od = (await tool(T, "open_deal_from_conversation", { id: conv.id, value: 300 })).json
ok(
  "open_deal_from_conversation opens a deal with the transcript",
  !!od.deal_id && !!od.activity_id && od.conversation.deal?.id === od.deal_id,
)
ok(
  "a conversation opens one deal",
  /already opened a deal/.test(
    (await tool(T, "open_deal_from_conversation", { id: conv.id })).text,
  ),
)
await rest(
  "POST",
  "/api/w/canned_replies",
  { title: "Thanks", body_en: "Thanks for writing.", body_ar: "شكرًا لتواصلك." },
  ws,
)
ok("list_canned_replies", (await tool(T, "list_canned_replies")).json.replies.length === 1)
ok("inbox_stats", !(await tool(T, "inbox_stats", { days: 7 })).isError)
const gcv = (await tool(T, "get_conversation", { id: conv.id })).json
ok(
  "get_conversation carries the reply language and the linked deal",
  !!gcv.reply_language && gcv.conversation.deal?.id === od.deal_id,
)

// ---- notifications (MH-380) ----
const created = (await tool(T, "create_webhook_source", { name: "E2E hook" })).json
const src = created.source
const deliver = async (secret, title) => {
  const raw = JSON.stringify({ title })
  const stamp = String(Math.floor(Date.now() / 1000))
  const sig = crypto
    .createHmac("sha256", secret)
    .update(stamp + "." + raw)
    .digest("hex")
  return (
    await fetch(`${base}/api/hooks/${src.id}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Webhook-Timestamp": stamp,
        "X-Webhook-Signature": "sha256=" + sig,
      },
      body: raw,
    })
  ).status
}
ok(
  "create_webhook_source returns id, URL and a once-only secret",
  !!src.id && src.url.endsWith(`/api/hooks/${src.id}`) && !!created.secret,
)
ok("a signed delivery is accepted", (await deliver(created.secret, "Build failed")) === 202)
ok(
  "list_webhook_sources shows id, URL and the delivery",
  (await tool(T, "list_webhook_sources")).json.sources.some(
    (s) => s.id === src.id && s.url === src.url && s.delivery_count === 1,
  ),
)
const items = (await tool(T, "list_notifications")).json.items
ok("list_notifications returns ids", !!items[0]?.id && items[0].title === "Build failed")
ok(
  "get_notification has the payload",
  (await tool(T, "get_notification", { id: items[0].id })).json.item.payload.includes(
    "Build failed",
  ),
)
ok(
  "mark_notification unread",
  (await tool(T, "mark_notification", { id: items[0].id, read: false })).json.item.read === false,
)
ok(
  "save_notification",
  (await tool(T, "save_notification", { id: items[0].id })).json.item.saved === true,
)
ok(
  "saved_notifications lists it",
  (await tool(T, "saved_notifications")).json.items[0]?.id === items[0].id,
)
ok("mark_all_notifications_read", (await tool(T, "mark_all_notifications_read")).json.marked >= 1)
const rot = (await tool(T, "rotate_webhook_source", { id: src.id })).json
ok(
  "rotate_webhook_source: the old secret stops, the new one works",
  (await deliver(created.secret, "old")) === 401 && (await deliver(rot.secret, "new")) === 202,
)
await tool(T, "disable_webhook_source", { id: src.id })
ok("disable_webhook_source refuses deliveries", (await deliver(rot.secret, "off")) === 403)
ok(
  "enable_webhook_source",
  (await tool(T, "enable_webhook_source", { id: src.id })).json.source.enabled === true,
)

// ---- trends (MH-386, on the trends center MH-385/387) ----
const added = (await tool(T, "add_trend_source", { url: feedURL })).json
ok(
  "add_trend_source fetches it at once",
  added.added === 3 && added.source.name === "E2E Wire" && added.source.interval_minutes === 720,
)
const second = (
  await tool(T, "add_trend_source", {
    url: feedURL.replace("/rss.xml", "/wire2.xml"),
    interval_minutes: 1440,
  })
).json
ok(
  "add_trend_source with an interval",
  second.source.interval_minutes === 1440 && second.added === 2,
)
ok("list_trend_sources", (await tool(T, "list_trend_sources")).json.sources.length === 2)
const catalog = (await tool(T, "list_trend_catalog")).json.entries
ok(
  "list_trend_catalog lists entries by key",
  catalog.length > 5 && catalog.some((e) => e.key === "hackernews" && e.provider === "hn"),
)
ok(
  "add_trend_source refuses an unknown catalog key",
  /no catalog source/.test((await tool(T, "add_trend_source", { catalog_key: "nope" })).text),
)
ok(
  "update_trend_source sets the interval",
  (
    await tool(T, "update_trend_source", {
      id: added.source.id,
      interval_minutes: 360,
      name: "Wire",
    })
  ).json.source.interval_minutes === 360,
)
ok(
  "update_trend_source refuses a bad interval",
  /360, 720 or 1440/.test(
    (await tool(T, "update_trend_source", { id: added.source.id, interval_minutes: 5 })).text,
  ),
)
const topics = (await tool(T, "list_trending_topics")).json
const cross = topics.topics.find((t) => t.source_count >= 2)
ok(
  "list_trending_topics clusters the two outlets' shared headlines",
  !!cross,
  JSON.stringify(topics.counts),
)
if (cross) {
  const det = (await tool(T, "get_trending_topic", { id: cross.id })).json
  ok(
    "get_trending_topic has every item and outlet",
    det.items.length >= 2 && det.outlets.length >= 2,
  )
  ok(
    "list_trend_items by trending topic",
    (await tool(T, "list_trend_items", { trending_topic_id: cross.id })).json.items.length ===
      det.items.length,
  )
  ok(
    "review_trending_topic marks it reviewed",
    (await tool(T, "review_trending_topic", { id: cross.id, reviewed: true })).json.topic.state ===
      "reviewed",
  )
  ok(
    "a reviewed topic leaves the open list",
    !(await tool(T, "list_trending_topics")).json.topics.some((t) => t.id === cross.id),
  )
}
const tp1 = (await tool(T, "list_trend_items", { limit: 2, source_id: added.source.id })).json
const tp2 = (
  await tool(T, "list_trend_items", {
    limit: 2,
    source_id: added.source.id,
    cursor: tp1.next_cursor,
  })
).json
ok(
  "list_trend_items pages with a cursor",
  tp1.items.length === 2 && tp1.has_more && tp2.items.length === 1 && !tp2.has_more,
)
await tool(T, "mark_trend_item", { id: tp1.items[0].id })
ok(
  "mark_trend_item + list_saved_trend_items",
  (await tool(T, "list_saved_trend_items")).json.items[0]?.id === tp1.items[0].id,
)
const b0 = (await tool(T, "trend_budget")).json.budget
ok("trend_budget counts today's fetches", b0.fetches_today === 2, JSON.stringify(b0))
const coll = (await tool(T, "collect_now")).json
ok(
  "collect_now fetches every enabled source and reclusters",
  coll.sources.length === 2 &&
    coll.added >= 1 &&
    coll.failed === 0 &&
    coll.clustered === true &&
    coll.budget.fetches_today === 4,
  JSON.stringify({
    added: coll.added,
    clustered: coll.clustered,
    new_topics: coll.new_topics,
    budget: coll.budget,
  }),
)
ok(
  "every collected item is placed in a trending topic",
  (await tool(T, "list_trend_items", { limit: 200 })).json.items.every(
    (n) => !!n.trending_topic_id,
  ),
)
await rest("PATCH", "/api/w/trend_settings", { fetch_budget_per_day: 5 }, ws)
const edge = (await tool(T, "collect_now")).json
ok(
  "collect_now stops at the budget",
  edge.over_budget === 1 && edge.budget.fetches_today === 5,
  JSON.stringify(edge.sources.map((x) => x.status)),
)
const spent = (await rest("POST", "/api/w/feed_sources/collect", null, ws)).json
ok(
  "POST /api/w/feed_sources/collect with the budget spent fetches nothing",
  spent.over_budget === 2 && spent.added === 0,
)

// ---- WhatsApp (MH-409) ----
const st0 = (await tool(T, "whatsapp_status")).json
if (!evoUp || !st0.available) {
  ok("whatsapp_status says WhatsApp is not configured (no fake Evolution)", st0.available === false)
  ok(
    "whatsapp_link says so too",
    /not available|Evolution/i.test((await tool(T, "whatsapp_link")).text),
  )
} else {
  ok("whatsapp_status before linking", st0.linked === false && st0.webhook.status === "not_linked")
  const link = (await tool(T, "whatsapp_link", { number: "+20 100 555 4321" })).json
  ok(
    "whatsapp_link returns the QR and a pairing code",
    link.qr_code === "2@E2E-QR" && link.pairing_code === "E2E-4321",
  )
  const st1 = (await tool(T, "whatsapp_status")).json
  ok(
    "whatsapp_status: linked, open, webhook ok",
    st1.linked && st1.state === "open" && st1.webhook.status === "ok",
  )
  ok(
    "send_whatsapp refuses a contact who never opted in",
    /not opted in/.test(
      (await tool(T, "send_whatsapp", { contact_id: ct.id, text: "Hello" })).text,
    ),
  )
  await rest("POST", `/api/w/contacts/${ct.id}/whatsapp/opt-in`, null, ws)
  const first = (await tool(T, "send_whatsapp", { contact_id: ct.id, text: "Pilot confirmed." }))
    .json
  ok(
    "a new thread asks for a confirmation code first",
    first.confirmation_required && first.code?.length === 6 && evo.sent.length === 0,
  )
  const second = (
    await tool(T, "send_whatsapp", {
      contact_id: ct.id,
      text: "Pilot confirmed.",
      code: first.code,
    })
  ).json
  ok(
    "with the code it sends and opens the thread",
    second.sent && second.new_thread && evo.sent.at(-1)?.text === "Pilot confirmed.",
  )
  await tool(T, "send_whatsapp", { contact_id: ct.id, text: "See you Tuesday." })
  const gap = evo.sent.at(-1).at - evo.sent.at(-2).at
  ok("sends are paced (≥ 1.2 s apart)", gap >= 1150, `${gap} ms`)
  ok(
    "the thread is read through the inbox tools",
    (await tool(T, "get_conversation", { id: second.conversation_id })).json.messages.length === 2,
  )
  ok(
    "whatsapp_status counts today's sends",
    (await tool(T, "whatsapp_status")).json.sent_today === 2,
  )
}

// ---- scopes: a token without the scope is refused, naming it ----
const narrow = (await mint("e2e narrow", ["members:read"])).plaintext
for (const [name, scope] of [
  ["list_conversations", "inbox:read"],
  ["reply", "inbox:write"],
  ["get_contact", "contacts:read"],
  ["update_deal", "contacts:write"],
  ["list_notifications", "notifications:read"],
  ["mark_notification", "notifications:write"],
  ["rotate_webhook_source", "notifications:manage"],
  ["list_trend_items", "trends:read"],
  ["mark_trend_item", "trends:write"],
  ["collect_now", "trends:manage"],
  ["list_trending_topics", "trends:read"],
  ["review_trending_topic", "trends:write"],
  ["trend_budget", "trends:read"],
  ["whatsapp_status", "inbox:read"],
  ["whatsapp_link", "inbox:manage"],
  ["send_whatsapp", "whatsapp:send"],
  ["snooze_conversation", "inbox:write"],
  ["list_activities", "contacts:read"],
  ["inbox_stats", "inbox:read"],
]) {
  const r = await tool(narrow, name, { id: "x" })
  ok(`${name} without ${scope} is refused`, r.isError && r.text.includes(scope))
}

// ---- token admin (MH-396): revoked means 401 on the next call ----
const doomed = await mint("e2e doomed", ["members:read"])
ok("a fresh token works", !(await tool(doomed.plaintext, "whoami")).isError)
await rest("DELETE", `/api/w/mcp_tokens/${doomed.token.id}`, null, ws)
ok(
  "a revoked token 401s immediately",
  (await rpc(doomed.plaintext, "tools/call", { name: "whoami", arguments: {} })).status === 401,
)

feed.close()
evoServer.close()
const failed = results.filter((r) => !r.pass)
console.log(`\n${results.length - failed.length}/${results.length} passed`)
process.exit(failed.length ? 1 : 0)
