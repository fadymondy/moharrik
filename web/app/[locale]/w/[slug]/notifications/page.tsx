"use client"

import Link from "next/link"
import { useMemo, useState } from "react"
import { useSearchParams } from "next/navigation"
import { BellOffIcon, BookmarkIcon, CheckIcon, SearchIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { Bdi } from "@/components/bidi"
import { useDebounced } from "@/components/crm/contact-picker"
import { PickSelect } from "@/components/crm/shared"
import { PageNav, PageSummary, useCursorStack } from "@/components/cursor-pager"
import {
  NotificationDetailSheet,
  severityTone,
  severityVariant,
  useKindLabel,
} from "@/components/notifications/notification-detail"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NotificationItem } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useNotifications, type NotificationFilter } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

/*
The notification center (MH-384, fadymondy.com's center page): search, the
source and kind filters with their counts, keyset paging, mark-all-read for
one source, and an item's full detail with the raw payload it was made from.
*/

const PAGE_SIZE = 25

export default function NotificationsPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.notifications")} · ${ws.summary.name}`)
  const canWrite = ws.can("notifications:write")
  const canManage = ws.can("notifications:manage")
  const kindLabel = useKindLabel()

  // ?unread=1 opens the feed already filtered — the Overview links its count here.
  const [unread, setUnread] = useState(useSearchParams().get("unread") === "1")
  const [saved, setSaved] = useState(false)
  const [sourceId, setSourceId] = useState("")
  const [kind, setKind] = useState("")
  const [severity, setSeverity] = useState("")
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim())
  const [openId, setOpenId] = useState<string | null>(null)

  // A cursor belongs to one filter: any change starts again at page one.
  const pager = useCursorStack([unread, saved, sourceId, kind, severity, q].join("|"))
  const filter: NotificationFilter = {
    unread,
    saved,
    sourceId: sourceId || undefined,
    kind: kind || undefined,
    severity: severity || undefined,
    q: q || undefined,
    limit: PAGE_SIZE,
    cursor: pager.cursor || undefined,
  }
  const feed = useNotifications(ws.id, filter)
  const page = feed.data

  // A delivery or a collected item lands here as it happens, and a read in
  // another tab (or by an agent over MCP) clears here too — no reload (MH-378).
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "notification" || e.kind === "notifications") void feed.mutate()
  })

  const sourceOpts = useMemo(
    () =>
      (page?.sources ?? []).map((s) => ({
        value: s.key,
        label: s.unread ? `${s.name} (${formatNumber(s.unread)})` : s.name,
      })),
    [page?.sources, formatNumber],
  )
  const kindOpts = useMemo(
    () =>
      (page?.kinds ?? []).map((k) => ({
        value: k.value,
        label: `${kindLabel(k.value)} · ${formatNumber(k.total)}`,
      })),
    [page?.kinds, kindLabel, formatNumber],
  )
  const severityOpts = [
    { value: "info", label: t("notifications.severity.info") },
    { value: "warning", label: t("notifications.severity.warning") },
    { value: "error", label: t("notifications.severity.error") },
  ]
  const selectedSource = page?.sources.find((s) => s.key === sourceId)
  const filtered = unread || saved || !!sourceId || !!kind || !!severity || !!q
  const nothingAtAll = !!page && page.total === 0 && !filtered

  async function markAllRead() {
    try {
      const qs = sourceId ? `?source_id=${encodeURIComponent(sourceId)}` : ""
      const res = await api<{ marked: number }>(`/api/w/notifications/mark_all_read${qs}`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("notifications.markedAllRead", { n: res.marked }))
      void feed.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const markAllDisabled = selectedSource ? selectedSource.unread === 0 : !page?.unread_count

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.notifications")}
        description={t("notifications.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canManage ? (
              <Button
                variant="outline"
                size="sm"
                nativeButton={false}
                render={<Link href={`/${locale}/w/${ws.slug}/notifications/sources`} />}
              >
                <SettingsIcon />
                {t("notifications.sources")}
              </Button>
            ) : null}
            {canWrite ? (
              <Button
                variant="outline"
                size="sm"
                onClick={markAllRead}
                disabled={markAllDisabled}
                data-mark-all={sourceId || "all"}
              >
                <CheckIcon />
                {selectedSource
                  ? t("notifications.markSourceRead", { name: selectedSource.name })
                  : t("notifications.markAllRead")}
              </Button>
            ) : null}
          </div>
        }
      />

      {nothingAtAll ? null : (
        <div
          className="flex flex-col gap-3 px-6 pb-4"
          role="group"
          aria-label={t("notifications.filters")}
        >
          <InputGroup className="sm:max-w-md">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("notifications.search")}
              aria-label={t("notifications.search")}
              data-notification-search
            />
          </InputGroup>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="sm"
              variant={unread ? "default" : "outline"}
              aria-pressed={unread}
              onClick={() => setUnread((v) => !v)}
              data-filter="unread"
            >
              {t("notifications.filterUnread")}
              {page ? (
                <span className="tabular-nums opacity-80">{formatNumber(page.unread_count)}</span>
              ) : null}
            </Button>
            <Button
              type="button"
              size="sm"
              variant={saved ? "default" : "outline"}
              aria-pressed={saved}
              onClick={() => setSaved((v) => !v)}
              data-filter="saved"
            >
              {t("notifications.filterSaved")}
              {page ? (
                <span className="tabular-nums opacity-80">{formatNumber(page.saved_count)}</span>
              ) : null}
            </Button>
            <div className="w-full sm:w-56">
              <PickSelect
                id="notif-source"
                value={sourceId}
                onChange={setSourceId}
                options={sourceOpts}
                noneLabel={t("notifications.allSources")}
              />
            </div>
            <div className="w-[calc(50%-0.25rem)] sm:w-44">
              <PickSelect
                id="notif-kind"
                value={kind}
                onChange={setKind}
                options={kindOpts}
                noneLabel={t("notifications.allKinds")}
              />
            </div>
            <div className="w-[calc(50%-0.25rem)] sm:w-36">
              <PickSelect
                id="notif-severity"
                value={severity}
                onChange={setSeverity}
                options={severityOpts}
                noneLabel={t("notifications.allSeverities")}
              />
            </div>
          </div>
        </div>
      )}

      {feed.error ? (
        <ErrorState error={feed.error} />
      ) : !page ? (
        <LoadingRows rows={5} />
      ) : nothingAtAll ? (
        <EmptyState
          title={t("notifications.empty")}
          body={canManage ? t("notifications.emptyBody") : undefined}
          action={
            canManage ? (
              <Button
                variant="outline"
                size="sm"
                nativeButton={false}
                render={<Link href={`/${locale}/w/${ws.slug}/notifications/sources`} />}
              >
                {t("notifications.addSource")}
              </Button>
            ) : undefined
          }
        />
      ) : page.total === 0 ? (
        <p
          className="border-y border-line px-6 py-6 text-sm text-grid-muted"
          data-notifications-no-match
        >
          {t("notifications.noMatch")}
        </p>
      ) : (
        <>
          <PageSummary
            index={pager.index}
            pageSize={PAGE_SIZE}
            shown={page.items.length}
            total={page.total}
            name="notifications"
          />
          <ul className="divide-y divide-line border-y border-line">
            {page.items.map((it) => (
              <NotificationRow
                key={it.id}
                item={it}
                canWrite={canWrite}
                onOpen={() => setOpenId(it.id)}
                onChange={() => feed.mutate()}
                kindLabel={kindLabel}
              />
            ))}
          </ul>
          <PageNav
            index={pager.index}
            pageSize={PAGE_SIZE}
            total={page.total}
            nextCursor={page.next_cursor || undefined}
            busy={feed.isLoading}
            onNext={pager.next}
            onPrev={pager.prev}
            name="notifications"
          />
        </>
      )}
      <div className="pb-8" />

      <NotificationDetailSheet
        id={openId}
        canWrite={canWrite}
        onOpenChange={(open) => (!open ? setOpenId(null) : undefined)}
        onChange={() => feed.mutate()}
      />
    </>
  )
}

