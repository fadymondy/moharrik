// Incoming links (moharrik://…, or app.moharrik.com URLs) rewritten to app routes.
import { targetOfUrl } from "@moharrik/client"

import { routeOf } from "@/lib/navigate"

export function redirectSystemPath({ path, initial }: { path: string; initial: boolean }): string | null {
  // The sign-in redirect is read by the auth session (src/lib/session.tsx), not routed.
  if (/^(moharrik:\/\/)?\/?auth\/callback/i.test(path)) return initial ? "/" : null
  const url = /^[a-z][a-z0-9+.-]*:/i.test(path) ? path : `moharrik://${path.replace(/^\/+/, "")}`
  const t = targetOfUrl(url)
  return t ? routeOf(t) : path
}
