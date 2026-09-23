/*
Connecting this browser by approval (MH-478).

The popup's "Connect to the site" button lands here. This asks the site for a
connect request, opens the site's approval page in a new tab, and waits for a
member to approve or refuse it there. Nothing is typed into the extension and
nothing is read off it: the member picks the workspace on the site.

Three things come back from connect/start, and each stays where it belongs:

  code         shown in the popup and on the approval page, so the person can
               see the two match. It is not enough to approve anything.
  browser key  goes ONLY into the approval page's address, after the #. A
               browser never sends a fragment to a server, so the key exists in
               that one tab and comes back to the site only with the decision.
  poll token   never leaves this extension. It is what collects the token.

The waiting happens in the service worker, not the popup: the popup closes the
moment the new tab opens. A worker can be stopped while it waits, so the state
is in storage and the keepalive alarm picks the wait up again (resumeConnect).
The server is the clock — it answers "expired" by itself.

Pure enough to test: every Chrome and network call comes in through `deps`.
*/

const POLL_EVERY = 2000

/**
 * @typedef {object} ConnectDeps
 * @property {() => Promise<{siteOrigin: string, apiOrigin: string}>} settings
 * @property {(key: string) => Promise<any>} get
 * @property {(key: string, value: any) => Promise<void>} set
 * @property {(...keys: string[]) => Promise<void>} clear
 * @property {(url: string, init?: RequestInit) => Promise<Response>} fetch
 * @property {(url: string) => Promise<void>} openTab
 * @property {() => Promise<object>} describe  what this browser says about itself
 * @property {(ms: number) => Promise<void>} sleep
 * @property {() => number} [now]
 * @property {{pairing: string, token: string, agent: string}} keys
 */

/** The site's origin, without a trailing slash. Connecting lives on the site, not the API. */
function site(settings) {
  return String(settings.siteOrigin || "").replace(/\/+$/, "")
}

/** The approval page's language: the browser's own, when it is one the site speaks. */
export function connectLocale(languages) {
  for (const l of languages || []) {
    const two = String(l || "").slice(0, 2).toLowerCase()
    if (two === "ar" || two === "en") return two
  }
  return "en"
}

/** A sentence from a failed response, never an HTML error page. */
async function failure(res, fallback) {
  try {
    const body = await res.json()
    return body.detail || body.error || body.title || `${fallback} (${res.status})`
  } catch {
    return `${fallback} (${res.status})`
  }
}

export function createConnect(deps) {
  const now = () => (deps.now ? deps.now() : Date.now())
  let waiting = null // the poll token this worker is waiting on, if any

  /**
   * Ask the site to connect, open the approval page, and start waiting.
   * Returns what the popup shows: the code and when it expires.
   */
  async function start({ languages } = {}) {
    const settings = await deps.settings()
    const about = await deps.describe()
    const res = await deps.fetch(`${site(settings)}/api/agents/connect/start`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...about, locale: connectLocale(languages) }),
    })
    if (!res.ok) {
      throw new Error(res.status === 429 ? "too many connect requests from this network — wait a while and try again" : await failure(res, "the site refused to start connecting"))
    }
    const body = await res.json()
    if (!body.code || !body.poll_token || !body.verify_path) throw new Error("the site's answer was not a connect request")

    const pairing = {
      code: body.code,
      pollToken: body.poll_token,
      verifyURL: site(settings) + body.verify_path,
      expiresAt: body.expires_at,
      status: "pending",
      startedAt: now(),
    }
    await deps.set(deps.keys.pairing, pairing)
    await deps.openTab(pairing.verifyURL)
    void wait()
    return { code: pairing.code, expiresAt: pairing.expiresAt }
  }

  /** Open the approval page again, for someone who closed it. */
  async function reopen() {
    const pairing = await deps.get(deps.keys.pairing)
    if (!pairing?.verifyURL || pairing.status !== "pending") return false
    await deps.openTab(pairing.verifyURL)
    return true
  }

  /** Stop waiting and forget the request. */
  async function cancel() {
    waiting = null
    await deps.clear(deps.keys.pairing)
  }

  /**
   * Wait for the member's decision. Idempotent: a second call while one wait
   * is running returns at once, so the alarm cannot start a parallel loop.
   */
  async function wait() {
    const first = await deps.get(deps.keys.pairing)
    if (!first?.pollToken || first.status !== "pending") return
    if (waiting === first.pollToken) return
    waiting = first.pollToken
    const settings = await deps.settings()

    for (;;) {
      await deps.sleep(POLL_EVERY)
      const current = await deps.get(deps.keys.pairing)
      // Cancelled, replaced by a newer request, or decided by another wait.
      if (!current || current.pollToken !== first.pollToken || current.status !== "pending" || waiting !== first.pollToken) {
        if (waiting === first.pollToken) waiting = null
        return
      }

      let res
      try {
        res = await deps.fetch(`${site(settings)}/api/agents/connect/poll`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ poll_token: first.pollToken }),
        })
      } catch {
        continue // the site is unreachable for a moment; the server's clock decides expiry
      }
      if (res.status === 404) {
        await settle(first.pollToken, { status: "expired" })
        return
      }
      if (!res.ok) continue
      let body
      try {
        body = await res.json()
      } catch {
        continue
      }

      if (body.status === "approved" && body.token) {
        // The token first, then the request is forgotten: the token appearing
        // in storage is what makes the socket connect (background.js).
        await deps.set(deps.keys.agent, {
          id: body.agent_id || "",
          name: body.name || "",
          workspaceName: body.workspace_name || "",
          workspaceSlug: body.workspace_slug || "",
          allowedHosts: [],
        })
        await deps.set(deps.keys.token, body.token)
        await deps.clear(deps.keys.pairing)
        waiting = null
        return
      }
      if (body.status === "denied" || body.status === "expired" || body.status === "claimed") {
        await settle(first.pollToken, { status: body.status })
        return
      }
    }
  }

  async function settle(pollToken, patch) {
    const current = await deps.get(deps.keys.pairing)
    if (current?.pollToken === pollToken) await deps.set(deps.keys.pairing, { ...current, ...patch })
    if (waiting === pollToken) waiting = null
  }

  /** Called on every worker start and alarm: pick an interrupted wait back up. */
  async function resume() {
    const pairing = await deps.get(deps.keys.pairing)
    if (pairing?.status === "pending") void wait()
  }

  return { start, reopen, cancel, wait, resume }
}
