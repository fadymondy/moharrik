/*
Where this browser talks to, and the names everything else reads.

The site is configurable because there are three in practice — production, a
dev stack, and whatever a future staging host is called — and hard-coding one
is the mistake this project's config rule exists to prevent.

The SOCKET is not derived from the site origin by rewriting the scheme alone.
A Next rewrite cannot carry a protocol upgrade, so in production the proxy
routes /api/agents/ws straight to the API; on a local stack that is a different
port entirely. Both are therefore stored, and apiOrigin defaults to the site.
*/

export const DEFAULTS = {
  siteOrigin: "https://app.moharrik.com",
  // Empty means "same as the site", which is right once the proxy routes
  // /api/agents/ws to the API.
  apiOrigin: "",
}

export const KEYS = {
  settings: "moh.settings",
  token: "moh.token",
  agent: "moh.agent",
  pairing: "moh.pairing",
  status: "moh.status",
  // This browser's own pause (MH-478): disconnected until switched back.
  paused: "moh.paused",
  // The one folder an upload step may take files from; unset, uploads are off.
  uploadFolder: "moh.uploadFolder",
}

export async function loadSettings() {
  const stored = await chrome.storage.local.get(KEYS.settings)
  return { ...DEFAULTS, ...(stored[KEYS.settings] || {}) }
}

export async function saveSettings(patch) {
  const current = await loadSettings()
  const next = { ...current, ...patch }
  await chrome.storage.local.set({ [KEYS.settings]: next })
  return next
}

export async function get(key) {
  const stored = await chrome.storage.local.get(key)
  return stored[key]
}

export async function set(key, value) {
  await chrome.storage.local.set({ [key]: value })
}

export async function clear(...keys) {
  await chrome.storage.local.remove(keys)
}

/** The WebSocket URL for the agent channel. */
export function socketURL(settings) {
  const origin = settings.apiOrigin || settings.siteOrigin
  return origin.replace(/^http/, "ws") + "/api/agents/ws"
}

/** The site's REST base (pairing lives on the site origin, not the API one). */
export function siteURL(settings, path) {
  return settings.siteOrigin.replace(/\/$/, "") + path
}
