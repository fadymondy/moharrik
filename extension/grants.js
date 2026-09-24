/*
Chrome's side of the allowed sites (MH-483).

There are two lists, and since MH-483 only one of them is a gate a person has
to think about:

  THE WORKSPACE'S LIST (allowed_hosts, set on the website) decides where a run
  may go. It is checked on the server and again here for every step (MH-422,
  hosts.js), and it is the ONLY list that decides.

  CHROME'S GRANT decides whether the extension can reach a site at all. Chrome
  gives it only in answer to a click in the browser, so the "Connect to the
  site" click asks for every http(s) site once — "broad access". From then on a
  site added on the website works at the next step, pushed live, with no second
  click here. (fadymondy.com asks for each site separately; this is Fady's
  call to depart from that, MH-483.)

Someone who declines broad access still connects. Each site the workspace adds
then waits for their OK: the toolbar icon shows a count and the popup lists
"Waiting for your OK: <host> [Approve]", one click each.

Patterns are written with explicit http:// and https:// schemes. The manifest's
optional_host_permissions are exactly "http://*\/*" and "https://*\/*", and
Chrome refuses a request for anything not inside them — "*://*\/*" included,
which is why "Allow on all sites" used to do nothing.

Pure enough to test: Chrome comes in through `permissions`.
*/

import { normaliseHost } from "./hosts.js"

/** Every http(s) site: what the Connect click asks for. */
export const BROAD = ["http://*/*", "https://*/*"]

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/

/**
 * The patterns that cover one workspace entry. An entry allows the host and
 * every subdomain of it (hosts.js), so the grant does too: "*.example.com"
 * matches example.com itself and anything under it. An IP address or a
 * single-label name has no subdomains to cover.
 */
export function originsFor(host) {
  const h = normaliseHost(host)
  if (!h) return []
  const pattern = IPV4.test(h) || h.includes(":") || !h.includes(".") ? h : `*.${h}`
  return [`https://${pattern}/*`, `http://${pattern}/*`]
}

/**
 * Where this browser stands: whether it holds broad access, and which of the
 * workspace's sites still wait for an OK in Chrome.
 *
 * @param {{contains: (p: {origins: string[]}) => Promise<boolean>}} permissions
 * @param {string[]} hosts the workspace's list for this browser
 */
export async function grantState(permissions, hosts) {
  const has = (origins) => permissions.contains({ origins }).catch(() => false)
  const broad = await has(BROAD)
  const pending = []
  if (!broad) {
    for (const host of hosts || []) {
      const origins = originsFor(host)
      // Either scheme is enough for a site to be reachable; https is what
      // nearly every site the workspace lists is on.
      if (!origins.length) continue
      if (!(await has([origins[0]])) && !(await has([origins[1]]))) pending.push(normaliseHost(host))
    }
  }
  return { mode: broad ? "all" : "sites", pending: [...new Set(pending)] }
}

/** The toolbar badge for a grant state and an update, pending sites first. */
export function badgeFor(state, update) {
  const n = state?.pending?.length || 0
  if (n > 0) {
    return { text: n > 99 ? "99+" : String(n), title: `Moharrik — ${n} site${n === 1 ? "" : "s"} waiting for your OK` }
  }
  if (update?.available) return { text: "NEW", title: `Moharrik — update available: ${update.latest}` }
  return { text: "", title: "Moharrik" }
}

/** Dotted versions compared numerically: "0.10.0" is newer than "0.9.3". */
export function newerThan(a, b) {
  const pa = String(a || "").split(".").map((x) => Number(x) || 0)
  const pb = String(b || "").split(".").map((x) => Number(x) || 0)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] || 0
    const y = pb[i] || 0
    if (x !== y) return x > y
  }
  return false
}
