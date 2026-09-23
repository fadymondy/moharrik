"use client"

import Link from "next/link"
import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { BellOffIcon, BookmarkIcon, CheckIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NotificationItem, type NotificationSeverity } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useNotifications, useWebhookSources, type NotificationFilter } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const severityTone: Record<NotificationSeverity, string> = {
  info: "bg-grid-brand",
  warning: "bg-grid-warn",
  error: "bg-grid-danger",
}

const severityVariant: Record<NotificationSeverity, "default" | "secondary" | "destructive"> = {
  info: "secondary",
  warning: "default",
  error: "destructive",
}

export default function NotificationsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.notifications")} · ${ws.summary.name}`)
  const canWrite = ws.can("notifications:write")
  const canManage = ws.can("notifications:manage")

  // ?unread=1 opens the feed already filtered — the Overview links its count here.
  const [unread, setUnread] = useState(useSearchParams().get("unread") === "1")
  const [saved, setSaved] = useState(false)
  const [sourceId, setSourceId] = useState("")
  const [severity, setSeverity] = useState("")

  const sources = useWebhookSources(ws.id)
  const filter: NotificationFilter = { unread, saved, sourceId: sourceId || undefined, severity: severity || undefined }
  const feed = useNotifications(ws.id, filter)

  // A webhook delivery lands on this page as it happens, and a read in another
  // tab (or by an agent over MCP) clears here too — no reload (MH-378).
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "notification" || e.kind === "notifications") void feed.mutate()
  })

  const sourceOpts = (sources.data?.sources ?? []).map((s) => ({ value: s.id, label: s.name }))
  const severityOpts = [
    { value: "info", label: t("notifications.severity.info") },
    { value: "warning", label: t("notifications.severity.warning") },
    { value: "error", label: t("notifications.severity.error") },
  ]

  async function markAllRead() {
    try {
      const res = await api<{ marked: number }>("/api/w/notifications/mark_all_read", { method: "POST", workspaceId: ws.id })
      toast.success(t("notifications.markedAllRead", { n: res.marked }))
      feed.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.notifications")}
        description={t("notifications.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canManage ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/notifications/sources`} />}>
                <SettingsIcon />
                {t("notifications.sources")}
              </Button>
            ) : null}
            {canWrite ? (
              <Button variant="outline" size="sm" onClick={markAllRead} disabled={!feed.data?.unread_count}>
                <CheckIcon />
                {t("notifications.markAllRead")}
              </Button>
            ) : null}
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-2 px-6 pb-4" role="group" aria-label={t("notifications.filters")}>
        <Button type="button" size="sm" variant={unread ? "default" : "outline"} aria-pressed={unread} onClick={() => setUnread((v) => !v)} data-filter="unread">
          {t("notifications.filterUnread")}
        </Button>
        <Button type="button" size="sm" variant={saved ? "default" : "outline"} aria-pressed={saved} onClick={() => setSaved((v) => !v)} data-filter="saved">
          {t("notifications.filterSaved")}
        </Button>
        <div className="w-40">
          <PickSelect id="notif-source" value={sourceId} onChange={setSourceId} options={sourceOpts} noneLabel={t("notifications.allSources")} />
        </div>
        <div className="w-36">
          <PickSelect id="notif-severity" value={severity} onChange={setSeverity} options={severityOpts} noneLabel={t("notifications.allSeverities")} />
        </div>
      </div>

      {feed.error ? (
        <ErrorState error={feed.error} />
      ) : !feed.data ? (
        <LoadingRows rows={5} />
      ) : feed.data.items.length === 0 ? (
        <EmptyState
          title={t("notifications.empty")}
          body={canManage ? t("notifications.emptyBody") : undefined}
          action={
            canManage ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/notifications/sources`} />}>
                {t("notifications.addSource")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {feed.data.items.map((it) => (
            <NotificationRow key={it.id} item={it} canWrite={canWrite} onChange={() => feed.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />
    </>
  )
}

function NotificationRow({ item, canWrite, onChange }: { item: NotificationItem; canWrite: boolean; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const isRead = !!item.read_at
  const isSaved = !!item.saved_at

  async function toggle(field: "read" | "saved", to: boolean) {
    try {
      await api(`/api/w/notifications/${encodeURIComponent(item.id)}`, { method: "PATCH", json: { [field]: to }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li
      className={`flex items-start gap-3 px-6 py-4 ${isRead ? "" : "bg-grid-card"}`}
      data-notification={item.title}
      data-notification-read={isRead ? "true" : "false"}
    >
      <span className={`mt-1.5 size-2 shrink-0 rounded-full ${severityTone[item.severity]}`} aria-hidden />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium break-words">{item.title}</p>
        {item.body ? <p className="mt-1 text-sm text-grid-muted break-words whitespace-pre-wrap">{item.body}</p> : null}
        <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-grid-muted">
          <Badge variant={severityVariant[item.severity]}>{t(`notifications.severity.${item.severity}`)}</Badge>
          <span>{item.source_name || t("notifications.unknownSource")}</span>
          {item.actor ? (
            <>
              <span aria-hidden>·</span>
              <span>{item.actor}</span>
            </>
          ) : null}
          <span aria-hidden>·</span>
          <span>{formatDate(item.occurred_at)}</span>
          {item.url ? (
            <>
              <span aria-hidden>·</span>
              <a className="underline underline-offset-4" href={item.url} target="_blank" rel="noreferrer">
                {t("notifications.open")}
              </a>
            </>
          ) : null}
        </p>
      </div>
      {canWrite ? (
        <div className="flex shrink-0 items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-pressed={isSaved}
            aria-label={isSaved ? t("notifications.unsave") : t("notifications.save")}
            onClick={() => toggle("saved", !isSaved)}
            className={isSaved ? "text-grid-brand" : undefined}
          >
            <BookmarkIcon fill={isSaved ? "currentColor" : "none"} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={isRead ? t("notifications.markUnread") : t("notifications.markRead")}
            onClick={() => toggle("read", !isRead)}
          >
            {isRead ? <BellOffIcon /> : <CheckIcon />}
          </Button>
        </div>
      ) : null}
    </li>
  )
}
