// Connecting a browser for a check script (MH-478).
//
// The six-digit pairing code is gone: a browser now connects by approval. The
// checks that only need a browser to exist use these two helpers instead of
// the old /api/agents/pair routes and /api/w/browsers/approve.
//
// `call(method, path, body, workspaceID)` is each script's own signed-in fetch
// (it carries the session cookie and the CSRF token).

/** Connect a STAND-IN browser through the API alone. Returns its token and id. */
export async function connectFake(base, call, workspaceID, { name = "Check browser", hosts = [], fingerprint = "" } = {}) {
  const start = await fetch(`${base}/api/agents/connect/start`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name, platform: "e2e", extension_version: "0.0.1-test", fingerprint: fingerprint || `fp-${Date.now()}-${Math.random()}` }),
  })
  if (start.status !== 201) throw new Error(`connect/start → ${start.status} ${await start.text()}`)
  const s = await start.json()
  const approved = await call("POST", "/api/agents/connect/approve", { code: s.code, key: s.browser_key, workspace: workspaceID, name, allowed_hosts: hosts })
  const polled = await fetch(`${base}/api/agents/connect/poll`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ poll_token: s.poll_token }),
  }).then((r) => r.json())
  if (!polled.token) throw new Error(`connect/poll gave no token: ${JSON.stringify(polled)}`)
  return { token: polled.token, agentID: approved.browser.id, browser: approved.browser, start: s }
}

/**
 * Connect the REAL extension loaded in `ctx`: point it at the site, press its
 * Connect button, approve the request the page it opened carries (through the
 * API, with the key from that page's address), and wait until it is online.
 */
export async function connectExtension({ ctx, extensionID, site, api = "", call, workspaceID, name, hosts }) {
  const popup = await ctx.newPage()
  await popup.goto(`chrome-extension://${extensionID}/popup.html`)
  await popup.evaluate(
    ([siteOrigin, apiOrigin]) => chrome.storage.local.set({ "moh.settings": { siteOrigin, apiOrigin } }),
    [site.replace(/\/$/, ""), api.replace(/\/$/, "")],
  )
  await popup.reload()
  await popup.waitForSelector("#connect:not([hidden])", { timeout: 20000 })
  const opened = ctx.waitForEvent("page", { timeout: 20000 })
  await popup.click("#connect")
  const approval = await opened
  const u = new URL(approval.url())
  const code = u.pathname.split("/").pop()
  const key = new URLSearchParams(u.hash.slice(1)).get("k")
  const { browser } = await call("POST", "/api/agents/connect/approve", { code, key, workspace: workspaceID, name, allowed_hosts: hosts }, workspaceID)
  await approval.close()
  let online = false
  for (let i = 0; i < 40 && !online; i++) {
    const { browsers } = await call("GET", "/api/w/browsers", undefined, workspaceID)
    online = browsers.some((b) => b.id === browser.id && b.status === "online")
    if (!online) await new Promise((r) => setTimeout(r, 1500))
  }
  return { browser, online, popup }
}
