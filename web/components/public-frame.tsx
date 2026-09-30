"use client"

import Link from "next/link"
import type { ReactNode } from "react"

import { ProductLogo } from "@fadymondy/nasaq/web"
import { cn } from "@/lib/utils"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import { useTranslations } from "@/lib/i18n"

/*
  The signed-out frame: a header with the product logo and the language / theme controls, a
  centred column with the eyebrow / title / description block, and a one-line footer. Built only
  from Nasaq tokens and type classes, so it follows the theme, RTL and the density like the shell.
*/
export function PublicFrame({
  eyebrow,
  title,
  description,
  children,
}: {
  eyebrow?: ReactNode
  title?: ReactNode
  description?: ReactNode
  children: ReactNode
}) {
  const { t, locale } = useTranslations()
  return (
    <div className="flex min-h-dvh flex-col bg-background text-foreground">
      <header className="sticky top-0 z-50 border-b border-border bg-background">
        <div className="mx-auto flex h-14 w-full max-w-4xl items-center gap-2 px-4">
          <Link href={`/${locale}`} className="flex items-center" aria-label="Moharrik">
            <ProductLogo brand="moharrik" size={24} />
          </Link>
          <div className="flex-1" />
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>

      <main className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-6 px-4 py-10">
        {title ? (
          <div className="space-y-2" data-slot="page-heading">
            {eyebrow ? <p className="eyebrow">{eyebrow}</p> : null}
            <h1 className="text-h1 text-balance">{title}</h1>
            {description ? (
              <p className="text-body text-pretty text-muted-foreground">{description}</p>
            ) : null}
          </div>
        ) : null}
        {children}
      </main>

      <footer className="border-t border-border">
        <div className="mx-auto flex w-full max-w-4xl flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 py-4 text-body-sm text-muted-foreground">
          <span>{t("footer.tagline")}</span>
          <a className="underline-offset-4 hover:underline" href="https://fadymondy.com" dir="ltr">
            fadymondy.com
          </a>
        </div>
      </footer>
    </div>
  )
}

/** One bordered section on a public page. */
export function PublicPanel({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-border bg-card">
      <div className={cn("p-4", className)}>{children}</div>
    </section>
  )
}

/** A section heading row for signed-in pages: title, optional description, optional action. */
export function SectionHeader({
  title,
  description,
  action,
  micro,
}: {
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  micro?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4 px-6 py-6">
      <div className="min-w-0 space-y-1.5">
        {micro ? <p className="eyebrow">{micro}</p> : null}
        <h1 className="font-medium tracking-tight text-foreground text-2xl">{title}</h1>
        {description ? (
          <p className="leading-relaxed text-nq-fg-body max-w-2xl text-sm">{description}</p>
        ) : null}
      </div>
      {action}
    </div>
  )
}
