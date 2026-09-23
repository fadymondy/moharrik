import type { ComponentProps, ElementType } from "react"

import { cn } from "@/lib/utils"

/*
  Bidirectional text (MH-465). The UI runs RTL in /ar, but what people TYPE — names, titles,
  descriptions, keywords, subjects, snippets — can be English, Arabic, or both. Dropped into an
  RTL line as-is, English that ends in a period shows the period at the wrong end
  (".Moharrik Demo — Nile Coffee Co"), and a count that follows mixed-language text gets
  pulled into it ("…, البن · 0 عنصراً" read as part of the keyword list).

  The rule, everywhere user text is shown:
    - <Bdi>       wraps user text inline. A real <bdi>: isolated from its neighbours, its
                  direction taken from its own first strong character, while the line keeps
                  the UI's direction and alignment.
    - <BdiBlock>  for user text that IS the whole element and gets truncated (a row title, a
                  preview line). dir="auto" puts the ellipsis at the text's own end; the
                  .bidi-block class keeps it aligned with the UI, not with the text.
    - <Meta>      wraps counts, dates and other UI meta sitting on the same line as user
                  text, so they stay in the UI's order instead of joining the user text's run.
    - isolate()   the same isolation for user text interpolated into a translated STRING
                  (toasts, dialog bodies, document titles, aria-labels), where no element fits.
  A multi-line block of user text (a message, a note) takes dir="auto" with text-start, so
  its lines align with its own language.
*/

/** User-supplied text, isolated, with its direction taken from its own content. */
export function Bdi(props: ComponentProps<"bdi">) {
  return <bdi {...props} />
}

/** A whole element of user text (usually truncated): content direction, UI alignment. */
export function BdiBlock<T extends ElementType = "span">({ as, className, ...props }: { as?: T } & ComponentProps<T>) {
  const Tag = (as ?? "span") as ElementType
  return <Tag dir="auto" className={cn("bidi-block", className)} {...props} />
}

/** UI meta (counts, dates, separators) next to user text: isolated, in the UI direction. */
export function Meta({ style, ...props }: ComponentProps<"span">) {
  return <span style={{ unicodeBidi: "isolate", ...style }} {...props} />
}

/** FSI … PDI: <bdi> for plain strings. Empty stays empty so `||` fallbacks still work. */
export function isolate(s: string | null | undefined): string {
  return s ? `⁨${s}⁩` : ""
}
