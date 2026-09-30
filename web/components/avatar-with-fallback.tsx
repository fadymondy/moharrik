"use client"

/*
MH-602: one round avatar renderer shared by the inbox (conversation list rows
and the thread header) and the CRM (contact page/dialog) — a photo when there
is one, initials on a neutral token background otherwise. Never a hardcoded
colour: `bg-muted`/`text-muted-foreground` come from the theme, exactly like
every other AvatarFallback in this app already does (see
components/inbox/floating-chat.tsx and components/alerts/notification-bell.tsx,
whose own local `initials()` this is lifted out of and shared from now).

`data-avatar` and `data-avatar-kind="initials"|"image"` are for e2e coverage
(web/e2e/inbox-avatars.mjs) — there is no other way from outside the DOM to
tell "no photo, rendering initials" from "photo failed to load, rendering
initials" apart from this attribute.
*/

import { Avatar } from "@fadymondy/nasaq/web"

/** First letter of up to two words. Works for Arabic as-is: toUpperCase() is
 *  a no-op on Arabic letters (no case), and splitting/slicing by codepoint
 *  index works the same for Arabic as for Latin here since none of the
 *  letters this touches are outside the BMP. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return "?"
  const chars =
    parts.length > 1 ? (parts[0]![0] ?? "") + (parts[1]![0] ?? "") : (parts[0] ?? "").slice(0, 2)
  return chars.toUpperCase()
}

export function PersonAvatar({
  name,
  photoUrl,
  size = "md",
  className,
}: {
  /** Kept so call sites need not change: Nasaq derives the fallback colour from the name. */
  id?: string | null
  name: string
  photoUrl?: string | null
  size?: "xs" | "sm" | "md" | "lg"
  className?: string
}) {
  const kind = photoUrl ? "image" : "initials"
  return (
    <Avatar
      name={name}
      src={photoUrl ?? undefined}
      size={size}
      className={className}
      data-avatar
      data-avatar-kind={kind}
    />
  )
}
