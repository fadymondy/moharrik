// MH-1108: signing in opens a workspace's dashboard, not the workspaces list. The one opened is
// the last workspace used on this device, else the one the API says I was last active in
// (my latest change there, or when I joined), else none and the list is shown.

const KEY = "moharrik.lastWorkspace"

type Pickable = { slug: string; last_active_at?: string }

/** Remembers slug as this device's last opened workspace. */
export function rememberWorkspace(slug: string) {
  try {
    window.localStorage.setItem(KEY, slug)
  } catch {
    // Private mode or storage disabled: the API's last_active_at still picks one.
  }
}

/** This device's last opened workspace, if any. */
export function rememberedWorkspace(): string | null {
  try {
    return window.localStorage.getItem(KEY)
  } catch {
    return null
  }
}

/**
 * The slug to open after sign-in: the remembered one if I still belong to it, otherwise the most
 * recently active, otherwise the last in the list. null when I belong to none.
 */
export function pickHomeWorkspace(list: Pickable[], remembered: string | null): string | null {
  if (list.length === 0) return null
  if (remembered && list.some((w) => w.slug === remembered)) return remembered
  let best = list[list.length - 1]
  let bestAt = Date.parse(best.last_active_at ?? "") || 0
  for (const w of list) {
    const at = Date.parse(w.last_active_at ?? "") || 0
    if (at > bestAt) {
      best = w
      bestAt = at
    }
  }
  return best.slug
}
