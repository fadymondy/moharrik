import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/**
 * Form layout pieces Nasaq has no component for (its Field is one labelled control; there is no
 * group, fieldset or plain label). They carry no look of their own beyond Nasaq's spacing and
 * typography tokens.
 */
export function FieldGroup({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="field-group" className={cn("flex w-full flex-col gap-5", className)} {...props} />
}

export function FieldSet({ className, ...props }: ComponentProps<"fieldset">) {
  return <fieldset data-slot="field-set" className={cn("flex flex-col gap-4", className)} {...props} />
}

export function FieldLegend({ className, ...props }: ComponentProps<"legend">) {
  return <legend data-slot="field-legend" className={cn("mb-1.5 text-label text-foreground", className)} {...props} />
}

export function Label({ className, ...props }: ComponentProps<"label">) {
  return (
    <label
      data-slot="label"
      className={cn("flex items-center gap-2 text-label text-foreground select-none peer-disabled:opacity-50", className)}
      {...props}
    />
  )
}
