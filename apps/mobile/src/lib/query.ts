// One QueryClient for the app. Screens refetch when the app returns to the foreground (React
// Query's focus manager listens to window focus, which a phone doesn't have) and on reconnect.
import { focusManager, onlineManager, QueryClient } from "@tanstack/react-query"
import { AppState, Platform } from "react-native"

import { ApiError } from "@moharrik/client"

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      // A refused request (4xx) won't succeed on retry; a dropped connection might.
      retry: (n, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && n < 2,
    },
  },
})

if (Platform.OS !== "web") {
  AppState.addEventListener("change", (s) => focusManager.setFocused(s === "active"))
  onlineManager.setOnline(true)
}

/** Query keys, scoped by workspace so switching never shows the previous one's data. */
export const qk = {
  workspaces: ["workspaces"] as const,
  bell: (ws: string) => ["bell", ws] as const,
  conversations: (ws: string, f: object) => ["conversations", ws, f] as const,
  conversation: (ws: string, id: string) => ["conversation", ws, id] as const,
  labels: (ws: string) => ["labels", ws] as const,
  members: (ws: string) => ["members", ws] as const,
  notifications: (ws: string, unread: boolean) => ["notifications", ws, unread] as const,
  contacts: (ws: string, q: string) => ["contacts", ws, q] as const,
  contact: (ws: string, id: string) => ["contact", ws, id] as const,
  timeline: (ws: string, id: string) => ["timeline", ws, id] as const,
  companies: (ws: string, q: string) => ["companies", ws, q] as const,
  company: (ws: string, id: string) => ["company", ws, id] as const,
  deals: (ws: string, stage: string) => ["deals", ws, stage] as const,
  pushPrefs: (ws: string) => ["pushPrefs", ws] as const,
  me: ["me"] as const,
}
