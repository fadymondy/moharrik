// A fake Evolution API server for MH-562 dev/manual verification (never the real one).
// Seeded with real-format WhatsApp chats, groups and messages, Arabic included.
//
//   node web/e2e/fake-evolution.mjs [port]
//
// Implements just the endpoints internal/whatsapp/{evolution,chats}.go call:
//   GET  /instance/connectionState/:instance
//   POST /chat/findChats/:instance
//   GET  /group/fetchAllGroups/:instance
//   POST /chat/findMessages/:instance
//   POST /message/sendText/:instance   (accepted, never delivered anywhere real)
import { createServer } from "node:http"

const port = Number(process.argv[2] ?? 8623)
const now = Math.floor(Date.now() / 1000)

const chats = [
  {
    id: "cmchat0001",
    remoteJid: "201015550101@s.whatsapp.net",
    pushName: "Rana Fathy",
    unreadCount: 1,
    lastMessage: {
      key: { id: "wamid.002", fromMe: false, remoteJid: "201015550101@s.whatsapp.net" },
      messageType: "conversation",
      message: { conversation: "هل وصل طلبي رقم NC-20417؟" },
      messageTimestamp: now - 3600,
    },
  },
  {
    id: "cmchat0002",
    remoteJid: "201227654321@s.whatsapp.net",
    pushName: "Omar Khalifa",
    unreadCount: 0,
    lastMessage: {
      key: { id: "wamid.003", fromMe: true, remoteJid: "201227654321@s.whatsapp.net" },
      messageType: "extendedTextMessage",
      message: { extendedTextMessage: { text: "Thanks, see you at the fair!" } },
      messageTimestamp: now - 7200,
    },
  },
]

const groups = [
  {
    id: "120363011234567890@g.us",
    subject: "عملاء القهوة - القاهرة",
    size: 3,
    participants: [
      { id: "201015550101@s.whatsapp.net", name: "Rana Fathy", admin: "superadmin" },
      { id: "201098765432@s.whatsapp.net", name: "Laila Hassan", admin: "" },
      { id: "201155501234@s.whatsapp.net", name: "Youssef Adly", admin: "" },
    ],
    lastMessage: {
      key: { id: "wamid.101", fromMe: false, remoteJid: "120363011234567890@g.us" },
      messageType: "conversation",
      message: { conversation: "الشحنة الجديدة وصلت اليوم ☕" },
      messageTimestamp: now - 1800,
    },
  },
]

const history = {
  "201015550101@s.whatsapp.net": [
    { key: { remoteJid: "201015550101@s.whatsapp.net", fromMe: false, id: "wamid.001" }, pushName: "Rana Fathy", messageTimestamp: now - 7200, message: { conversation: "مرحبًا، عايزة أعرف حالة طلبي" } },
    { key: { remoteJid: "201015550101@s.whatsapp.net", fromMe: true, id: "wamid.002" }, pushName: "", messageTimestamp: now - 5400, message: { conversation: "أهلًا رنا، جاري المتابعة الآن" } },
    { key: { remoteJid: "201015550101@s.whatsapp.net", fromMe: false, id: "wamid.003" }, pushName: "Rana Fathy", messageTimestamp: now - 3600, message: { conversation: "هل وصل طلبي رقم NC-20417؟" } },
  ],
  "201227654321@s.whatsapp.net": [
    { key: { remoteJid: "201227654321@s.whatsapp.net", fromMe: false, id: "wamid.101" }, pushName: "Omar Khalifa", messageTimestamp: now - 9000, message: { conversation: "See you at the coffee fair?" } },
    { key: { remoteJid: "201227654321@s.whatsapp.net", fromMe: true, id: "wamid.102" }, pushName: "", messageTimestamp: now - 7200, message: { conversation: "Thanks, see you at the fair!" } },
  ],
  "120363011234567890@g.us": [
    { key: { remoteJid: "120363011234567890@g.us", fromMe: false, id: "wamid.201" }, pushName: "Laila Hassan", messageTimestamp: now - 3600, message: { conversation: "متى الشحنة الجاية؟" } },
    { key: { remoteJid: "120363011234567890@g.us", fromMe: false, id: "wamid.202" }, pushName: "Rana Fathy", messageTimestamp: now - 1800, message: { conversation: "الشحنة الجديدة وصلت اليوم ☕" } },
  ],
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost")
  const send = (code, body) => {
    res.writeHead(code, { "content-type": "application/json" })
    res.end(JSON.stringify(body))
  }
  let body = ""
  for await (const chunk of req) body += chunk
  const json = body ? JSON.parse(body) : {}

  if (req.method === "GET" && url.pathname.startsWith("/instance/connectionState/")) {
    return send(200, { instance: { state: "open" } })
  }
  if (req.method === "POST" && url.pathname === "/instance/create") {
    return send(200, { qrcode: { pairingCode: "FAKE-PAIR", code: "2@fake", base64: "" } })
  }
  if (req.method === "GET" && url.pathname.startsWith("/instance/connect/")) {
    return send(200, { pairingCode: "FAKE-PAIR", code: "2@fake", base64: "" })
  }
  if (req.method === "POST" && url.pathname.startsWith("/webhook/set/")) {
    return send(200, { webhook: { enabled: true } })
  }
  if (req.method === "DELETE" && url.pathname.startsWith("/instance/delete/")) {
    return send(200, { status: "deleted" })
  }
  if (req.method === "POST" && url.pathname.startsWith("/chat/findChats/")) {
    return send(200, chats)
  }
  if (req.method === "GET" && url.pathname.startsWith("/group/fetchAllGroups/")) {
    return send(200, groups)
  }
  if (req.method === "POST" && url.pathname.startsWith("/chat/findMessages/")) {
    const jid = json?.where?.key?.remoteJid
    return send(200, { messages: { records: history[jid] ?? [] } })
  }
  if (req.method === "POST" && url.pathname.startsWith("/message/sendText/")) {
    return send(200, { key: { id: "wamid.fake-sent-" + Date.now() } })
  }
  send(404, { message: "not found in the fake Evolution server" })
})

server.listen(port, () => {
  console.log(`fake Evolution API on http://127.0.0.1:${port} — never the real one`)
})
