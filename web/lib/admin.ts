"use client"

// The superadmin panel (MH-1184): contract types, SWR hooks and the mutating calls for /api/admin/*.
// Those routes answer 404 to anyone who is not a signed-in platform admin, and never take
// X-Workspace-ID — they act across every workspace.
import useSWR, { mutate } from "swr"

import { api, ApiError } from "@/lib/api"

const noRetryOn4xx = {
  shouldRetryOnError: (err: unknown) => !(err instanceof ApiError && err.status < 500),
  revalidateOnFocus: false,
}

export type Role = "owner" | "admin" | "manager" | "member" | "client"
export const ROLES: Role[] = ["owner", "admin", "manager", "member", "client"]

export type StatDay = { date: string; value: number }

export type PlatformStats = {
  users: {
    total: number
    new_30d: number
    active_7d: number
    active_30d: number
    admins: number
    suspended: number
  }
  workspaces: { total: number; new_30d: number; active_30d: number; suspended: number }
  records: {
    contacts: number
    companies: number
    deals: number
    leads: number
    conversations: number
    messages_30d: number
    campaigns: number
    automation_runs_30d: number
  }
  roles: Record<string, number>
  signups: StatDay[]
  new_workspaces: StatDay[]
  messages: StatDay[]
  top_workspaces: {
    id: string
    slug: string
    name: string
    messages_30d: number
    members: number
  }[]
}

export type AdminMembership = {
  workspace_id: string
  slug: string
  name: string
  role: Role
  status: string
}

export type AdminUser = {
  id: string
  email: string
  is_admin: boolean
  status: "active" | "suspended"
  suspended_reason?: string
  created_at: string
  last_seen_at: string | null
  workspaces: AdminMembership[]
}

export type AdminUserDetail = AdminUser & {
  sessions_valid_after: string | null
  mcp_tokens: number
}

export type AdminWorkspace = {
  id: string
  slug: string
  name: string
  plan: string
  status: "active" | "suspended"
  suspended_reason?: string
  owner_email: string
  members: number
  contacts: number
  conversations: number
  messages_30d: number
  last_active_at: string | null
  created_at: string
}

export type AdminMember = {
  user_id: string
  email: string
  role: Role
  status: string
  joined_at: string
  is_admin: boolean
  last_seen_at: string | null
}

export type AdminWorkspaceDetail = AdminWorkspace & {
  member_list: AdminMember[]
  counts: Record<string, number>
}

export type PlatformPermissions = {
  roles: Role[]
  actions: { action: string; group: string; min_role: Role | "" }[]
}

export type AdminAuditEntry = {
  id: string
  actor_id: string
  actor_email: string
  action: string
  target_type: string
  target_id: string
  target_label: string
  changes?: unknown
  ip: string
  via: string
  at: string
}

function qs(params: Record<string, string | number | undefined>) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") p.set(k, String(v))
  const s = p.toString()
  return s ? `?${s}` : ""
}

/** Whether the signed-in account is a platform admin. Everyone may ask. */
export function usePlatform(enabled = true) {
  return useSWR(
    enabled ? "/api/me/platform" : null,
    () => api<{ is_admin: boolean }>("/api/me/platform"),
    noRetryOn4xx,
  )
}

export function useAdminStats() {
  return useSWR("/api/admin/stats", () => api<PlatformStats>("/api/admin/stats"), noRetryOn4xx)
}

export function useAdminUsers(f: { q?: string; status?: string } = {}) {
  const path = `/api/admin/users${qs({ q: f.q, status: f.status, limit: 1000 })}`
  return useSWR(path, () => api<AdminUser[] | null>(path).then((r) => r ?? []), noRetryOn4xx)
}

export function useAdminUser(id: string | undefined) {
  const path = id ? `/api/admin/users/${encodeURIComponent(id)}` : null
  return useSWR(path, () => api<AdminUserDetail>(path!), noRetryOn4xx)
}

export function useAdminWorkspaces(f: { q?: string; status?: string } = {}) {
  const path = `/api/admin/workspaces${qs({ q: f.q, status: f.status, limit: 1000 })}`
  return useSWR(path, () => api<AdminWorkspace[] | null>(path).then((r) => r ?? []), noRetryOn4xx)
}

export function useAdminWorkspace(id: string | undefined) {
  const path = id ? `/api/admin/workspaces/${encodeURIComponent(id)}` : null
  return useSWR(path, () => api<AdminWorkspaceDetail>(path!), noRetryOn4xx)
}

export function useAdminPermissions() {
  return useSWR(
    "/api/admin/permissions",
    () => api<PlatformPermissions>("/api/admin/permissions"),
    noRetryOn4xx,
  )
}

export function useAdminAudit(f: { q?: string; target?: string } = {}) {
  const path = `/api/admin/audit${qs({ q: f.q, target: f.target, limit: 500 })}`
  return useSWR(path, () => api<AdminAuditEntry[] | null>(path).then((r) => r ?? []), noRetryOn4xx)
}

/** After a change: every admin list, detail and the stats may show it. */
export function refreshAdmin() {
  return mutate(
    (key) =>
      typeof key === "string" && (key.startsWith("/api/admin/") || key === "/api/me/platform"),
  )
}

const enc = encodeURIComponent

async function run<T>(p: Promise<T>): Promise<T> {
  const out = await p
  await refreshAdmin()
  return out
}

export const adminActions = {
  suspendUser: (id: string, reason: string) =>
    run(
      api<AdminUserDetail>(`/api/admin/users/${enc(id)}/suspend`, {
        method: "POST",
        json: { reason },
      }),
    ),
  reactivateUser: (id: string) =>
    run(api<AdminUserDetail>(`/api/admin/users/${enc(id)}/reactivate`, { method: "POST" })),
  signOutUser: (id: string) =>
    run(api<AdminUserDetail>(`/api/admin/users/${enc(id)}/sign-out`, { method: "POST" })),
  setAdmin: (id: string, admin: boolean) =>
    run(
      api<AdminUserDetail>(`/api/admin/users/${enc(id)}/admin`, { method: "PUT", json: { admin } }),
    ),
  suspendWorkspace: (id: string, reason: string) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/suspend`, {
        method: "POST",
        json: { reason },
      }),
    ),
  reactivateWorkspace: (id: string) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/reactivate`, { method: "POST" }),
    ),
  setPlan: (id: string, plan: string) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/plan`, {
        method: "PUT",
        json: { plan },
      }),
    ),
  deleteWorkspace: (id: string, confirm: string) =>
    run(api(`/api/admin/workspaces/${enc(id)}${qs({ confirm })}`, { method: "DELETE" })),
  addMember: (id: string, user: string, role: Role) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/members`, {
        method: "POST",
        json: { user, role },
      }),
    ),
  setMemberRole: (id: string, user: string, role: Role) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/members/${enc(user)}`, {
        method: "PUT",
        json: { role },
      }),
    ),
  removeMember: (id: string, user: string) =>
    run(
      api<AdminWorkspaceDetail>(`/api/admin/workspaces/${enc(id)}/members/${enc(user)}`, {
        method: "DELETE",
      }),
    ),
}

/** The message of a failed call, for a toast or an inline error. */
export function errorText(err: unknown, fallback: string) {
  return err instanceof Error && err.message ? err.message : fallback
}
