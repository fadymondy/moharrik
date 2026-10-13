// Push on the phone through Firebase Cloud Messaging (APNs is reached via Firebase), the same
// shape as Zekra's src/features/push/push.ts.
//
// - Permission is asked only when the member turns push on in Settings: iOS gives one prompt, and
//   spending it at launch is how an app ends up permanently denied.
// - After that, every launch and every FCM token rotation re-registers silently at
//   POST /api/me/devices — FCM rotates tokens without telling anyone.
// - The server decides WHAT is pushed (per-workspace prefs, /api/w/push/prefs); the app only
//   registers and routes: tapping a notification opens its conversation (@moharrik/client links).
//
// Every entry point is a no-op in a build without the Firebase config files.
import type { RemoteMessage } from "@react-native-firebase/messaging"
import * as Application from "expo-application"
import * as Device from "expo-device"
import { PermissionsAndroid, Platform } from "react-native"

import { targetOfPush, type Api, type Locale, type Target } from "@moharrik/client"

import { firebaseReady, rnfbMessaging } from "./firebase"
import { getJSON, KEYS, setJSON } from "./storage"

export type PushState = "unavailable" | "off" | "denied" | "on"

type MessagingModule = typeof import("@react-native-firebase/messaging")
const fm = () => rnfbMessaging() as MessagingModule

function messaging() {
  const mod = rnfbMessaging()
  if (!firebaseReady() || !mod) return null
  try {
    return mod.getMessaging()
  } catch {
    return null
  }
}

async function permission(ask: boolean): Promise<"granted" | "denied" | "undetermined"> {
  const m = messaging()
  if (!m) return "denied"
  if (Platform.OS === "android") {
    if (typeof Platform.Version === "number" && Platform.Version < 33) return "granted"
    const perm = PermissionsAndroid.PERMISSIONS.POST_NOTIFICATIONS
    if (await PermissionsAndroid.check(perm)) return "granted"
    if (!ask) return "undetermined"
    const r = await PermissionsAndroid.request(perm)
    return r === PermissionsAndroid.RESULTS.GRANTED ? "granted" : r === PermissionsAndroid.RESULTS.NEVER_ASK_AGAIN ? "denied" : "undetermined"
  }
  const S = fm().AuthorizationStatus
  const s = ask ? await fm().requestPermission(m) : await fm().hasPermission(m)
  if (s === S.AUTHORIZED || s === S.PROVISIONAL || s === S.EPHEMERAL) return "granted"
  return s === S.DENIED ? "denied" : "undetermined"
}

async function register(api: Api, locale: Locale): Promise<void> {
  const m = messaging()
  if (!m) return
  const token = await fm().getToken(m)
  if (!token) return
  await api.registerDevice({
    platform: Platform.OS === "ios" ? "ios" : "android",
    token,
    name: Device.deviceName || Device.modelName || undefined,
    app_version: `${Application.nativeApplicationVersion ?? ""} (${Application.nativeBuildVersion ?? ""})`,
    locale,
  })
  await setJSON(KEYS.pushToken, token)
}

export async function pushState(): Promise<PushState> {
  if (!messaging()) return "unavailable"
  const [p, token] = await Promise.all([permission(false), getJSON<string>(KEYS.pushToken)])
  if (p === "denied") return "denied"
  return p === "granted" && token ? "on" : "off"
}

/** The member's explicit "turn push on": asks (once, here), then registers this device. */
export async function enablePush(api: Api, locale: Locale): Promise<PushState> {
  if (!messaging()) return "unavailable"
  const p = await permission(true)
  if (p !== "granted") return p === "denied" ? "denied" : "off"
  await register(api, locale)
  return "on"
}

/** Stops pushes to this phone (the device token stays signed in). */
export async function disablePush(api: Api): Promise<void> {
  const m = messaging()
  const devices = await api.devices().catch(() => null)
  const current = devices?.devices?.find((d) => d.current)
  if (current) await api.removeDevice(current.id).catch(() => {})
  if (m) await fm().deleteToken(m).catch(() => {})
  await setJSON(KEYS.pushToken, null)
}

/**
 * Keeps the registration fresh and routes taps. Returns the unsubscribe. onOpen gets the target
 * of a tapped notification — including the one that launched the app.
 */
export function watchPush(o: {
  api: Api
  locale: Locale
  onOpen: (t: Target) => void
  onForeground: (m: { title: string; body: string; target: Target | null }) => void
}): () => void {
  const m = messaging()
  if (!m) return () => {}
  const offs: (() => void)[] = []
  void (async () => {
    if ((await getJSON<string>(KEYS.pushToken)) && (await permission(false)) === "granted") {
      await register(o.api, o.locale).catch(() => {})
    }
    const first = await fm().getInitialNotification(m)
    const t = first ? targetOf(first) : null
    if (t) o.onOpen(t)
  })()
  offs.push(
    fm().onTokenRefresh(m, () => {
      void getJSON<string>(KEYS.pushToken).then((had) => (had ? register(o.api, o.locale).catch(() => {}) : undefined))
    }),
    fm().onNotificationOpenedApp(m, (msg) => {
      const t = targetOf(msg)
      if (t) o.onOpen(t)
    }),
    // In the foreground the OS shows nothing; the app shows its own banner.
    fm().onMessage(m, (msg) => {
      o.onForeground({ title: msg.notification?.title ?? "", body: msg.notification?.body ?? "", target: targetOf(msg) })
    }),
  )
  return () => offs.forEach((f) => f())
}

function targetOf(msg: RemoteMessage): Target | null {
  const d: Record<string, string> = {}
  for (const [k, v] of Object.entries(msg.data ?? {})) if (typeof v === "string") d[k] = v
  return targetOfPush(d)
}
