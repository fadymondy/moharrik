// What the main process and the window say to each other (preload.ts carries it).
import type { Api, Locale, Target, Workspace } from "@moharrik/client"

export type CurrentWorkspace = Pick<Workspace, "id" | "slug" | "name" | "role" | "avatar_url">

export interface Prefs {
  theme: "system" | "light" | "dark"
  locale: Locale
  /** Native notifications for what the server marks alert:true. */
  notify: boolean
  sound: boolean
  /** The unread count on the taskbar / Dock icon. */
  badge: boolean
}

/** The session as the window sees it: never the token. */
export interface SessionOut {
  server: string
  email: string
  workspace: CurrentWorkspace | null
}

export type ApiMethod = keyof Api
export type ApiResult<T = unknown> = { ok: true; data: T } | { ok: false; status: number; message: string }

export interface Unread {
  conversations: number
  notifications: number
}

/** A live event, forwarded so open lists refresh (the window never opens the stream itself). */
export interface LiveOut {
  kind: string
  conversation_id?: string
  item_id?: string
}

export type { Target }

/** A screen of the window. Notifications and links arrive as a Target, which is one of these. */
export type Route =
  | Target
  | { screen: "contacts"; tab?: "contacts" | "companies" }
  | { screen: "contact"; id: string }
  | { screen: "company"; id: string }
  | { screen: "deals" }
  | { screen: "settings" }

export const INVOKE = [
  "app:info",
  "app:prefs",
  "app:setPrefs",
  "app:openExternal",
  "app:overlay",
  "app:screen",
  "session:get",
  "session:setServer",
  "session:signIn",
  "session:cancelSignIn",
  "session:signOut",
  "session:selectWorkspace",
  "api",
] as const

export const EVENTS = ["session", "prefs", "unread", "live", "navigate"] as const

export type InvokeChannel = (typeof INVOKE)[number]
export type EventChannel = (typeof EVENTS)[number]

export interface Bridge {
  platform: NodeJS.Platform
  invoke<T = unknown>(channel: InvokeChannel, ...args: unknown[]): Promise<T>
  on(channel: EventChannel, cb: (...args: never[]) => void): () => void
}
