"use client"

// MH-1157: the record page frame shared by contacts, companies and leads, after Mahaam's project
// header. A cover band in the brand colour (the record's photo, blurred, when it has one), the
// avatar over the cover's edge, the name, its primary actions, the key facts as chips, and the
// record's tabs along the bottom edge. Each tab is its own route, so the sections that used to
// be stacked on one long page each get a page of their own.
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useEffect, type ReactNode } from "react"
import { ChevronLeftIcon, type LucideIcon } from "lucide-react"
import { Badge, Skeleton, Tabs, TabsList, TabsTab } from "@fadymondy/nasaq/web"

import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type RecordTab = {
  value: string
  label: string
  icon: LucideIcon
  href: string
  count?: number
}

const COVER =
  "linear-gradient(135deg, color-mix(in oklab, var(--primary) 34%, var(--card)), color-mix(in oklab, var(--primary) 8%, var(--card)))"

/** A key fact under the name: an icon and a short value, as a pill. */
export function RecordChip({
  icon,
  children,
  className,
  title,
}: {
  icon?: ReactNode
  children: ReactNode
  className?: string
  title?: string
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex h-7 items-center gap-1.5 rounded-full border border-border bg-background/60 px-2.5 text-xs whitespace-nowrap text-nq-fg-body [&_svg]:size-3.5 [&_svg]:text-muted-foreground",
        className,
      )}
    >
      {icon}
      {children}
    </span>
  )
}

/** The record's picture, or its initials (or an icon) on the brand tint. */
export function RecordAvatar({
  name,
  src,
  icon,
  className,
}: {
  name: string
  src?: string | null
  icon?: ReactNode
  className?: string
}) {
  const initials = name
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0] ?? "")
    .join("")
    .toUpperCase()
  return (
    <span
      className={cn(
        "relative flex size-18 shrink-0 items-center justify-center overflow-hidden rounded-xl bg-card text-xl font-semibold text-primary shadow-sm ring-4 ring-card sm:size-22 sm:text-2xl [&_svg]:size-8",
        className,
      )}
      data-record-avatar
    >
      {src ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={src} alt="" className="size-full object-cover" />
      ) : (
        <span className="flex size-full items-center justify-center bg-primary/12">
          {icon ?? initials ?? "?"}
        </span>
      )}
    </span>
  )
}

export function RecordHeader({
  back,
  avatar,
  coverSrc,
  title,
  subtitle,
  status,
  actions,
  description,
  chips,
  meta,
  tabs,
  active,
}: {
  back: { href: string; label: string }
  avatar: ReactNode
  coverSrc?: string | null
  title: ReactNode
  subtitle?: ReactNode
  /** Pills on the cover's top end: the record's state. */
  status?: ReactNode
  actions?: ReactNode
  description?: ReactNode
  chips?: ReactNode
  /** Right end of the chip row: "Since …", "Updated …". */
  meta?: ReactNode
  tabs: RecordTab[]
  active: string
}) {
  const { isRtl } = useTranslations()
  return (
    // md:-mx-page: AppMain keeps its md:px-page gutter under p-0, so the cover would stop short of
    // both edges. The record runs edge to edge, as Mahaam's project header does.
    <header className="border-b border-border bg-card md:-mx-page" data-record-header>
      <div
        className="relative h-28 overflow-hidden sm:h-36"
        style={{ background: COVER }}
        data-record-cover
      >
        {coverSrc ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={coverSrc}
            alt=""
            aria-hidden
            className="absolute inset-0 size-full scale-125 object-cover opacity-40 blur-2xl"
          />
        ) : null}
        <div
          aria-hidden
          className="absolute inset-0 bg-[radial-gradient(circle_at_1px_1px,var(--border)_1px,transparent_0)] bg-size-[14px_14px] opacity-60"
        />
        <Link
          href={back.href}
          className="absolute start-4 top-4 inline-flex items-center gap-1 rounded-full bg-background/75 px-2.5 py-1 text-xs text-muted-foreground backdrop-blur-sm hover:text-foreground sm:start-6"
          data-record-back
        >
          <ChevronLeftIcon className={isRtl ? "size-3.5 rotate-180" : "size-3.5"} />
          {back.label}
        </Link>
        {status ? (
          <div className="absolute end-4 top-4 flex items-center gap-1.5 sm:end-6 [&>*]:bg-background/75 [&>*]:backdrop-blur-sm">
            {status}
          </div>
        ) : null}
      </div>

      <div className="px-4 sm:px-6">
        {/* Only the avatar rides over the cover's edge; the name and actions start below it
            (pt = the avatar's overlap + a gap), so a long name or a subtitle never runs into the cover. */}
        <div className="-mt-9 flex flex-wrap items-start gap-x-4 gap-y-3 sm:-mt-11">
          {avatar}
          <div className="min-w-0 flex-1 basis-60 pt-12 sm:pt-14">
            <h1 className="min-w-0 text-2xl font-semibold break-words" dir="auto" data-record-title>
              {title}
            </h1>
            {subtitle ? <div className="min-w-0 text-sm text-nq-fg-body">{subtitle}</div> : null}
          </div>
          {actions ? (
            <div className="flex flex-wrap items-center gap-2 sm:pt-14" data-record-actions>
              {actions}
            </div>
          ) : null}
        </div>

        {description ? (
          <div className="mt-3 line-clamp-2 max-w-3xl text-sm text-muted-foreground" dir="auto">
            {description}
          </div>
        ) : null}

        <div className="mt-3 flex flex-wrap items-center gap-2" data-record-chips>
          {chips}
          {meta ? <span className="ms-auto text-xs text-muted-foreground">{meta}</span> : null}
        </div>

        {/* Shifted 1px over the header's bottom border so the active tab covers it and opens into the page. */}
        <nav
          className="relative top-px mt-4 overflow-x-auto"
          aria-label={typeof title === "string" ? title : undefined}
          data-record-tabs
        >
          <Tabs value={active}>
            <TabsList variant="underline" className="w-max min-w-full gap-1 border-b-0">
              {tabs.map((tab) => {
                const Icon = tab.icon
                return (
                  <TabsTab
                    key={tab.value}
                    value={tab.value}
                    nativeButton={false}
                    render={<Link href={tab.href} />}
                    className="h-10 rounded-t-lg border border-b-0 border-transparent px-3.5 hover:bg-background/60 data-active:border-border data-active:bg-background"
                    data-tab={tab.value}
                  >
                    <Icon />
                    {tab.label}
                    {tab.count ? (
                      <Badge variant="neutral" className="ms-0.5">
                        {tab.count}
                      </Badge>
                    ) : null}
                  </TabsTab>
                )
              })}
            </TabsList>
          </Tabs>
        </nav>
      </div>
    </header>
  )
}

