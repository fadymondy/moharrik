// Deep links: where tapping a notification (or a moharrik:// link) lands.
//
// The server puts the same data on every push (internal/alerts/native.go nativeMessage):
// kind, workspace_id, slug, url, and conversation_id or item_id. Apps turn that into a route
// inside the right workspace, switching to it first when it isn't the current one.

export type Target =
  | { screen: "conversation"; workspaceId?: string; slug?: string; id: string }
  | { screen: "notifications"; workspaceId?: string; slug?: string; itemId?: string }
  | { screen: "inbox"; workspaceId?: string; slug?: string }

/** The data block of a push (FCM data, an SSE event, or the desktop's own notification). */
export function targetOfPush(data: Record<string, string | undefined> | null | undefined): Target | null {
  if (!data) return null
  const ws = { workspaceId: data.workspace_id || undefined, slug: data.slug || undefined }
  if (data.conversation_id) return { screen: "conversation", ...ws, id: data.conversation_id }
  if (data.kind === "notification" || data.item_id) return { screen: "notifications", ...ws, itemId: data.item_id || undefined }
  if (data.url) return targetOfUrl(data.url) ?? { screen: "inbox", ...ws }
  return ws.workspaceId || ws.slug ? { screen: "inbox", ...ws } : null
}

/**
 * moharrik://w/{slug}/inbox/{id}, moharrik://w/{slug}/notifications, or a web URL of the same
 * page (https://app.moharrik.com/{locale}/w/{slug}/inbox?c={id}).
 */
export function targetOfUrl(raw: string): Target | null {
  let path: string
  let query: URLSearchParams
  const m = /^moharrik:\/\/(.*)$/i.exec(raw)
  if (m) {
    const [p, q = ""] = m[1]!.split("?")
    path = "/" + p!
    query = new URLSearchParams(q)
  } else {
    try {
      const u = new URL(raw)
      path = u.pathname
      query = u.searchParams
    } catch {
      return null
    }
  }
  const parts = path.split("/").filter(Boolean)
  if (parts[0] === "en" || parts[0] === "ar") parts.shift()
  let slug: string | undefined
  if (parts[0] === "w" && parts[1]) {
    slug = decodeURIComponent(parts[1])
    parts.splice(0, 2)
  }
  const ws = slug ? { slug } : {}
  if (parts[0] === "inbox") {
    const id = parts[1] ?? query.get("c") ?? undefined
    return id ? { screen: "conversation", ...ws, id: decodeURIComponent(id) } : { screen: "inbox", ...ws }
  }
  if (parts[0] === "notifications") return { screen: "notifications", ...ws, itemId: parts[1] ?? query.get("item") ?? undefined }
  return slug ? { screen: "inbox", ...ws } : null
}

/** The app link of a target, the inverse of targetOfUrl. */
export function linkOf(t: Target): string {
  const w = t.slug ? `w/${encodeURIComponent(t.slug)}/` : ""
  if (t.screen === "conversation") return `moharrik://${w}inbox/${encodeURIComponent(t.id)}`
  if (t.screen === "notifications") return `moharrik://${w}notifications${t.itemId ? "/" + encodeURIComponent(t.itemId) : ""}`
  return `moharrik://${w}inbox`
}

/** The public pages the apps link to: the legal pages (store review asks for both in the app) and support. */
export type SitePage = "privacy" | "terms" | "contact"

export const SITE_URL = "https://moharrik.com"

/** https://moharrik.com/{locale}/{page}: the marketing site serves both languages. */
export function siteUrl(locale: "ar" | "en", page: SitePage): string {
  return `${SITE_URL}/${locale}/${page}`
}
