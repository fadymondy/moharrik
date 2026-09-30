"use client"

import { useState } from "react"
import { ChevronDownIcon, ExternalLinkIcon, TriangleAlertIcon } from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@fadymondy/nasaq/web"
import { useTranslations } from "@/lib/i18n"

/**
 * One setup guide's step (MH-594). `titleKey`/`bodyKey`/`warningKey` are i18n
 * keys — the copy always goes through en/ar, never inline text — while a
 * link's `href` and visible `text` stay literal: a URL, a product name or a
 * scope name is not something to translate, and per the RTL rules it must
 * render LTR regardless of locale.
 */
export type SetupGuideStep = {
  titleKey: string
  bodyKey?: string
  /** Renders as a callout, not a plain line — for the one or two steps that silently break the setup if skipped. */
  warningKey?: string
  links?: { href: string; text: string }[]
}

/**
 * A numbered, collapsible setup guide for one channel type (MH-594: "there is
 * no setup steps on each channel!"). Every *-channel-form renders one of
 * these, above its fields, with that type's exact steps — naming the exact
 * fields the form below asks for, so nobody has to guess which box a token
 * or a secret goes in.
 */
export function SetupGuide({
  id,
  titleKey,
  steps,
  defaultOpen,
}: {
  id: string
  titleKey: string
  steps: SetupGuideStep[]
  defaultOpen: boolean
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(defaultOpen)

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="border border-nq-line bg-card"
      data-setup-guide={id}
    >
      <CollapsibleTrigger className="group flex w-full items-center gap-2 px-4 py-3 text-start text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
        <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-aria-expanded:rotate-180" />
        <span className="flex-1">{t(titleKey)}</span>
      </CollapsibleTrigger>
      <CollapsiblePanel>
        <ol className="list-decimal space-y-4 px-4 pb-4 ps-9 text-sm marker:font-medium marker:text-muted-foreground">
          {steps.map((step, i) => (
            <li key={i} className="ps-1">
              <p className="font-medium text-foreground">{t(step.titleKey)}</p>
              {step.bodyKey ? <p className="mt-0.5 text-muted-foreground">{t(step.bodyKey)}</p> : null}
              {step.warningKey ? (
                <p
                  className="mt-1.5 flex items-start gap-2 border border-nq-danger-text/40 bg-nq-danger-text/5 px-3 py-2 text-nq-danger-text"
                  role="alert"
                  data-setup-warning
                >
                  <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
                  <span>{t(step.warningKey)}</span>
                </p>
              ) : null}
              {step.links?.length ? (
                <p className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1">
                  {step.links.map((link) => (
                    <a
                      key={link.href}
                      href={link.href}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1 text-nq-brand underline underline-offset-4"
                    >
                      <Ltr>{link.text}</Ltr>
                      <ExternalLinkIcon className="size-3" />
                    </a>
                  ))}
                </p>
              ) : null}
            </li>
          ))}
        </ol>
      </CollapsiblePanel>
    </Collapsible>
  )
}
