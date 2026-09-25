// create_campaign + send_campaign over real JSON-RPC, with a throwaway SMTP
// server catching the mail — so "it sent" means a message actually arrived,
// not that a row changed status.
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import net from "node:net"

const base = process.argv[2] ?? "http://localhost:3122"
const SMTP_PORT = 2527

/** The smallest SMTP server that will hold a conversation, so a real send lands somewhere. */
function smtpCatcher(port) {
  const inbox = []
  const server = net.createServer((socket) => {
    let mode = "cmd"
    let current = { to: [], data: "" }
    socket.write("220 catcher\r\n")
    socket.on("data", (chunk) => {
      for (const line of chunk.toString("utf8").split("\r\n")) {
        if (mode === "data") {
          if (line === ".") {
            inbox.push({ ...current })
            current = { to: [], data: "" }
            mode = "cmd"
            socket.write("250 ok\r\n")
          } else current.data += line + "\n"
          continue
        }
        if (!line) continue
        const up = line.toUpperCase()
        if (up.startsWith("EHLO") || up.startsWith("HELO"))
          socket.write("250-catcher\r\n250 OK\r\n")
        else if (up.startsWith("MAIL FROM")) socket.write("250 ok\r\n")
        else if (up.startsWith("RCPT TO")) {
          current.to.push((line.match(/<(.+)>/) || [])[1] ?? line)
          socket.write("250 ok\r\n")
        } else if (up === "DATA") {
          mode = "data"
          socket.write("354 send it\r\n")
        } else if (up === "QUIT") {
          socket.write("221 bye\r\n")
          socket.end()
        } else socket.write("250 ok\r\n")
      }
    })
    socket.on("error", () => {})
  })
  return new Promise((resolve) =>
    server.listen(port, "127.0.0.1", () => resolve({ server, inbox })),
  )
}

const out = {}
const { server, inbox } = await smtpCatcher(SMTP_PORT)

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mc-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  viewport: { width: 1280, height: 900 },
})
const page = ctx.pages()[0]
const email = `mc${Date.now()}@x.test`
await page.goto(`${base}/en/register`, { waitUntil: "networkidle" })
await page.fill("#email", email)
await page.fill("#password", "Passw0rd!long-enough")
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })
if (!/\/w\//.test(page.url())) {
  await page.fill("#ws-name", "MCP Campaign " + Date.now())
  await page.click("button[type=submit]")
  await page.waitForURL(/\/w\//, { timeout: 30000 })
}
const slug = page.url().split("/w/")[1].split("/")[0]

// SMTP for this workspace, so a send has somewhere to go.
await page.goto(`${base}/en/w/${slug}/settings/email`, { waitUntil: "networkidle" })
await page.fill("#smtp-host", "127.0.0.1")
await page.fill("#smtp-port", String(SMTP_PORT))
await page.fill("#smtp-from-email", "support@moharrik.test")
await page.fill("#smtp-from-name", "Moharrik")
const tls = await page.$("#smtp-tls")
if (tls) {
  await page.click("#smtp-tls")
  await page
    .getByRole("option", { name: /none/i })
    .click({ timeout: 4000 })
    .catch(() => {})
}
await page.click('[data-form="smtp"] button[type=submit]')
await page.waitForTimeout(1200)

// A list with one CONFIRMED subscriber (no double opt-in, so it confirms immediately).
await page.goto(`${base}/en/w/${slug}/contacts`, { waitUntil: "networkidle" })
await page.fill("#list-name", "Announcements")
// Base UI renders a hidden <input> for the Switch; the clickable element is
// the one carrying role=switch. Turning it OFF means the contact is confirmed
// immediately, so the campaign has a recipient without an email round-trip.
await page.click("[data-form=create-list] [role=switch]")
await page.click("[data-form=create-list] button[type=submit]")
await page.waitForSelector('[data-list-row="Announcements"]', { timeout: 10000 })
await page.fill("#contact-email", "reader@example.test")
await page.click("[data-form=add-contact] button[type=submit]")
await page.waitForSelector('[data-contact-row="reader@example.test"]', { timeout: 10000 })

// An MCP token that may read and write campaigns.
const token = await page.evaluate(async () => {
  const csrf = await (await fetch("/api/auth/csrf")).json()
  const ws = (await (await fetch("/api/me/workspaces")).json()).workspaces[0].id
  const r = await fetch("/api/w/mcp_tokens", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf.csrf_token,
      "X-Workspace-ID": ws,
    },
    body: JSON.stringify({
      name: "campaign check",
      scopes: ["contacts:read", "campaigns:read", "campaigns:write"],
    }),
  })
  return (await r.json()).plaintext
})
out.gotToken = !!token

async function call(name, args = {}) {
  const res = await fetch(`${base}/api/mcp`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: { name, arguments: args },
    }),
  })
  const j = await res.json()
  if (j.error) return { error: j.error.message }
  return { text: j.result.content?.[0]?.text ?? "", isError: !!j.result.isError }
}

out.createUnknownList = (
  await call("create_campaign", { list: "Nope", subject: "x", body: "y" })
).text
out.create = (
  await call("create_campaign", {
    list: "Announcements",
    subject: "Launch week",
    body: "# Hello\n\nWe shipped.",
  })
).text
out.sendUnknown = (await call("send_campaign", { subject: "Not a campaign" })).text
out.send = (await call("send_campaign", { subject: "Launch week" })).text

// Poll list_campaigns the way the tool tells an agent to.
let listed = ""
for (let i = 0; i < 20; i++) {
  listed = (await call("list_campaigns")).text
  if (/\[sent\]/.test(listed)) break
  await new Promise((r) => setTimeout(r, 1000))
}
out.listed = listed.split("\n").filter(Boolean).slice(0, 3)
out.mailDelivered = inbox.length
out.mailTo = inbox[0]?.to ?? []
out.mailHasSubject = (inbox[0]?.data ?? "").includes("Launch week")
out.mailHasUnsubscribe = /List-Unsubscribe/i.test(inbox[0]?.data ?? "")
out.sendTwice = (await call("send_campaign", { subject: "Launch week" })).text

// A token WITHOUT campaigns:write must be refused in-band.
const narrow = await page.evaluate(async () => {
  const csrf = await (await fetch("/api/auth/csrf")).json()
  const ws = (await (await fetch("/api/me/workspaces")).json()).workspaces[0].id
  const r = await fetch("/api/w/mcp_tokens", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrf.csrf_token,
      "X-Workspace-ID": ws,
    },
    body: JSON.stringify({ name: "read only", scopes: ["campaigns:read"] }),
  })
  return (await r.json()).plaintext
})
const refused = await fetch(`${base}/api/mcp`, {
  method: "POST",
  headers: { Authorization: `Bearer ${narrow}`, "Content-Type": "application/json" },
  body: JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "tools/call",
    params: { name: "send_campaign", arguments: { subject: "Launch week" } },
  }),
})
const rj = await refused.json()
out.withoutScope = {
  isError: !!rj.result?.isError,
  text: rj.result?.content?.[0]?.text ?? rj.error?.message,
}

server.close()
await ctx.close()
console.log(JSON.stringify(out, null, 1))
