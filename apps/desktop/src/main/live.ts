// The workspace's live stream (GET /api/w/events), read by the main process so it works with the
// window closed: every event refreshes the unread count and the open lists; an event the server
// marked alert:true for this member (internal/realtime/hub.go — audience, mute and assignee are
// decided there) becomes a native notification that opens the conversation when clicked.
import { net, Notification } from "electron"

import { alertText, parseLive, readSse, translator, type LiveEvent } from "@moharrik/client"

import type { LiveOut, Target, Unread } from "../shared/types"
import { getApi, getClient } from "./session"

export interface LiveDeps {
  notify: () => boolean
  sound: () => boolean
  locale: () => "en" | "ar"
  /** True while the window is focused on this conversation: no popup for what is on screen. */
  showing: (conversationId: string) => boolean
  onEvent: (e: LiveOut) => void
  onUnread: (u: Unread) => void
  open: (t: Target) => void
}

let deps: LiveDeps
let abort: AbortController | null = null
let generation = 0
let poll: ReturnType<typeof setInterval> | null = null
let pendingCount: ReturnType<typeof setTimeout> | null = null
// Keep a reference: a Notification that is garbage-collected loses its click handler.
const shown = new Set<Notification>()
let alerts = 0

/** How many native notifications this run has raised (the scripted run checks it). */
export const alertsShown = () => alerts

export function initLive(d: LiveDeps): void {
  deps = d
}

export async function refreshUnread(): Promise<void> {
  const a = getApi()
  if (!a || !getClient().workspaceId) {
    deps.onUnread({ conversations: 0, notifications: 0 })
    return
  }
  try {
    const b = await a.bell()
    deps.onUnread({ conversations: b.unread_conversations ?? 0, notifications: b.unread_notifications ?? 0 })
  } catch {
    /* offline: keep the last count */
  }
}

function countSoon(): void {
  if (pendingCount) clearTimeout(pendingCount)
  pendingCount = setTimeout(() => void refreshUnread(), 400)
}

function alert(ev: LiveEvent): void {
  if (!deps.notify() || !Notification.isSupported()) return
  if (ev.conversation_id && deps.showing(ev.conversation_id)) return
  const { title, body } = alertText(translator(deps.locale()), ev)
  const n = new Notification({ title, body, silent: !deps.sound() })
  const ws = getClient().workspaceId ?? undefined
  const target: Target = ev.conversation_id
    ? { screen: "conversation", id: ev.conversation_id, workspaceId: ws }
    : { screen: "notifications", itemId: ev.item_id, workspaceId: ws }
  n.on("click", () => {
    deps.open(target)
    shown.delete(n)
  })
  n.on("close", () => shown.delete(n))
  shown.add(n)
  alerts++
  n.show()
}

/** (Re)connects for the current workspace; stops when signed out or no workspace is chosen. */
export function startLive(): void {
  stopLive()
  const c = getClient()
  if (!getApi() || !c.workspaceId) return
  const gen = ++generation
  const ctl = new AbortController()
  abort = ctl
  void refreshUnread()
  poll = setInterval(() => void refreshUnread(), 60_000)
  void (async () => {
    let delay = 1000
    while (gen === generation && !ctl.signal.aborted) {
      try {
        const res = await net.fetch(c.url("/api/w/events"), { headers: { ...(await c.headers()), Accept: "text/event-stream" }, signal: ctl.signal })
        if (res.status === 401 || res.status === 403) return
        if (!res.ok || !res.body) throw new Error(`events: ${res.status}`)
        delay = 1000
        // Reconnecting may have missed events: catch the counts and lists up.
        countSoon()
        deps.onEvent({ kind: "resync" })
        for await (const raw of readSse(res.body)) {
          const ev = parseLive(raw)
          if (!ev || ev.kind === "typing") continue
          deps.onEvent({ kind: ev.kind, conversation_id: ev.conversation_id, item_id: ev.item_id })
          countSoon()
          if (ev.alert) alert(ev)
        }
      } catch {
        if (ctl.signal.aborted) return
      }
      await new Promise((r) => setTimeout(r, delay))
      delay = Math.min(delay * 2, 30_000)
    }
  })()
}

export function stopLive(): void {
  generation++
  abort?.abort()
  abort = null
  if (poll) clearInterval(poll)
  poll = null
}
