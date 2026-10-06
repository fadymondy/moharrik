"use client"

import type { ReactNode } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { Skeleton, Spinner } from "@fadymondy/nasaq/web"
import { ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export type LoadingLayout =
  "list" | "table" | "detail" | "form" | "cards" | "conversations" | "chat"

/**
 * A loading state shaped like the page it stands in for, so nothing jumps when the data lands,
 * with a spinner and "Loading…" on top so it never reads as an empty page.
 * `list` (default): avatar + two lines per row. `table`: columns. `detail`: a record header,
 * stat tiles and a body. `form`: label + field pairs. `cards`: a grid of cards.
 * `conversations` / `chat`: the inbox list and a thread.
 */
export function LoadingRows({
  rows = 3,
  layout = "list",
}: {
  rows?: number
  layout?: LoadingLayout
}) {
  const { t } = useTranslations()
  const n = Array.from({ length: rows }, (_, i) => i)
  // Varied widths read as content rather than a barcode.
  const w = (i: number, set: string[]) => set[i % set.length]
  let body: ReactNode
  switch (layout) {
    case "table":
      body = (
        <div className="divide-y divide-nq-line border-y border-nq-line">
          {n.map((i) => (
            <div key={i} className="flex items-center gap-4 px-6 py-4">
              <Skeleton className={`h-4 ${w(i, ["w-40", "w-32", "w-48"])}`} />
              <Skeleton className="h-4 w-24 max-sm:hidden" />
              <Skeleton className="h-4 w-20 max-md:hidden" />
              <Skeleton className="ms-auto h-4 w-16" />
            </div>
          ))}
        </div>
      )
      break
    case "detail":
      body = (
        <div className="flex flex-col gap-6 px-6 py-6">
          <div className="flex items-center gap-4">
            <Skeleton className="size-14 rounded-full" />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton className="h-5 w-56" />
              <Skeleton className="h-3.5 w-40" />
            </div>
            <Skeleton className="h-9 w-28 max-sm:hidden" />
          </div>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="flex flex-col gap-2 border border-nq-line p-4">
                <Skeleton className="h-3 w-20" />
                <Skeleton className="h-6 w-16" />
              </div>
            ))}
          </div>
          <div className="flex flex-col gap-3">
            {n.map((i) => (
              <Skeleton
                key={i}
                className={`h-4 ${w(i, ["w-full", "w-11/12", "w-4/5", "w-2/3"])}`}
              />
            ))}
          </div>
        </div>
      )
      break
    case "form":
      body = (
        <div className="flex max-w-2xl flex-col gap-5 px-6 py-6">
          {n.map((i) => (
            <div key={i} className="flex flex-col gap-2">
              <Skeleton className={`h-3.5 ${w(i, ["w-28", "w-36", "w-24"])}`} />
              <Skeleton className="h-9 w-full" />
            </div>
          ))}
          <Skeleton className="h-9 w-24" />
        </div>
      )
      break
    case "cards":
      body = (
        <div className="grid gap-3 px-6 py-4 sm:grid-cols-2 xl:grid-cols-3">
          {n.map((i) => (
            <div key={i} className="flex flex-col gap-3 border border-nq-line p-4">
              <div className="flex items-center gap-3">
                <Skeleton className="size-8 rounded-full" />
                <Skeleton className={`h-4 ${w(i, ["w-32", "w-40", "w-28"])}`} />
              </div>
              <Skeleton className="h-3.5 w-full" />
              <Skeleton className="h-3.5 w-3/4" />
              <div className="flex gap-2">
                <Skeleton className="h-5 w-14 rounded-full" />
                <Skeleton className="h-5 w-16 rounded-full" />
              </div>
            </div>
          ))}
        </div>
      )
      break
    case "conversations":
      body = (
        <div className="flex flex-col">
          {n.map((i) => (
            <div key={i} className="flex items-start gap-3 px-3 py-3">
              <Skeleton className="size-10 shrink-0 rounded-full" />
              <div className="flex min-w-0 flex-1 flex-col gap-2 pt-0.5">
                <div className="flex items-center gap-2">
                  <Skeleton className={`h-3.5 ${w(i, ["w-28", "w-36", "w-24"])}`} />
                  <Skeleton className="ms-auto h-3 w-10" />
                </div>
                <Skeleton className={`h-3 ${w(i, ["w-4/5", "w-3/5", "w-11/12"])}`} />
              </div>
            </div>
          ))}
        </div>
      )
      break
    case "chat":
      body = (
        <div className="flex flex-col">
          <div className="flex items-center gap-3 border-b border-nq-line px-4 py-3">
            <Skeleton className="size-9 rounded-full" />
            <div className="flex flex-col gap-1.5">
              <Skeleton className="h-3.5 w-32" />
              <Skeleton className="h-3 w-20" />
            </div>
            <Skeleton className="ms-auto h-8 w-20" />
          </div>
          <div className="flex flex-col gap-3 px-4 py-5">
            <Skeleton className="mx-auto h-5 w-20 rounded-full" />
            {n.map((i) => {
              const out = i % 2 === 1
              return (
                <Skeleton
                  key={i}
                  className={`h-10 rounded-2xl ${out ? "ms-auto rounded-se-md" : "rounded-ss-md"} ${w(i, ["w-3/5", "w-2/5", "w-1/2", "w-1/3"])}`}
                />
              )
            })}
          </div>
        </div>
      )
      break
    default:
      body = (
        <div className="divide-y divide-nq-line border-y border-nq-line">
          {n.map((i) => (
            <div key={i} className="flex items-center gap-3 px-6 py-3.5">
              <Skeleton className="size-8 shrink-0 rounded-full" />
              <div className="flex min-w-0 flex-1 flex-col gap-2">
                <Skeleton className={`h-3.5 ${w(i, ["w-40", "w-52", "w-32"])}`} />
                <Skeleton className={`h-3 ${w(i, ["w-24", "w-32", "w-20"])}`} />
              </div>
              <Skeleton className="h-4 w-16 max-sm:hidden" />
            </div>
          ))}
        </div>
      )
  }
  return (
    <div aria-busy data-loading={layout} className="relative">
      <div
        className="flex items-center gap-2 px-6 py-2 text-xs text-muted-foreground"
        data-loading-indicator
      >
        <Spinner className="size-3.5 text-primary" label={t("common.loading")} />
        <span aria-hidden>{t("common.loading")}</span>
      </div>
      {body}
    </div>
  )
}

export function EmptyState({
  title,
  body,
  action,
}: {
  title: string
  body?: string
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-2 border-y border-nq-line bg-card px-6 py-12 text-center">
      <p className="text-sm font-medium text-foreground">{title}</p>
      {body ? <p className="max-w-md text-sm text-muted-foreground">{body}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}

export function ErrorState({ error }: { error: unknown }) {
  const { t } = useTranslations()
  const message =
    error instanceof ApiError
      ? error.status >= 500
        ? t("common.apiUnavailable")
        : error.message
      : t("common.networkError")
  return (
    <div role="alert" className="flex items-start gap-3 border-y border-nq-line px-6 py-4 text-sm">
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger" />
      <p className="text-nq-danger-text">{message}</p>
    </div>
  )
}
