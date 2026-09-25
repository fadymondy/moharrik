"use client"

import { useEffect, useMemo, useRef } from "react"
import { BookmarkIcon, ExternalLinkIcon } from "lucide-react"
import { toast } from "sonner"

import { Bdi } from "@/components/bidi"
import { LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NotificationSeverity } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useNotificationItem } from "@/lib/queries"

export const severityTone: Record<NotificationSeverity, string> = {
  info: "bg-grid-brand",
  warning: "bg-grid-warn",
  error: "bg-grid-danger",
}

export const severityVariant: Record<
  NotificationSeverity,
  "default" | "secondary" | "destructive"
> = {
  info: "secondary",
  warning: "default",
  error: "destructive",
}

/** A kind's label: the known ones are translated, anything else is shown as it came. */
export function useKindLabel() {
  const { t } = useTranslations()
  return (kind: string) => {
    const key = `notifications.kindName.${kind}`
    const v = t(key)
    return v === key ? kind : v
  }
}

/*
One notification in full (MH-384): its body, where it came from, and the raw
payload it was made from — the webhook body or the collected row, verbatim,
so a badly normalised title can be understood from the original. Opening an
unread item marks it read, as opening a message does.
*/
export function NotificationDetailSheet({
  id,
  canWrite,
  onOpenChange,
  onChange,
}: {
  id: string | null
  canWrite: boolean
  onOpenChange: (open: boolean) => void
  onChange: () => void
}) {
  const { t, isRtl, formatDate } = useTranslations()
  const ws = useWorkspace()
  const detail = useNotificationItem(ws.id, id)
  const item = detail.data?.item
  const kindLabel = useKindLabel()
  const marked = useRef<string | null>(null)

  useEffect(() => {
    if (!canWrite || !item || item.read_at || marked.current === item.id) return
    marked.current = item.id
    api(`/api/w/notifications/${encodeURIComponent(item.id)}`, {
      method: "PATCH",
      json: { read: true },
      workspaceId: ws.id,
    })
      .then(() => {
        void detail.mutate()
        onChange()
      })
      .catch(() => {
        /* it stays unread; nothing to tell */
      })
  }, [canWrite, item, ws.id, detail, onChange])

  const payload = useMemo(() => {
    if (!item?.payload) return ""
    try {
      return JSON.stringify(JSON.parse(item.payload), null, 2)
    } catch {
      return item.payload
    }
  }, [item?.payload])

  async function toggleSaved() {
    if (!item) return
    try {
      await api(`/api/w/notifications/${encodeURIComponent(item.id)}`, {
        method: "PATCH",
        json: { saved: !item.saved_at },
        workspaceId: ws.id,
      })
      void detail.mutate()
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <Sheet open={!!id} onOpenChange={onOpenChange}>
      <SheetContent
        side={isRtl ? "left" : "right"}
        className="w-full overflow-y-auto data-[side=left]:w-full data-[side=right]:w-full data-[side=left]:sm:max-w-xl data-[side=right]:sm:max-w-xl"
        data-notification-detail
      >
        <SheetHeader className="pe-12">
          <SheetTitle className="break-words">
            {item ? <Bdi>{item.title}</Bdi> : t("notifications.loading")}
          </SheetTitle>
          <SheetDescription>
            {item ? <Bdi>{item.source_name || t("notifications.unknownSource")}</Bdi> : null}
          </SheetDescription>
        </SheetHeader>
        {detail.error ? (
          <p className="px-4 text-sm text-grid-danger-text" role="alert">
            {detail.error instanceof ApiError ? detail.error.message : t("common.networkError")}
          </p>
        ) : !item ? (
          <LoadingRows rows={3} />
        ) : (
          <div className="flex flex-col gap-5 px-4 pb-6">
            <div className="flex flex-wrap items-center gap-2 text-xs text-grid-muted">
              <Badge variant={severityVariant[item.severity]}>
                {t(`notifications.severity.${item.severity}`)}
              </Badge>
              {item.kind ? <Badge variant="outline">{kindLabel(item.kind)}</Badge> : null}
              <Badge variant="outline">{t(`notifications.origin.${item.origin}`)}</Badge>
              <span>{formatDate(item.occurred_at)}</span>
            </div>

            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              {item.actor ? (
                <>
                  <dt className="text-grid-muted">{t("notifications.actor")}</dt>
                  <dd className="min-w-0 break-words">
                    <Bdi>{item.actor}</Bdi>
                  </dd>
                </>
              ) : null}
              <dt className="text-grid-muted">{t("notifications.readState")}</dt>
              <dd>
                {item.read_at
                  ? t("notifications.readAt", { date: formatDate(item.read_at) })
                  : t("notifications.filterUnread")}
              </dd>
              {item.url ? (
                <>
                  <dt className="text-grid-muted">{t("notifications.link")}</dt>
                  <dd className="min-w-0">
                    <a
                      className="inline-flex max-w-full items-center gap-1 break-all underline underline-offset-4"
                      href={item.url}
                      target="_blank"
                      rel="noreferrer"
                      dir="ltr"
                    >
                      {item.url}
                      <ExternalLinkIcon className="size-3.5 shrink-0" />
                    </a>
                  </dd>
                </>
              ) : null}
            </dl>

            {item.body ? (
              <p
                className="text-sm break-words whitespace-pre-wrap"
                dir="auto"
                data-notification-body
              >
                {item.body}
              </p>
            ) : null}

            {canWrite ? (
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={toggleSaved}
                  aria-pressed={!!item.saved_at}
                >
                  <BookmarkIcon fill={item.saved_at ? "currentColor" : "none"} />
                  {item.saved_at ? t("notifications.unsave") : t("notifications.save")}
                </Button>
              </div>
            ) : null}

            <section className="flex flex-col gap-2">
              <h3 className="grid-micro">{t("notifications.payload")}</h3>
              <p className="text-xs text-grid-muted">{t("notifications.payloadHint")}</p>
              <pre
                className="max-h-96 overflow-auto border border-line bg-grid-card p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all"
                dir="ltr"
                data-notification-payload
              >
                {payload || "{}"}
              </pre>
            </section>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
