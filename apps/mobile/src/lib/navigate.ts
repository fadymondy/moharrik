// Where a push tap or a moharrik:// link lands. Both become an app route carrying the target's
// workspace as ?ws= (an id or a slug); the screen switches to that workspace first when it isn't
// the current one (useWorkspaceParam), so a push from another workspace opens in the right one.
import { useEffect } from "react"

import type { Target } from "@moharrik/client"

import { useSession, type CurrentWorkspace } from "./session"

export function routeOf(t: Target): string {
  const ws = t.workspaceId || t.slug
  const q = ws ? `?ws=${encodeURIComponent(ws)}` : ""
  switch (t.screen) {
    case "conversation":
      return `/conversation/${encodeURIComponent(t.id)}${q}`
    case "notifications":
      return `/notifications${q}`
    default:
      return `/inbox${q}`
  }
}

export const pickWorkspace = (w: CurrentWorkspace): CurrentWorkspace => ({
  id: w.id,
  slug: w.slug,
  name: w.name,
  role: w.role,
  avatar_url: w.avatar_url,
})

/** Switches to the workspace a link names, when it is one of the member's. */
export function useWorkspaceParam(ws: string | string[] | undefined) {
  const { api, workspace, selectWorkspace, status } = useSession()
  const want = Array.isArray(ws) ? ws[0] : ws
  useEffect(() => {
    if (status !== "signedIn" || !want || want === workspace?.id || want === workspace?.slug) return
    let live = true
    api
      .workspaces()
      .then((r) => {
        const w = r.workspaces?.find((x) => x.id === want || x.slug === want)
        if (live && w) void selectWorkspace(pickWorkspace(w))
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [api, want, workspace?.id, workspace?.slug, selectWorkspace, status])
}
