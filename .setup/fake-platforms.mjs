// A tiny fake Telegram/Slack/Discord server for MANUAL browser verification of
// MH-563 during development — never a real bot, account or network (batch rules).
// Run: node .setup/fake-platforms.mjs [port]
import http from "node:http"

const port = Number(process.argv[2] || 8590)

const server = http.createServer((req, res) => {
  let body = ""
  req.on("data", (c) => (body += c))
  req.on("end", () => {
    res.setHeader("Content-Type", "application/json")
    const path = req.url || ""
    console.log(new Date().toISOString(), req.method, path, body.slice(0, 300))

    // Telegram: /bot<token>/<method>
    if (path.startsWith("/bot")) {
      const method = path.split("/").pop()
      if (method === "getMe") return res.end(JSON.stringify({ ok: true, result: { id: 1, is_bot: true, username: "moharrik_fake_bot" } }))
      if (method === "sendMessage") return res.end(JSON.stringify({ ok: true, result: { message_id: Date.now() % 100000 } }))
      return res.end(JSON.stringify({ ok: true, result: true }))
    }
    // Slack
    if (path.startsWith("/auth.test")) return res.end(JSON.stringify({ ok: true, team: "Fake Team", team_id: "T-FAKE", user_id: "U-FAKEBOT" }))
    if (path.startsWith("/chat.postMessage")) return res.end(JSON.stringify({ ok: true, ts: (Date.now() / 1000).toFixed(6) }))
    // Discord
    if (path === "/users/@me") return res.end(JSON.stringify({ id: "1", username: "moharrik_fake_bot", discriminator: "0" }))
    if (/^\/channels\/.+\/messages$/.test(path)) return res.end(JSON.stringify({ id: String(Date.now() % 100000) }))

    res.end(JSON.stringify({ ok: true }))
  })
})

server.listen(port, () => console.log(`fake platforms listening on :${port}`))
