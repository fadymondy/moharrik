// Seed a demo workspace through the PUBLIC API, the way the UI and a customer's website would.
//
//   node e2e/seed-demo.mjs https://app.moharrik.com
//   node e2e/seed-demo.mjs http://localhost:3122
//
// Signs in as demo@moharrik.com (registers it on the first run), creates the workspace
// "Moharrik Demo — Nile Coffee Co." and fills it: CRM, marketing, website forms + public
// submissions, inbox chats, signed webhook deliveries, real RSS feeds, one research run and
// a saved automation workflow. Re-runnable: anything that already exists (by name/email) is
// skipped. Never writes to the database directly.
//
// The password: DEMO_PASSWORD, else ~/.moharrik-demo.json (keyed by base URL), else a new one
// is generated on registration, stored there, and printed ONCE at the end. Never committed.
//
// Safety: every email is on the reserved .example TLD and every phone number is in a mobile
// range no operator has allocated (Egypt +20 16/17, KSA +966 51, UAE +971 53), so a campaign
// sent from this workspace by mistake cannot reach a real person.
import { createHmac, randomBytes } from "node:crypto"
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "http://localhost:3122").replace(/\/$/, "")
const EMAIL = process.env.DEMO_EMAIL ?? "demo@moharrik.com"
const WS_NAME = "Moharrik Demo — Nile Coffee Co."
const SITE = "https://nilecoffee.example"
const STORE = path.join(homedir(), ".moharrik-demo.json")

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a)
const skipped = [] // [what, why]

// ---- a tiny cookie-jar HTTP session ------------------------------------------------------

class Session {
  cookies = new Map()
  csrf = null
  ws = null
  async raw(method, p, { json, headers = {}, ws = this.ws, origin } = {}) {
    const h = { Accept: "application/json", ...headers }
    if (json !== undefined) h["Content-Type"] = "application/json"
    if (this.cookies.size) h.Cookie = [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ")
    if (ws) h["X-Workspace-ID"] = ws
    if (method !== "GET") {
      if (!this.csrf) await this.refreshCsrf()
      h["X-CSRF-Token"] = this.csrf
      h.Origin = origin ?? base
      h.Referer = base + "/"
    }
    const res = await fetch(base + p, {
      method,
      headers: h,
      body: json !== undefined ? JSON.stringify(json) : undefined,
      redirect: "manual",
    })
    for (const c of res.headers.getSetCookie?.() ?? []) {
      const [pair] = c.split(";")
      const i = pair.indexOf("=")
      const k = pair.slice(0, i).trim()
      const v = pair.slice(i + 1).trim()
      if (/max-age=0|expires=thu, 01 jan 1970/i.test(c) || v === "") this.cookies.delete(k)
      else this.cookies.set(k, v)
    }
    return res
  }
  async refreshCsrf() {
    this.csrf = null
    const r = await this.raw("GET", "/api/auth/csrf", { ws: null })
    this.csrf = (await r.json()).csrf_token
  }
  async call(method, p, json, opts = {}) {
    for (let attempt = 0; ; attempt++) {
      let res = await this.raw(method, p, { json, ...opts })
      if (res.status === 403 && method !== "GET") {
        const t = await res.clone().text()
        if (/csrf/i.test(t)) {
          await this.refreshCsrf()
          res = await this.raw(method, p, { json, ...opts })
        }
      }
      if (res.status === 429 && attempt < 3) {
        const wait = Math.min(+(res.headers.get("retry-after") ?? 60), 900) + 2
        log(`429 on ${method} ${p}: waiting ${wait}s`)
        await sleep(wait * 1000)
        continue
      }
      const text = await res.text()
      let body = null
      try {
        body = text ? JSON.parse(text) : null
      } catch {
        body = text
      }
      if (!res.ok && !opts.allow?.includes(res.status)) {
        throw new Error(
          `${method} ${p} -> ${res.status}: ${typeof body === "string" ? body : JSON.stringify(body)}`.slice(
            0,
            600,
          ),
        )
      }
      return { status: res.status, body }
    }
  }
  get = (p, o) => this.call("GET", p, undefined, o).then((r) => r.body)
  post = (p, j, o) => this.call("POST", p, j ?? {}, o).then((r) => r.body)
  patch = (p, j, o) => this.call("PATCH", p, j, o).then((r) => r.body)
  put = (p, j, o) => this.call("PUT", p, j, o).then((r) => r.body)
}

/** A public call from the customer's website: no session, the site's Origin. */
async function publicPost(p, json, extraHeaders = {}) {
  for (let attempt = 0; attempt < 4; attempt++) {
    const res = await fetch(base + p, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        Origin: SITE,
        Referer: SITE + "/",
        ...extraHeaders,
      },
      body: JSON.stringify(json),
    })
    if (res.status === 429) {
      const wait = Math.min(+(res.headers.get("retry-after") ?? 60), 900) + 2
      log(`429 on public ${p}: waiting ${wait}s (the per-address limit)`)
      await sleep(wait * 1000)
      continue
    }
    const text = await res.text()
    if (!res.ok) throw new Error(`public POST ${p} -> ${res.status}: ${text}`.slice(0, 400))
    return text ? JSON.parse(text) : {}
  }
  throw new Error(`public POST ${p}: still rate limited`)
}

// ---- sign in --------------------------------------------------------------------------------

function loadStore() {
  try {
    return existsSync(STORE) ? JSON.parse(readFileSync(STORE, "utf8")) : {}
  } catch {
    return {}
  }
}
const store = loadStore()
let password = process.env.DEMO_PASSWORD ?? store[base]?.password
let newPassword = false

const s = new Session()
await s.refreshCsrf()
let signedIn = false
if (password) {
  const r = await s.call(
    "POST",
    "/api/auth/login",
    { email: EMAIL, password },
    { allow: [401, 400, 422] },
  )
  signedIn = r.status < 300
  if (!signedIn) log(`login as ${EMAIL} failed (${r.status}); trying to register`)
}
if (!signedIn) {
  if (!password) {
    password = `Nile-${randomBytes(9).toString("base64url")}-${randomBytes(3).toString("hex")}!`
    newPassword = true
  }
  const r = await s.call(
    "POST",
    "/api/auth/register",
    { email: EMAIL, password },
    { allow: [400, 409, 422] },
  )
  if (r.status >= 300) {
    throw new Error(
      `${EMAIL} already exists and the password is unknown here. Set DEMO_PASSWORD. (${r.status} ${JSON.stringify(r.body)})`,
    )
  }
  log(`registered ${EMAIL}`)
  store[base] = { email: EMAIL, password }
  writeFileSync(STORE, JSON.stringify(store, null, 2))
}
await s.refreshCsrf() // a new session carries a new token
const me = await s.get("/api/auth/me")
log(`signed in as ${me.email} (user ${me.id})`)

// ---- workspace --------------------------------------------------------------------------------

let { workspaces } = await s.get("/api/me/workspaces")
let ws = workspaces.find((w) => w.name === WS_NAME)
if (!ws) {
  ws = (await s.post("/api/me/workspaces", { name: WS_NAME })).workspace
  log(`created workspace ${ws.slug}`)
} else log(`workspace exists: ${ws.slug}`)
if (ws.name !== WS_NAME) throw new Error("refusing to seed a workspace that is not the demo one")
s.ws = ws.id
const current = await s.get("/api/w/current")
const CUR = current.workspace.currency || "USD"
log(`workspace ${current.workspace.name} · role ${current.role} · currency ${CUR}`)

// Values are written in USD and converted to the workspace currency, rounded to a price a
// sales person would actually quote.
const RATE = { USD: 1, EGP: 48.5, SAR: 3.75, AED: 3.67, EUR: 0.92, GBP: 0.79 }
const money = (usd) => {
  const v = usd * (RATE[CUR] ?? 1)
  const step = v > 100000 ? 1000 : v > 10000 ? 500 : 50
  return Math.round(v / step) * step * 100
}

// ---- CRM: companies ----------------------------------------------------------------------------

