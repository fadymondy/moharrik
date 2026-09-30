/*
React Flow UI's node-status-indicator (https://ui.reactflow.dev/node-status-indicator),
on the grid tokens: running spins a brand-coloured border round the node, done
is the ok colour, failed the danger colour. The fixed blues, emeralds and reds
of the original are gone, so both themes read correctly.

`radius` is the node's own corner radius, so the ring hugs a trigger node's
rounded side as well as a square step.
*/
import { type ReactNode } from "react"

import { cn } from "@/lib/utils"

export type NodeStatus = "loading" | "success" | "error" | "skipped" | "initial"

export type NodeStatusIndicatorProps = {
  status?: NodeStatus
  children: ReactNode
  /** Tailwind rounded-* classes matching the node. */
  radius?: string
}

function BorderLoadingIndicator({ children, radius }: { children: ReactNode; radius: string }) {
  return (
    <>
      <div className="pointer-events-none absolute -inset-[3px]" aria-hidden>
        <div className={cn("absolute inset-0 overflow-hidden", radius)}>
          <div className="flow-status-spinner rounded-full bg-[conic-gradient(from_0deg_at_50%_50%,var(--primary)_0deg,transparent_300deg)]" />
        </div>
      </div>
      {children}
    </>
  )
}

function StatusBorder({
  children,
  className,
  radius,
}: {
  children: ReactNode
  className?: string
  radius: string
}) {
  return (
    <>
      <div
        className={cn("pointer-events-none absolute -inset-[3px] border-2", radius, className)}
        aria-hidden
      />
      {children}
    </>
  )
}

export function NodeStatusIndicator({
  status,
  children,
  radius = "rounded-lg",
}: NodeStatusIndicatorProps) {
  switch (status) {
    case "loading":
      return <BorderLoadingIndicator radius={radius}>{children}</BorderLoadingIndicator>
    case "success":
      return (
        <StatusBorder radius={radius} className="border-nq-success">
          {children}
        </StatusBorder>
      )
    case "error":
      return (
        <StatusBorder radius={radius} className="border-nq-danger">
          {children}
        </StatusBorder>
      )
    case "skipped":
      return (
        <StatusBorder radius={radius} className="border-dashed border-nq-fg-muted/60">
          {children}
        </StatusBorder>
      )
    default:
      return <>{children}</>
  }
}
