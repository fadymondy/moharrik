/*
React Flow UI's base-node (https://ui.reactflow.dev/base-node), on the grid
tokens: the card and its rule come from --grid-card / --grid-line, a selected
node takes the brand ring. React Flow marks a selected node's wrapper with
`.selected`, which is what `in-[.selected]` reads.
*/
import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

export function BaseNode({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "relative rounded-md border border-nq-line bg-card text-foreground",
        "transition-shadow hover:ring-1 hover:ring-nq-fg-muted/40",
        "in-[.selected]:border-primary in-[.selected]:ring-2 in-[.selected]:ring-primary/30",
        className,
      )}
      tabIndex={0}
      {...props}
    />
  )
}

/** A header row inside a <BaseNode />. */
export function BaseNodeHeader({ className, ...props }: ComponentProps<"header">) {
  return (
    <header
      {...props}
      className={cn(
        "mx-0 my-0 -mb-1 flex flex-row items-center justify-between gap-2 px-3 py-2",
        className,
      )}
    />
  )
}

/** The node's title. Not selectable, as in a native canvas. */
export function BaseNodeHeaderTitle({ className, ...props }: ComponentProps<"h3">) {
  return (
    <h3
      data-slot="base-node-title"
      className={cn("flex-1 font-semibold select-none", className)}
      {...props}
    />
  )
}

export function BaseNodeContent({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="base-node-content"
      className={cn("flex flex-col gap-y-2 p-3", className)}
      {...props}
    />
  )
}

export function BaseNodeFooter({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="base-node-footer"
      className={cn(
        "flex flex-col items-center gap-y-2 border-t border-nq-line px-3 pt-2 pb-3",
        className,
      )}
      {...props}
    />
  )
}