const COMPANIES = [
  {
    key: "bnb",
    country: "Egypt",
    name: "Bean & Brew Cafés",
    domain: "beanandbrew.example",
    industry: "Cafés & coffee shops",
    size: "51-200",
    notes:
      "Nine cafés across Cairo (Zamalek, Maadi, New Cairo, Sheikh Zayed). Buys the house espresso blend weekly, pays net-30.",
  },
  {
    key: "nile",
    country: "Egypt",
    name: "فندق النيل ريزيدنس — Nile Residence Hotel",
    domain: "nileresidence.example",
    industry: "Hospitality",
    size: "201-1000",
    notes:
      "Five-star, Garden City, Cairo. Lobby lounge, all-day dining and banqueting. The coffee tender renews every January.",
  },
  {
    key: "corniche",
    country: "Egypt",
    name: "Corniche Grand Hotel",
    domain: "cornichegrand.example",
    industry: "Hospitality",
    size: "201-1000",
    notes:
      "Alexandria, Stanley Bay. Wants a single origin for the lobby lounge and a filter programme for breakfast.",
  },
  {
    key: "qahwa",
    country: "Saudi Arabia",
    name: "بيت القهوة — Qahwa House",
    domain: "qahwahouse.example",
    industry: "Cafés & coffee shops",
    size: "11-50",
    notes:
      "Specialty café group in Riyadh (Olaya, Al Malqa). Imports through Jeddah; needs SFDA paperwork on every shipment.",
  },
  {
    key: "marina",
    country: "United Arab Emirates",
    name: "Marina Workspace",
    domain: "marinaworkspace.example",
    industry: "Coworking & offices",
    size: "11-50",
    notes:
      "Coworking, Dubai Marina. Office-coffee subscription across 4 sites with bean-to-cup machines.",
  },
  {
    key: "delta",
    country: "Egypt",
    name: "Delta Tech Offices",
    domain: "deltatech.example",
    industry: "Corporate offices",
    size: "1000+",
    notes:
      "Smart Village, Giza. The facilities team runs six pantries; procurement buys through an annual RFQ.",
  },
  {
    key: "masa",
    country: "Egypt",
    name: "مخبز الماسة — Al Masa Bakery",
    domain: "almasa.example",
    industry: "Bakery & café",
    size: "51-200",
    notes:
      "Twelve branches in Alexandria. Turkish-coffee grind with cardamom plus an espresso blend for the new café corners.",
  },
  {
    key: "zbooks",
    country: "Egypt",
    name: "Zamalek Books & Coffee",
    domain: "zamalekbooks.example",
    industry: "Cafés & coffee shops",
    size: "1-10",
    notes:
      "Independent bookshop café on 26th of July St. Small but loyal — sells our retail bags on consignment.",
  },
  {
    key: "redsea",
    country: "Egypt",
    name: "Red Sea Resorts Group",
    domain: "redsearesorts.example",
    industry: "Hospitality",
    size: "1000+",
    notes: "Four resorts in Hurghada and El Gouna. High season October to April; volumes double.",
  },
  {
    key: "rose",
    country: "Saudi Arabia",
    name: "Desert Rose Catering",
    domain: "desertrose.example",
    industry: "Catering",
    size: "201-1000",
    notes: "Riyadh. Corporate and event catering — Saudi qahwa service and mobile espresso bars.",
  },
  {
    key: "bayleaf",
    country: "United Arab Emirates",
    name: "Bay Leaf Café",
    domain: "bayleaf.example",
    industry: "Cafés & coffee shops",
    size: "11-50",
    notes: "Jumeirah, Dubai. Brunch café switching roaster in Q4; tasted our Sidamo and Huila.",
  },
  {
    key: "maadi",
    country: "Egypt",
    name: "Maadi Coworking Hub",
    domain: "maadihub.example",
    industry: "Coworking & offices",
    size: "11-50",
    notes: "Road 9, Maadi. 180 members on a monthly office-coffee plan.",
  },
]

const created = {
  companies: 0,
  contacts: 0,
  subscriptions: 0,
  whatsappOptIns: 0,
  deals: 0,
  activities: 0,
}
const companyId = {}
{
  // Paged since MH-467; a demo workspace fits one page of 200.
  const { companies } = await s.get("/api/w/companies?limit=200")
  for (const c of COMPANIES) {
    const have = companies.find((x) => x.name === c.name || (c.domain && x.domain === c.domain))
    if (have) {
      companyId[c.key] = have.id
      // MH-395: companies made before countries existed get theirs (the profile's regions match it).
      if (!have.country && c.country)
        await s.patch(`/api/w/companies/${have.id}`, { country: c.country })
      continue
    }
    const { key, ...body } = c
    const r = await s.post("/api/w/companies", body)
    companyId[key] = r.company.id
    created.companies++
  }
  log(`companies: ${Object.keys(companyId).length} (${created.companies} new)`)
}

// ---- Marketing: lists -----------------------------------------------------------------------

const LISTS = [
  {
    key: "wholesale",
    name: "Wholesale buyers",
    description:
      "Cafés, hotels and offices buying by the kilo. Price lists and new-crop announcements.",
    double_opt_in: false,
  },
  {
    key: "club",
    name: "Coffee club",
    description:
      "Monthly subscription members and the newsletter — roast notes, brew guides, early access.",
    double_opt_in: true,
  },
  {
    key: "ar",
    name: "عملاء الجملة",
    description: "عملاء الجملة الناطقون بالعربية: المقاهي والفنادق والمكاتب في مصر والخليج.",
    double_opt_in: false,
  },
]
const listId = {}
{
  const { lists } = await s.get("/api/w/mail/lists")
  for (const l of LISTS) {
    const have = lists.find((x) => x.name === l.name)
    if (have) {
      listId[l.key] = have.id
      continue
    }
    const { key, ...body } = l
    listId[key] = (await s.post("/api/w/mail/lists", body)).list.id
  }
  log(`lists: ${Object.keys(listId).length}`)
}

// ---- CRM: contacts -------------------------------------------------------------------------

// [email local, name, locale, phone, title, company key, lists, whatsapp opt-in, tags]
const C = (email, name, locale, phone, title, co, lists, wa = false, tags = []) => ({
  email,
  name,
  locale,
  phone,
  title,
  co,
  lists,
  wa,
  tags,
})
const CONTACTS = [
  C(
    "hana.elsayed@beanandbrew.example",
    "Hana El-Sayed",
    "en",
    "+20 16 2748 3910",
    "Head of Purchasing",
    "bnb",
    ["wholesale"],
    true,
    ["decision-maker"],
  ),
  C(
    "karim.fouad@beanandbrew.example",
    "كريم فؤاد",
    "ar",
    "+20 17 0915 2266",
    "مدير العمليات",
    "bnb",
    ["ar"],
    true,
  ),
  C("nour.adel@beanandbrew.example", "Nour Adel", "en", "", "Head Barista, Zamalek", "bnb", [
    "club",
  ]),
  C(
    "m.shawky@nileresidence.example",
    "Mahmoud Shawky",
    "en",
    "+20 16 5520 1847",
    "F&B Director",
    "nile",
    ["wholesale"],
    true,
    ["decision-maker", "tender"],
  ),
  C(
    "r.gamal@nileresidence.example",
    "رانيا جمال",
    "ar",
    "+20 17 3381 9054",
    "مسؤولة المشتريات",
    "nile",
    ["ar"],
    true,
    ["tender"],
  ),
  C(
    "chef.tarek@nileresidence.example",
    "Tarek Mansour",
    "en",
    "",
    "Executive Pastry Chef",
    "nile",
    [],
  ),
  C(
    "s.hamdy@cornichegrand.example",
    "Salma Hamdy",
    "en",
    "+20 16 8812 4470",
    "Food & Beverage Manager",
    "corniche",
    ["wholesale"],
    true,
    ["decision-maker"],
  ),
  C("a.ragab@cornichegrand.example", "أحمد رجب", "ar", "", "مدير اللوبي لاونج", "corniche", ["ar"]),
  C(
    "f.alqahtani@qahwahouse.example",
    "فيصل القحطاني",
    "ar",
    "+966 51 284 7731",
    "المؤسس والمدير العام",
    "qahwa",
    ["ar"],
    true,
    ["decision-maker"],
  ),
  C(
    "reem@qahwahouse.example",
    "Reem Al-Harbi",
    "en",
    "+966 51 903 6628",
    "Head of Coffee",
    "qahwa",
    ["wholesale", "club"],
    true,
  ),
  C("ops@qahwahouse.example", "Omar Al-Dosari", "en", "", "Operations & Imports", "qahwa", []),
  C(
    "daniel.k@marinaworkspace.example",
    "Daniel Kovacs",
    "en",
    "+971 53 418 2290",
    "Community & Facilities Lead",
    "marina",
    ["wholesale"],
    true,
    ["decision-maker"],
  ),
  C("layla.m@marinaworkspace.example", "Layla Mansoori", "en", "", "Office Manager", "marina", [
    "club",
  ]),
  C(
    "procurement@deltatech.example",
    "Sherif Abdelaziz",
    "en",
    "+20 16 1197 6603",
    "Procurement Specialist",
    "delta",
    ["wholesale"],
    false,
    ["rfq"],
  ),
  C(
    "y.nabil@deltatech.example",
    "ياسمين نبيل",
    "ar",
    "+20 17 6620 3318",
    "مديرة المرافق",
    "delta",
    ["ar"],
    true,
    ["decision-maker"],
  ),
  C("h.farouk@deltatech.example", "Hossam Farouk", "en", "", "Workplace Experience", "delta", []),
  C(
    "i.saleh@almasa.example",
    "إبراهيم صالح",
    "ar",
    "+20 16 4033 7815",
    "صاحب المخابز",
    "masa",
    ["ar"],
    true,
    ["decision-maker"],
  ),
  C(
    "mona.saleh@almasa.example",
    "Mona Saleh",
    "en",
    "+20 17 2254 9087",
    "Café Corners Manager",
    "masa",
    ["wholesale"],
    false,
  ),
  C(
    "youssef@zamalekbooks.example",
    "Youssef Badr",
    "en",
    "+20 16 7309 1142",
    "Owner",
    "zbooks",
    ["wholesale", "club"],
    true,
    ["consignment"],
  ),
  C("dina@zamalekbooks.example", "دينا مراد", "ar", "", "مسؤولة المقهى", "zbooks", ["club"]),
  C(
    "gm.hurghada@redsearesorts.example",
    "Khaled Ezzat",
    "en",
    "+20 16 9021 5536",
    "Group F&B Director",
    "redsea",
    ["wholesale"],
    true,
    ["decision-maker"],
  ),
  C(
    "purchasing@redsearesorts.example",
    "مروة عبد الحميد",
    "ar",
    "+20 17 5580 2649",
    "رئيسة المشتريات",
    "redsea",
    ["ar"],
    false,
  ),
  C(
    "elgouna.fb@redsearesorts.example",
    "Marco Bianchi",
    "en",
    "",
    "Outlet Manager, El Gouna",
    "redsea",
    [],
  ),
  C(
    "n.alotaibi@desertrose.example",
    "نورة العتيبي",
    "ar",
    "+966 51 776 1405",
    "مديرة الفعاليات",
    "rose",
    ["ar"],
    true,
  ),
  C(
    "sami@desertrose.example",
    "Sami Haddad",
    "en",
    "+966 51 330 8912",
    "Head of Procurement",
    "rose",
    ["wholesale"],
    false,
    ["decision-maker"],
  ),
  C(
    "chloe@bayleaf.example",
    "Chloe Anderson",
    "en",
    "+971 53 902 7714",
    "Co-founder",
    "bayleaf",
    ["wholesale", "club"],
    true,
    ["decision-maker"],
  ),
  C("rashid@bayleaf.example", "Rashid Al Suwaidi", "en", "", "Co-founder", "bayleaf", []),
  C(
    "amr@maadihub.example",
    "عمرو الشريف",
    "ar",
    "+20 16 3346 8820",
    "المدير العام",
    "maadi",
    ["ar"],
    true,
    ["decision-maker"],
  ),
  C("community@maadihub.example", "Farida Lotfy", "en", "", "Community Manager", "maadi", ["club"]),
  C("heba.kamal@nileresidence.example", "Heba Kamal", "en", "", "Banqueting Manager", "nile", []),
  C(
    "w.aziz@cornichegrand.example",
    "Waleed Aziz",
    "en",
    "+20 17 4417 0932",
    "Purchasing Manager",
    "corniche",
    ["wholesale"],
    false,
  ),
  C("t.alshehri@qahwahouse.example", "تركي الشهري", "ar", "", "مدير فرع الملقا", "qahwa", ["ar"]),
  C("finance@beanandbrew.example", "Ahmed Zaki", "en", "", "Finance Manager", "bnb", []),
  C("s.nasser@marinaworkspace.example", "Sara Nasser", "en", "", "Finance & Admin", "marina", []),
  C("ziad.m@deltatech.example", "Ziad Mostafa", "en", "", "IT Campus Pantry Lead", "delta", [
    "club",
  ]),
  C("lina.hosny@club.example", "Lina Hosny", "en", "+20 16 8195 2273", "Home barista", null, [
    "club",
  ]),
  C("omar.tawfik@club.example", "عمر توفيق", "ar", "", "مشترك شهري", null, ["club"]),
  C(
    "maya.rizk@club.example",
    "Maya Rizk",
    "en",
    "",
    "Food blogger — @mayabrews",
    null,
    ["club"],
    false,
    ["press"],
  ),
  C("hassan.eid@club.example", "Hassan Eid", "en", "", "Subscriber since 2024", null, ["club"]),
  C("nadine.f@club.example", "نادين فهمي", "ar", "", "مشتركة شهرية", null, ["club"]),
]

