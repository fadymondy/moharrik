"use client"

import { useEffect, useRef } from "react"

/**
 * What the server publishes (internal/realtime/hub.go). A hint to refetch — the
 * lists and threads are always re-read through the normal endpoints — plus, for
 * an inbound message or a new notification item, just enough to alert with.
 *
 * The server decides per member what they receive: an event reaches only those
 * who may read it, and `alert` is true only for the members it should alert
 * (an assigned conversation alerts its assignee; nobody is alerted by their own
 * reply). The client never widens that — it can only stay quieter.
 */
export type WorkspaceEvent = {
  kind:
    "message" | "conversation" | "notification" | "notifications" | "schedule" | "browser" | string
  conversation_id?: string
  item_id?: string
  /** in = the customer wrote, out = a member replied. */
  direction?: "in" | "out"
  channel?: "chat" | "email" | "whatsapp" | string
  sender?: string
  snippet?: string
  title?: string
  severity?: "info" | "warning" | "error" | string
  source?: string
  author_user_id?: string
  alert?: boolean
  /** MH-660: a typing/recording indicator. "" (or absent) on a "typing" event
   *  means the customer stopped. */
  presence?: "composing" | "recording" | string
}

type Listener = (e: WorkspaceEvent) => void

/**
 * One stream per workspace per tab, shared by every listener in it.
 *
 * The shell's bell and live alerts listen on every page, and the inbox and
 * automation pages listen as well; a connection each would be two or three
 * long-lived requests per tab against a browser limit of six per origin.
 * Opened with the first listener, closed with the last.
 */
type Stream = { listeners: Set<Listener>; stop: () => void }
const streams = new Map<string, Stream>()

function open(workspaceId: string, listeners: Set<Listener>): () => void {
  const abort = new AbortController()
  let stopped = false
  let attempt = 0

  const run = async () => {
    while (!stopped) {
      try {
        // fetch + ReadableStream rather than EventSource: EventSource cannot
        // send headers, and X-Workspace-ID is how every other request in this
        // app names its workspace.
        const res = await fetch("/api/w/events", {
          headers: { Accept: "text/event-stream", "X-Workspace-ID": workspaceId },
          credentials: "same-origin",
          cache: "no-store",
          signal: abort.signal,
        })
        if (!res.ok || !res.body) throw new Error(`stream ${res.status}`)
        attempt = 0

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ""
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          // SSE frames are separated by a blank line; a frame can arrive split
          // across reads, so only whole ones are taken.
          const frames = buffer.split("\n\n")
          buffer = frames.pop() ?? ""
          for (const frame of frames) {
            for (const line of frame.split("\n")) {
              if (!line.startsWith("data:")) continue // ": ping" and friends
              let ev: WorkspaceEvent
              try {
                ev = JSON.parse(line.slice(5).trim()) as WorkspaceEvent
              } catch {
                continue // a frame we cannot parse is not worth dropping the stream for
              }
              for (const l of [...listeners]) {
                try {
                  l(ev)
                } catch {
                  // one listener's bug must not starve the others
                }
              }
            }
          }
        }
      } catch {
        if (stopped) return
      }
      if (stopped) return
      // A dropped stream is silent — nothing would notice the bell had stopped
      // updating — so it reconnects: 1s, 2s, 4s … capped at 30s.
      const wait = Math.min(30000, 1000 * 2 ** attempt++)
      await new Promise((r) => setTimeout(r, wait))
    }
  }
  void run()

  return () => {
    stopped = true
    abort.abort()
  }
}

function subscribe(workspaceId: string, l: Listener): () => void {
  let s = streams.get(workspaceId)
  if (!s) {
    const listeners = new Set<Listener>()
    s = { listeners, stop: open(workspaceId, listeners) }
    streams.set(workspaceId, s)
  }
  s.listeners.add(l)
  return () => {
    const cur = streams.get(workspaceId)
    if (!cur) return
    cur.listeners.delete(l)
    if (cur.listeners.size === 0) {
      cur.stop()
      streams.delete(workspaceId)
    }
  }
}

/** Subscribe to this workspace's event stream (shared per tab; reconnects with backoff). */
export function useWorkspaceEvents(
  workspaceId: string | null | undefined,
  onEvent: (e: WorkspaceEvent) => void,
) {
  // The callback is almost always an inline arrow; holding it in a ref keeps a
  // re-render from tearing down and reopening the subscription.
  const handler = useRef(onEvent)
  handler.current = onEvent

  useEffect(() => {
    if (!workspaceId) return
    return subscribe(workspaceId, (e) => handler.current(e))
  }, [workspaceId])
}
