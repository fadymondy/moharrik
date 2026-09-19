"use client"

import { createContext, useContext } from "react"

import type { CurrentWorkspace, WorkspaceSummary } from "@/lib/api"

export type WorkspaceCtx = {
  /** Resolved from the URL slug via /api/me/workspaces. */
  summary: WorkspaceSummary
  /** /api/w/current — undefined while loading. */
  current?: CurrentWorkspace
  id: string
  slug: string
  can: (permission: string) => boolean
}

export const WorkspaceContext = createContext<WorkspaceCtx | null>(null)

export function useWorkspace(): WorkspaceCtx {
  const ctx = useContext(WorkspaceContext)
  if (!ctx) throw new Error("useWorkspace must be used inside the workspace shell")
  return ctx
}