const contactId = {} // by email
{
  const { contacts } = await s.get("/api/w/contacts")
  const byEmail = Object.fromEntries(contacts.map((c) => [c.email, c]))
  for (const c of CONTACTS) {
    let row = byEmail[c.email]
    const fresh = !row
    // First list on create (as the UI's "add contact" does), the rest by re-adding.
    const subscribedTo = new Set((row?.subscriptions ?? []).map((x) => x.list_id))
    const lists = c.lists.map((k) => listId[k])
    if (!row) {
      const r = await s.post("/api/w/contacts", {
        email: c.email,
        name: c.name,
        locale: c.locale,
        ...(lists[0] ? { list_id: lists[0] } : {}),
      })
      row = r.contact
      if (lists[0]) {
        subscribedTo.add(lists[0])
        created.subscriptions++
      }
      created.contacts++
    }
    for (const l of lists) {
      if (subscribedTo.has(l)) continue
      await s.post("/api/w/contacts", {
        email: c.email,
        name: c.name,
        locale: c.locale,
        list_id: l,
      })
      created.subscriptions++
    }
    contactId[c.email] = row.id
    if (
      fresh ||
      row.phone !== c.phone ||
      row.title !== c.title ||
      (c.co && row.company_id !== companyId[c.co])
    ) {
      await s.patch(`/api/w/contacts/${row.id}`, {
        phone: c.phone,
        title: c.title,
        ...(c.co ? { company_id: companyId[c.co] } : {}),
        ...(c.tags.length ? { tags: c.tags } : {}),
      })
    }
    if (c.wa && !row.whatsapp_opt_in_at) {
      await s.post(`/api/w/contacts/${row.id}/whatsapp/opt-in`, {}, { allow: [409] })
      created.whatsappOptIns++
    }
  }
  log(
    `contacts: ${Object.keys(contactId).length} (${created.contacts} new, ${created.subscriptions} subscriptions, ${created.whatsappOptIns} WhatsApp opt-ins)`,
  )
  skipped.push([
    "A contact who said STOP on WhatsApp",
    "No API route sets an opt-out: it is only recorded from an inbound WhatsApp 'STOP' (marketing/whatsapp_broadcast.go), which needs a linked WhatsApp number.",
  ])
}

// ---- CRM: deals + activities ---------------------------------------------------------------

const board = await s.get("/api/w/deals")
const stages = [...board.stages].sort((a, b) => a.position - b.position)
const open = stages.filter((x) => x.kind === "open")
const stage = {
  lead: open[0]?.id,
  qualified: (open[1] ?? open[0])?.id,
  proposal: (open[2] ?? open.at(-1))?.id,
  won: stages.find((x) => x.kind === "won")?.id,
  lost: stages.find((x) => x.kind === "lost")?.id,
}
const inDays = (d) => new Date(Date.now() + d * 86400000).toISOString().slice(0, 10)
// [title, usd, stage, company, contact email, expected close in days]
const DEALS = [
  [
    "Bean & Brew — house espresso blend, 1.2 t / year",
    38400,
    "won",
    "bnb",
    "hana.elsayed@beanandbrew.example",
    -20,
  ],
  [
    "Nile Residence — 2027 coffee tender (lounge + banqueting)",
    52000,
    "proposal",
    "nile",
    "m.shawky@nileresidence.example",
    45,
  ],
  [
    "Corniche Grand — lobby single origin (Ethiopia Sidamo)",
    9600,
    "qualified",
    "corniche",
    "s.hamdy@cornichegrand.example",
    30,
  ],
  [
    "Qahwa House — Riyadh wholesale, first container",
    27500,
    "proposal",
    "qahwa",
    "f.alqahtani@qahwahouse.example",
    60,
  ],
  [
    "Marina Workspace — office coffee, 4 sites",
    14200,
    "qualified",
    "marina",
    "daniel.k@marinaworkspace.example",
    21,
  ],
  [
    "Delta Tech — annual pantry RFQ 2027",
    31000,
    "lead",
    "delta",
    "procurement@deltatech.example",
    90,
  ],
  ["مخبز الماسة — بن تركي محوج لـ ١٢ فرع", 18700, "won", "masa", "i.saleh@almasa.example", -45],
  [
    "Zamalek Books — retail bags on consignment",
    2400,
    "won",
    "zbooks",
    "youssef@zamalekbooks.example",
    -10,
  ],
  [
    "Red Sea Resorts — high season supply Oct–Apr",
    44800,
    "proposal",
    "redsea",
    "gm.hurghada@redsearesorts.example",
    14,
  ],
  [
    "Desert Rose — Saudi qahwa blend for events",
    12300,
    "lead",
    "rose",
    "sami@desertrose.example",
    75,
  ],
  [
    "Bay Leaf — switch roaster, brunch espresso",
    7800,
    "qualified",
    "bayleaf",
    "chloe@bayleaf.example",
    28,
  ],
  [
    "Maadi Hub — monthly office plan, 180 members",
    5200,
    "won",
    "maadi",
    "amr@maadihub.example",
    -5,
  ],
  [
    "Corniche Grand — breakfast filter programme",
    6100,
    "lost",
    "corniche",
    "w.aziz@cornichegrand.example",
    -30,
  ],
  [
    "Delta Tech — barista training for pantry staff",
    3500,
    "lead",
    "delta",
    "y.nabil@deltatech.example",
    40,
  ],
  [
    "Desert Rose — mobile espresso bar rental, Riyadh Season",
    9900,
    "lost",
    "rose",
    "n.alotaibi@desertrose.example",
    -15,
  ],
]
const dealId = {}
const newDeals = new Set()
// The stages a deal walks through to reach where it ends (MH-394): the board records every move,
// so the Reports funnel sees how far each deal got — including the ones that were lost.
const WALK = {
  lead: [],
  qualified: ["qualified"],
  proposal: ["qualified", "proposal"],
  won: ["qualified", "proposal", "won"],
  lost: ["qualified", "lost"],
  lostLate: ["qualified", "proposal", "lost"],
}
const closedDay = (iso) => (iso ? iso.slice(0, 10) : "")
/** Open a deal in the first stage and move it, stage by stage, to where it ends. */
async function openAndWalk(body, walk, closedOn) {
  const r = await s.post("/api/w/deals", { ...body, stage_id: stage.lead })
  const steps = WALK[walk] ?? []
  for (const [i, st] of steps.entries()) {
    const last = i === steps.length - 1
    const target = st
    await s.patch(`/api/w/deals/${r.deal.id}`, {
      stage_id: stage[target],
      ...(last && closedOn && (target === "won" || target === "lost")
        ? { closed_on: closedOn }
        : {}),
    })
  }
  return r.deal.id
}
{
  const have = Object.fromEntries(board.deals.map((d) => [d.title, d]))
  let redated = 0
  for (const [title, usd, st, co, email, days] of DEALS) {
    const closing = st === "won" || st === "lost"
    const h = have[title]
    if (h) {
      dealId[title] = h.id
      // A won/lost deal made before close dates existed closed the moment it was created: give it
      // the day it really closed (its expected close), once — a deal already re-dated is left alone.
      if (
        closing &&
        h.closed_at &&
        closedDay(h.closed_at) === closedDay(h.created_at) &&
        days < 0
      ) {
        await s.patch(`/api/w/deals/${h.id}`, { closed_on: inDays(days) })
        redated++
      }
      continue
    }
    dealId[title] = await openAndWalk(
      {
        title,
        value_minor: money(usd),
        company_id: companyId[co],
        contact_id: contactId[email],
        expected_close: inDays(days),
      },
      st,
      closing ? inDays(Math.min(days, 0)) : "",
    )
    newDeals.add(title)
    created.deals++
  }
  log(
    `deals: ${Object.keys(dealId).length} (${created.deals} new, ${redated} given their close day) in ${CUR}`,
  )
}

