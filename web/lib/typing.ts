"use client"

import { useEffect, useRef, useState } from "react"

import { api } from "@/lib/api"
import { useWorkspaceEvents, type WorkspaceEvent } from "@/lib/events"

/*
Typing/recording indicators (MH-660), both ways:

  - useTypingByConversation listens on the same realtime.Event stream as
    everything else in the inbox (internal/realtime — never a new channel) and
    keeps a conversation id -> presence map, so the thread header and the
    conversation list row can both read the same state. Nothing here is ever
    fetched or stored: a missed/late event just means the indicator is a
    little stale until the timeout clears it, same as the ephemeral guarantee
    on the server.
  - useAgentTyping is the composer's half: it tells the backend the agent is
    typing so it can relay "composing" to WhatsApp (throttled server-side,
    see internal/inbox/typing_agent.go) and to the chat widget visitor.

25s matches the server's visitor SSE heartbeat order of magnitude and is a
safety net only — a real "stopped" (paused, or the send happened) always
clears it sooner.
*/

const TYPING_TIMEOUT_MS = 25_000

export type TypingByConversation = Record<string, string>

/** conversation id -> current presence ("composing" | "recording"), auto-expiring. */
export function useTypingByConversation(workspaceId: string | null | undefined) {
  const [typing, setTyping] = useState<TypingByConversation>({})
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())

  useEffect(
    () => () => {
      for (const timer of timers.current.values()) clearTimeout(timer)
      timers.current.clear()
    },
    [],
  )

  const clear = (id: string) => {
    const t = timers.current.get(id)
    if (t) clearTimeout(t)
    timers.current.delete(id)
    setTyping((prev) => {
      if (!(id in prev)) return prev
      const next = { ...prev }
      delete next[id]
      return next
    })
  }

  useWorkspaceEvents(workspaceId, (e: WorkspaceEvent) => {
    if (e.kind !== "typing" || !e.conversation_id) return
    const id = e.conversation_id
    const existing = timers.current.get(id)
    if (existing) clearTimeout(existing)
    if (!e.presence) {
      clear(id)
      return
    }
    setTyping((prev) => (prev[id] === e.presence ? prev : { ...prev, [id]: e.presence! }))
    timers.current.set(
      id,
      setTimeout(() => clear(id), TYPING_TIMEOUT_MS),
    )
  })

  return typing
}

/** The composer's half: call notifyTyping() as the agent types, and
 *  notifyStopped() right after a send. Presence to WhatsApp is throttled on
 *  the server; this just avoids posting on every keystroke and clears itself
 *  after a short idle so a customer never sees a stuck "typing…". */
export function useAgentTyping(
  conversationId: string | null | undefined,
  workspaceId: string | null | undefined,
) {
  const idleTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastSent = useRef(0)

  useEffect(
    () => () => {
      if (idleTimer.current) clearTimeout(idleTimer.current)
    },
    [conversationId],
  )

  const send = (presence: string) => {
    if (!conversationId) return
    void api(`/api/w/conversations/${conversationId}/typing`, {
      method: "POST",
      json: { presence },
      workspaceId,
    }).catch(() => {
      // Best-effort: a missed presence call is not worth surfacing to the agent.
    })
  }

  const notifyTyping = () => {
    if (!conversationId) return
    if (idleTimer.current) clearTimeout(idleTimer.current)
    const now = Date.now()
    if (now - lastSent.current > 4000) {
      lastSent.current = now
      send("composing")
    }
    idleTimer.current = setTimeout(() => {
      lastSent.current = 0
      send("")
    }, 3000)
  }

  const notifyStopped = () => {
    if (idleTimer.current) clearTimeout(idleTimer.current)
    lastSent.current = 0
    send("")
  }

  return { notifyTyping, notifyStopped }
}
