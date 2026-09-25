"use client"

import Link from "next/link"
import type { ReactNode } from "react"

import { cn } from "@/lib/utils"
import { CubeMark } from "@/components/brand/cube-mark"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import { MOHARRIK_MARK } from "@/lib/brand/mark"
import { useTranslations } from "@/lib/i18n"

/*
  The signed-out frame, copied from fadymondy.com's /en/login anatomy
  (web/app/[locale]/login/page.tsx -> PageShell + Panel + LoginForm):

    sticky full-width header: 860px column bounded by hairlines, mark + wordmark, toggles
    a 40px spacer, then the eyebrow / title / description band between full-bleed rules
    one Panel with the content centred (forms at max-w-sm, 384px)
    a filler that keeps the column rules running down to the footer
    the footer: a hatch band and a title-block row, like the reference's CAD footer

  Full-bleed rules are ::before/::after at z-index -1, so the root isolates a stacking context.
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
    <div className="relative isolate flex min-h-dvh flex-col overflow-x-clip">
      <header className="sticky top-0 z-50 bg-background px-2">
        <div className="grid-screen-line-top grid-screen-line-bottom mx-auto flex h-14 max-w-[860px] items-center gap-2 border-x border-line ps-4 pe-2">
          <Link href={`/${locale}`} className="flex items-center gap-2.5" aria-label="Moharrik">
            <CubeMark mark={MOHARRIK_MARK} size={22} />
            <span className="text-[15px] font-medium">{t("brand.name")}</span>
          </Link>
          <div className="flex-1" />
          <LanguageSwitcher />
          <ThemeToggle />
        </div>
      </header>

      <main className="flex flex-1 flex-col px-2">
        <div className="mx-auto flex w-full max-w-[860px] flex-1 flex-col">
          <div className="h-10 border-x border-line" />
          {title ? (
            <div className="border-x border-line" data-slot="page-heading">
              {eyebrow ? (
                <div className="grid-mono px-4 pb-2 text-sm/none tracking-wider text-grid-muted">
                  {eyebrow}
                </div>
              ) : null}
              <h1 className="grid-screen-line-top grid-screen-line-bottom px-4 text-3xl font-medium tracking-tight text-balance">
                {title}
              </h1>
              {description ? (
                <p className="grid-screen-line-bottom p-4 text-base text-pretty text-grid-muted">
                  {description}
                </p>
              ) : null}
            </div>
          ) : null}
          {children}
          <div className="flex-1 border-x border-line" />
        </div>
      </main>

      <footer className="px-2">
        <div className="mx-auto max-w-[860px] border-x border-line">
          <div className="grid-screen-line-top grid-screen-line-bottom">
            <div className="grid-hatch h-12" />
          </div>
          <div className="grid-screen-line-bottom flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 py-3 text-sm">
            <span className="grid-mono font-medium">{t("brand.name")}</span>
            <span className="text-grid-muted">{t("footer.tagline")}</span>
          </div>
          <dl className="grid grid-cols-2 md:grid-cols-4">
            <FooterCell label={t("footer.product")}>{t("footer.productValue")}</FooterCell>
            <FooterCell label={t("footer.craftedBy")}>
              <a
                className="underline-offset-4 hover:underline"
                href="https://fadymondy.com"
                dir="ltr"
              >
                fadymondy.com
              </a>
            </FooterCell>
            <FooterCell label={t("footer.stack")}>
              <span dir="ltr">Go · Next.js</span>
            </FooterCell>
            <FooterCell label={t("footer.languages")}>English · العربية</FooterCell>
          </dl>
          <div className="grid-screen-line-top grid-screen-line-bottom mt-4 flex items-center px-4 py-3 text-grid-muted">
            <Link href={`/${locale}`} aria-label="Moharrik">
              <CubeMark mark={MOHARRIK_MARK} size={16} />
            </Link>
          </div>
        </div>
      </footer>
    </div>
  )
}

function FooterCell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 border-e border-b border-line px-4 py-3 last:border-e-0 max-md:nth-[2n]:border-e-0">
      <dt className="grid-micro">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  )
}

/** One bordered section between full-bleed rules (the reference's Panel). */
export function PublicPanel({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <section className="grid-screen-line-top grid-screen-line-bottom border-x border-line">
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
        {micro ? <p className="grid-micro">{micro}</p> : null}
        <h1 className="grid-title text-2xl">{title}</h1>
        {description ? <p className="grid-body max-w-2xl text-sm">{description}</p> : null}
      </div>
      {action}
    </div>
  )
}