// ---- CRM: the year behind (MH-394) ---------------------------------------------------------
// Deals won and lost over the last eleven months, so Reports has a history to draw: wins and
// losses per month, a win rate, an average deal. Each walks the stages it went through and closes
// on the day it closed. Re-runnable: skipped by title.
// [title, usd, walk, company, contact email, closed N days ago]
const HISTORY = [
  [
    "Bean & Brew — Ramadan iftar blend, 3 branches",
    6200,
    "won",
    "bnb",
    "hana.elsayed@beanandbrew.example",
    330,
  ],
  ["Qahwa House — trial shipment, 120 kg", 4800, "won", "qahwa", "reem@qahwahouse.example", 300],
  [
    "Red Sea Resorts — summer season top-up",
    16500,
    "lost",
    "redsea",
    "purchasing@redsearesorts.example",
    285,
  ],
  [
    "Nile Residence — 2026 banqueting coffee",
    21000,
    "won",
    "nile",
    "m.shawky@nileresidence.example",
    260,
  ],
  [
    "Marina Workspace — pilot at JLT",
    2900,
    "won",
    "marina",
    "daniel.k@marinaworkspace.example",
    230,
  ],
  [
    "Delta Tech — pantry RFQ 2026",
    27000,
    "lostLate",
    "delta",
    "procurement@deltatech.example",
    205,
  ],
  ["مخبز الماسة — خلطة إسبريسو لركن القهوة", 5400, "won", "masa", "mona.saleh@almasa.example", 180],
  ["Desert Rose — Founding Day event bar", 7300, "lost", "rose", "sami@desertrose.example", 160],
  ["Bay Leaf — guest espresso, one month", 1800, "won", "bayleaf", "chloe@bayleaf.example", 140],
  [
    "Corniche Grand — lobby lounge trial",
    3600,
    "lostLate",
    "corniche",
    "s.hamdy@cornichegrand.example",
    115,
  ],
  ["Maadi Hub — coffee for the members' summit", 1500, "won", "maadi", "amr@maadihub.example", 95],
  ["Zamalek Books — holiday gift boxes", 2100, "won", "zbooks", "youssef@zamalekbooks.example", 70],
  [
    "Qahwa House — Al Malqa branch opening",
    9800,
    "won",
    "qahwa",
    "f.alqahtani@qahwahouse.example",
    52,
  ],
  [
    "Red Sea Resorts — El Gouna outlet espresso",
    8700,
    "lost",
    "redsea",
    "elgouna.fb@redsearesorts.example",
    38,
  ],
  [
    "Bean & Brew — New Cairo branch launch",
    7600,
    "won",
    "bnb",
    "karim.fouad@beanandbrew.example",
    24,
  ],
  [
    "Delta Tech — town-hall coffee service",
    2200,
    "lost",
    "delta",
    "h.farouk@deltatech.example",
    12,
  ],
]
{
  const have = new Set(board.deals.map((d) => d.title))
  let n = 0
  for (const [title, usd, walk, co, email, ago] of HISTORY) {
    if (have.has(title)) continue
    await openAndWalk(
      {
        title,
        value_minor: money(usd),
        company_id: companyId[co],
        contact_id: contactId[email],
        expected_close: inDays(-ago),
      },
      walk,
      inDays(-ago),
    )
    n++
  }
  log(`history: ${n} closed deals over the last eleven months`)
}

// Activities only on records made in THIS run, so a re-run does not double the timeline.
const ACTIVITIES = [
  [
    "deal",
    "Nile Residence — 2027 coffee tender (lounge + banqueting)",
    "meeting",
    "Cupping with Mahmoud and the pastry chef at the hotel. They preferred the Huila for the lounge and want the house blend for banqueting. Asked for a two-price proposal (with and without machine maintenance).",
  ],
  [
    "deal",
    "Nile Residence — 2027 coffee tender (lounge + banqueting)",
    "task",
    "Send the two-price proposal and the SFDA-style spec sheets to Mahmoud",
    3,
  ],
  [
    "deal",
    "Qahwa House — Riyadh wholesale, first container",
    "call",
    "مكالمة مع فيصل: يريدون ٦٠٠ كجم في الشحنة الأولى، والتسليم عن طريق جدة. طلب شهادة المنشأ وشهادة التحليل قبل التأكيد.",
  ],
  [
    "deal",
    "Qahwa House — Riyadh wholesale, first container",
    "note",
    "Freight quote from the forwarder: 20ft reefer not needed; dry container fine for roasted beans in valve bags. Lead time 18 days door to door.",
  ],
  [
    "deal",
    "Red Sea Resorts — high season supply Oct–Apr",
    "meeting",
    "Visited the Hurghada resort. 4 outlets, 3 La Marzocco machines, 1 needs a new group gasket. They want monthly deliveries of 350 kg with a price lock until April.",
  ],
  [
    "deal",
    "Red Sea Resorts — high season supply Oct–Apr",
    "task",
    "Confirm the price lock with finance before Thursday",
    2,
  ],
  [
    "deal",
    "Marina Workspace — office coffee, 4 sites",
    "call",
    "Daniel confirmed the bean-to-cup machines are Jura; medium roast, 18 kg per site per month. Trial at the JLT site first.",
  ],
  [
    "deal",
    "Corniche Grand — breakfast filter programme",
    "note",
    "Lost to a local roaster on price (their offer was 11% below ours). Keep them for the lobby single-origin deal.",
  ],
  [
    "deal",
    "Bay Leaf — switch roaster, brunch espresso",
    "meeting",
    "Tasting at Bay Leaf: Chloe liked the Sidamo as a guest espresso; Rashid wants a chocolatey house blend. Sending 2 kg samples of each.",
  ],
  [
    "deal",
    "Delta Tech — annual pantry RFQ 2027",
    "task",
    "Register on the Delta supplier portal and download the RFQ pack",
    7,
  ],
  [
    "company",
    "bnb",
    "note",
    "Bean & Brew pays on time (net-30). Opening two more branches in New Cairo in November — volume should rise ~20%.",
  ],
  [
    "company",
    "masa",
    "call",
    "اتصال مع إبراهيم: راضي عن الطحنة التركي، ويريد تجربة إضافة الحبهان بنسبة ٥٪ في فروع العجمي.",
  ],
  [
    "company",
    "delta",
    "meeting",
    "Intro meeting with facilities and procurement. Six pantries, ~2,400 staff on campus. They score suppliers 60% price, 40% service.",
  ],
  [
    "contact",
    "reem@qahwahouse.example",
    "note",
    "Reem is a licensed Q grader — send green samples, not just roasted.",
  ],
  [
    "contact",
    "chloe@bayleaf.example",
    "call",
    "Chloe asked about oat-milk pairing for the new flat white; sending the Huila tasting notes.",
  ],
]
{
  const newCompanies = created.companies > 0
  for (const [kind, ref, akind, body, dueDays] of ACTIVITIES) {
    const payload = { kind: akind, body, ...(dueDays ? { due_at: inDays(dueDays) } : {}) }
    if (kind === "deal") {
      if (!newDeals.has(ref)) continue
      payload.deal_id = dealId[ref]
    }
    if (kind === "company") {
      if (!newCompanies) continue
      payload.company_id = companyId[ref]
    }
    if (kind === "contact") {
      if (created.contacts === 0) continue
      payload.contact_id = contactId[ref]
    }
    await s.post("/api/w/activities", payload)
    created.activities++
  }
  log(`activities: ${created.activities} new`)
}

