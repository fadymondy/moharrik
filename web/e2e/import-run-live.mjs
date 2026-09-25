// Production check for MH-562: a real WhatsApp import, run on the demo workspace's
// own number only — its self-chat, never a customer's thread. It imports that one
// chat, imports it again (must reuse, not duplicate), and opens the thread in the
// inbox. Nothing is sent. Prints counts, never message content.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/import-run-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "irl-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

const api = (spec) =>
  page.evaluate(
    async ({ slug, spec }) => {
      const ws = await (await fetch("/api/me/workspaces", { credentials: "include" })).json()
      const id = ((ws.workspaces ?? ws.items ?? ws).find?.((w) => w.slug === slug) ?? {}).id
      const headers = { "X-Workspace-ID": id, "Content-Type": "application/json" }
      if (spec.method && spec.method !== "GET")
        headers["X-CSRF-Token"] = (
          await (
            await fetch("/api/auth/csrf", { credentials: "include", cache: "no-store" })
          ).json()
        ).csrf_token
      const r = await fetch(spec.url, {
        method: spec.method ?? "GET",
        credentials: "include",
        headers,
        body: spec.body ? JSON.stringify(spec.body) : undefined,
      })
      return { status: r.status, json: await r.json().catch(() => null) }
    },
    { slug, spec },
  )

const src = await api({ url: "/api/w/import/sources" })
const conn = src.json.sources.find((s) => s.source === "whatsapp")?.connections.find((c) => c.ready)
if (!conn) {
  console.log("no ready WhatsApp connection")
  process.exit(1)
}
// The connection id is not the number ("default"); its label carries it.
const own = "+" + String(conn.label ?? "").replace(/\D/g, "")
const prev = await api({
  url: `/api/w/import/whatsapp/preview?connection_id=${encodeURIComponent(conn.id)}`,
})
const self = prev.json.items.find((i) => i.kind === "direct" && i.identity === own)
check("selfChatListed", !!self)
if (!self) {
  console.log(JSON.stringify(out))
  process.exit(1)
}

const run = async () => {
  const s = await api({
    url: "/api/w/import/whatsapp",
    method: "POST",
    body: { connection_id: conn.id, items: [self.id], history_depth: 20 },
  })
  if (s.status !== 200) return { status: s.status, err: s.json?.detail }
  let job = s.json.job
  for (let i = 0; i < 60 && job.status === "running"; i++) {
    await page.waitForTimeout(1000)
    job = (await api({ url: `/api/w/import/jobs/${job.id}` })).json.job
  }
  const {
    id,
    status,
    total,
    done,
    contacts_created,
    contacts_reused,
    conversations_created,
    conversations_reused,
    messages_imported,
    skipped,
    last_error,
  } = job
  return {
    id,
    status,
    total,
    done,
    contacts_created,
    contacts_reused,
    conversations_created,
    conversations_reused,
    messages_imported,
    skipped,
    last_error,
  }
}
const first = await run()
out.first = first
check("firstImportDone", first.status === "done" && first.done === 1, first)
const second = await run()
out.second = second
check(
  "reimportReuses",
  second.status === "done" &&
    second.conversations_created === 0 &&
    second.contacts_created === 0 &&
    second.messages_imported === 0,
  second,
)

const prev2 = await api({
  url: `/api/w/import/whatsapp/preview?connection_id=${encodeURIComponent(conn.id)}`,
})
check(
  "previewSaysImported",
  prev2.json.items.find((i) => i.id === self.id)?.already_imported === true,
)

// The thread is in the inbox with its history.
const convs = await api({ url: "/api/w/conversations?limit=100" })
const conv = convs.json.conversations.find(
  (c) =>
    c.channel === "whatsapp" &&
    (c.external_thread_id === self.id ||
      String(c.customer_phone ?? "").replace(/\D/g, "") === own.slice(1)),
)
check("threadInInbox", !!conv)
if (conv) {
  await page.goto(`${base}/en/w/${slug}/inbox?c=${conv.id}`, { waitUntil: "domcontentloaded" })
  await page
    .waitForSelector("[data-message], [data-msg], article", { timeout: 20000 })
    .catch(() => {})
  out.messagesShown = await page.locator("[data-message], [data-msg]").count()
  check("historyShown", out.messagesShown > 0, out.messagesShown)
  if (shots)
    await page.screenshot({ path: path.join(shots, "mh562-imported-thread-en-light-1440.png") })
}
check("noPageErrors", errs.length === 0, errs)
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
