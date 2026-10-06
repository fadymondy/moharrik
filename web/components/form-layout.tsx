import type { ComponentProps } from "react"

import { cn } from "@/lib/utils"

/**
 * Form layout pieces Nasaq has no component for (its Field is one labelled control; there is no
 * group, fieldset or plain label). They carry no look of their own beyond Nasaq's spacing and
 * typography tokens.
 */
export function FieldGroup({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="field-group"
      className={cn("flex w-full flex-col gap-5", className)}
      {...props}
    />
  )
}

export function FieldSet({ className, ...props }: ComponentProps<"fieldset">) {
  return (
    <fieldset data-slot="field-set" className={cn("flex flex-col gap-4", className)} {...props} />
  )
}

export function FieldLegend({ className, ...props }: ComponentProps<"legend">) {
  return (
    <legend
      data-slot="field-legend"
      className={cn("mb-1.5 text-label text-foreground", className)}
      {...props}
    />
  )
}

export function Label({ className, ...props }: ComponentProps<"label">) {
  return (
    <label
      data-slot="label"
      className={cn(
        "flex items-center gap-2 text-label text-foreground select-none peer-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  )
}

/**
 * An error line that is always shown. Nasaq's FieldError (Base UI) renders only inside a Field and
 * only for a matching validity flag; these screens keep the error in state and render it when set,
 * often outside any Field (a form-level failure under the buttons).
 */
export function FieldError({ className, ...props }: ComponentProps<"p">) {
  return (
    <p
      role="alert"
      data-slot="field-error"
      className={cn("text-caption text-nq-danger-text", className)}
      {...props}
    />
  )
}

/** A helper line for places that have no surrounding Nasaq Field to link it to. */
export function FormHint({ className, ...props }: ComponentProps<"p">) {
  return (
    <p
      data-slot="form-hint"
      className={cn("text-caption text-muted-foreground", className)}
      {...props}
    />
  )
}