// ---- CRM: the ideal-customer profile (MH-395) ----------------------------------------------
// Who Nile Coffee sells to: hotels, cafés and caterers of 50+ people in Egypt and Saudi Arabia,
// reached through purchasing. Set once; a profile the workspace has already saved is left alone.
{
  const cur = await s.get("/api/w/icp")
  if (!cur.configured) {
    const out = await s.put("/api/w/icp", {
      industries: {
        values: ["Hospitality", "Cafés & coffee shops", "Catering", "Bakery & café"],
        weight: 3,
      },
      sizes: { values: ["51-200", "201-1000", "1000+"], weight: 2 },
      regions: { values: ["Egypt", "Saudi Arabia"], weight: 2 },
      keywords: {
        values: ["purchasing", "procurement", "F&B", "decision-maker", "owner", "المشتريات"],
        weight: 2,
      },
      signals: {
        open_deal: 3,
        won_deal: 3,
        lost_deal: -1,
        inbound_lead: 2,
        conversation: 1,
        subscribed: 1,
        whatsapp_opt_in: 1,
        recent_activity: 2,
      },
    })
    log(`ideal-customer profile: saved, ${out.scored} of ${out.contacts} contacts scored`)
  } else log("ideal-customer profile: already set, left as it is")
}

// ---- Marketing: campaigns ------------------------------------------------------------------

const CAMPAIGNS = [
  {
    list: "wholesale",
    channel: "email",
    locale: "en",
    subject: "New crop: Ethiopia Sidamo and Colombia Huila are in the roastery",
    body: `Hello,

The first containers of the **2026/27 crop** landed in Alexandria last week, and both coffees are now on our wholesale list.

## What's new

- **Ethiopia Sidamo, washed** — jasmine, bergamot and a honey finish. Lovely as a guest espresso or on filter.
- **Colombia Huila, washed** — milk chocolate, red apple, a round body that holds up in flat whites.

## Wholesale prices, per kilo

- Sidamo — from 1,150 EGP (10 kg+)
- Huila — from 1,040 EGP (10 kg+)
- House espresso blend — unchanged at 890 EGP

Orders placed before **the 15th** roast on Monday and reach Cairo and Alexandria in 48 hours. Hotels on a monthly plan get the new crop at the old price for the first delivery.

Reply to this email for samples, or book a cupping at the roastery in 6th of October.

Nile Coffee Co.`,
  },
  {
    list: "wholesale",
    channel: "whatsapp",
    locale: "en",
    subject: "WhatsApp — reorder reminder, October",
    body: `Hi from Nile Coffee ☕
Your October reorder window is open until Thursday. Reply *1* to repeat last month's order, *2* for this month's new-crop Sidamo samples, or tell us what you need and we'll roast it Monday.`,
  },
  {
    list: "ar",
    channel: "email",
    locale: "ar",
    subject: "محصول جديد وأسعار الجملة لشهر أكتوبر",
    body: `أهلاً بك،

وصلت أولى شحنات **محصول ٢٠٢٦/٢٧** إلى الإسكندرية الأسبوع الماضي، وأصبح البن الإثيوبي سيدامو والكولومبي هويلا متاحين الآن لعملاء الجملة.

## الجديد هذا الشهر

- **إثيوبيا سيدامو (مغسول):** نكهات الياسمين والبرغموت مع لمسة عسل.
- **كولومبيا هويلا (مغسول):** شوكولاتة بالحليب وتفاح أحمر، قوام متوازن مع الحليب.
- **البن التركي المحوّج:** طحنة ناعمة مع حبهان بنسبة ٥٪، مناسب للمخابز والمقاهي.

## أسعار الجملة للكيلو

- سيدامو — من ١١٥٠ جنيهًا (١٠ كجم فأكثر)
- هويلا — من ١٠٤٠ جنيهًا (١٠ كجم فأكثر)
- خلطة الإسبريسو — بدون تغيير ٨٩٠ جنيهًا

الطلبات قبل يوم **١٥** تُحمَّص يوم الإثنين وتصل القاهرة والإسكندرية خلال ٤٨ ساعة. للعينات أو لحجز جلسة تذوق في المحمصة بمدينة ٦ أكتوبر، رُد على هذه الرسالة.

شركة قهوة النيل`,
  },
]
{
  const { campaigns } = await s.get("/api/w/campaigns?limit=200")
  let n = 0
  for (const c of CAMPAIGNS) {
    if (campaigns.some((x) => x.subject === c.subject)) continue
    await s.post("/api/w/campaigns", {
      list_id: listId[c.list],
      channel: c.channel,
      subject: c.subject,
      body: c.body,
      locale: c.locale,
    })
    n++
  }
  log(`campaigns: ${n} new drafts`)
}

// ---- Website forms + public submissions -------------------------------------------------------

let subscribeForm, inquiryForm
{
  const { forms } = await s.get("/api/w/subscribe-forms")
  subscribeForm = forms.find((f) => f.name === "Newsletter — nilecoffee.example")
  if (!subscribeForm)
    subscribeForm = (
      await s.post("/api/w/subscribe-forms", {
        kind: "subscribe",
        list_id: listId.club,
        name: "Newsletter — nilecoffee.example",
        allowed_origins: [SITE],
        thanks_en: "Welcome to the Coffee club! Check your inbox to confirm.",
        thanks_ar: "أهلاً بك في نادي القهوة! راجع بريدك لتأكيد الاشتراك.",
      })
    ).form
  inquiryForm = forms.find((f) => f.name === "Wholesale inquiry — nilecoffee.example")
  if (!inquiryForm)
    inquiryForm = (
      await s.post("/api/w/subscribe-forms", {
        kind: "inquiry",
        name: "Wholesale inquiry — nilecoffee.example",
        allowed_origins: [SITE],
        thanks_en: "Thanks — our wholesale team replies within one working day.",
        thanks_ar: "شكرًا لك — يرد فريق الجملة خلال يوم عمل واحد.",
      })
    ).form
  log(
    `forms: subscribe ${subscribeForm.public_key.slice(0, 8)}…, inquiry ${inquiryForm.public_key.slice(0, 8)}…`,
  )
}

const SIGNUPS = [
  {
    email: "aya.samir@mail.example",
    name: "Aya Samir",
    locale: "en",
    phone: "+20 16 5402 8813",
    whatsapp_opt_in: true,
  },
  { email: "m.elkady@mail.example", name: "محمد القاضي", locale: "ar" },
  { email: "jana.w@mail.example", name: "Jana Wagdy", locale: "en" },
  {
    email: "abdullah.r@mail.example",
    name: "عبدالله الراشد",
    locale: "ar",
    phone: "+966 51 447 2096",
    whatsapp_opt_in: true,
  },
  { email: "sophie.l@mail.example", name: "Sophie Laurent", locale: "en" },
  {
    email: "k.nassar@mail.example",
    name: "Kareem Nassar",
    locale: "en",
    phone: "+20 17 8830 5561",
    whatsapp_opt_in: true,
  },
]
{
  const { contacts } = await s.get("/api/w/contacts")
  const known = new Set(contacts.map((c) => c.email))
  let n = 0
  for (const b of SIGNUPS) {
    if (known.has(b.email)) continue
    await publicPost(`/api/subscribe/${subscribeForm.public_key}`, b)
    n++
    await sleep(1500)
  }
  log(`newsletter signups: ${n} new`)
}