/** The page under the header: a padded column the tabs fill. */
export function RecordBody({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-4 px-4 py-6 sm:px-6 md:-mx-page", className)}>
      {children}
    </div>
  )
}

/** A number on the overview: an icon chip, its label, the value. Links to where it comes from. */
export function RecordStat({
  icon,
  label,
  value,
  href,
  hint,
}: {
  icon: ReactNode
  label: string
  value: ReactNode
  href?: string
  hint?: ReactNode
}) {
  const body = (
    <>
      <div className="flex items-center gap-2.5 text-sm text-muted-foreground">
        <span
          className="flex size-8 items-center justify-center rounded-lg bg-nq-surface-soft [&_svg]:size-4"
          aria-hidden
        >
          {icon}
        </span>
        {label}
      </div>
      <div className="mt-3 text-2xl font-semibold tabular-nums">{value}</div>
      {hint ? <div className="mt-1 text-xs text-muted-foreground">{hint}</div> : null}
    </>
  )
  const cls = "block rounded-xl border border-border bg-card p-4"
  return href ? (
    <Link
      href={href}
      className={cn(cls, "transition-colors hover:border-primary/50")}
      data-record-stat
    >
      {body}
    </Link>
  ) : (
    <div className={cls} data-record-stat>
      {body}
    </div>
  )
}

/** A titled card on a record tab, with an optional "View all →" style action. */
export function RecordCard({
  title,
  action,
  children,
  className,
  bodyClassName,
  ...rest
}: {
  title?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
} & { [k: `data-${string}`]: string | boolean | undefined }) {
  return (
    <section
      className={cn("min-w-0 overflow-hidden rounded-xl border border-border bg-card", className)}
      {...rest}
    >
      {title ? (
        <div className="flex items-center gap-3 px-4 pt-4 pb-2">
          <h2 className="text-sm font-medium">{title}</h2>
          {action ? <div className="ms-auto text-xs">{action}</div> : null}
        </div>
      ) : null}
      <div className={bodyClassName}>{children}</div>
    </section>
  )
}

/** Header and tab outlines while the record loads. */
export function RecordSkeleton() {
  return (
    <div aria-busy data-loading="record" className="md:-mx-page">
      <div className="border-b border-border bg-card">
        <Skeleton className="h-28 w-full rounded-none sm:h-36" />
        <div className="px-4 sm:px-6">
          <div className="-mt-9 flex items-start gap-4 sm:-mt-11">
            <Skeleton className="size-18 rounded-xl ring-4 ring-card sm:size-22" />
            <div className="flex flex-1 flex-col gap-2 pt-12 sm:pt-14">
              <Skeleton className="h-6 w-56" />
              <Skeleton className="h-4 w-40" />
            </div>
            <Skeleton className="mt-14 h-9 w-28 max-sm:hidden" />
          </div>
          <div className="mt-3 flex gap-2">
            <Skeleton className="h-7 w-24 rounded-full" />
            <Skeleton className="h-7 w-28 rounded-full" />
            <Skeleton className="h-7 w-20 rounded-full" />
          </div>
          <div className="mt-4 flex gap-2 pb-2">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-8 w-24" />
            ))}
          </div>
        </div>
      </div>
      <div className="grid gap-4 px-4 py-6 sm:grid-cols-2 sm:px-6 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-28 rounded-xl" />
        ))}
      </div>
    </div>
  )
}

/**
 * `?focus=<element id>` on a tab's URL: once the tab is in, focus that control and bring it into view.
 * The overview's actions use it to land on the form they stand for, which now lives on another tab.
 */
export function useFocusParam(ready: boolean) {
  const search = useSearchParams()
  const focus = search.get("focus")
  useEffect(() => {
    if (!ready || !focus) return
    const id = window.setTimeout(() => {
      const el = document.getElementById(focus)
      if (!el) return
      el.scrollIntoView({ behavior: "smooth", block: "center" })
      ;(el as HTMLElement).focus()
    }, 250)
    return () => window.clearTimeout(id)
  }, [ready, focus])
}
