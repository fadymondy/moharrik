// A LOCAL demo stack for app screenshots and manual testing — never point it at a real server.
//
//   node apps/scripts/demo-stack.mjs --api http://127.0.0.1:8131 [--port 8130] [--seed]
//
// --seed (or a first run) registers a demo user, creates a workspace and fills it through the
// public API: companies, contacts, deals across the pipeline, labels, chat-widget conversations
// (visitor messages: inbound only, nothing is sent to anyone) and webhook notifications.
//
// Then it serves :port as the "web app": every request is proxied to the API, except the browser
// sign-in page /{locale}/app-signin, which it completes for the demo user (the step a person does
// in the browser) and redirects straight back to the app. So the phone and desktop sign-in flows run
// end to end with no browser interaction. From the Android emulator, use http://10.0.2.2:<port>.
import { createHmac } from "node:crypto"
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import { createServer, request } from "node:http"
import { fileURLToPath } from "node:url"

const arg = (k, d) => {
  const i = process.argv.indexOf(`--${k}`)
  return i > 0 ? process.argv[i + 1] : d
}
const API = arg("api", "http://127.0.0.1:8131")
const PORT = Number(arg("port", "8130"))
const STATE = arg("state", fileURLToPath(new URL("./.demo-state.json", import.meta.url)))
const ORIGIN = `http://127.0.0.1:${PORT}`

