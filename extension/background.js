/*
The socket, and everything that keeps it up.

AUTHENTICATION IS THE FIRST FRAME. A browser's WebSocket API cannot set
headers, so the token goes in a `hello` frame rather than in Authorization.
The server closes the socket if it does not arrive quickly, and nothing secret
ever appears in a URL.

An MV3 service worker is STOPPED when idle, which would drop the socket
silently. The alarm below wakes it; the reconnect loop then finds the socket
gone and opens a new one. That is why this file is mostly about coming back.
*/

import { KEYS, get, loadSettings, set, socketURL } from "./config.js"
import { closeRun, runStep } from "./driver.js"

let sock = null
let attempt = 0
let stopped = false

const KEEPALIVE = "moh-keepalive"

// One minute, because that is Chrome's floor for an MV3 alarm: asking for
// thirty seconds gets you silence, not a faster alarm.
chrome.alarms.create(KEEPALIVE, { periodInMinutes: 1 })
chrome.runtime.onInstalled.addListener(() => connect())
chrome.runtime.onStartup.addListener(() => connect())
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === KEEPALIVE) connect()
})

// The token appearing IS the signal to connect. Relying on the popup's message
// instead meant a popup that closed a moment too early left this browser
// paired and silent until the next alarm — and a worker that was asleep when
// the message was sent never received it at all.
chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local") return
  if (KEYS.token in changes) {
    stopped = false
    connect(true)
  }
})

// The popup asks for these.
chrome.runtime.onMessage.addListener((msg, _sender, reply) => {
  if (msg?.type === "connect") {
    stopped = false
    connect(true)
    reply({ ok: true })
    return true
  }
  if (msg?.type === "disconnect") {
    stopped = true
    if (sock) sock.close()
    setStatus({ state: "off" })
    reply({ ok: true })
    return true
  }
  if (msg?.type === "halt") {
    // "Stop everything", from this browser. Honoured by the server for the
    // whole workspace: a kill switch that only the web app can throw is not
    // there when the web app is not open.
    send({ type: "halt" })
    reply({ ok: true })
    return true
  }
  return false
})

async function setStatus(patch) {
  const current = (await get(KEYS.status)) || {}
  await set(KEYS.status, { ...current, ...patch, at: Date.now() })
}

async function connect(force = false) {
  if (stopped) return
  if (sock && (sock.readyState === WebSocket.OPEN || sock.readyState === WebSocket.CONNECTING) && !force) return

  const fp = await browserFingerprint()
  const token = await get(KEYS.token)
  if (!token) {
    await setStatus({ state: "unpaired" })
    return
  }
  const settings = await loadSettings()

  // The socket this call owns. Two connects can overlap — the module loads and
  // connects, and the token arriving connects again — and without this check
  // the OLDER socket's close handler writes "offline" after the newer one has
  // written "connected". The extension then reported itself disconnected while
  // it was, in fact, running steps.
  let mine
  try {
    mine = new WebSocket(socketURL(settings))
    if (sock && sock.readyState === WebSocket.OPEN) sock.close()
    sock = mine
  } catch (e) {
    await setStatus({ state: "error", error: String(e).slice(0, 200) })
    return
  }

  mine.onopen = () => {
    mine.send(
      JSON.stringify({
        id: "hello",
        type: "hello",
        payload: {
          token,
          fingerprint: fp,
          platform: navigator.userAgent.includes("Windows") ? "Windows" : navigator.platform || "unknown",
          browser_version: (navigator.userAgent.match(/Chrome\/([\d.]+)/) || [])[1] || "",
          extension_version: chrome.runtime.getManifest().version,
        },
      }),
    )
  }

  mine.onmessage = async (event) => {
    let env
    try {
      env = JSON.parse(event.data)
    } catch {
      return
    }

    if (env.type === "hello.ok") {
      attempt = 0
      const ack = env.payload || {}
      await set(KEYS.agent, { id: ack.agent_id, name: ack.name, allowedHosts: ack.allowed_hosts || [] })
      await setStatus({ state: "connected", name: ack.name, error: "" })
      return
    }

    if (env.type === "run.step") {
      const step = env.payload || {}
      try {
        const result = await runStep(env.run, step)
        send({ id: env.id, type: "run.step.result", payload: result })
      } catch (e) {
        // The browser's own words: "nothing on the page matches #submit" is
        // what the person reading the run needs, not "step failed".
        send({ id: env.id, type: "run.step.result", error: String(e?.message || e).slice(0, 300) })
      }
      return
    }

    if (env.type === "run.end") {
      await closeRun(env.run)
      return
    }
  }

  mine.onclose = async (event) => {
    if (sock !== mine) return // a socket that has already been replaced
    // 1008 is "your token is no good": stop trying, and say so. 4001 is the
    // kill switch, which is temporary by design — keep retrying so this browser
    // comes back by itself when someone resumes.
    if (event.code === 1008) {
      await set(KEYS.token, "")
      await setStatus({ state: "unpaired", error: event.reason || "this browser is no longer paired" })
      return
    }
    await setStatus({ state: event.code === 4001 ? "paused" : "offline", error: event.reason || "" })
    if (stopped) return
    const wait = Math.min(30000, 1000 * 2 ** attempt++)
    setTimeout(connect, wait)
  }

  mine.onerror = () => {
    // onclose always follows; the reconnect is handled there so it happens once.
  }
}

function send(frame) {
  if (sock && sock.readyState === WebSocket.OPEN) sock.send(JSON.stringify(frame))
}

/**
 * A stable id for this browser profile, so a reinstall is recognised as the
 * same browser rather than a second one. It is random and stored — NOT derived
 * from anything about the machine, which would be a fingerprint in the other
 * sense.
 *
 * chrome.storage, not localStorage: a service worker has no localStorage at
 * all, and referencing it throws a ReferenceError that optional chaining
 * cannot catch — which silently killed the whole connect path.
 */
async function browserFingerprint() {
  let id = await get("moh.fp")
  if (!id) {
    id = crypto.randomUUID()
    await set("moh.fp", id)
  }
  return id
}

connect()
