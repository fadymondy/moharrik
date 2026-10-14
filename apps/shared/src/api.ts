// The calls the apps make, named. Each is one typed MoharrikClient.call, so the request and
// response shapes are the generated ones; screens import these rather than spelling paths.
import type { MoharrikClient } from "./client"
import type { ConversationPatch, DealFields, OrganisePatch, PushPrefs, RegisterDevice } from "./types"

/** The web's User (web/lib/api.ts). */
export interface Me {
  id: string | number
  email: string
  roles?: string[] | null
}

export type ConversationFilter = {
  status?: "" | "open" | "snoozed" | "closed"
  unread?: boolean
  channel?: string
  q?: string
  label_id?: string
  cursor?: string
  limit?: number
}

export function api(c: MoharrikClient) {
  return {
    // Account
    /** togo's auth module serves this outside the OpenAPI document. */
    me: () => c.raw<Me>("get", "/api/auth/me", { workspace: null }),
    workspaces: () => c.call("get", "/api/me/workspaces", { workspace: null }),
    /** Creates a workspace; the signed-in user becomes its owner. */
    createWorkspace: (name: string) => c.call("post", "/api/me/workspaces", { workspace: null, body: { name } }),
    signOut: () => c.call("post", "/api/me/app-signout", { workspace: null }),
    devices: () => c.call("get", "/api/me/devices", { workspace: null }),
    registerDevice: (d: RegisterDevice) => c.call("post", "/api/me/devices", { body: d, workspace: null }),
    removeDevice: (id: string) => c.call("delete", "/api/me/devices/{id}", { path: { id }, workspace: null }),
    signInMethods: () => c.call("get", "/api/me/identities", { workspace: null }),
    /** What deleting the account would do (workspaces deleted with it, ones blocking it, fresh). */
    accountDeletion: () => c.call("get", "/api/me/account/deletion", { workspace: null }),
    /** Deletes the account for good. confirm is its e-mail; password unless signed in recently. */
    deleteAccount: (confirm: string, password?: string) =>
      c.call("post", "/api/me/account/delete", { workspace: null, body: { confirm, password } }),

    // Inbox
    conversations: (f: ConversationFilter = {}) =>
      c.call("get", "/api/w/conversations", { query: { order: "recent", limit: 40, ...f } }),
    conversation: (id: string) => c.call("get", "/api/w/conversations/{id}", { path: { id } }),
    messages: (id: string, cursor?: string) =>
      c.call("get", "/api/w/conversations/{id}/messages", { path: { id }, query: { cursor, limit: 50 } }),
    /** Sends a REAL message to the customer on the conversation's channel. */
    reply: (id: string, body: string, replyTo?: string) =>
      c.call("post", "/api/w/conversations/{id}/reply", { path: { id }, body: { body, reply_to_message_id: replyTo } }),
    updateConversation: (id: string, patch: ConversationPatch) =>
      c.call("patch", "/api/w/conversations/{id}", { path: { id }, body: patch }),
    organise: (id: string, patch: OrganisePatch) =>
      c.call("patch", "/api/w/conversations/{id}/organise", { path: { id }, body: patch }),
    /** Blocks (or unblocks) the customer: their messages are dropped and replies are off. Needs manager or above. */
    setBlocked: (id: string, blocked: boolean) =>
      c.call("patch", "/api/w/conversations/{id}/organise", { path: { id }, body: { blocked } }),
    labels: () => c.call("get", "/api/w/conversation_labels"),
    members: () => c.call("get", "/api/w/members"),

    // Alerts and the notification center
    bell: () => c.call("get", "/api/w/alerts"),
    notifications: (q: { view?: "" | "actionable" | "snoozed" | "resolved" | "all"; unread?: boolean; cursor?: string } = {}) =>
      c.call("get", "/api/w/notifications", { query: { limit: 40, ...q } }),
    markNotification: (id: string, read: boolean) =>
      c.call("patch", "/api/w/notifications/{id}", { path: { id }, body: { read } }),
    markAllNotificationsRead: () => c.call("post", "/api/w/notifications/mark_all_read"),

    // CRM
    contacts: (q?: string, cursor?: string) => c.call("get", "/api/w/contacts", { query: { q, cursor, limit: 40 } }),
    contact: (id: string) => c.call("get", "/api/w/contacts/{id}", { path: { id } }),
    timeline: (id: string, cursor?: string) =>
      c.call("get", "/api/w/contacts/{id}/timeline", { path: { id }, query: { cursor, limit: 40 } }),
    companies: (q?: string, cursor?: string) => c.call("get", "/api/w/companies", { query: { q, cursor, limit: 40 } }),
    company: (id: string) => c.call("get", "/api/w/companies/{id}", { path: { id } }),
    deals: (stageId?: string, cursor?: string) =>
      c.call("get", "/api/w/deals", { query: { view: "board", stage_id: stageId, cursor } }),
    stages: () => c.call("get", "/api/w/pipeline/stages"),
    updateDeal: (id: string, fields: Partial<DealFields>) =>
      c.call("patch", "/api/w/deals/{id}", { path: { id }, body: fields as DealFields }),

    // Push preferences (per workspace)
    pushPrefs: () => c.call("get", "/api/w/push/prefs"),
    setPushPrefs: (p: PushPrefs) => c.call("put", "/api/w/push/prefs", { body: p }),
  }
}

export type Api = ReturnType<typeof api>