class Session {
  cookies = new Map()
  csrf = ""
  ws = ""
  async req(method, path, body, { raw = false } = {}) {
    if (method !== "GET" && !this.csrf) this.csrf = (await this.req("GET", "/api/auth/csrf")).csrf_token
    const h = { "Content-Type": "application/json" }
    if (this.cookies.size) h.Cookie = [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ")
    if (this.ws) h["X-Workspace-ID"] = this.ws
    if (method !== "GET") h["X-CSRF-Token"] = this.csrf
    const res = await fetch(API + path, { method, headers: h, body: body == null ? undefined : JSON.stringify(body), redirect: "manual" })
    for (const c of res.headers.getSetCookie()) {
      const [kv] = c.split(";")
      const i = kv.indexOf("=")
      this.cookies.set(kv.slice(0, i), kv.slice(i + 1))
    }
    const text = await res.text()
    if (raw) return { status: res.status, text }
    if (res.status >= 300) throw new Error(`${method} ${path}: ${res.status} ${text.slice(0, 300)}`)
    return text ? JSON.parse(text) : {}
  }
}

async function seed() {
  const s = new Session()
  const email = `demo-${Date.now().toString(36)}@moharrik.test`
  const password = "demo-pass-" + Math.random().toString(36).slice(2)
  await s.req("POST", "/api/auth/register", { email, password })
  const ws = (await s.req("POST", "/api/me/workspaces", { name: "استوديو النيل", slug: "nile-studio" })).workspace
  await s.req("POST", "/api/me/workspaces", { name: "Acme Support", slug: "acme-support" })
  s.ws = ws.id

  const co = {}
  const companies = {
    nile: { name: "شركة النيل للتقنية", domain: "nile-tech.example", industry: "Software", country: "EG", size: "51-200" },
    delta: { name: "Delta Logistics", domain: "delta-logistics.example", industry: "Logistics", country: "AE", size: "201-1000" },
    sahara: { name: "صحارى للتجارة", domain: "sahara-trade.example", industry: "Retail", country: "SA", size: "11-50" },
  }
  for (const [k, c] of Object.entries(companies)) co[k] = (await s.req("POST", "/api/w/companies", c)).company.id

  const people = [
    ["أحمد السيد", "ahmed@nile-tech.example", "+20 100 111 2233", "nile", "customer", "CTO"],
    ["Sara Haddad", "sara@delta-logistics.example", "+971 50 123 4567", "delta", "qualified", "Ops Lead"],
    ["منى عبد الله", "mona@sahara-trade.example", "+966 55 765 4321", "sahara", "lead", "Founder"],
    ["Omar Khaled", "omar@example.test", "+20 122 333 4455", null, "subscriber", ""],
    ["ليلى حسن", "laila@nile-tech.example", "", "nile", "lead", "Marketing"],
  ]
  const ct = []
  for (const [name, mail, phone, company, lifecycle, title] of people) {
    const c = (await s.req("POST", "/api/w/contacts", { name, email: mail, phone: phone || undefined })).contact
    const r = await s.req("PATCH", `/api/w/contacts/${c.id}`, { lifecycle, title, company_id: company ? co[company] : undefined }, { raw: true })
    if (r.status >= 300) console.warn("contact update:", r.status, r.text.slice(0, 200))
    ct.push(c.id)
  }

  const stages = (await s.req("GET", "/api/w/pipeline/stages")).stages ?? []
  const st = (i) => stages[Math.min(i, stages.length - 1)]?.id
  const deals = [
    ["تطوير الموقع الإلكتروني", 4500000, "EGP", 0, 0, "nile"],
    ["Fleet tracking dashboard", 1200000, "AED", 1, 1, "delta"],
    ["متجر إلكتروني", 850000, "SAR", 2, 2, "sahara"],
    ["Mobile app MVP", 2000000, "USD", 2, 0, "nile"],
    ["حملة تسويقية", 300000, "EGP", 3, 4, "nile"],
  ]
  for (const [title, value_minor, currency, stage, contact, company] of deals)
    await s.req("POST", "/api/w/deals", { title, value_minor, currency, stage_id: st(stage), contact_id: ct[contact], company_id: co[company] })

  for (const [name, color] of [["VIP", "violet"], ["دعم فني", "teal"], ["فواتير", "amber"]])
    await s.req("POST", "/api/w/conversation_labels", { name, color })

  // Visitor messages through a chat widget.
  const widget = (await s.req("POST", "/api/w/chat_widgets", { name: "Website chat", allowed_origins: [ORIGIN] })).widget
  const visitor = async (name, mail, bodies) => {
    let token
    for (const body of bodies) {
      const r = await fetch(`${API}/api/chat/${widget.public_key}/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Origin: ORIGIN },
        body: JSON.stringify({ body, name, email: mail, visitor_token: token }),
      })
      if (r.status !== 201) throw new Error(`chat message: ${r.status} ${await r.text()}`)
      token = (await r.json()).visitor_token ?? token
    }
  }
  await visitor("Omar Khaled", "omar@example.test", ["Is there an Arabic version of the dashboard?", "Also, do you support WhatsApp?"])
  await visitor("أحمد السيد", "ahmed@nile-tech.example", ["تم استلام الفاتورة، شكراً لكم 🙏"])
  await visitor("Sara Haddad", "sara@delta-logistics.example", ["Hi! Can we move our demo call to Thursday?"])
  await visitor("منى عبد الله", "mona@sahara-trade.example", ["السلام عليكم، عندي سؤال عن باقة المتجر الإلكتروني", "هل يشمل السعر الاستضافة لمدة سنة؟"])

  // Notifications from a webhook source, signed exactly like a real sender.
  const src = await s.req("POST", "/api/w/webhook_sources", { name: "Website forms" })
  for (const title of ["تم دفع الفاتورة #1042", "New signup: omar@example.test", "نموذج تواصل جديد من صفحة الأسعار"]) {
    const raw = JSON.stringify({ title })
    const stamp = String(Math.floor(Date.now() / 1000))
    const sig = createHmac("sha256", src.secret).update(stamp + "." + raw).digest("hex")
    const r = await fetch(`${API}/api/hooks/${src.source.id}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Webhook-Timestamp": stamp, "X-Webhook-Signature": "sha256=" + sig },
      body: raw,
    })
    if (r.status !== 202) throw new Error(`webhook: ${r.status} ${await r.text()}`)
  }

  const state = { email, password, workspace: ws.id, cookies: [...s.cookies], csrf: s.csrf }
  writeFileSync(STATE, JSON.stringify(state, null, 2))
  console.log(`seeded workspace ${ws.id} for ${email}`)
  return state
}

const state = process.argv.includes("--seed") || !existsSync(STATE) ? await seed() : JSON.parse(readFileSync(STATE, "utf8"))
const user = new Session()
user.cookies = new Map(state.cookies)
user.csrf = state.csrf

createServer(async (req, res) => {
  const u = new URL(req.url, ORIGIN)
  if (req.method === "GET" && /^\/(en|ar)\/app-signin$/.test(u.pathname)) {
    const q = Object.fromEntries(u.searchParams)
    const r = await user.req("POST", "/api/me/app-signin", { ...q, code_challenge_method: "S256" }, { raw: true })
    if (r.status !== 200) {
      res.writeHead(r.status, { "Content-Type": "text/plain" }).end(r.text)
      return
    }
    const to = JSON.parse(r.text).redirect_to
    console.log("signed in", q.device, "→", to.replace(/code=[^&]+/, "code=…"))
    res.writeHead(302, { Location: to }).end()
    return
  }
  const api = new URL(API)
  const p = request({ host: api.hostname, port: api.port, method: req.method, path: req.url, headers: { ...req.headers, host: api.host } }, (r) => {
    res.writeHead(r.statusCode, r.headers)
    r.pipe(res)
  })
  p.on("error", (e) => res.writeHead(502).end(String(e)))
  req.pipe(p)
}).listen(PORT, "0.0.0.0", () => console.log(`demo web on ${ORIGIN} → ${API}`))
