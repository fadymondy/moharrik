"use client"

import { useCallback, useEffect, useRef } from "react"
import { usePathname, useRouter } from "next/navigation"
import { BellIcon, MessageCircleIcon } from "lucide-react"
import { toast } from "sonner"

import { chime, desktopNotify, PUSH_MESSAGE } from "@/lib/alerts"
import { useWorkspaceEvents, type WorkspaceEvent } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { isolate } from "@/components/bidi"

/*
A customer's message, told to the member the moment it lands — on ANY page of
the workspace, not only the inbox (MH-378; fadymondy.com 01b5345 is the
single-owner original). A popup with the sender, a snippet and a way into the
thread; a chime unless this browser muted it; and, when the tab is in the
background, a desktop notification (once allowed from a click).

Only what the server marked `alert` for THIS member: it has already dropped
anything they may not read, alerted only the assignee of an assigned
conversation, and never alerts anyone for their own reply. Quieter still, here:
nothing for the thread the member is looking at right now.

Fed by the event stream and, as a fallback, by a web push the service worker
hands to an open tab; whichever arrives first wins and the other is dropped.
*/

const DEDUPE_MS = 5000
const SHOW_MS = 10000

type Alert = { key: string; title: string; body: string; url: string; kind: "message" | "notification"; conversationId?: string }

export function LiveAlerts({ workspaceId, slug }: { workspaceId: string; slug: string }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const seen = useRef(new Map<string, number>())
  const path = useRef(pathname)
  path.current = pathname

  const announce = useCallback(
    (a: Alert) => {
      const last = seen.current.get(a.key) ?? 0
      if (Date.now() - last < DEDUPE_MS) return
      seen.current.set(a.key, Date.now())

      const visible = document.visibilityState === "visible"
      // The thread already on screen updates itself; alerting about it is noise.
      if (a.conversationId && visible && /\/inbox\/?$/.test(path.current)) {
        const open = new URLSearchParams(window.location.search).get("c")
        if (open === a.conversationId) return
      }

      if (a.kind === "message") chime()
      toast(a.title, {
        id: a.key, // a second message on the same thread replaces the first popup
        description: a.body ? <span dir="auto">{a.body}</span> : undefined,
        duration: SHOW_MS,
        icon: a.kind === "message" ? <MessageCircleIcon className="size-4 text-grid-action" /> : <BellIcon className="size-4 text-grid-action" />,
        action: { label: t("alerts.open"), onClick: () => router.push(a.url) },
        className: "moharrik-alert",
      })
      if (!visible) void desktopNotify(a.title, a.body, a.key, a.url)
    },
    [router, t],
  )

  const fromEvent = useCallback(
    (e: WorkspaceEvent): Alert | null => {
      if (!e.alert) return null
      if (e.kind === "message" && e.direction === "in" && e.conversation_id) {
        return {
          kind: "message",
          key: `conv-${e.conversation_id}`,
          conversationId: e.conversation_id,
          title: e.sender ? t("alerts.messageFrom", { name: isolate(e.sender) }) : t("alerts.newMessage"),
          body: e.snippet ?? "",
          url: `/${locale}/w/${slug}/inbox?c=${encodeURIComponent(e.conversation_id)}`,
        }
      }
      if (e.kind === "notification" && e.item_id) {
        return {
          kind: "notification",
          key: `item-${e.item_id}`,
          title: e.title || t("alerts.notification"),
          body: e.source ?? "",
          url: `/${locale}/w/${slug}/notifications`,
        }
      }
      return null
    },
    [locale, slug, t],
  )

  useWorkspaceEvents(workspaceId, (e) => {
    const a = fromEvent(e)
    if (a) announce(a)
  })

  // A push that reached an open tab (sw-push.js posts it instead of showing a
  // notification over the page). Its text was written by the server, per member.
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return
    const onMessage = (ev: MessageEvent) => {
      const data = ev.data as { type?: string; payload?: { kind?: string; title?: string; body?: string; url?: string; tag?: string; conversation_id?: string } }
      const p = data?.payload
      if (data?.type !== PUSH_MESSAGE || !p?.tag || !p.url || p.kind === "test") return
      // Only this workspace's pushes, and only to a path on this origin.
      if (!p.url.startsWith(`/${locale}/w/${slug}/`) && !p.url.includes(`/w/${slug}/`)) return
      announce({
        kind: p.kind === "notification" ? "notification" : "message",
        key: p.tag,
        conversationId: p.conversation_id,
        title: p.title ?? "",
        body: p.body ?? "",
        url: p.url,
      })
    }
    navigator.serviceWorker.addEventListener("message", onMessage)
    return () => navigator.serviceWorker.removeEventListener("message", onMessage)
  }, [announce, locale, slug])

  return null
}