function NotificationRow({
  item,
  canWrite,
  onOpen,
  onChange,
  kindLabel,
}: {
  item: NotificationItem
  canWrite: boolean
  onOpen: () => void
  onChange: () => void
  kindLabel: (k: string) => string
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const isRead = !!item.read_at
  const isSaved = !!item.saved_at

  async function toggle(field: "read" | "saved", to: boolean) {
    try {
      await api(`/api/w/notifications/${encodeURIComponent(item.id)}`, {
        method: "PATCH",
        json: { [field]: to },
        workspaceId: ws.id,
      })
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
      <span
        className={`mt-1.5 size-2 shrink-0 rounded-full ${severityTone[item.severity]}`}
        aria-hidden
      />
      <div className="min-w-0 flex-1">
        <button
          type="button"
          className="text-start text-sm font-medium break-words hover:underline hover:underline-offset-4"
          onClick={onOpen}
          data-open-notification
        >
          <Bdi>{item.title}</Bdi>
        </button>
        {item.body ? (
          <p
            className="mt-1 line-clamp-3 text-sm break-words whitespace-pre-wrap text-grid-muted"
            dir="auto"
          >
            {item.body}
          </p>
        ) : null}
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-grid-muted">
          <Badge variant={severityVariant[item.severity]}>
            {t(`notifications.severity.${item.severity}`)}
          </Badge>
          {item.kind ? <Badge variant="outline">{kindLabel(item.kind)}</Badge> : null}
          {item.source_name ? (
            <Bdi>{item.source_name}</Bdi>
          ) : (
            <span>{t("notifications.unknownSource")}</span>
          )}
          {item.actor ? (
            <>
              <span aria-hidden>·</span>
              <Bdi>{item.actor}</Bdi>
            </>
          ) : null}
          <span aria-hidden>·</span>
          <span>{formatDate(item.occurred_at)}</span>
          {item.url ? (
            <>
              <span aria-hidden>·</span>
              <a
                className="underline underline-offset-4"
                href={item.url}
                target="_blank"
                rel="noreferrer"
              >
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
