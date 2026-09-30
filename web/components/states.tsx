"use client"

import type { ReactNode } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { Skeleton } from "@/components/ui/skeleton"
import { ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function LoadingRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="divide-y divide-nq-line border-y border-nq-line" aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-4 px-6 py-4">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="ms-auto h-4 w-16" />
        </div>
      ))}
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
