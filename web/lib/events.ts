"use client"

import { useEffect, useRef } from "react"

/** What the server publishes. The payload is a hint to refetch, never the data itself. */
export type WorkspaceEvent = {
  kind: string
  conversation_id?: string
}

/**
 * Subscribe to this workspace's event stream.
 *
 * fetch + ReadableStream rather than EventSource: EventSource cannot send
 * headers, and X-Workspace-ID is how every other request in this app names its
 * workspace. Resolving the workspace from a query parameter instead would mean
 * a second way into tenant resolution that has to be kept in step with the
 * first one — not worth it for the convenience of automatic reconnection,
 * which is about fifteen lines.
 *
 * Reconnects with backoff, because a dropped stream is silent: nothing would
 * notice that the inbox had quietly stopped updating.
 */
export function useWorkspaceEvents(workspaceId: string | null | undefined, onEvent: (e: WorkspaceEvent) => void) {
  // The callback is almost always an inline arrow; holding it in a ref keeps a
  // re-render from tearing down and reopening the stream.
  const handler = useRef(onEvent)
  handler.current = onEvent

  useEffect(() => {
    if (!workspaceId) return
    const abort = new AbortController()
    let stopped = false
    let attempt = 0

    const run = async () => {
      while (!stopped) {
        try {
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
            // SSE frames are separated by a blank line; a frame can arrive
            // split across reads, so only whole ones are taken.
            const frames = buffer.split("\n\n")
            buffer = frames.pop() ?? ""
            for (const frame of frames) {
              for (const line of frame.split("\n")) {
                if (!line.startsWith("data:")) continue // ": ping" and friends
                try {
                  handler.current(JSON.parse(line.slice(5).trim()) as WorkspaceEvent)
                } catch {
                  // A frame we cannot parse is not worth dropping the stream for.
                }
              }
            }
          }
        } catch {
          if (stopped) return
        }
        if (stopped) return
        // 1s, 2s, 4s … capped at 30s.
        const wait = Math.min(30000, 1000 * 2 ** attempt++)
        await new Promise((r) => setTimeout(r, wait))
      }
    }
    void run()

    return () => {
      stopped = true
      abort.abort()
    }
  }, [workspaceId])
}