const INQUIRIES = [
  {
    name: "Mostafa Hegazy",
    email: "mostafa@cairobites.example",
    phone: "+20 16 2219 4405",
    company: "Cairo Bites",
    locale: "en",
    message:
      "We are opening 3 cafés in New Cairo in January and need an espresso blend supplier, roughly 150 kg a month. Can you share wholesale prices and whether you lend grinders?",
    utm_source: "google",
    utm_medium: "cpc",
    utm_campaign: "wholesale-coffee-cairo",
    utm_term: "coffee beans wholesale cairo",
    gclid: "Cj0KCQjw8e-gBhD0ARIsAJiDsaV3nq",
    landing_page: `${SITE}/wholesale`,
    referrer: "https://www.google.com/",
  },
  {
    name: "سارة المطيري",
    email: "sara@alnakheel-hotel.example",
    phone: "+966 51 662 3840",
    company: "فندق النخيل",
    locale: "ar",
    message:
      "نبحث عن مورد قهوة مختصة لفندقنا في الرياض (٢٢٠ غرفة). نحتاج خلطة إسبريسو وقهوة سعودية للضيافة، مع شهادات هيئة الغذاء والدواء. هل يمكن إرسال عينات؟",
    utm_source: "linkedin",
    utm_medium: "paid_social",
    utm_campaign: "ksa-hotels-q4",
    utm_content: "carousel-cupping",
    landing_page: `${SITE}/ar/hotels`,
    referrer: "https://www.linkedin.com/",
  },
  {
    name: "Olivia Grant",
    email: "olivia.grant@deskhive.example",
    company: "DeskHive Coworking",
    locale: "en",
    message:
      "Saw your roastery tour reel. We run two coworking spaces in Dubai (JLT and Business Bay) and want a monthly office-coffee plan for ~250 members. Do you deliver to the UAE?",
    utm_source: "instagram",
    utm_medium: "social",
    utm_campaign: "roastery-tour-reel",
    landing_page: `${SITE}/offices`,
    referrer: "https://l.instagram.com/",
  },
  {
    name: "Tamer Lotfy",
    email: "tamer@tamerlotfy-events.example",
    phone: "+20 17 9914 2270",
    company: "Lotfy Events",
    locale: "en",
    message:
      "Your October newsletter mentioned the new Sidamo. I cater weddings on the North Coast — could we do a pour-over bar for 300 guests on 14 November? Need beans + 2 baristas.",
    utm_source: "newsletter",
    utm_medium: "email",
    utm_campaign: "october-new-crop",
    landing_page: `${SITE}/events`,
  },
  {
    name: "هالة إسماعيل",
    email: "hala@beitelsham.example",
    company: "بيت الشام للحلويات",
    locale: "ar",
    message:
      "عندنا ٥ فروع حلويات شامية في الإسكندرية ونحتاج بن تركي محوج بكميات أسبوعية، حوالي ٤٠ كجم في الأسبوع. ما هي الأسعار وشروط الدفع؟",
    landing_page: `${SITE}/ar/wholesale`,
  },
  {
    name: "James Whitfield",
    email: "j.whitfield@gulfholdings.example",
    company: "Gulf Holdings",
    locale: "en",
    message:
      "Procurement enquiry: we need a supplier for 11 office pantries in Riyadh and Jeddah from Q1 2027. Please send a company profile, food-safety certificates and a price list per kg.",
    utm_source: "google",
    utm_medium: "cpc",
    utm_campaign: "office-coffee-ksa",
    utm_term: "office coffee supplier riyadh",
    gclid: "EAIaIQobChMI0p2qk7uLhgMVl5KDBx",
    landing_page: `${SITE}/offices`,
    referrer: "https://www.google.com/",
  },
  {
    name: "Rana Khalil",
    email: "rana@beanandbrew.example",
    company: "Bean & Brew Cafés",
    locale: "en",
    message:
      "Hi, it's Rana from Bean & Brew's new Sheikh Zayed branch — can we get our own delivery slot on Wednesdays instead of sharing the Zamalek one?",
    utm_source: "linkedin",
    utm_medium: "paid_social",
    utm_campaign: "wholesale-retarget",
    landing_page: `${SITE}/contact`,
    referrer: "https://www.linkedin.com/",
  },
  {
    name: "Crypto Growth Team",
    email: "growth@seo-rank-fast.example",
    locale: "en",
    message:
      "We will get your coffee website to #1 on Google in 7 days guaranteed!!! 500 backlinks for $49. Reply now for a free audit.",
    landing_page: `${SITE}/`,
  },
]
{
  const { leads } = await s.get("/api/w/leads")
  const known = new Set(leads.map((l) => l.email))
  let n = 0
  for (const b of INQUIRIES) {
    if (known.has(b.email)) continue
    await publicPost(`/api/inquiry/${inquiryForm.public_key}`, b)
    n++
    log(`inquiry ${n}: ${b.email}`)
    await sleep(2000)
  }
  log(`inquiries: ${n} new`)
}

// Work the leads the way a person would: convert two, one spam, the rest triaged.
{
  const { leads } = await s.get("/api/w/leads")
  const by = Object.fromEntries(leads.map((l) => [l.email, l]))
  const act = async (email, fn) => {
    const l = by[email]
    if (l) await fn(l)
  }
  for (const email of ["mostafa@cairobites.example", "sara@alnakheel-hotel.example"]) {
    await act(email, async (l) => {
      if (l.status !== "converted") await s.post(`/api/w/leads/${l.id}/convert`, {})
    })
  }
  // The Google Ads lead from Cairo Bites became a customer (MH-394: Reports credits the won deal
  // to the lead's source). Walked through the stages once; a deal already past Lead is left alone.
  {
    const cb = (await s.get("/api/w/leads")).leads.find(
      (l) => l.email === "mostafa@cairobites.example",
    )
    const dealRef = cb?.converted_deal
    if (dealRef) {
      const { deal } = await s.get(`/api/w/deals/${dealRef.id}`)
      if (deal.stage_id === stage.lead) {
        await s.patch(`/api/w/deals/${deal.id}`, {
          title: "Cairo Bites — espresso blend for 3 New Cairo cafés",
          value_minor: money(5400),
        })
        for (const st of ["qualified", "proposal"])
          await s.patch(`/api/w/deals/${deal.id}`, { stage_id: stage[st] })
        await s.patch(`/api/w/deals/${deal.id}`, { stage_id: stage.won, closed_on: inDays(-1) })
        log("leads: the Cairo Bites deal is won")
      }
    }
  }
  await act("growth@seo-rank-fast.example", async (l) => {
    if (l.status !== "spam") await s.patch(`/api/w/leads/${l.id}`, { status: "spam" })
  })
  await act("olivia.grant@deskhive.example", async (l) => {
    if (l.status === "new") await s.patch(`/api/w/leads/${l.id}`, { status: "contacted" })
  })
  await act("j.whitfield@gulfholdings.example", async (l) => {
    if (l.status === "new") await s.patch(`/api/w/leads/${l.id}`, { status: "qualified" })
  })
  log("leads: 2 converted, 1 spam, 1 contacted, 1 qualified")
}

// ---- Inbox: chat widget + visitor conversations ------------------------------------------------

{
  const { widgets } = await s.get("/api/w/chat_widgets")
  let widget = widgets.find((w) => w.name === "nilecoffee.example — live chat")
  if (!widget)
    widget = (
      await s.post("/api/w/chat_widgets", {
        name: "nilecoffee.example — live chat",
        allowed_origins: [SITE],
        greeting:
          "Hi! Ask us about wholesale, subscriptions or your order. · أهلاً! اسألنا عن الجملة أو الاشتراك أو طلبك.",
      })
    ).widget

  const CHATS = [
    {
      name: "Nadia Fawzy",
      email: "nadia.fawzy@mail.example",
      locale: "en",
      page: "/shop/ethiopia-sidamo",
      msgs: [
        "Hi, is the Sidamo good for a moka pot or is it too light?",
        "Also do you grind it for me if I order online?",
      ],
      reply:
        'Hi Nadia! The Sidamo works nicely in a moka pot — use a slightly finer grind than filter. And yes: pick "Moka pot" under Grind on the product page and we grind it the morning it ships.',
    },
    {
      name: "أحمد سعيد",
      email: "ahmed.saeed@mail.example",
      locale: "ar",
      page: "/ar/wholesale",
      msgs: [
        "السلام عليكم، عندي كافيه صغير في المنصورة، هل توصلون خارج القاهرة والإسكندرية؟",
        "الكمية حوالي ٢٠ كيلو في الشهر",
      ],
      reply:
        "وعليكم السلام أستاذ أحمد، نعم نوصل للمنصورة عن طريق شركة الشحن خلال ٣ أيام عمل. لطلب ٢٠ كجم شهريًا يبدأ سعر خلطة الإسبريسو من ٨٩٠ جنيهًا للكيلو. هل أرسل لك عينة ٢٥٠ جرام؟",
    },
    {
      name: "Ben Carter",
      email: "ben.carter@mail.example",
      locale: "en",
      page: "/subscriptions",
      msgs: [
        "My October coffee club box still says 'processing' — order NC-20417. Has it shipped?",
      ],
    },
    {
      name: "Mariam Adel",
      email: "mariam.adel@mail.example",
      locale: "en",
      page: "/offices",
      msgs: [
        "We're a 40-person office in Maadi. Do you rent machines or only sell beans?",
        "Ideally something like a bean-to-cup that the team can use without training.",
      ],
    },
    { name: "", email: "", locale: "ar", page: "/ar/", msgs: ["هل عندكم قهوة منزوعة الكافيين؟"] },
  ]
  const { conversations } = await s.get("/api/w/conversations")
  const opened = []
  for (const c of CHATS) {
    if (c.email && conversations.some((x) => x.customer_email === c.email)) continue
    if (!c.email && conversations.some((x) => x.preview?.includes("منزوعة"))) continue
    let token = ""
    for (const m of c.msgs) {
      const r = await publicPost(
        `/api/chat/${widget.public_key}/messages`,
        {
          body: m,
          visitor_token: token || undefined,
          name: c.name || undefined,
          email: c.email || undefined,
          page_url: SITE + c.page,
          locale: c.locale,
        },
        {
          "User-Agent":
            "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
          "Accept-Language": c.locale === "ar" ? "ar-EG,ar;q=0.9" : "en-GB,en;q=0.9",
        },
      )
      token = r.visitor_token
      await sleep(1200)
    }
    opened.push(c)
  }
  log(`chats: ${opened.length} new conversations`)

  // Work them as a member would: reply to two, assign one, link one to a contact.
  if (opened.length) {
    const list = (await s.get("/api/w/conversations")).conversations
    const conv = (email) => list.find((x) => x.customer_email === email)
    for (const c of opened.filter((x) => x.reply)) {
      const cv = conv(c.email)
      if (cv) await s.post(`/api/w/conversations/${cv.id}/reply`, { body: c.reply })
    }
    const ben = conv("ben.carter@mail.example")
    if (ben) await s.patch(`/api/w/conversations/${ben.id}`, { assignee_user_id: String(me.id) })
    const mariam = conv("mariam.adel@mail.example")
    if (mariam) {
      // Mariam becomes a CRM contact, and the thread is linked to her.
      const r = await s.post("/api/w/contacts", {
        email: "mariam.adel@mail.example",
        name: "Mariam Adel",
        locale: "en",
      })
      await s.patch(`/api/w/contacts/${r.contact.id}`, {
        title: "Office Manager, 40-person office in Maadi",
      })
      await s.patch(`/api/w/conversations/${mariam.id}`, { contact_id: r.contact.id })
    }
    log("inbox: 2 replied, 1 assigned, 1 linked to a contact")
  }
}

