"use client"

import { useCallback, useEffect, useState } from "react"

/*
The browser side of MH-378: the chime, the desktop notification, and web push
registration. Everything here is per device — the chime preference lives in
localStorage, and a push subscription belongs to one browser.
*/

// ── the chime ──────────────────────────────────────────────────────────────

const CHIME_KEY = "moharrik.alerts.chime"

export function chimeMuted(): boolean {
  try {
    return localStorage.getItem(CHIME_KEY) === "off"
  } catch {
    return false
  }
}

/** The mute toggle, kept in step across tabs through the storage event. */
export function useChimeMuted(): [boolean, (muted: boolean) => void] {
  const [muted, setMuted] = useState(false)
  useEffect(() => {
    setMuted(chimeMuted())
    const onStorage = (e: StorageEvent) => {
      if (e.key === CHIME_KEY) setMuted(e.newValue === "off")
    }
    window.addEventListener("storage", onStorage)
    return () => window.removeEventListener("storage", onStorage)
  }, [])
  const set = useCallback((m: boolean) => {
    try {
      localStorage.setItem(CHIME_KEY, m ? "off" : "on")
    } catch {
      // private mode: the toggle still works for this page
    }
    setMuted(m)
  }, [])
  return [muted, set]
}

/**
 * Two short sine notes, generated rather than shipped as a file. A browser that
 * will not play sound before the first click (autoplay policy) is not an error:
 * the popup and the badge still arrive.
 */
export function chime() {
  if (chimeMuted()) return
  try {
    const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!Ctx) return
    const ctx = new Ctx()
    const now = ctx.currentTime
    for (const [i, freq] of [880, 1320].entries()) {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.type = "sine"
      osc.frequency.value = freq
      gain.gain.setValueAtTime(0.0001, now + i * 0.12)
      gain.gain.exponentialRampToValueAtTime(0.18, now + i * 0.12 + 0.02)
      gain.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.12 + 0.25)
      osc.connect(gain).connect(ctx.destination)
      osc.start(now + i * 0.12)
      osc.stop(now + i * 0.12 + 0.3)
    }
    setTimeout(() => void ctx.close(), 800)
  } catch {
    // no audio device, or not allowed yet
  }
}

// ── desktop notifications (the page's own, while a tab is open) ─────────────

export type Permission = NotificationPermission | "unsupported"

export function notificationPermission(): Permission {
  return typeof window === "undefined" || typeof Notification === "undefined" ? "unsupported" : Notification.permission
}

/** The permission, re-read when the tab regains focus (it can change in site settings). */
export function useNotificationPermission(): [Permission, () => Promise<Permission>] {
  const [perm, setPerm] = useState<Permission>("unsupported")
  useEffect(() => {
    setPerm(notificationPermission())
    const onFocus = () => setPerm(notificationPermission())
    window.addEventListener("focus", onFocus)
    return () => window.removeEventListener("focus", onFocus)
  }, [])
  // Only ever called from a click: browsers ignore (and some penalise) a
  // permission prompt nobody asked for, and asking on load is exactly that.
  const request = useCallback(async () => {
    if (typeof Notification === "undefined") return "unsupported" as const
    const p = await Notification.requestPermission()
    setPerm(p)
    return p
  }, [])
  return [perm, request]
}

/**
 * Show a desktop notification for a tab that is not being looked at. Through
 * the service worker when it is registered — the same `tag` as the web push
 * for the same thread, so the two replace each other instead of stacking — and
 * with the plain constructor otherwise.
 */
export async function desktopNotify(title: string, body: string, tag: string, url: string) {
  if (notificationPermission() !== "granted") return
  try {
    const reg = "serviceWorker" in navigator ? await navigator.serviceWorker.getRegistration(SW_SCOPE) : undefined
    if (reg) {
      await reg.showNotification(title, { body, tag, data: { url } })
      return
    }
  } catch {
    // fall through to the page notification
  }
  const n = new Notification(title, { body, tag })
  n.onclick = () => {
    window.focus()
    window.location.assign(url)
    n.close()
  }
}

// ── web push ────────────────────────────────────────────────────────────────

export const SW_URL = "/sw-push.js"
export const SW_SCOPE = "/"
/** Posted by the service worker to open tabs when a push lands (see sw-push.js). */
export const PUSH_MESSAGE = "moharrik-push"

export function pushSupported(): boolean {
  return typeof window !== "undefined" && "serviceWorker" in navigator && "PushManager" in window && typeof Notification !== "undefined"
}

function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4)
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"))
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export async function pushRegistration(): Promise<ServiceWorkerRegistration> {
  const existing = await navigator.serviceWorker.getRegistration(SW_SCOPE)
  if (existing) return existing
  await navigator.serviceWorker.register(SW_URL, { scope: SW_SCOPE })
  return navigator.serviceWorker.ready
}

/** This browser's current subscription, if any (no prompt, no registration). */
export async function currentSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null
  const reg = await navigator.serviceWorker.getRegistration(SW_SCOPE)
  return reg ? reg.pushManager.getSubscription() : null
}

/**
 * Subscribe this browser. The permission prompt, when there is one, comes from
 * pushManager.subscribe inside the click that called this.
 *
 * An existing subscription is reused only when it can still work: made with
 * the server's current VAPID key (a rotated pair makes the push service refuse
 * every message), and — when `known` is given — one the server still lists.
 * The server deletes a subscription the push service answered 404/410 for, so
 * one it no longer lists may be dead; re-registering that same endpoint would
 * fail the same way for ever. Dropping it costs another workspace on this
 * browser that shared it nothing it still had: its row answers 410 on the next
 * push and is removed the same way.
 */
export async function subscribePush(publicKey: string, known?: string[]): Promise<PushSubscriptionJSON> {
  const reg = await pushRegistration()
  const key = keyBytes(publicKey)
  let sub = await reg.pushManager.getSubscription()
  if (sub) {
    const current = sub.options.applicationServerKey ? new Uint8Array(sub.options.applicationServerKey) : null
    const same = current && current.length === key.length && current.every((b, i) => b === key[i])
    if (!same || (known && !known.includes(sub.endpoint))) {
      await sub.unsubscribe()
      sub = null
    }
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
  return sub.toJSON()
}
