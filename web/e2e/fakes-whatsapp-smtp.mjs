// Local fakes for batch N's browser checks. NOTHING here reaches a real
// WhatsApp number or a real mailbox: an Evolution API stub on 127.0.0.1:8914
// and two SMTP sinks on 127.0.0.1:2614 (A) and :2615 (B), each logging what it
// was given to a JSON file in the log directory:
//   node e2e/fakes-whatsapp-smtp.mjs [log-dir]
// Point the API at it: EVOLUTION_API_URL=http://127.0.0.1:8914 EVOLUTION_API_KEY=stub-key
import { createServer } from "node:http"
import net from "node:net"
import { writeFileSync } from "node:fs"
import path from "node:path"
import { tmpdir } from "node:os"

// Logs go to the directory given as the first argument (default: the OS temp dir).
const dir = process.argv[2] ?? tmpdir()
const FAKE_QR =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

const evo = { state: "close", webhook: null, sent: [], created: [], pairingRequests: [] }
const dump = () => writeFileSync(path.join(dir, "evo-log.json"), JSON.stringify(evo, null, 2))

async function hook(event, data) {
  if (!evo.webhook?.url) return "no webhook"
  const res = await fetch(evo.webhook.url, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(evo.webhook.headers || {}) },
    body: JSON.stringify({ event, instance: evo.webhook.instance, data }),
  })
  return res.status
}

createServer((req, res) => {
  let body = ""
  req.on("data", (c) => (body += c))
  req.on("end", async () => {
    const json = () => {
      try {
        return JSON.parse(body || "{}")
      } catch {
        return {}
      }
    }
    const reply = (code, obj) => {
      res.writeHead(code, { "Content-Type": "application/json" })
      res.end(JSON.stringify(obj))
    }
    const url = new URL(req.url, "http://x")
    // control endpoints for the checks (no apikey)
    if (url.pathname === "/__ctl/state") {
      const b = json()
      evo.state = b.state
      dump()
      return reply(200, { hook: await hook("connection.update", { state: b.state, wuid: b.wuid || "" }) })
    }
    if (url.pathname === "/__ctl/inbound") {
      const b = json()
      const id = "IN" + Date.now()
      return reply(200, {
        hook: await hook("messages.upsert", {
          key: { remoteJid: b.phone + "@s.whatsapp.net", fromMe: false, id },
          pushName: b.name || "",
          message: { conversation: b.text },
        }),
      })
    }
    if (url.pathname === "/__ctl/log") return reply(200, evo)
    if (url.pathname === "/__ctl/reset") {
      evo.sent = []
      dump()
      return reply(200, {})
    }
    if (req.headers["apikey"] !== "stub-key") return reply(401, { message: "bad apikey" })

    if (url.pathname === "/instance/create") {
      evo.created.push(json().instanceName)
      if (evo.state === "close") evo.state = "connecting"
      dump()
      const number = json().number
      return reply(201, { qrcode: { base64: FAKE_QR, code: "2@fake", ...(number ? { pairingCode: "FAKE1234" } : {}) } })
    }
    if (url.pathname.startsWith("/instance/connect/")) {
      const number = url.searchParams.get("number")
      if (number) evo.pairingRequests.push(number)
      if (evo.state === "close") evo.state = "connecting"
      dump()
      return reply(200, { base64: FAKE_QR, code: "2@fake", ...(number ? { pairingCode: "FAKE1234" } : {}) })
    }
    if (url.pathname.startsWith("/webhook/set/")) {
      evo.webhook = { instance: url.pathname.split("/").pop(), ...json().webhook }
      dump()
      return reply(200, { webhook: { enabled: true } })
    }
    if (url.pathname.startsWith("/webhook/find/")) {
      return reply(200, evo.webhook ? { enabled: evo.webhook.enabled, url: evo.webhook.url } : null)
    }
    if (url.pathname.startsWith("/instance/connectionState/")) {
      return reply(200, { instance: { state: evo.state } })
    }
    if (url.pathname.startsWith("/message/sendText/")) {
      const b = json()
      evo.sent.push({ at: new Date().toISOString(), number: b.number, text: b.text })
      dump()
      return reply(201, { key: { id: "WAMID" + Date.now() + evo.sent.length } })
    }
    if (url.pathname.startsWith("/instance/delete/")) {
      evo.state = "close"
      evo.webhook = null
      dump()
      return reply(200, { status: "SUCCESS" })
    }
    return reply(404, { message: "not stubbed: " + url.pathname })
  })
}).listen(8914, "127.0.0.1", () => console.log("evolution stub on 127.0.0.1:8914"))

function sink(port, label) {
  const log = []
  net
    .createServer((sock) => {
      let buf = ""
      let data = null
      let m = {}
      const say = (l) => sock.write(l + "\r\n")
      say("220 sink-" + label)
      sock.on("data", (chunk) => {
        buf += chunk.toString("utf8")
        let i
        while ((i = buf.indexOf("\n")) >= 0) {
          const line = buf.slice(0, i).replace(/\r$/, "")
          buf = buf.slice(i + 1)
          if (data !== null) {
            if (line === ".") {
              m.data = data
              m.at = new Date().toISOString()
              m.subject = (/^Subject: (.*)$/m.exec(data) || [])[1] || ""
              log.push(m)
              writeFileSync(path.join(dir, `smtp-${label}.json`), JSON.stringify(log, null, 2))
              data = null
              m = {}
              say("250 queued")
            } else data += line + "\n"
            continue
          }
          const cmd = line.toUpperCase()
          if (cmd.startsWith("EHLO")) {
            sock.write("250-sink-" + label + "\r\n")
            say("250 AUTH PLAIN")
          } else if (cmd.startsWith("AUTH PLAIN")) {
            const dec = Buffer.from(line.split(" ")[2] || "", "base64").toString("utf8").split("\0")
            m.user = dec[1] || ""
            say("235 ok")
          } else if (cmd.startsWith("MAIL FROM:")) {
            m.from = line.slice(10).replace(/[<> ]/g, "")
            say("250 ok")
          } else if (cmd.startsWith("RCPT TO:")) {
            m.to = line.slice(8).replace(/[<> ]/g, "")
            say("250 ok")
          } else if (cmd === "DATA") {
            data = ""
            say("354 go")
          } else if (cmd === "QUIT") {
            say("221 bye")
            sock.end()
          } else say("250 ok")
        }
      })
      sock.on("error", () => {})
    })
    .listen(port, "127.0.0.1", () => console.log(`smtp sink ${label} on 127.0.0.1:${port}`))
}
sink(2614, "A")
sink(2615, "B")
