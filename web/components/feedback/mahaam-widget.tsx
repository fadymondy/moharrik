"use client"

import Script from "next/script"
import { useEffect, useSyncExternalStore } from "react"

import { useTranslations } from "@/lib/i18n"

declare global {
  interface Window {
    MahaamFeedback?: { open: () => void; close: () => void; show: () => void }
  }
}

// Mahaam's embeddable feedback widget (console.mahaam.app/embed/v1.js): the "Report a problem"
// dialog — screenshot, element picker, console/network capture, type and priority.
//
// It posts to OUR /feedback route (app/feedback/route.ts), not to Mahaam directly: that route
// adds the workspace, account and host, attaches Moharrik's real project key server-side and
// forwards to Mahaam's /api/feedback/embed. The project key never ships to the browser.
//
// The widget sends everything with credentials: "omit", so our route never sees the session
// cookie. The data-key is therefore a short-lived signed reporter token for the signed-in account,
// fetched from /feedback/session (MH-464, lib/feedback-token.ts); the widget posts it back as its
// public_key. It also asks <endpoint>/config for its look, answered by app/feedback/config.
const EMBED = process.env.NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN ?? "https://console.mahaam.app"

// One widget per page, so its state is module-level: the key once fetched, and whether the
// script has loaded. The shell's buttons read it to stay hidden until reporting can work.
type State = { key: string | null; loaded: boolean; requested: boolean }
let state: State = { key: null, loaded: false, requested: false }
const listeners = new Set<() => void>()
function set(next: Partial<State>) {
  state = { ...state, ...next }
  listeners.forEach((l) => l())
}
function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}
const server: State = { key: null, loaded: false, requested: false }

function requestKey() {
  if (state.requested) return
  set({ requested: true })
  fetch("/feedback/session", { cache: "no-store" })
    .then((r) => (r.ok ? (r.json() as Promise<{ key?: string | null }>) : null))
    .then((b) => {
      if (b?.key) set({ key: b.key })
    })
    .catch(() => {})
}

export function MahaamFeedbackWidget() {
  const { locale } = useTranslations()
  const { key } = useSyncExternalStore(subscribe, () => state, () => server)
  useEffect(requestKey, [])
  // Not configured on this deployment, or not signed in: no widget, and no button (useFeedback).
  if (!key) return null
  return (
    <Script
      id="mahaam-feedback"
      src={`${EMBED}/embed/v1.js`}
      strategy="afterInteractive"
      data-key={key}
      data-endpoint="/feedback"
      data-locale={locale}
      data-launcher="false"
      onReady={() => set({ loaded: true })}
    />
  )
}

/** Whether "Report a problem" can open: the widget has a key and its script has loaded. */
export function useFeedbackReady(): boolean {
  const s = useSyncExternalStore(subscribe, () => state, () => server)
  return !!s.key && s.loaded
}

/** Opens the widget (a no-op until it has loaded). */
export function openFeedback() {
  window.MahaamFeedback?.open()
}
