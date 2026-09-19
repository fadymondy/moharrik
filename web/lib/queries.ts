"use client"

import useSWR from "swr"

import {
  api,
  ApiError,
  type CurrentWorkspace,
  type Invitation,
  type McpScope,
  type McpToken,
  type Member,
  type User,
  type WorkspaceSummary,
} from "@/lib/api"

const noRetryOn4xx = {
  shouldRetryOnError: (err: unknown) => !(err instanceof ApiError && err.status < 500),
  revalidateOnFocus: false,
}

/** The signed-in user, `null` when signed out (401), undefined while loading. */
export function useMe() {
  return useSWR<User | null>(
    "/api/auth/me",
    async () => {
      try {
        return await api<User>("/api/auth/me")
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null
        throw err
      }
    },
    noRetryOn4xx,
  )
}

export function useMyWorkspaces(enabled = true) {
  return useSWR(
    enabled ? "/api/me/workspaces" : null,
    () => api<{ workspaces: WorkspaceSummary[] | null }>("/api/me/workspaces").then((r) => r.workspaces ?? []),
    noRetryOn4xx,
  )
}

function useScoped<T>(path: string, workspaceId: string | null | undefined, enabled = true) {
  return useSWR<T>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<T>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

export const useCurrentWorkspace = (id?: string | null) => useScoped<CurrentWorkspace>("/api/w/current", id)

export const useMembers = (id?: string | null) => useScoped<{ members: Member[] | null }>("/api/w/members", id)

export const useInvitations = (id?: string | null, enabled = true) =>
  useScoped<{ invitations: Invitation[] | null }>("/api/w/invitations", id, enabled)

export const useMcpTokens = (id?: string | null) =>
  useScoped<{ tokens: McpToken[] | null; scopes: McpScope[] | null }>("/api/w/mcp_tokens", id)