// ---- Notifications: webhook sources + signed deliveries -------------------------------------

{
  const { sources } = await s.get("/api/w/webhook_sources")
  const ensure = async (name, kind) => {
    const have = sources.find((x) => x.name === name)
    if (have && have.delivery_count > 0) return null // already fed on an earlier run
    if (have)
      return {
        id: have.id,
        secret: (await s.post(`/api/w/webhook_sources/${have.id}/rotate`, {})).secret,
      }
    const r = await s.post("/api/w/webhook_sources", { name, kind })
    return { id: r.source.id, secret: r.secret }
  }
  const hook = async (src, bodyObj, headers) => {
    const res = await fetch(`${base}/api/hooks/${src.id}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...headers },
      body: bodyObj,
    })
    if (!res.ok) throw new Error(`hook ${src.id} -> ${res.status} ${await res.text()}`)
  }
  const generic = async (src, obj) => {
    const body = JSON.stringify(obj)
    const ts = String(Math.floor(Date.now() / 1000))
    const sig = createHmac("sha256", src.secret)
      .update(ts + "." + body)
      .digest("hex")
    await hook(src, body, { "X-Webhook-Timestamp": ts, "X-Webhook-Signature": "sha256=" + sig })
  }
  const github = async (src, event, obj) => {
    const body = JSON.stringify(obj)
    const sig = createHmac("sha256", src.secret).update(body).digest("hex")
    await hook(src, body, {
      "X-GitHub-Event": event,
      "X-Hub-Signature-256": "sha256=" + sig,
      "X-GitHub-Delivery": randomBytes(16).toString("hex"),
    })
  }

  let sent = 0
  const shop = await ensure("Online shop (orders)", "generic")
  if (shop) {
    await generic(shop, {
      kind: "order.failed",
      severity: "error",
      title: "Order NC-20431 failed: card declined at checkout",
      body: "Customer Ben Carter · 2× Ethiopia Sidamo 250 g, 1× V60 filters · 1,480 EGP. Paymob returned 'Do not honour'. The cart is saved; the customer was emailed a retry link.",
      actor: "shop.nilecoffee.example",
      url: `${SITE}/admin/orders/NC-20431`,
      external_id: "order-NC-20431-failed",
    })
    sent++
    await generic(shop, {
      kind: "stock.low",
      severity: "warning",
      title: "Low stock: Colombia Huila 1 kg — 6 bags left",
      body: "At the current rate this sells out in 3 days. The next roast is scheduled for Monday.",
      actor: "inventory",
      url: `${SITE}/admin/products/huila-1kg`,
      external_id: "stock-huila-1kg-low",
    })
    sent++
    await generic(shop, {
      kind: "order.created",
      severity: "info",
      title: "New wholesale order: Bean & Brew — 60 kg house blend",
      body: "Delivery Wednesday, Zamalek + Sheikh Zayed branches. Net-30.",
      actor: "hana.elsayed@beanandbrew.example",
      url: `${SITE}/admin/orders/NC-20433`,
      external_id: "order-NC-20433-created",
    })
    sent++
  }
  const pay = await ensure("Paymob payments", "generic")
  if (pay) {
    await generic(pay, {
      type: "payment.succeeded",
      level: "info",
      subject: "Payment received: 18,450 EGP from مخبز الماسة",
      message: "Invoice INV-2026-0918 paid in full by bank transfer.",
      author: "paymob",
      link: `${SITE}/admin/invoices/INV-2026-0918`,
      external_id: "pay-INV-2026-0918",
    })
    sent++
    await generic(pay, {
      type: "payment.refunded",
      level: "warning",
      subject: "Refund issued: 640 EGP on order NC-20388",
      message: "Customer reported a torn bag on delivery; refunded one 500 g Huila.",
      author: "paymob",
      link: `${SITE}/admin/orders/NC-20388`,
      external_id: "refund-NC-20388",
    })
    sent++
  }
  const gh = await ensure("GitHub — nilecoffee/shop", "github")
  if (gh) {
    const repo = { full_name: "nilecoffee/shop", html_url: "https://github.com/nilecoffee/shop" }
    await github(gh, "workflow_run", {
      action: "completed",
      repository: repo,
      sender: { login: "omar-dev" },
      workflow_run: {
        name: "Deploy storefront",
        head_branch: "main",
        conclusion: "failure",
        display_title: "Checkout: add Paymob wallet option",
        html_url: "https://github.com/nilecoffee/shop/actions/runs/11873021554",
      },
    })
    sent++
    await github(gh, "push", {
      ref: "refs/heads/main",
      compare: "https://github.com/nilecoffee/shop/compare/4f1c2d9...a83be07",
      repository: repo,
      pusher: { name: "omar-dev" },
      sender: { login: "omar-dev" },
      head_commit: {
        message:
          "Fix wallet redirect on Paymob callback\n\nThe callback lost the order id on mobile Safari.",
      },
      commits: [{ id: "a83be07" }, { id: "9d02e1f" }],
    })
    sent++
    await github(gh, "pull_request", {
      action: "closed",
      repository: repo,
      sender: { login: "salma-design" },
      pull_request: {
        number: 214,
        title: "Arabic product pages: RTL price and grind selector",
        merged: true,
        body: "Fixes the grind selector overflowing in RTL.",
        html_url: "https://github.com/nilecoffee/shop/pull/214",
      },
    })
    sent++
  }
  log(`webhook deliveries: ${sent} signed and accepted`)

  if (sent) {
    const { items } = await s.get("/api/w/notifications")
    const find = (t) => items.find((i) => i.title.startsWith(t))
    const failed = find("Order NC-20431 failed")
    if (failed) await s.patch(`/api/w/notifications/${failed.id}`, { saved: true })
    for (const t of ["Payment received", "PR #214", "New wholesale order"]) {
      const it = find(t)
      if (it) await s.patch(`/api/w/notifications/${it.id}`, { read: true })
    }
    log("notifications: 1 saved, 3 marked read")
  }
}

// ---- Trends: real feeds + topics -------------------------------------------------------------

const FEEDS = [
  { name: "Daily Coffee News", url: "https://dailycoffeenews.com/feed/" },
  { name: "BBC News — Business", url: "https://feeds.bbci.co.uk/news/business/rss.xml" },
  { name: "بي بي سي عربي", url: "https://feeds.bbci.co.uk/arabic/rss.xml" },
]
{
  const { sources } = await s.get("/api/w/feed_sources")
  for (const f of FEEDS) {
    const check = await fetch(f.url, {
      headers: { "User-Agent": "Mozilla/5.0 (Moharrik demo seed)" },
    })
      .then(async (r) => r.ok && /<(rss|feed)[\s>]/.test((await r.text()).slice(0, 2000)))
      .catch(() => false)
    if (!check) {
      skipped.push([`Feed ${f.url}`, "did not return an RSS/Atom document when checked"])
      continue
    }
    let src = sources.find((x) => x.url === f.url)
    if (!src) {
      const r = await s.post("/api/w/feed_sources", f)
      src = r.source
      log(`feed ${f.name}: added, ${r.added} items on first fetch`)
    }
  }
  // Hacker News is read through its API (MH-387), from the catalog: the API
  // gives each story's front-page rank and points, which the RSS mirror does not.
  if (!(await s.get("/api/w/feed_sources")).sources.some((x) => x.catalog_key === "hackernews")) {
    const r = await s.post("/api/w/feed_sources", { catalog_key: "hackernews" }, { allow: [409] })
    log(
      `source Hacker News (API, from the catalog): added, ${r?.added ?? "?"} items on first fetch`,
    )
  }
  const TOPICS = [
    {
      name: "coffee prices",
      keywords: [
        "coffee price",
        "coffee prices",
        "arabica",
        "robusta",
        "futures",
        "tariff",
        "أسعار القهوة",
        "البن",
      ],
    },
    {
      name: "specialty coffee",
      keywords: [
        "specialty coffee",
        "roaster",
        "roastery",
        "espresso",
        "barista",
        "café",
        "cafe",
        "قهوة مختصة",
        "قهوة",
      ],
    },
  ]
  const { topics } = await s.get("/api/w/topics")
  for (const t of TOPICS)
    if (!topics.some((x) => x.name === t.name)) await s.post("/api/w/topics", t)
  // Fetch now on every feed, so the topics label what just came in.
  const now = (await s.get("/api/w/feed_sources")).sources
  for (const src of now) {
    const r = await s.post(
      `/api/w/feed_sources/${src.id}/fetch`,
      {},
      { allow: [409, 422, 502, 504] },
    )
    log(`fetch ${src.name}: ${r?.added ?? "?"} new`)
  }
  const news = await s.get("/api/w/news?limit=1")
  log(`news items collected: ${news.total}`)
}

// ---- Inbox: canned replies (en + ar; the thread's language picks the variant) ---------------

const CANNED = [
  {
    title: "Opening hours",
    body_en:
      "We're open every day from 8 am to 11 pm, and until midnight on Thursdays and Fridays.",
    body_ar: "نفتح يوميًا من 8 صباحًا حتى 11 مساءً، وحتى منتصف الليل يومي الخميس والجمعة.",
  },
  {
    title: "Wholesale enquiry",
    body_en:
      "Thanks for your interest in wholesale! Could you share your café's name, city and roughly how many kilos a month you need? Our team will reply within one working day.",
    body_ar:
      "شكرًا لاهتمامك بالجملة! ممكن تشاركنا اسم المقهى والمدينة وتقريبًا كم كيلو تحتاج شهريًا؟ فريقنا هيرد عليك خلال يوم عمل.",
  },
  {
    title: "Order status",
    body_en:
      "Let me check that for you. Could you send your order number? It's in the confirmation email.",
    body_ar: "خليني أتأكد لك. ممكن تبعت رقم الطلب؟ هتلاقيه في إيميل التأكيد.",
  },
]
{
  const { replies } = await s.get("/api/w/canned_replies")
  for (const c of CANNED)
    if (!replies.some((r) => r.title === c.title)) await s.post("/api/w/canned_replies", c)
  log(`canned replies: ${CANNED.length}`)
}

// ---- Research: one real run --------------------------------------------------------------------

{
  const TOPIC = "What are green coffee import rules and tariffs for Egypt in 2026?"
  const list = await s.get("/api/w/research")
  let run = list.runs.find(
    (r) => r.topic === TOPIC && r.status !== "failed" && r.status !== "cancelled",
  )
  if (!list.can_search)
    skipped.push(["Research run", "this deployment has no search provider (can_search=false)"])
  else {
    if (!run) {
      const r = await s.call("POST", "/api/w/research", { topic: TOPIC }, { allow: [409, 501] })
      if (r.status >= 300) skipped.push(["Research run", `${r.status}: ${JSON.stringify(r.body)}`])
      else run = { id: r.body.run_id, status: "queued" }
    }
    for (let i = 0; run && i < 90 && !["ok", "failed", "cancelled"].includes(run.status); i++) {
      await sleep(5000)
      run = (await s.get(`/api/w/research/${run.id}`)).run
    }
    if (run)
      log(
        `research: ${run.status} · found ${run.found_count}, read ${run.read_count}${run.error ? ` · ${run.error}` : ""}`,
      )
  }
}

// ---- Automation: presets on real sites, a browser allowed to reach them, a schedule ------------
//
// Two presets from the library (MH-478) that work on the real sites: Google
// News headlines for a query, and Google Trends' interest in a term. Added
// with the demo browser, so their sites go onto its allowed sites in the same
// step, the way an admin does it in the Presets gallery. The browser is the
// workspace's own when one is connected; otherwise a stand-in is connected
// through the same approval flow the extension uses, and stays offline until
// a real extension connects.

{
  const { browsers } = await s.get("/api/w/browsers")
  let browser = browsers.find((b) => !b.revoked_at)
  if (!browser) {
    const start = await fetch(`${base}/api/agents/connect/start`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: "Office Chrome",
        platform: "Windows",
        extension_version: "0.5.0",
        fingerprint: `demo-${Date.now()}`,
      }),
    }).then((r) => r.json())
    browser = (
      await s.post(
        "/api/agents/connect/approve",
        {
          code: start.code,
          key: start.browser_key,
          workspace: s.ws,
          name: "Office Chrome",
          allowed_hosts: [],
        },
        { ws: null },
      )
    ).browser
    log(`browser: connected a stand-in "${browser.name}" (offline until a real extension connects)`)
  }

  // The fake importer this used to point at is replaced by real sites.
  const { workflows } = await s.get("/api/w/workflows")
  const fake = workflows.find((w) => w.name === "Check green-coffee supplier prices")
  if (fake) await s.call("DELETE", `/api/w/workflows/${fake.id}`)

  const { presets } = await s.get("/api/w/automation/presets")
  for (const slug of ["google-news-search", "google-news-topic", "google-trends-explore"]) {
    const p = presets.find((x) => x.slug === slug)
    if (!p) throw new Error(`the preset library has no ${slug}`)
    if (p.workflow_id) continue
    const out = await s.post(`/api/w/automation/presets/${slug}/add`, { agent_id: browser.id })
    log(
      `preset: ${slug} added; ${browser.name} may now visit ${out.hosts_added.join(", ") || "(already allowed)"}`,
    )
  }

  const { schedules } = await s.get("/api/w/schedules")
  const schedName = "Coffee price headlines"
  if (!schedules.some((x) => x.name === schedName)) {
    const wf = (await s.get("/api/w/workflows")).workflows.find(
      (w) => w.preset === "google-news-search",
    )
    await s.post("/api/w/schedules", {
      name: schedName,
      workflow_id: wf.id,
      agent_id: "",
      cron: "0 8 * * 1-5",
      timezone: "Africa/Cairo",
      catch_up: "skip",
      values: { query: "coffee prices when:7d", hl: "en-US", gl: "EG", ceid: "EG:en" },
    })
    log("schedule: weekday coffee-price headlines, in any connected browser")
  }
  skipped.push([
    "Automation runs",
    "A run needs the browser extension connected; the stand-in browser stays offline.",
  ])
}

skipped.push([
  "Invitation for info@3x1.io",
  "Skipped on the owner's instruction (he is added as an owner directly).",
])
skipped.push([
  "Inbox email / WhatsApp channels",
  "Need real IMAP credentials and a linked WhatsApp number; not seedable with demo data.",
])
skipped.push([
  "SMTP settings",
  "Real SMTP credentials would be needed; left unconfigured, so campaigns stay drafts and double opt-in stays pending.",
])

// ---- read back ----------------------------------------------------------------------------------

const [co, ct, dl, ls, cp, fm, ld, cv, wg, whs, nt, fs, tp, nw, rs, wf] = await Promise.all([
  s.get("/api/w/companies?limit=200"),
  s.get("/api/w/contacts"),
  s.get("/api/w/deals"),
  s.get("/api/w/mail/lists"),
  s.get("/api/w/campaigns?limit=200"),
  s.get("/api/w/subscribe-forms"),
  s.get("/api/w/leads"),
  s.get("/api/w/conversations"),
  s.get("/api/w/chat_widgets"),
  s.get("/api/w/webhook_sources"),
  s.get("/api/w/notifications"),
  s.get("/api/w/feed_sources"),
  s.get("/api/w/topics"),
  s.get("/api/w/news?limit=1"),
  s.get("/api/w/research"),
  s.get("/api/w/workflows"),
])
const tally = (arr, key) => arr.reduce((m, x) => ((m[x[key]] = (m[x[key]] ?? 0) + 1), m), {})
const summary = {
  workspace: {
    id: ws.id,
    slug: ws.slug,
    name: WS_NAME,
    currency: CUR,
    url: `${base}/en/w/${ws.slug}`,
  },
  companies: co.total,
  contacts: ct.contacts.length,
  contacts_with_phone: ct.contacts.filter((c) => c.phone).length,
  contacts_by_lifecycle: tally(
    ct.contacts.map((c) => ({ stage: c.lifecycle || "none" })),
    "stage",
  ),
  contacts_scored: ct.contacts.filter((c) => c.score !== null).length,
  whatsapp_opted_in: ct.contacts.filter((c) => c.whatsapp_opt_in_at).length,
  lists: ls.lists.map((l) => `${l.name}: ${l.subscribed} subscribed, ${l.pending} pending`),
  deals: dl.total,
  deals_by_stage: tally(dl.deals, "stage_name"),
  pipeline_value: dl.stages.map(
    (x) => `${x.name}: ${x.deal_count} deals, ${(x.value_minor / 100).toLocaleString("en")} ${CUR}`,
  ),
  campaigns: cp.campaigns.map((c) => `${c.channel}/${c.locale} ${c.status}: ${c.subject}`),
  forms: fm.forms.map((f) => `${f.kind}: ${f.name}`),
  leads: ld.leads.length,
  leads_by_status: ld.counts,
  conversations: cv.conversations.length,
  conversations_unread: cv.unread_count,
  chat_widgets: wg.widgets.length,
  webhook_sources: whs.sources.map(
    (x) =>
      `${x.kind} "${x.name}": ${x.delivery_count} deliveries${x.last_error ? `, last error: ${x.last_error}` : ""}`,
  ),
  notifications: nt.items.length,
  notifications_unread: nt.unread_count,
  notifications_saved: nt.items.filter((i) => i.saved_at).length,
  feeds: fs.sources.map(
    (x) => `${x.name}: ${x.item_count} items${x.last_error ? `, error: ${x.last_error}` : ""}`,
  ),
  topics: tp.topics.map((x) => `${x.name}: ${x.item_count} items`),
  news_items: nw.total,
  research: rs.runs.map(
    (r) =>
      `${r.status} · found ${r.found_count} · read ${r.read_count} · model "${r.model}" · ${r.error || "-"} · ${r.id}`,
  ),
  research_capabilities: { search: rs.can_search, read: rs.can_read, summarise: rs.can_summarise },
  workflows: wf.workflows.map((w) => w.name),
}
console.log("\n=== read back from the API ===")
console.log(JSON.stringify(summary, null, 2))
console.log("\n=== not seeded ===")
for (const [what, why] of skipped) console.log(`- ${what}: ${why}`)
console.log(`\nLogin: ${EMAIL}`)
if (newPassword)
  console.log(`Password (shown once; also stored in ${STORE}, outside the repo): ${password}`)
else console.log(`Password: unchanged (DEMO_PASSWORD or ${STORE})`)
