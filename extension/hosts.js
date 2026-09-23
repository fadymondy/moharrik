/*
The allow-list rule, in the browser (MH-422).

This is a PORT of internal/automation/guardrails.go — HostAllowed and
normaliseHost — and it must stay the same rule, not a similar one. The server
checks a goto before it is queued; this file checks the page the run's tab
actually ended up on. If the two disagreed, a site the server allowed could be
refused mid-run, or worse, a host the server would refuse could be acted on
because the browser matched it more loosely. The recorder used to have its own
looser matcher (it stripped "www." from both sides); it uses this one now.

Both implementations are tested against ONE table of cases,
internal/automation/testdata/host_matching.json, so a change to either side
that the other does not share fails a test.

The rule:
  - an entry matches the host itself and any subdomain of it: "example.com"
    allows "www.example.com" and "app.example.com"
  - never a mere text suffix: "notexample.com" is not "example.com"
  - ports are ignored: "example.com" allows example.com:8443
  - "www." is NOT special: an entry "www.example.com" does not allow the apex
  - case and a trailing dot do not matter
  - an empty list allows nothing
*/

/** normaliseHost, exactly as guardrails.go has it. Anything unusable is "". */
export function normaliseHost(value) {
  let v = String(value ?? "").trim().toLowerCase()
  if (!v) return ""
  if (v.includes("://")) {
    let u
    try {
      u = new URL(v)
    } catch {
      return ""
    }
    v = bareHostname(u)
    if (!v) return ""
  }
  // A host with a path or a port, typed by hand.
  let i = v.indexOf("/")
  if (i >= 0) v = v.slice(0, i)
  i = v.indexOf(":")
  if (i >= 0) v = v.slice(0, i)
  if (v.endsWith(".")) v = v.slice(0, -1) // "example.com." is the same host
  if (!v || /[ \t]/.test(v)) return ""
  return v
}

/** HostAllowed, exactly as guardrails.go has it. */
export function hostAllowed(list, host) {
  const h = normaliseHost(host)
  if (!h) return false
  for (const raw of list || []) {
    const entry = normaliseHost(raw)
    if (!entry) continue
    if (h === entry || h.endsWith("." + entry)) return true
  }
  return false
}

/**
 * Go's url.Hostname(): the host without its port, and an IPv6 literal without
 * its brackets. The URL API keeps the brackets; Go does not, and the two must
 * produce the same string before it reaches normaliseHost.
 */
function bareHostname(u) {
  const h = u.hostname || ""
  return h.startsWith("[") && h.endsWith("]") ? h.slice(1, -1) : h
}

/**
 * What a URL the run's tab is on means for the run.
 *
 *   allowed  an http(s) page whose host is on the list — steps may act on it
 *   blank    about:blank / about:srcdoc, or nothing committed yet. Normal for
 *            a moment during a navigation and for a brand-new tab, so it is
 *            NOT leaving the list; but nothing is acted on there either (a
 *            step that finds the page blank fails as an ordinary step error)
 *   outside  everything else: an http(s) host not on the list, and ANY other
 *            scheme — data:, javascript:, blob:, file:, chrome:, view-source:,
 *            another extension's pages or this one's own. None of them is a
 *            site the business listed, and the list is a list of sites.
 *
 * `where` names it for a person reading the run: the host for a website, a
 * description for anything else. The URL itself is never echoed — it can carry
 * a session token in its query string, and this text goes into a run record.
 */
export function pageVerdict(url, list) {
  const raw = String(url ?? "").trim()
  if (!raw) return { state: "blank" }
  const lower = raw.toLowerCase()
  if (lower === "about:blank" || lower.startsWith("about:blank#") || lower.startsWith("about:blank?") || lower === "about:srcdoc") {
    return { state: "blank" }
  }
  let u
  try {
    u = new URL(raw)
  } catch {
    return { state: "outside", where: "an address that could not be read" }
  }
  if (u.protocol === "http:" || u.protocol === "https:") {
    const host = normaliseHost(bareHostname(u))
    if (host && hostAllowed(list, host)) return { state: "allowed", host }
    return { state: "outside", host, where: host || "an address with no host" }
  }
  const scheme = u.protocol.replace(/:$/, "")
  const named = {
    data: "a data: URL",
    javascript: "a javascript: URL",
    blob: "a blob: URL",
    file: "a file on this computer",
    "chrome-extension": "an extension's page",
    about: `about:${u.pathname}`,
  }[scheme]
  return { state: "outside", where: named || `a ${scheme}: page` }
}

/**
 * Where a goto step is aimed, read the way guardrails.go's StepHost reads it:
 * a URL's host, or — for "example.com/x" with no scheme — the text itself.
 */
export function gotoHost(url) {
  const raw = String(url ?? "").trim()
  if (!raw) return ""
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(raw)) {
    try {
      const u = new URL(raw)
      if (u.protocol !== "http:" && u.protocol !== "https:") return ""
      return normaliseHost(bareHostname(u))
    } catch {
      return ""
    }
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(raw) && !/^[^:/]+:\d/.test(raw)) return "" // data:, javascript:, …
  return normaliseHost(raw)
}
