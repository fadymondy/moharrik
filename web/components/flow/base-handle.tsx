/*
React Flow UI's base-handle (https://ui.reactflow.dev/base-handle), on the grid
tokens rather than slate: the dot a connection leaves from or arrives at.
*/
import type { ComponentProps } from "react"
import { Handle, type HandleProps } from "@xyflow/react"

import { cn } from "@/lib/utils"

export type BaseHandleProps = HandleProps

export function BaseHandle({ className, children, ...props }: ComponentProps<typeof Handle>) {
  return (
    <Handle
      {...props}
      className={cn(
        "size-[11px] rounded-full border border-grid-muted bg-grid-card transition",
        className,
      )}
    >
      {children}
    </Handle>
  )
}
