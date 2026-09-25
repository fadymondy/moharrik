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

import type { CSSProperties } from "react"

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"

/** First letter of up to two words. Works for Arabic as-is: toUpperCase() is
 *  a no-op on Arabic letters (no case), and splitting/slicing by codepoint
 *  index works the same for Arabic as for Latin here since none of the
 *  letters this touches are outside the BMP. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return "?"
  const chars = parts.length > 1 ? (parts[0]![0] ?? "") + (parts[1]![0] ?? "") : (parts[0] ?? "").slice(0, 2)
  return chars.toUpperCase()
}

// MH-588: "a colour derived from the id, using theme tokens only" — every
// entry here is a CSS custom property this app already themes for both
// light/dark (globals.css / app/styles/grid-tokens.css), never a literal hex.
// Mixed at low opacity over --card for the tile and used at full strength for
// the initials so it reads in both themes without a contrast check per pair.
const AVATAR_PALETTE = ["--primary", "--secondary", "--accent", "--destructive", "--grid-ok", "--grid-warn", "--grid-danger"] as const

function hashString(id: string): number {
  let h = 0
  for (let i = 0; i < id.length; i++) {
    h = (Math.imul(h, 31) + id.charCodeAt(i)) >>> 0
  }
  return h
}

/** A stable background/text pair for one id — same id always yields the same
 *  tile, so a person's colour does not shift between the list, the thread and
 *  the contact page. */
function colorFromId(id: string): CSSProperties {
  const token = AVATAR_PALETTE[hashString(id) % AVATAR_PALETTE.length]
  return {
    backgroundColor: `color-mix(in oklab, var(${token}) 22%, var(--card))`,
    color: `var(${token})`,
  }
}

export function PersonAvatar({
  id,
  name,
  photoUrl,
  size = "default",
  className,
}: {
  /** Used only to derive the no-photo fallback colour; omit to keep the
   *  neutral `bg-muted` tile (e.g. a still-loading row with no id yet). */
  id?: string | null
  name: string
  photoUrl?: string | null
  size?: "default" | "sm" | "lg"
  className?: string
}) {
  const kind = photoUrl ? "image" : "initials"
  return (
    <Avatar size={size} className={className} data-avatar data-avatar-kind={kind}>
      {photoUrl ? <AvatarImage src={photoUrl} alt="" /> : null}
      <AvatarFallback style={id ? colorFromId(id) : undefined}>{initials(name)}</AvatarFallback>
    </Avatar>
  )
}
